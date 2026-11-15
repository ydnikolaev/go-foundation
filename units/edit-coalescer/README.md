# edit-coalescer

Sends the latest data of each remote object at most once per window. When a message, a status or a record is edited
many times a second but the remote allows one edit per object per second or so, `Put` as often as you like: one
goroutine per live object waits the window, sends the last data put (the trailing edge) and goes on while puts keep
landing during the edit. A sender's rate-limit answer holds that sender's edits back, and `Close` flushes what is
waiting. The edit call and the reading of its errors are yours (`Editor`, `Reader`); what happened comes back through
`Hooks`, so every log line is yours too. Standard library only.

## Install

```sh
go get github.com/ydnikolaev/go-foundation/units/edit-coalescer@v0.1.0
```

go-foundation is public: no GOPRIVATE, no token, no vendoring.

## Wire

1. Implement `Editor` over the remote call: `Edit(ctx, object, data) error`. It may run for several objects at once.
2. Implement `Reader` over that remote's errors, or leave `Config.Errors` nil (every error is then a plain failure):
   `NotModified` (the remote already holds the data: a success), `Refused` (sending again would get the same answer:
   the entry is dropped) and `FloodWait` (a rate limit and how long the remote asks to wait).
3. Build one `Coalescer` with `New(ctx, Config{...})` and keep it for the life of the process.
4. Call `Put(editor, senderID, tag, object, data)` for each change. It never blocks. `senderID` is the key of rate-limit
   holds (one sender's flood wait holds only that sender's edits); `tag` is your label, handed back in the hooks.
5. On shutdown call `Close(ctx)`; it sends each waiting object once, bounded by `CloseBound` and by `ctx`.

## Configure

A zero `Config` field takes its default.

| field | default | meaning |
|---|---|---|
| `Window` | `DefaultWindow`, 1.2 s | spacing of edits to one object |
| `EditTimeout` | `DefaultEditTimeout`, 5 s | bounds one edit call |
| `MaxHold` | `DefaultMaxHold`, 1 min | caps a rate-limit wait before the one retry |
| `MaxEntries` | `DefaultMaxEntries`, 4096 | live objects; a `Put` past it is dropped |
| `CloseBound` | `DefaultCloseBound`, 5 s | bounds `Close` in all |
| `OverflowEvery` | `DefaultOverflowEvery`, 1 min | spacing of `Hooks.Full` calls |
| `Now`, `Wait` | `time.Now`, a real timer | inject a clock and a wait in tests |
| `Errors` | a `Reader` that finds nothing special | your `Reader` |
| `Hooks` | none | `Edited`, `Full`, `Dropped`; a nil hook is skipped |

## API

<!-- api: devkit foundation api; do not edit -->
```go
CONSTANTS
const (
	// DefaultWindow is the spacing of edits to one object.
	DefaultWindow = 1200 * time.Millisecond
	// DefaultEditTimeout bounds one edit call.
	DefaultEditTimeout = 5 * time.Second
	// DefaultMaxHold caps the rate-limit wait a sender's edits sit out before the one retry.
	DefaultMaxHold = time.Minute
	// DefaultMaxEntries bounds the live objects; a Put past it is dropped.
	DefaultMaxEntries = 4096
	// DefaultCloseBound bounds Close in all.
	DefaultCloseBound = 5 * time.Second
	// DefaultOverflowEvery spaces the Hooks.Full calls of a full coalescer.
	DefaultOverflowEvery = time.Minute
)
TYPES
type Coalescer struct {
	// Has unexported fields.
}
func New(parent context.Context, cfg Config) *Coalescer
func (c *Coalescer) Close(ctx context.Context)
func (c *Coalescer) Len() int
func (c *Coalescer) Put(bot Editor, botID, tag, object, data string)
func (c *Coalescer) Wait()
type Config struct {
	Window, EditTimeout, MaxHold, CloseBound, OverflowEvery time.Duration
	MaxEntries                                              int
	Now                                                     func() time.Time
	Wait                                                    func(ctx context.Context, d time.Duration) error
	Errors                                                  Reader
	Hooks                                                   Hooks
}
type Editor interface {
	Edit(ctx context.Context, object, data string) error
}
type Hooks struct {
	// Edited is called once per edit sequence, after it.
	Edited func(Result)
	// Full is called when a Put is dropped for a full map, at most once per OverflowEvery; dropped counts the Puts
	// dropped since the last call.
	Full func(tag string, limit, dropped int)
	// Dropped is called by Close, once, with the number of objects it gave up on at its bound.
	Dropped func(n int)
}
type Reader interface {
	// NotModified reports that the remote already holds the data: the edit counts as a success.
	NotModified(err error) bool
	// Refused reports an answer that sending again would get again: the object's entry is dropped, not retried.
	Refused(err error) bool
	// FloodWait reports a rate limit and how long the remote asks to wait; a d of zero or less is one window.
	FloodWait(err error) (d time.Duration, ok bool)
}
type Result struct {
	Tag, Object string
	OK          bool
	Took        time.Duration
	Err         error // nil when OK
}
```

## Example

Three drafts of one message land inside a window; only the last is sent, once (this is `Example`, compiled and run by
the unit's tests; the test shuts the window by a injected `Wait`, a real caller leaves `Wait` nil):

```go
open := make(chan struct{})
c := editcoalescer.New(context.Background(), editcoalescer.Config{
	Wait: func(ctx context.Context, _ time.Duration) error {
		select {
		case <-open:
			return nil
		case <-ctx.Done():
			return fmt.Errorf("wait: %w", ctx.Err())
		}
	},
	Hooks: editcoalescer.Hooks{
		Edited: func(r editcoalescer.Result) { fmt.Println("sent", r.Object, "ok:", r.OK) },
	},
})

var ed printEditor
c.Put(ed, "sender-1", "draft", "msg-7", "he")
c.Put(ed, "sender-1", "draft", "msg-7", "hello")
c.Put(ed, "sender-1", "draft", "msg-7", "hello world")
fmt.Println("live objects:", c.Len())

close(open)
c.Wait()
c.Close(context.Background())
// Output:
// live objects: 1
// edit msg-7 hello world
// sent msg-7 ok: true
```

## Extension points

- `Editor` is the one port to the remote; `Reader` says what its errors mean (`NotModified`, `Refused`, `FloodWait`).
- `Hooks` (`Edited`, `Full`, `Dropped`) report outcomes: logging, metrics and alerts are the caller's.
- `Now` and `Wait` replace the clock and the timer, so a test runs without real waiting.

## Do not

- Do not call back into the coalescer from a hook.
- Do not expect every `Put` to be sent: the latest data of an object wins, and a `Put` past `MaxEntries` or after
  `Close` is dropped.
- Do not expect a retry of a refused or failed edit: a `Refused` answer drops the entry, and a failure waits for the
  next `Put`. Only a rate limit is retried, once, after the hold.
- Do not wait on `Wait` while still putting: it returns when no edit goroutine runs.
- Do not copy this package. Extend it here; a copy carries `go-foundation-copy: edit-coalescer@<sha>` and a removal row.
