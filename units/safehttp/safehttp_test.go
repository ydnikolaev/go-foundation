package safehttp_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/go-foundation/units/safehttp"
)

// loopbackOnly is the AllowHosts of a test whose httptest server listens on 127.0.0.1.
var loopbackOnly = safehttp.Options{AllowHosts: []string{"127.0.0.1"}}

func get(t *testing.T, c *safehttp.Client, u string) (*http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	resp, err := c.Get(ctx, u)
	if resp != nil {
		t.Cleanup(func() { _ = resp.Body.Close() })
	}
	if err != nil {
		return resp, fmt.Errorf("get %s: %w", u, err)
	}
	return resp, nil
}

func TestBlocked(t *testing.T) {
	t.Parallel()
	tests := []struct {
		ip   string
		want bool
	}{
		{"127.0.0.1", true},
		{"127.8.9.10", true},
		{"::1", true},
		{"169.254.169.254", true}, // cloud metadata
		{"fd00:ec2::254", true},   // AWS IPv6 metadata
		{"fe80::1", true},
		{"10.1.2.3", true},
		{"172.16.0.1", true},
		{"192.168.1.1", true},
		{"100.64.0.1", true}, // CGNAT
		{"0.0.0.0", true},
		{"::", true},
		{"::ffff:127.0.0.1", true},       // IPv4-mapped loopback
		{"::ffff:169.254.169.254", true}, // IPv4-mapped metadata
		{"::ffff:10.0.0.1", true},
		{"64:ff9b::a00:1", true}, // NAT64 of 10.0.0.1
		{"2002:a00:1::", true},   // 6to4 of 10.0.0.1
		{"224.0.0.1", true},
		{"255.255.255.255", true},
		{"192.0.2.1", true},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"2606:4700:4700::1111", false},
		{"::ffff:8.8.8.8", false},
	}
	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			t.Parallel()
			if got := safehttp.Blocked(netip.MustParseAddr(tt.ip)); got != tt.want {
				t.Errorf("Blocked(%s) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}

func TestRefusesNonPublicDestinations(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "inside")
	}))
	t.Cleanup(srv.Close)
	_, p, _ := strings.Cut(srv.Listener.Addr().String(), ":")

	tests := []struct {
		name string
		url  string
	}{
		{"loopback literal", srv.URL},
		{"loopback name", "http://localhost:" + p + "/"},
		{"metadata", "http://169.254.169.254/latest/meta-data/"},
		{"mapped metadata", "http://[::ffff:169.254.169.254]/"},
		{"private", "http://10.0.0.1/"},
		{"unspecified", "http://0.0.0.0:" + p + "/"},
	}
	c := safehttp.New(safehttp.Options{}) // no AllowHosts: production shape
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resp, err := get(t, c, tt.url)
			if !errors.Is(err, safehttp.ErrBlocked) {
				t.Fatalf("Get(%s) = %v, %v; want ErrBlocked", tt.url, resp, err)
			}
		})
	}
}

func TestRefusesOtherSchemes(t *testing.T) {
	t.Parallel()
	c := safehttp.New(safehttp.Options{})
	for _, u := range []string{"file:///etc/passwd", "ftp://example.com/", "gopher://example.com/"} {
		if _, err := get(t, c, u); err == nil {
			t.Errorf("Get(%s) succeeded", u)
		}
	}
}

func TestAllowHostsReachesTheTestServer(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "hello")
	}))
	t.Cleanup(srv.Close)
	resp, err := get(t, safehttp.New(loopbackOnly), srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil || string(body) != "hello" {
		t.Errorf("body = %q, %v", body, err)
	}
}

func TestRedirectToPrivateIsRefused(t *testing.T) {
	t.Parallel()
	inside := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("the redirect target was reached")
		_, _ = io.WriteString(w, "secret")
	}))
	t.Cleanup(inside.Close)
	insideURL, err := url.Parse(inside.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := strings.Cut(insideURL.Host, ":")

	tests := []struct {
		name   string
		target string
	}{
		{"metadata", "http://169.254.169.254/latest/meta-data/"},
		{"private", "http://10.0.0.1/"},
		// 127.0.0.2 is loopback too but not in AllowHosts: the hop is checked again, not trusted by origin.
		{"another loopback", "http://127.0.0.2:" + port + "/"},
		{"loopback by name", "http://localhost:" + port + "/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, tt.target, http.StatusFound)
			}))
			t.Cleanup(origin.Close)
			if _, err := get(t, safehttp.New(loopbackOnly), origin.URL); !errors.Is(err, safehttp.ErrBlocked) {
				t.Fatalf("err = %v, want ErrBlocked", err)
			}
		})
	}
}

func TestRedirectLimit(t *testing.T) {
	t.Parallel()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srv.URL+"/again", http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	opts := loopbackOnly
	opts.MaxRedirects = 2
	if _, err := get(t, safehttp.New(opts), srv.URL); !errors.Is(err, safehttp.ErrTooManyRedirects) {
		t.Fatalf("err = %v, want ErrTooManyRedirects", err)
	}
}

func TestBodyCap(t *testing.T) {
	t.Parallel()
	const limit = 1024
	tests := []struct {
		name     string
		size     int
		chunked  bool
		wantErr  bool
		whenRead bool // the error comes from the body read, not from Get
	}{
		{"under", limit - 1, false, false, false},
		{"exactly", limit, false, false, false},
		{"exactly, chunked", limit, true, false, false},
		{"declared over", limit + 1, false, true, false},
		{"streamed over", limit * 4, true, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				body := strings.Repeat("a", tt.size)
				if tt.chunked {
					// Two writes with a flush between: no Content-Length, a chunked body.
					_, _ = io.WriteString(w, body[:tt.size/2])
					http.NewResponseController(w).Flush()
					_, _ = io.WriteString(w, body[tt.size/2:])
					return
				}
				_, _ = io.WriteString(w, body)
			}))
			t.Cleanup(srv.Close)
			opts := loopbackOnly
			opts.MaxBodyBytes = limit
			resp, err := get(t, safehttp.New(opts), srv.URL)
			if !tt.whenRead {
				if tt.wantErr {
					if !errors.Is(err, safehttp.ErrBodyTooLarge) {
						t.Fatalf("Get err = %v, want ErrBodyTooLarge", err)
					}
					return
				}
				if err != nil {
					t.Fatalf("Get: %v", err)
				}
			}
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			body, err := io.ReadAll(resp.Body)
			if tt.wantErr {
				if !errors.Is(err, safehttp.ErrBodyTooLarge) || len(body) > limit {
					t.Fatalf("read %d bytes, err %v; want at most %d and ErrBodyTooLarge", len(body), err, limit)
				}
				return
			}
			if err != nil || len(body) != tt.size {
				t.Errorf("read %d bytes, err %v; want %d", len(body), err, tt.size)
			}
		})
	}
}

func TestStatusError(t *testing.T) {
	t.Parallel()
	const limit = 1024
	tests := []struct {
		name       string
		handler    http.HandlerFunc
		maxRedir   int
		wantStatus int
		wantIs     error
		wantMsg    string
	}{
		{"redirect refused at once", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/to", http.StatusFound)
		}, -1, http.StatusFound, safehttp.ErrTooManyRedirects, "too many redirects (0)"},
		{"permanent redirect refused at once", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/to", http.StatusPermanentRedirect)
		}, -1, http.StatusPermanentRedirect, safehttp.ErrTooManyRedirects, "too many redirects (0)"},
		{"redirect loop past the limit", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/again", http.StatusMovedPermanently)
		}, 2, http.StatusMovedPermanently, safehttp.ErrTooManyRedirects, "too many redirects (2)"},
		{"declared length over the cap", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, strings.Repeat("a", limit+1))
		}, 0, http.StatusOK, safehttp.ErrBodyTooLarge, "Content-Length 1025 > 1024"},
		{"declared length over the cap, 206", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", strconv.Itoa(limit+1))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = io.WriteString(w, strings.Repeat("a", limit+1))
		}, 0, http.StatusPartialContent, safehttp.ErrBodyTooLarge, "Content-Length 1025 > 1024"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(tt.handler)
			t.Cleanup(srv.Close)
			opts := loopbackOnly
			opts.MaxBodyBytes = limit
			opts.MaxRedirects = tt.maxRedir
			_, err := get(t, safehttp.New(opts), srv.URL)
			if !errors.Is(err, tt.wantIs) {
				t.Fatalf("err = %v, want %v", err, tt.wantIs)
			}
			se, ok := errors.AsType[*safehttp.StatusError](err)
			if !ok {
				t.Fatalf("err = %v (%T), want a *StatusError in its chain", err, err)
			}
			if se.Status != tt.wantStatus {
				t.Errorf("Status = %d, want %d", se.Status, tt.wantStatus)
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("err = %q, want it to contain %q", err, tt.wantMsg)
			}
		})
	}
}

func TestTimeoutPerClient(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	opts := loopbackOnly
	opts.Timeout = 100 * time.Millisecond
	resp, err := safehttp.New(opts).Get(context.Background(), srv.URL)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil {
		t.Fatal("a request past the client timeout succeeded")
	}
}
