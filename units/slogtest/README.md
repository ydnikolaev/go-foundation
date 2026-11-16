# slogtest

Test helpers for code that logs through the default `log/slog` logger. `CaptureLog` and `CaptureJSONLog` route the
default logger into a buffer for the length of one test and put it back when the test ends; `JSONLines` decodes the
buffer line by line and `WithMsg` keeps the lines of one message, so a test asserts on what was logged and with which
fields. Standard library only.

## Install

```sh
go get github.com/ydnikolaev/go-foundation/units/slogtest@v0.1.0
```

go-foundation is public: no GOPRIVATE, no token, no vendoring.

## Wire

1. In a test, call `CaptureLog(t)` for a text buffer (time dropped, so the lines are stable) or `CaptureJSONLog(t)` for a
   JSON buffer. Both swap `slog.Default()` and restore it in `t.Cleanup`.
2. Run the code under test; it logs through `slog` as usual.
3. Compare the text buffer as a string, or decode the JSON buffer with `JSONLines[any](t, buf)` and narrow it with
   `WithMsg(lines, "message")`. A line that is not JSON fails the test.

## Configure

Nothing to configure: no options, no environment. The value type of a decoded line is a parameter of `LogLine[V]`, so a
test writes `LogLine[any]` (JSON numbers decode as `float64`).

## API

<!-- api: devkit foundation api; do not edit -->
```go
FUNCTIONS
func CaptureJSONLog(t testing.TB) *bytes.Buffer
func CaptureLog(t testing.TB) *bytes.Buffer
TYPES
type LogLine[V any] map[string]V
func JSONLines[V any](t testing.TB, buf *bytes.Buffer) []LogLine[V]
func WithMsg[V any](lines []LogLine[V], msg string) []LogLine[V]
```

## Example

Capture the JSON log, then read back the lines of one message (this is `Example`, compiled and run by the unit's tests;
a real test passes its own `*testing.T` instead of the stand-in used there):

```go
buf := slogtest.CaptureJSONLog(t)

slog.Info("saved", "id", 7)
slog.Warn("slow")
slog.Info("saved", "id", 8)

lines := slogtest.JSONLines[any](t, buf)
for _, l := range slogtest.WithMsg(lines, "saved") {
	fmt.Println(l["msg"], l["id"])
}
// Output:
// saved 7
// saved 8
```

## Extension points

- `LogLine[V]` is generic over the value type, so a test can decode into a narrower type than `any`.
- The helpers take a `testing.TB`, so a test, a benchmark or a fake that fails by its own means all work.

## Do not

- Do not use it in production code: it replaces the process-wide default logger.
- Do not call it from a parallel test: the default logger is global, so two captures at once see each other's lines.
- Do not decode a text buffer with `JSONLines`: only a JSON buffer from `CaptureJSONLog` is JSON.
- Do not copy this package. Extend it here; a copy carries `go-foundation-copy: slogtest@<sha>` and a removal row.
