# alert

An operational alert seam: an event (a panic, a 5xx, a failed job) is rendered to plain text, deduplicated per failure
with a cooldown window, paced, and sent through a `Notifier`. Telegram and log adapters are included. Standard library
only.

Use when a service should tell its owner, in a chat message, that something broke: a 5xx, a recovered panic, a failed
job or cron task, a billing callback gone wrong, a crash reported by a web client. Not for metrics, tracing, log
shipping or paging with escalation; it sends short plain-text messages to one chat, and its dedup state lives in one
process.

## Install

```sh
go get github.com/ydnikolaev/go-foundation/units/alert@v0.1.1
```

go-foundation is public: no GOPRIVATE, no token, no vendoring.

## Wire

1. Build a notifier: `alert.NewTelegram(alert.TelegramConfig{Token, ChatID})`, or `alert.NewLog(logger)` for development
   and tests.
2. Start the sender: `a := alert.New(notifier, alert.Config{Prefix: "myservice alert: ", Colour: ..., Release: ...})`.
   Pass a base logger as `Config.Log` (never the one fronted by `Handler`, or a failing notifier alerts about itself).
3. Tee your logger: `slog.New(alert.NewHandler(baseHandler, a))`. Every record at Error or above becomes one `Event`,
   built from an attribute allowlist; the base handler still gets the record unchanged.
4. Producers that are not log calls (a crash report, a cron tick) call `a.Submit(alert.Event{...})`; code that must not
   depend on the alerter takes `alert.Sink`, and `alert.NopSink{}` drops everything.
5. On shutdown, after the HTTP server has stopped: `a.Close(ctx)` queues the closing counts and drains the queue.

A complete wiring, with Telegram and a prefix (this is `Example_telegram`, compiled by the unit's tests):

```go
notifier, err := alert.NewTelegram(alert.TelegramConfig{
	Token:  os.Getenv("ALERT_TELEGRAM_TOKEN"),
	ChatID: os.Getenv("ALERT_TELEGRAM_CHAT_ID"),
})
if err != nil {
	log.Fatal(err)
}
base := slog.NewJSONHandler(os.Stderr, nil)
alerter := alert.New(notifier, alert.Config{
	Prefix: "myservice alert: ", // the separator space is yours
	Log:    slog.New(base),      // the base logger, never the one fronted by the tee
})
logger := slog.New(alert.NewHandler(base, alerter))

logger.Error("request failed", "route", "GET /v1/items", "status", 500, "code", "internal")
// ... on shutdown, after the HTTP server has stopped:
_ = alerter.Close(ctx)
```

## Configure

`alert.Config`: the zero value of every field means its default.

| field | meaning | default |
|---|---|---|
| `Prefix` | opening of every message, up to the kind, stamped on every event that carries no `Event.Prefix`; the separator (the space before the kind) is yours, the unit adds nothing | `alert: ` |
| `Colour`, `Release` | stamped on every event that carries none | empty |
| `QueueSize`, `Window`, `MaxFingerprints` | pending events; dedup window per failure; open windows kept | 256, 10 min, 1024 |
| `WebBudget` | `KindWeb` events sent per window across all clients; the rest fold into one count | 20 |
| `Pace`, `SendTimeout` | minimum gap between two sends; per delivery attempt | 3 s, 10 s |
| `Log` | the alerter's own failures | discarded |
| `Clock`, `Sleep` | test seams | wall clock, a timer |

`Event.Prefix` is the same text set on one event; it wins over `Config.Prefix`. Non-printable characters in it (anything
`unicode.IsPrint` rejects) become spaces, like every other field.

`alert.TelegramConfig`: `Token` and `ChatID` (read them from your own configuration), optional `BaseURL` and `Client`.

### What the slog tee reads

`NewHandler` turns a record at Error or above into one `Event`. The kind comes from the record's message, which must be
a constant; an Error record matching none of these rows becomes `KindError`:

| message | kind |
|---|---|
| `request failed` | `KindHTTP` |
| `panic recovered` | `KindPanic` |
| starts with `cron: ` | `KindCron` |
| `jobs worker stopped` | `KindWorker` |
| starts with `jobs: ` | `KindJob` |
| starts with `billing: ` | `KindBilling` |
| anything else | `KindError` |

`KindWeb` is never derived from a record: submit those events yourself with `Submit`. Only these top-level attributes
are read, nothing else and nothing inside a group:

| attribute | goes to |
|---|---|
| `route` | `Event.Route` (a route template, never a concrete path) |
| `code` | `Event.Code` |
| `status` | `Event.Status` |
| `provider` | `Event.Provider` |
| `request_id` | `Event.RequestID` |
| `kind` | `Event.Task` (the job kind) |
| `key` | `Event.Task`, only on records whose message starts with `cron: ` |
| `error`, `panic` | `Event.ErrorClass`: the Go type of the root cause, never its text |

## API

<!-- api: devkit foundation api; do not edit -->
```go
CONSTANTS
const (
	KindHTTP    = "http"    // an API request answered 5xx
	KindPanic   = "panic"   // a recovered handler panic
	KindCron    = "cron"    // a scheduled task failed or ran past its deadline
	KindJob     = "job"     // a background job died
	KindWorker  = "worker"  // the jobs worker stopped
	KindBilling = "billing" // a payment callback or a charge went wrong
	KindWeb     = "web"     // a crash the web app reported
	KindError   = "error"   // any other Error-level record
)
const (
	// DefaultPrefix opens a message when neither the event nor the Alerter's Config names a prefix.
	DefaultPrefix = "alert: "
)
const (
	DefaultQueueSize       = 256
	DefaultWindow          = 10 * time.Minute
	DefaultMaxFingerprints = 1024
	DefaultWebBudget       = 20
	DefaultPace            = 3 * time.Second
	DefaultSendTimeout     = 10 * time.Second
)
const DefaultTelegramURL = "https://api.telegram.org"
TYPES
type Alerter struct {
	// Has unexported fields.
}
func New(n Notifier, cfg Config) *Alerter
func (a *Alerter) Close(ctx context.Context) error
func (a *Alerter) Stats() Stats
func (a *Alerter) Submit(ev Event)
type Clock interface {
	Now() time.Time
	AfterFunc(d time.Duration, f func()) Timer
}
type Config struct {
	QueueSize       int           // pending events; more are dropped and counted
	Window          time.Duration // dedup window per fingerprint
	MaxFingerprints int           // open windows kept; the oldest is evicted past this
	WebBudget       int           // KindWeb events sent per window across all clients; the rest fold into one count
	Pace            time.Duration // minimum gap between two sends
	SendTimeout     time.Duration // per delivery attempt
	Colour  string // the deployment colour, stamped on every event that carries none
	Release string // the build version, stamped on every event that carries none
	Prefix  string // the opening of every message, up to the kind, separator included; stamped on events with none; "" = DefaultPrefix
	// Log receives the Alerter's own failures. It must be the base logger, not the one fronted by Handler, or a
	// failing notifier would alert about itself. nil discards them.
	Log *slog.Logger
	Clock Clock                                            // nil = the wall clock
	Sleep func(ctx context.Context, d time.Duration) error // nil = a timer; tests inject an instant one
}
type Event struct {
	Kind        string // one of the Kind constants
	Message     string // a constant, developer-written description (the log message)
	Route       string // the route template: a mux pattern or a page route, never a concrete path or a query
	ErrorClass  string // the Go type of the root cause, or the web error class; never the error text
	Status      int    // HTTP status, 0 when none
	Code        string // the application's error code
	Task        string // the job kind or the cron key
	Provider    string // the payment provider
	Fingerprint string // a client-computed key (web events), hex
	RequestID   string
	Colour      string // the deployment colour; the Alerter stamps it
	Release     string // the build version (a web event carries the web build); the Alerter stamps it when empty
	Prefix      string // the opening of the message, up to the kind, separator included; the Alerter stamps it when empty
	Count       int    // how many times this fingerprint happened in the window; 0 reads as 1
}
func (e Event) Key() string
func (e Event) Text() string
type Handler struct {
	// Has unexported fields.
}
func NewHandler(base slog.Handler, sink Sink) *Handler
func (h *Handler) Enabled(ctx context.Context, l slog.Level) bool
func (h *Handler) Handle(ctx context.Context, r slog.Record) error
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler
func (h *Handler) WithGroup(name string) slog.Handler
type Log struct {
	// Has unexported fields.
}
func NewLog(l *slog.Logger) *Log
func (n *Log) Notify(ctx context.Context, ev Event) error
type NopSink struct{}
func (NopSink) Submit(Event)
type Notifier interface {
	Notify(ctx context.Context, ev Event) error
}
type RateLimitError struct{ RetryAfter time.Duration }
func (e *RateLimitError) Error() string
type Sink interface {
	Submit(ev Event)
}
type Stats struct {
	Sent    int64 // events handed to the notifier successfully
	Dropped int64 // events lost to a full queue or a closed Alerter, or abandoned on shutdown
	Failed  int64 // events the notifier could not deliver (after the one 429 retry)
}
type Telegram struct {
	// Has unexported fields.
}
func NewTelegram(cfg TelegramConfig) (*Telegram, error)
func (t *Telegram) Notify(ctx context.Context, ev Event) error
type TelegramConfig struct {
	Token  string // the bot token; the caller reads it from its own configuration
	ChatID string // the chat the alerts go to
	BaseURL string       // nil-value = DefaultTelegramURL; tests point it at a fake server
	Client  *http.Client // nil = a client with a 15 s timeout
}
type Timer interface{ Stop() bool }
```

## Example

An Alerter that sends through Telegram, fed by a slog tee (this is `Example_telegram`, compiled by the unit's tests, not run):

```go
	notifier, err := alert.NewTelegram(alert.TelegramConfig{
		Token:  os.Getenv("ALERT_TELEGRAM_TOKEN"),
		ChatID: os.Getenv("ALERT_TELEGRAM_CHAT_ID"),
	})
	if err != nil {
		log.Fatal(err)
	}
	base := slog.NewJSONHandler(os.Stderr, nil)
	alerter := alert.New(notifier, alert.Config{
		Prefix:  "myservice alert: ", // the separator space is yours
		Colour:  "blue",
		Release: "v1.2.3",
		Log:     slog.New(base), // the base logger, never the one fronted by the tee
	})
	logger := slog.New(alert.NewHandler(base, alerter))

	logger.Error("request failed", "route", "GET /v1/items", "status", 500, "code", "internal")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := alerter.Close(ctx); err != nil {
		log.Print(err)
	}
```

## Extension points

- `Notifier` (`Notify(ctx, Event) error`): your own adapter. Return `*alert.RateLimitError` to have the sender wait
  once and retry.
- `Sink` (`Submit(Event)`): what producers depend on; `Alerter` and `NopSink` implement it.
- `Config.Clock` (`Now`, `AfterFunc`) and `Config.Sleep`: drive time by hand in a test.

## Do not

- Do not put an error string, an address or a URL query in an `Event`: `Text` renders a fixed set of fields on purpose,
  and the `Handler` allowlist drops the rest. Keep `Message` a constant.
- Do not pass the logger fronted by `Handler` as `Config.Log`.
- Do not rely on deduplication across processes: its state is in-process.
- Do not copy this package. Extend it here; a copy carries `go-foundation-copy: alert@<sha>` and a removal row.
