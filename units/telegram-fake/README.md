# telegram-fake

A fake Telegram Bot API server for tests. `New` starts a local HTTP server that keeps the URL path and the JSON body of
each call it receives and answers every call with one reply the caller gave; the code under test points its Bot API
client at `URL`, and the test reads the calls back with `Calls`. Standard library only.

## Install

```sh
go get github.com/ydnikolaev/go-foundation/units/telegram-fake@v0.1.0
```

go-foundation is public: no GOPRIVATE, no token, no vendoring.

## Wire

1. In a test, call `s := telegramfake.New(reply)` with the Telegram JSON body every call is answered with, and
   `t.Cleanup(s.Close)`.
2. Point the Bot API client under test at `s.URL` (no trailing slash) as its API root.
3. Run the code, then compare `s.Calls()` with the paths and bodies you expect.

## Configure

The one argument of `New` is the reply, written as is for every call: `{"ok":true,"result":...}` for a success, or a
refusal body such as `{"ok":false,"error_code":400,"description":"Bad Request"}`. Nothing else is configurable.

## API

<!-- api: devkit foundation api; do not edit -->
```go
TYPES
type Call struct {
	Path string
	Body json.RawMessage
}
type Server struct {
	// URL is the base URL of the fake, with no trailing slash: the Bot API client's API root.
	URL string
	// Has unexported fields.
}
func New(reply string) *Server
func (s *Server) Calls() []Call
func (s *Server) Close()
```

## Example

Start the fake, send one call, read it back (this is `Example`, compiled and run by the unit's tests; a plain POST
stands in for the client under test):

```go
s := telegramfake.New(`{"ok":true,"result":true}`)
defer s.Close()

resp, err := http.Post(s.URL+"/bot1:x/sendMessage", "application/json", strings.NewReader(`{"chat_id":7}`))
if err != nil {
	fmt.Println(err)
	return
}
answer, _ := io.ReadAll(resp.Body)
_ = resp.Body.Close()

fmt.Println(string(answer))
for _, c := range s.Calls() {
	fmt.Println(c.Path, string(c.Body))
}
// Output:
// {"ok":true,"result":true}
// /bot1:x/sendMessage {"chat_id":7}
```

## Extension points

- `Calls` returns `Call` values with the raw path and body, so a test decodes the body into whatever type it needs.
- The reply is a plain string, so a test can start one server per scenario (a success, a refusal, a flood-wait body).

## Do not

- Do not use it in production code: it starts a local HTTP server.
- Do not expect it to know Bot API methods: every path gets the same reply, whatever the method.
- Do not forget `Close`: the server runs until it is closed.
- Do not copy this package. Extend it here; a copy carries `go-foundation-copy: telegram-fake@<sha>` and a removal row.
