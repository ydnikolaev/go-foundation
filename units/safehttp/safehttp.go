// Package safehttp is an HTTP client for URLs a caller does not control (a source page, a feed): it refuses to
// reach the host's own network. Every connection resolves its host ONCE, refuses the dial when any resolved address
// is private, loopback, link-local, a cloud metadata address or otherwise not public, and dials the checked address
// itself, so no second lookup can rebind the name. A redirect hop is checked again (its own dial goes through the
// same check), the response body is capped, and the client has its own timeout.
//
// Use it when code fetches a URL it does not control (one a user, a source or a feed supplied), or calls an outside
// API: never the default client for such a URL. The default timeout is 10 s: a call that legitimately runs longer
// (a language-model call, for one) uses another client.
package safehttp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Defaults for a zero Options field.
const (
	DefaultTimeout      = 10 * time.Second
	DefaultMaxBodyBytes = 2 << 20 // a fetched URL body is at most 2 MiB
	DefaultMaxRedirects = 5
)

var (
	// ErrBlocked is returned (wrapped) when a URL, a redirect hop or a resolved address is not a public host.
	ErrBlocked = errors.New("safehttp: destination is not a public address")
	// ErrBodyTooLarge is returned (wrapped) by a response body read past the cap, or at once when the declared
	// Content-Length is over it.
	ErrBodyTooLarge = errors.New("safehttp: response body over the cap")
	// ErrTooManyRedirects is returned (wrapped) when a request redirects more than MaxRedirects times.
	ErrTooManyRedirects = errors.New("safehttp: too many redirects")
)

// StatusError is the error of a refusal that came after the server answered: a redirect the client will not follow
// (it unwraps to ErrTooManyRedirects; Status is the 3xx status that asked for it), or a declared Content-Length over
// the cap (it unwraps to ErrBodyTooLarge; Status is the response status). No response is returned with either, so a
// caller that must tell an answered request from a failed one finds it with errors.As and reads Status. The message
// is that of the wrapped error.
type StatusError struct {
	// Status is the HTTP status of the response that was refused.
	Status int

	err error
}

// Error returns the message of the wrapped error.
func (e *StatusError) Error() string { return e.err.Error() }

// Unwrap returns ErrTooManyRedirects or ErrBodyTooLarge (wrapped).
func (e *StatusError) Unwrap() error { return e.err }

// Options shape a Client. A zero field takes its default.
type Options struct {
	// Timeout bounds a whole request, redirects and body read included. Default DefaultTimeout.
	Timeout time.Duration
	// MaxBodyBytes caps a response body. Default DefaultMaxBodyBytes.
	MaxBodyBytes int64
	// MaxRedirects caps the redirect hops. Default DefaultMaxRedirects; a negative value follows none.
	MaxRedirects int
	// AllowHosts exempts these host names or IP literals (no port) from the address check. For tests (an httptest
	// server listens on loopback) and for a client wrapper that passes one loopback IP literal on purpose; no other
	// production code sets it.
	AllowHosts []string
}

// Client is an SSRF-safe HTTP client. It is safe for concurrent use.
type Client struct {
	hc      *http.Client
	maxBody int64
}

// New returns a Client under opts.
func New(opts Options) *Client {
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultTimeout
	}
	if opts.MaxBodyBytes <= 0 {
		opts.MaxBodyBytes = DefaultMaxBodyBytes
	}
	if opts.MaxRedirects == 0 {
		opts.MaxRedirects = DefaultMaxRedirects
	}
	allow := make(map[string]bool, len(opts.AllowHosts))
	for _, h := range opts.AllowHosts {
		allow[strings.ToLower(strings.Trim(h, "[]"))] = true
	}
	d := &dialer{allow: allow, resolver: net.DefaultResolver}
	tr := &http.Transport{
		Proxy:                 nil, // an environment proxy would dial on our behalf, past the check
		DialContext:           d.dialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          16,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   opts.Timeout,
		ResponseHeaderTimeout: opts.Timeout,
	}
	maxRedirects := max(opts.MaxRedirects, 0)
	return &Client{
		maxBody: opts.MaxBodyBytes,
		hc: &http.Client{
			Transport: tr,
			Timeout:   opts.Timeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) > maxRedirects {
					se := &StatusError{err: fmt.Errorf("%w (%d)", ErrTooManyRedirects, maxRedirects)}
					if req.Response != nil { // the 3xx answer that asked for this hop
						se.Status = req.Response.StatusCode
					}
					return se
				}
				return checkURL(req, allow)
			},
		},
	}
}

// Get fetches url with ctx.
func (c *Client) Get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("safehttp: build request: %w", err)
	}
	return c.Do(req)
}

// Do sends req. The returned body is capped: a read past MaxBodyBytes fails with ErrBodyTooLarge, and a declared
// Content-Length over it fails at once. A refused redirect and that declared over-cap length are a *StatusError. The
// caller closes the body.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	if err := checkURL(req, nil); err != nil {
		return nil, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("safehttp: send request: %w", err)
	}
	if resp.ContentLength > c.maxBody {
		_ = resp.Body.Close()
		return nil, &StatusError{
			Status: resp.StatusCode,
			err:    fmt.Errorf("%w: Content-Length %d > %d", ErrBodyTooLarge, resp.ContentLength, c.maxBody),
		}
	}
	resp.Body = &cappedBody{rc: resp.Body, left: c.maxBody}
	return resp, nil
}

// checkURL refuses a scheme other than http(s) and, when allow is non-nil, an IP-literal host that is not public.
// A host name is checked at dial time, after its one lookup.
func checkURL(req *http.Request, allow map[string]bool) error {
	switch req.URL.Scheme {
	case "http", "https":
	default:
		return fmt.Errorf("safehttp: scheme %q is not http or https", req.URL.Scheme)
	}
	if allow == nil {
		return nil
	}
	host := strings.ToLower(req.URL.Hostname())
	if allow[host] {
		return nil
	}
	if ip, err := netip.ParseAddr(host); err == nil && Blocked(ip) {
		return fmt.Errorf("%w: redirect to %s", ErrBlocked, ip)
	}
	return nil
}

type dialer struct {
	allow    map[string]bool
	resolver *net.Resolver
}

// dialContext resolves host once, refuses any non-public answer, and dials the checked address. The net.Dialer's
// Control re-checks the socket address the kernel is about to connect to, with no lookup.
func (d *dialer) dialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("safehttp: address %q: %w", addr, err)
	}
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return nil, fmt.Errorf("safehttp: port %q: %w", portStr, err)
	}
	allowed := d.allow[strings.ToLower(host)]

	var ips []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		ips = []netip.Addr{ip}
	} else {
		ips, err = d.resolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, fmt.Errorf("safehttp: resolve %s: %w", host, err)
		}
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("safehttp: resolve %s: no address", host)
	}
	if !allowed {
		// One bad record refuses the host: a name that also points inside is not a public host.
		for _, ip := range ips {
			if Blocked(ip) {
				return nil, fmt.Errorf("%w: %s resolves to %s", ErrBlocked, host, ip.Unmap())
			}
		}
	}
	nd := &net.Dialer{Timeout: 10 * time.Second}
	if !allowed {
		nd.Control = func(_, address string, _ syscall.RawConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil || Blocked(ap.Addr()) {
				return fmt.Errorf("%w: dial %s", ErrBlocked, address)
			}
			return nil
		}
	}
	var lastErr error
	for _, ip := range ips {
		conn, err := nd.DialContext(ctx, network, netip.AddrPortFrom(ip.Unmap(), uint16(port)).String())
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// blockedPrefixes are the non-public ranges netip's own predicates do not cover.
var blockedPrefixes = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{
		"0.0.0.0/8",       // "this network"
		"100.64.0.0/10",   // carrier-grade NAT
		"192.0.0.0/24",    // IETF protocol assignments
		"192.0.2.0/24",    // documentation
		"198.18.0.0/15",   // benchmarking
		"198.51.100.0/24", // documentation
		"203.0.113.0/24",  // documentation
		"240.0.0.0/4",     // reserved, and the broadcast address
		"::/96",           // IPv4-compatible (deprecated)
		"64:ff9b::/96",    // NAT64: maps onto IPv4, possibly an inside one
		"64:ff9b:1::/48",  // local-use NAT64
		"100::/64",        // discard
		"2001:db8::/32",   // documentation
		"2002::/16",       // 6to4: embeds an IPv4 address
		"fec0::/10",       // site-local (deprecated)
	} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// Blocked reports whether ip is not a public unicast address: loopback, private (RFC 1918, fc00::/7, so the
// fd00:ec2::254 metadata address too), link-local (169.254.0.0/16 holds 169.254.169.254), multicast, unspecified,
// CGNAT, documentation, reserved, and the IPv4-mapped form of any of them.
func Blocked(ip netip.Addr) bool {
	ip = ip.Unmap().WithZone("")
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	for _, p := range blockedPrefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// cappedBody fails a read once more than its cap has come through.
type cappedBody struct {
	rc   io.ReadCloser
	left int64
}

func (b *cappedBody) Read(p []byte) (int, error) {
	if b.left < 0 {
		return 0, ErrBodyTooLarge
	}
	if int64(len(p)) > b.left+1 {
		p = p[:b.left+1]
	}
	n, err := b.rc.Read(p)
	b.left -= int64(n)
	if b.left < 0 {
		return n + int(b.left), ErrBodyTooLarge
	}
	switch {
	case err == nil:
		return n, nil
	case errors.Is(err, io.EOF):
		return n, io.EOF // the io.Reader contract: callers compare the end of the body to io.EOF itself
	default:
		return n, fmt.Errorf("safehttp: read body: %w", err)
	}
}

func (b *cappedBody) Close() error {
	if err := b.rc.Close(); err != nil {
		return fmt.Errorf("safehttp: close body: %w", err)
	}
	return nil
}
