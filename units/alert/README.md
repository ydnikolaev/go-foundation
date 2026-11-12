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
go get github.com/ydnikolaev/go-foundation/units/alert@v0.1.0
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
