// Package alert is a service's own error tracking: an Error-level log record or a web crash report becomes one
// plain-text message to the owner, through a Notifier seam (Telegram is an adapter, the log adapter serves dev and
// tests).
//
// The message carries what kind of thing broke, where and how often, never who. An Event has a fixed set of
// fields and Text renders only those, so nothing else can enter a message: the caller cannot smuggle an error
// string, an address or a URL query through it. Handler (the slog tee) fills an Event from a log record by an
// attribute allowlist; Alerter queues, deduplicates and paces the sends off the request path.
package alert

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Event kinds. Kind says what sort of failure it is; the tee derives it from the log record (see Handler).
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

// Event is one alert. Every field is bounded and printable; Text renders exactly these and nothing more.
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
	Prefix      string // the opening of the message, up to the kind; the Alerter stamps it when empty, "alert: " if still empty
	Count       int    // how many times this fingerprint happened in the window; 0 reads as 1
}

// Notifier delivers one Event to the owner.
type Notifier interface {
	Notify(ctx context.Context, ev Event) error
}

// Sink accepts events without blocking and without failing: the producer is a request, a cron tick or a log call.
type Sink interface {
	Submit(ev Event)
}

// NopSink drops every event (a test or a deployment without alerting).
type NopSink struct{}

// Submit implements Sink.
func (NopSink) Submit(Event) {}

// maxFieldLen bounds every rendered value; maxTextLen is Telegram's message limit (4096 characters).
const (
	// DefaultPrefix opens a message when neither the event nor the Alerter's Config names a prefix.
	DefaultPrefix = "alert: "

	maxFieldLen = 160
	maxTextLen  = 4096
)

// Key identifies a failure for deduplication: a hash of what kind of failure it is and where, never of its varying
// detail. The request id, the colour, the release and the count are not part of it.
func (e Event) Key() string {
	h := sha256.New()
	for _, part := range []string{e.Kind, e.Message, e.Route, e.Code, e.ErrorClass, e.Task, e.Provider, e.Fingerprint} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Text renders the event as plain text for a chat message (no markup, no parse mode).
func (e Event) Text() string {
	var b strings.Builder
	prefix := e.Prefix
	if prefix == "" {
		prefix = DefaultPrefix
	}
	b.WriteString(cleanPrefix(prefix))
	b.WriteString(clean(e.Kind))
	b.WriteString("\n")
	line := func(label, value string) {
		if value = clean(value); value != "" {
			b.WriteString(label + ": " + value + "\n")
		}
	}
	line("what", e.Message)
	line("route", e.Route)
	if e.Status != 0 {
		line("status", strconv.Itoa(e.Status))
	}
	line("code", e.Code)
	line("error class", e.ErrorClass)
	line("task", e.Task)
	line("provider", e.Provider)
	line("fingerprint", e.Fingerprint)
	line("request", e.RequestID)
	line("colour", e.Colour)
	line("release", e.Release)
	if e.Count > 1 {
		line("count", "x"+strconv.Itoa(e.Count)+" in the window")
	}
	return truncate(strings.TrimRight(b.String(), "\n"), maxTextLen)
}

// clean makes a value safe for one line of plain text: control characters become spaces and the length is bounded.
func clean(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return ' '
		}
		return r
	}, s)
	return truncate(strings.TrimSpace(s), maxFieldLen)
}

// cleanPrefix is clean for the prefix, which keeps its trailing space: the prefix runs straight into the kind.
func cleanPrefix(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return ' '
		}
		return r
	}, s)
	return truncate(strings.TrimLeft(s, " "), maxFieldLen)
}

// truncate cuts s to at most n runes.
func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
