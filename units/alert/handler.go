package alert

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
)

// Handler is the slog tee: it passes every record to the base handler unchanged and, for records at Error or
// above, also submits an Event built from an attribute allowlist. One hook covers every Error site, present
// and future, with no call at the site.
//
// What reaches the Event: the record's message (a constant by convention), the attributes route, code, status,
// provider, request_id and kind (the job kind), key (the cron key, only on "cron: " records: other sites log
// addresses under that name), and the class of the root cause under error or panic (its Go type,
// never its text). Everything else, the error chain included, is dropped here.
type Handler struct {
	base    slog.Handler
	sink    Sink
	pre     []slog.Attr // WithAttrs attributes made outside any group
	grouped bool        // a WithGroup was opened: later attributes are nested and never allowlisted
}

var _ slog.Handler = (*Handler)(nil)

// NewHandler wraps base. Events go to sink.
func NewHandler(base slog.Handler, sink Sink) *Handler { return &Handler{base: base, sink: sink} }

// Enabled implements slog.Handler.
func (h *Handler) Enabled(ctx context.Context, l slog.Level) bool { return h.base.Enabled(ctx, l) }

// WithAttrs implements slog.Handler.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := *h
	next.base = h.base.WithAttrs(attrs)
	if !h.grouped {
		next.pre = append(append([]slog.Attr(nil), h.pre...), attrs...)
	}
	return &next
}

// WithGroup implements slog.Handler.
func (h *Handler) WithGroup(name string) slog.Handler {
	next := *h
	next.base = h.base.WithGroup(name)
	if name != "" {
		next.grouped = true
	}
	return &next
}

// Handle implements slog.Handler. The base handler's result is returned; the alert path never fails a log call.
func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	err := h.base.Handle(ctx, r)
	if r.Level >= slog.LevelError {
		h.sink.Submit(h.event(r))
	}
	if err != nil {
		return fmt.Errorf("alert: base handler: %w", err)
	}
	return nil
}

func (h *Handler) event(r slog.Record) Event {
	ev := Event{Message: r.Message}
	cron := strings.HasPrefix(r.Message, "cron: ")
	apply := func(a slog.Attr) bool {
		v := a.Value.Resolve()
		switch a.Key {
		case "route":
			ev.Route = v.String()
		case "code":
			ev.Code = v.String()
		case "status":
			ev.Status = intOf(v)
		case "provider":
			ev.Provider = v.String()
		case "request_id":
			ev.RequestID = v.String()
		case "kind":
			ev.Task = v.String()
		case "key":
			if cron {
				ev.Task = v.String()
			}
		case "error", "panic":
			if ev.ErrorClass == "" || a.Key == "error" {
				ev.ErrorClass = classOf(v)
			}
		}
		return true
	}
	for _, a := range h.pre {
		apply(a)
	}
	if !h.grouped {
		r.Attrs(apply)
	}
	ev.Kind = kindOf(r.Message)
	return ev
}

// kindOf classifies a record by its message prefix: each Error site has a constant message.
func kindOf(msg string) string {
	switch {
	case msg == "request failed":
		return KindHTTP
	case msg == "panic recovered":
		return KindPanic
	case strings.HasPrefix(msg, "cron: "):
		return KindCron
	case msg == "jobs worker stopped":
		return KindWorker
	case strings.HasPrefix(msg, "jobs: "):
		return KindJob
	case strings.HasPrefix(msg, "billing: "):
		return KindBilling
	}
	return KindError
}

func intOf(v slog.Value) int {
	if v.Kind() == slog.KindInt64 {
		return int(v.Int64())
	}
	n, _ := strconv.Atoi(v.String())
	return n
}

// classOf is the Go type of the root cause of an error value (the walk errors.Unwrap does, following the first
// branch of a joined error), or of any other value (a recovered panic).
func classOf(v slog.Value) string {
	val := v.Any()
	err, ok := val.(error)
	if !ok {
		return fmt.Sprintf("%T", val)
	}
	for {
		var next error
		// The switch looks at which Unwrap method the value has, not at the error's own type, hence any(err).
		switch u := any(err).(type) {
		case interface{ Unwrap() error }:
			next = u.Unwrap()
		case interface{ Unwrap() []error }:
			if errs := u.Unwrap(); len(errs) > 0 {
				next = errs[0]
			}
		}
		if next == nil {
			break
		}
		err = next
	}
	return fmt.Sprintf("%T", err)
}
