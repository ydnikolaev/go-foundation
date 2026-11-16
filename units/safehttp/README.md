# safehttp

An HTTP client for URLs a caller does not control (a source page, a feed): it refuses to reach the host's own network.
Every connection resolves its host once, refuses the dial when any resolved address is private, loopback, link-local, a
cloud metadata address or otherwise not public, and dials the checked address itself, so no second lookup can rebind the
name. A redirect hop is checked again, the response body is capped, and the client has its own timeout. Standard library
only.

## Install

```sh
go get github.com/ydnikolaev/go-foundation/units/safehttp@v0.1.0
```

go-foundation is public: no GOPRIVATE, no token, no vendoring.

## Wire

1. Build one client at start-up: `c := safehttp.New(safehttp.Options{})`. It is safe for concurrent use.
2. Fetch with `c.Get(ctx, url)` or `c.Do(req)`, and close the response body.
3. Tell the refusals apart with `errors.Is`: `ErrBlocked` (a URL, a redirect hop or a resolved address that is not
   public), `ErrBodyTooLarge` (a read past the cap, or a declared `Content-Length` over it) and `ErrTooManyRedirects`.
   When a refusal came after the server answered, `errors.As` into `*StatusError` gives the `Status`.
4. `Blocked(netip.Addr)` answers whether one address is non-public, for a caller that validates an address itself.

Use it whenever code fetches a URL that a user, a source or a feed supplied, or calls an outside API: never the default
client for such a URL.

## Configure

`Options`, each zero field taking its default:

- `Timeout` bounds a whole request, redirects and body read included (`DefaultTimeout`, 10 s).
- `MaxBodyBytes` caps a response body (`DefaultMaxBodyBytes`, 2 MiB).
- `MaxRedirects` caps the redirect hops (`DefaultMaxRedirects`, 5; a negative value follows none).
- `AllowHosts` exempts host names or IP literals (no port) from the address check: for tests (an `httptest` server
  listens on loopback), not for production code.

## API

<!-- api: devkit foundation api; do not edit -->
```go
CONSTANTS
const (
	DefaultTimeout      = 10 * time.Second
	DefaultMaxBodyBytes = 2 << 20 // a fetched URL body is at most 2 MiB
	DefaultMaxRedirects = 5
)
VARIABLES
var (
	// ErrBlocked is returned (wrapped) when a URL, a redirect hop or a resolved address is not a public host.
	ErrBlocked = errors.New("safehttp: destination is not a public address")
	// ErrBodyTooLarge is returned (wrapped) by a response body read past the cap, or at once when the declared
	// Content-Length is over it.
	ErrBodyTooLarge = errors.New("safehttp: response body over the cap")
	// ErrTooManyRedirects is returned (wrapped) when a request redirects more than MaxRedirects times.
	ErrTooManyRedirects = errors.New("safehttp: too many redirects")
)
FUNCTIONS
func Blocked(ip netip.Addr) bool
TYPES
type Client struct {
	// Has unexported fields.
}
func New(opts Options) *Client
func (c *Client) Do(req *http.Request) (*http.Response, error)
func (c *Client) Get(ctx context.Context, url string) (*http.Response, error)
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
type StatusError struct {
	// Status is the HTTP status of the response that was refused.
	Status int
	// Has unexported fields.
}
func (e *StatusError) Error() string
func (e *StatusError) Unwrap() error
```

## Example

A loopback address is refused before any connection is made (this is `Example`, compiled and run by the unit's tests):

```go
c := safehttp.New(safehttp.Options{})

// An address inside the host's own network is refused before any connection is made.
_, err := c.Get(context.Background(), "http://127.0.0.1:8080/admin")
fmt.Println(errors.Is(err, safehttp.ErrBlocked))

// The check a caller can reuse on its own: is an address a public one?
fmt.Println(safehttp.Blocked(netip.MustParseAddr("10.0.0.1")), safehttp.Blocked(netip.MustParseAddr("93.184.216.34")))
// Output:
// true
// true false
```

## Extension points

- `Options` shapes the client: timeout, body cap, redirect cap and the test-only `AllowHosts`.
- `Blocked` is exported, so a caller can check an address it resolved itself with the same rule.
- Errors are wrapped sentinels: a caller maps them with `errors.Is` and `errors.As` where it likes.

## Do not

- Do not use the default `http.Client` for a URL you do not control.
- Do not set `AllowHosts` in production code: it turns the address check off for those hosts.
- Do not use it for a call that legitimately runs longer than the timeout (a language-model call, for one): build another
  client.
- Do not read the body without closing it.
- Do not copy this package. Extend it here; a copy carries `go-foundation-copy: safehttp@<sha>` and a removal row.
