package alert

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// collector is a Sink that keeps the events it is given.
type collector struct {
	mu     sync.Mutex
	events []Event
}

func (c *collector) Submit(ev Event) {
	c.mu.Lock()
	c.events = append(c.events, ev)
	c.mu.Unlock()
}

func (c *collector) got() []Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Event(nil), c.events...)
}

func teeLogger(buf io.Writer) (*slog.Logger, *collector) {
	c := &collector{}
	return slog.New(NewHandler(slog.NewJSONHandler(buf, nil), c)), c
}

// An Error record whose chain holds personal data produces an Event without it, and the base handler still gets
// the full record.
func TestTeeKeepsPersonalDataOut(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger, c := teeLogger(&buf)
	root := errors.New("user 42 a@b.c")
	chain := fmt.Errorf("address of account 42: %w", fmt.Errorf("send to a@b.c: %w", root))

	logger.Error("request failed", "error", chain, "code", "internal", "status", 500, "method", "POST",
		"path", "/v1/accounts/42", "request_id", "rid-1", "client_ip", "203.0.113.9", "email", "a@b.c")

	events := c.got()
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	text := events[0].Text()
	for _, leak := range []string{"42", "a@b.c", "203.0.113.9", "address of account", "/v1/accounts"} {
		if strings.Contains(text, leak) {
			t.Errorf("text leaks %q:\n%s", leak, text)
		}
	}
	for _, want := range []string{"http", "request failed", "status: 500", "code: internal", "*errors.errorString", "request: rid-1"} {
		if !strings.Contains(text, want) {
			t.Errorf("text lacks %q:\n%s", want, text)
		}
	}
	if !strings.Contains(buf.String(), "a@b.c") {
		t.Errorf("the base handler did not get the full record:\n%s", buf.String())
	}
}

func TestTeeIgnoresRecordsBelowError(t *testing.T) {
	t.Parallel()
	logger, c := teeLogger(io.Discard)
	logger.Debug("d")
	logger.Info("i")
	logger.Warn("w", "error", errors.New("x"))
	if got := c.got(); len(got) != 0 {
		t.Errorf("events = %v, want none", got)
	}
}

func TestTeeRequestIDFromWith(t *testing.T) {
	t.Parallel()
	logger, c := teeLogger(io.Discard)
	logger.With("request_id", "rid-9").Error("request failed", "status", 500)
	logger.With("request_id", "rid-9").WithGroup("g").With("request_id", "nested").Error("request failed", "route", "GET /nope")
	events := c.got()
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	if events[0].RequestID != "rid-9" {
		t.Errorf("request id = %q, want rid-9", events[0].RequestID)
	}
	if events[1].RequestID != "rid-9" || events[1].Route != "" {
		t.Errorf("grouped attributes reached the event: %+v", events[1])
	}
}

func TestTeeCronKeyOnlyOnCronRecords(t *testing.T) {
	t.Parallel()
	logger, c := teeLogger(io.Discard)
	logger.Error("cron: task failed", "key", "jobs.reap", "error", errors.New("x"))
	logger.Error("auth: repeated failure", "key", "login:1.2.3.4")
	events := c.got()
	if events[0].Task != "jobs.reap" {
		t.Errorf("cron task = %q, want jobs.reap", events[0].Task)
	}
	if events[1].Task != "" || strings.Contains(events[1].Text(), "1.2.3.4") {
		t.Errorf("a non-cron key entered the event: %+v", events[1])
	}
}

// Every known Error site, with its exact message and attributes, becomes one Event of the right kind.
func TestTeeKnownSites(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	tests := []struct {
		name  string
		msg   string
		attrs []any
		check func(Event) bool
		kind  string
	}{
		{"5xx", "request failed", []any{"error", boom, "code", "internal", "status", 500, "method", "GET", "path", "/v1/x", "route", "GET /v1/x"},
			func(e Event) bool { return e.Status == 500 && e.Code == "internal" && e.Route == "GET /v1/x" }, KindHTTP},
		{"marshal failure", "httpx: marshal response", []any{"error", boom},
			func(e Event) bool { return e.ErrorClass == "*errors.errorString" }, KindError},
		{"panic", "panic recovered", []any{"panic", "oops", "method", "GET", "path", "/v1/x", "stack", "goroutine 1", "route", "GET /v1/x"},
			func(e Event) bool { return e.ErrorClass == "string" && e.Route == "GET /v1/x" }, KindPanic},
		{"cron failed", "cron: task failed", []any{"key", "kv.purge", "error", boom},
			func(e Event) bool { return e.Task == "kv.purge" }, KindCron},
		{"cron deadline", "cron: task deadline exceeded", []any{"key", "jobs.reap", "timeout", "1m0s"},
			func(e Event) bool { return e.Task == "jobs.reap" }, KindCron},
		{"job dead", "jobs: attempt failed", []any{"id", int64(5), "kind", "mail.send", "attempt", 5, "max_attempts", 5, "dead", true, "error", boom},
			func(e Event) bool { return e.Task == "mail.send" }, KindJob},
		{"job outcome", "jobs: record outcome", []any{"id", int64(5), "kind", "mail.send", "error", boom},
			func(e Event) bool { return e.Task == "mail.send" }, KindJob},
		{"worker stopped", "jobs worker stopped", []any{"error", boom},
			func(e Event) bool { return e.ErrorClass == "*errors.errorString" }, KindWorker},
		{"bad signature", "billing: provider callback rejected", []any{"provider", "acme-pay", "error", boom, "bad_signature", true},
			func(e Event) bool { return e.Provider == "acme-pay" }, KindBilling},
		{"amount mismatch", "billing: provider callback amount differs from the invoice",
			[]any{"provider", "acme-pay", "invoice_id", int64(9), "amount", int64(1000), "error", boom},
			func(e Event) bool { return e.Provider == "acme-pay" }, KindBilling},
		{"dead charge", "billing: status unavailable for a dead charge, settling it failed", []any{"invoice_id", int64(9), "error", boom},
			func(e Event) bool { return e.ErrorClass == "*errors.errorString" }, KindBilling},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			logger, c := teeLogger(io.Discard)
			logger.Error(tt.msg, tt.attrs...)
			events := c.got()
			if len(events) != 1 {
				t.Fatalf("events = %d, want 1", len(events))
			}
			e := events[0]
			if e.Kind != tt.kind || e.Message != tt.msg || !tt.check(e) {
				t.Errorf("event = %+v, want kind %s and message %q", e, tt.kind, tt.msg)
			}
			for _, leak := range []string{"boom", "invoice_id", "1000", "goroutine", "/v1/x"} {
				if strings.Contains(strings.ReplaceAll(e.Text(), "GET /v1/x", ""), leak) {
					t.Errorf("text leaks %q:\n%s", leak, e.Text())
				}
			}
		})
	}
}

func TestTeeEnabledFollowsTheBase(t *testing.T) {
	t.Parallel()
	h := NewHandler(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}), NopSink{})
	if h.Enabled(context.Background(), slog.LevelInfo) || !h.Enabled(context.Background(), slog.LevelError) {
		t.Error("Enabled does not follow the base handler")
	}
}
