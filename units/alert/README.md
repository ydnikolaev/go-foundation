# alert

An operational alert seam: an event (a panic, a 5xx, a failed job) is rendered to plain text, deduplicated per failure
with a cooldown window, paced, and sent through a `Notifier`. Telegram and log adapters are included. Standard library
only.

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

## Configure

`alert.Config`: the zero value of every field means its default.

| field | meaning | default |
|---|---|---|
| `Prefix` | opening of every message, up to the kind; yours, with its trailing space | `alert: ` |
| `Colour`, `Release` | stamped on every event that carries none | empty |
| `QueueSize`, `Window`, `MaxFingerprints` | pending events; dedup window per failure; open windows kept | 256, 10 min, 1024 |
| `WebBudget` | `KindWeb` events sent per window across all clients; the rest fold into one count | 20 |
| `Pace`, `SendTimeout` | minimum gap between two sends; per delivery attempt | 3 s, 10 s |
| `Log` | the alerter's own failures | discarded |
| `Clock`, `Sleep` | test seams | wall clock, a timer |

`alert.TelegramConfig`: `Token` and `ChatID` (read them from your own configuration), optional `BaseURL` and `Client`.

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
