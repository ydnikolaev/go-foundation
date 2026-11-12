package alert

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeClock is a manual clock: AfterFunc timers fire inside Advance.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

type fakeTimer struct {
	at      time.Time
	f       func()
	stopped bool
}

func (t *fakeTimer) Stop() bool { was := !t.stopped; t.stopped = true; return was }

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) AfterFunc(d time.Duration, f func()) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{at: c.now.Add(d), f: f}
	c.timers = append(c.timers, t)
	return t
}

// Advance moves time forward and runs the timers that came due, outside the clock's lock.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	var due []*fakeTimer
	for _, t := range c.timers {
		if !t.stopped && !t.at.After(c.now) {
			t.stopped = true
			due = append(due, t)
		}
	}
	c.mu.Unlock()
	for _, t := range due {
		t.f()
	}
}

// recorder is a Notifier that keeps what it was given; gate, when set, blocks each Notify until it is closed.
type recorder struct {
	mu      sync.Mutex
	events  []Event
	calls   int
	err     func(call int) error
	gate    chan struct{}
	entered chan struct{}
	arrived chan struct{} // closed and replaced on every recorded event; made on first use
}

// arrivedLocked returns the channel that is closed when the next event is recorded.
func (r *recorder) arrivedLocked() chan struct{} {
	if r.arrived == nil {
		r.arrived = make(chan struct{})
	}
	return r.arrived
}

func (r *recorder) Notify(ctx context.Context, ev Event) error {
	r.mu.Lock()
	r.calls++
	call := r.calls
	r.mu.Unlock()
	if r.entered != nil {
		select {
		case r.entered <- struct{}{}:
		default:
		}
	}
	if r.gate != nil {
		select {
		case <-r.gate:
		case <-ctx.Done():
			return fmt.Errorf("recorder: %w", ctx.Err())
		}
	}
	if r.err != nil {
		if err := r.err(call); err != nil {
			return err
		}
	}
	r.mu.Lock()
	r.events = append(r.events, ev)
	close(r.arrivedLocked())
	r.arrived = nil
	r.mu.Unlock()
	return nil
}

func (r *recorder) sent() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Event(nil), r.events...)
}

func (r *recorder) waitSent(t *testing.T, n int) {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		r.mu.Lock()
		got, next := len(r.events), r.arrivedLocked()
		r.mu.Unlock()
		if got >= n {
			return
		}
		select {
		case <-next:
		case <-timeout:
			t.Fatalf("waiting for %d sends: got %d", n, len(r.sent()))
		}
	}
}

func instantSleep(sleeps *[]time.Duration, mu *sync.Mutex) func(context.Context, time.Duration) error {
	return func(_ context.Context, d time.Duration) error {
		mu.Lock()
		*sleeps = append(*sleeps, d)
		mu.Unlock()
		return nil
	}
}

func closeAlerter(t *testing.T, a *Alerter) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.Close(ctx); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func httpEvent(route string) Event {
	return Event{Kind: KindHTTP, Message: "request failed", Route: route, Status: 500, Code: "internal", ErrorClass: "*pgconn.PgError"}
}

func newTestAlerter(n Notifier, clk *fakeClock, mutate func(*Config)) *Alerter {
	cfg := Config{Clock: clk, Sleep: func(context.Context, time.Duration) error { return nil }}
	if mutate != nil {
		mutate(&cfg)
	}
	return New(n, cfg)
}

// Three events, one fingerprint, one window: one send at once, and the window's closing message carries the total.
func TestDedupSendsOnceThenTheTotal(t *testing.T) {
	t.Parallel()
	clk, rec := newFakeClock(), &recorder{}
	a := newTestAlerter(rec, clk, nil)
	for range 3 {
		a.Submit(httpEvent("GET /v1/x"))
	}
	rec.waitSent(t, 1)
	if got := rec.sent()[0]; got.Count != 1 {
		t.Errorf("first message count = %d, want 1", got.Count)
	}

	clk.Advance(DefaultWindow)
	rec.waitSent(t, 2)
	closeAlerter(t, a)

	sent := rec.sent()
	if len(sent) != 2 {
		t.Fatalf("sends = %d, want 1 + the closing count", len(sent))
	}
	if sent[1].Count != 3 {
		t.Errorf("closing count = %d, want 3", sent[1].Count)
	}
	if !strings.Contains(sent[1].Text(), "x3 in the window") {
		t.Errorf("closing text %q does not carry the count", sent[1].Text())
	}

	// A new window starts fresh: the next event sends at once again.
	if st := a.Stats(); st.Sent != 2 || st.Dropped != 0 || st.Failed != 0 {
		t.Errorf("stats = %+v, want 2 sent", st)
	}
}

func TestDedupWindowReopensAfterClose(t *testing.T) {
	t.Parallel()
	clk, rec := newFakeClock(), &recorder{}
	a := newTestAlerter(rec, clk, nil)
	a.Submit(httpEvent("GET /v1/x"))
	rec.waitSent(t, 1)
	clk.Advance(DefaultWindow) // a window with one event closes silently
	a.Submit(httpEvent("GET /v1/x"))
	rec.waitSent(t, 2)
	closeAlerter(t, a)
}

func TestDifferentFingerprintsEachSend(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	a := newTestAlerter(rec, newFakeClock(), nil)
	a.Submit(httpEvent("GET /v1/a"))
	a.Submit(httpEvent("GET /v1/b"))
	closeAlerter(t, a)
	if got := len(rec.sent()); got != 2 {
		t.Fatalf("sends = %d, want 2", got)
	}
}

// A full queue drops the new event, counts it, and leaves its fingerprint free for the next try.
func TestOverflowDropsAndCounts(t *testing.T) {
	t.Parallel()
	rec := &recorder{gate: make(chan struct{}), entered: make(chan struct{}, 1)}
	a := newTestAlerter(rec, newFakeClock(), func(c *Config) { c.QueueSize = 2 })

	a.Submit(httpEvent("GET /v1/0"))
	<-rec.entered // the sender holds event 0; the queue is empty and has room for two
	a.Submit(httpEvent("GET /v1/1"))
	a.Submit(httpEvent("GET /v1/2"))
	a.Submit(httpEvent("GET /v1/3")) // dropped
	a.Submit(httpEvent("GET /v1/4")) // dropped
	if got := a.Stats().Dropped; got != 2 {
		t.Fatalf("dropped = %d, want 2", got)
	}

	close(rec.gate)
	closeAlerter(t, a)
	if got := len(rec.sent()); got != 3 {
		t.Errorf("sends = %d, want 3 (events 0-2)", got)
	}
}

func TestCloseDrainsAndFlushesOpenWindows(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	a := newTestAlerter(rec, newFakeClock(), nil)
	for range 4 {
		a.Submit(httpEvent("GET /v1/x"))
	}
	a.Submit(httpEvent("GET /v1/y"))
	closeAlerter(t, a)

	sent := rec.sent()
	if len(sent) != 3 {
		t.Fatalf("sends = %d, want 2 firsts + the closing count of x", len(sent))
	}
	if last := sent[2]; last.Route != "GET /v1/x" || last.Count != 4 {
		t.Errorf("closing = %+v, want the route x with count 4", last)
	}

	a.Submit(httpEvent("GET /v1/z")) // after Close: dropped, never delivered
	if got := a.Stats().Dropped; got != 1 {
		t.Errorf("dropped after close = %d, want 1", got)
	}
	if got := len(rec.sent()); got != 3 {
		t.Errorf("an event submitted after Close was delivered")
	}
}

func TestCloseStopsWaitingWhenItsContextEnds(t *testing.T) {
	t.Parallel()
	rec := &recorder{gate: make(chan struct{}), entered: make(chan struct{}, 1)}
	a := newTestAlerter(rec, newFakeClock(), nil)
	a.Submit(httpEvent("GET /v1/x"))
	<-rec.entered

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := a.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close = %v, want the context deadline", err)
	}
}

func TestRateLimitWaitsOnceThenRetries(t *testing.T) {
	t.Parallel()
	var (
		mu     sync.Mutex
		sleeps []time.Duration
	)
	rec := &recorder{err: func(call int) error {
		if call == 1 {
			return &RateLimitError{RetryAfter: 120 * time.Second}
		}
		return nil
	}}
	a := newTestAlerter(rec, newFakeClock(), func(c *Config) { c.Sleep = instantSleep(&sleeps, &mu) })
	a.Submit(httpEvent("GET /v1/x"))
	closeAlerter(t, a)

	mu.Lock()
	defer mu.Unlock()
	if len(sleeps) != 1 || sleeps[0] != maxRetryAfter {
		t.Errorf("sleeps = %v, want one wait capped at %s", sleeps, maxRetryAfter)
	}
	if st := a.Stats(); st.Sent != 1 || st.Failed != 0 {
		t.Errorf("stats = %+v, want sent after the retry", st)
	}
}

func TestRateLimitTwiceDropsAndCounts(t *testing.T) {
	t.Parallel()
	rec := &recorder{err: func(int) error { return &RateLimitError{RetryAfter: time.Second} }}
	a := newTestAlerter(rec, newFakeClock(), nil)
	a.Submit(httpEvent("GET /v1/x"))
	closeAlerter(t, a)
	if rec.calls != 2 {
		t.Errorf("notify calls = %d, want 2 (one retry)", rec.calls)
	}
	if st := a.Stats(); st.Failed != 1 || st.Sent != 0 {
		t.Errorf("stats = %+v, want one failure", st)
	}
}

// A failing notifier is logged through the base logger and never fed back: one Notify, one log line, no loop.
func TestFailingNotifierDoesNotRecurse(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	base := slog.NewJSONHandler(&buf, nil)
	rec := &recorder{err: func(int) error { return errors.New("boom") }}
	a := newTestAlerter(rec, newFakeClock(), func(c *Config) { c.Log = slog.New(base) })
	logger := slog.New(NewHandler(base, a))

	logger.Error("request failed", "status", 500)
	closeAlerter(t, a)

	if rec.calls != 1 {
		t.Errorf("notify calls = %d, want 1: the failure was alerted about itself", rec.calls)
	}
	out := buf.String()
	if got := strings.Count(out, "alert: send failed"); got != 1 {
		t.Errorf("send-failed lines = %d, want 1:\n%s", got, out)
	}
}

func TestPanickingNotifierIsAFailureNotACrash(t *testing.T) {
	t.Parallel()
	a := newTestAlerter(NotifierFunc(func(context.Context, Event) error { panic("kaput") }), newFakeClock(), nil)
	a.Submit(httpEvent("GET /v1/x"))
	closeAlerter(t, a)
	if st := a.Stats(); st.Failed != 1 {
		t.Errorf("stats = %+v, want one failure", st)
	}
}

// NotifierFunc adapts a function (tests only).
type NotifierFunc func(context.Context, Event) error

func (f NotifierFunc) Notify(ctx context.Context, ev Event) error { return f(ctx, ev) }

func TestWebBudgetFoldsTheRest(t *testing.T) {
	t.Parallel()
	clk, rec := newFakeClock(), &recorder{}
	a := newTestAlerter(rec, clk, func(c *Config) { c.WebBudget = 2 })
	for i := range 5 {
		a.Submit(Event{Kind: KindWeb, Message: "web error: vue", ErrorClass: "TypeError", Fingerprint: strings.Repeat(string(rune('a'+i)), 8)})
	}
	a.Submit(httpEvent("GET /v1/x")) // api alerts are not starved by the web budget
	rec.waitSent(t, 3)
	clk.Advance(DefaultWindow)
	rec.waitSent(t, 4)
	closeAlerter(t, a)

	var web, api int
	var folded Event
	for _, ev := range rec.sent() {
		switch {
		case ev.Kind == KindHTTP:
			api++
		case ev.Count > 1:
			folded = ev
		default:
			web++
		}
	}
	if web != 2 || api != 1 || folded.Count != 3 {
		t.Errorf("web sent = %d, api = %d, folded count = %d; want 2, 1, 3", web, api, folded.Count)
	}
}

func TestOldestFingerprintIsEvicted(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	a := newTestAlerter(rec, newFakeClock(), func(c *Config) { c.MaxFingerprints = 2 })
	a.Submit(httpEvent("GET /v1/a"))
	a.Submit(httpEvent("GET /v1/b"))
	a.Submit(httpEvent("GET /v1/c")) // evicts a
	a.Submit(httpEvent("GET /v1/a")) // a is new again: sends at once
	closeAlerter(t, a)
	if got := len(rec.sent()); got != 4 {
		t.Errorf("sends = %d, want 4", got)
	}
}

func TestEventsCarryColourAndRelease(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	a := newTestAlerter(rec, newFakeClock(), func(c *Config) { c.Colour, c.Release = "blue", "v1.2.3" })
	a.Submit(httpEvent("GET /v1/x"))
	a.Submit(Event{Kind: KindWeb, Message: "web error: vue", Release: "web-9"})
	closeAlerter(t, a)
	for _, ev := range rec.sent() {
		if ev.Colour != "blue" {
			t.Errorf("%s colour = %q, want blue", ev.Kind, ev.Colour)
		}
		want := map[string]string{KindHTTP: "v1.2.3", KindWeb: "web-9"}[ev.Kind]
		if ev.Release != want {
			t.Errorf("%s release = %q, want %q", ev.Kind, ev.Release, want)
		}
	}
}

func TestMessagePrefixIsTheCallersOrTheDefault(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		prefix string
		want   string
	}{
		{"default", "", DefaultPrefix + "http\n"},
		{"configured", "svc alert: ", "svc alert: http\n"},
		{"control characters become spaces", "svc\nalert: ", "svc alert: http\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := &recorder{}
			a := newTestAlerter(rec, newFakeClock(), func(c *Config) { c.Prefix = tt.prefix })
			a.Submit(httpEvent("GET /v1/x"))
			closeAlerter(t, a)
			sent := rec.sent()
			if len(sent) != 1 {
				t.Fatalf("sent = %d, want 1", len(sent))
			}
			if got := sent[0].Text(); !strings.HasPrefix(got, tt.want) {
				t.Errorf("text = %q, want prefix %q", got, tt.want)
			}
		})
	}
}

func TestPaceWaitsBetweenSends(t *testing.T) {
	t.Parallel()
	var (
		mu     sync.Mutex
		sleeps []time.Duration
	)
	rec := &recorder{}
	a := newTestAlerter(rec, newFakeClock(), func(c *Config) { c.Sleep = instantSleep(&sleeps, &mu) })
	a.Submit(httpEvent("GET /v1/a"))
	a.Submit(httpEvent("GET /v1/b"))
	closeAlerter(t, a)
	mu.Lock()
	defer mu.Unlock()
	if len(sleeps) != 1 || sleeps[0] != DefaultPace {
		t.Errorf("sleeps = %v, want one pace wait of %s before the second send", sleeps, DefaultPace)
	}
}

func TestTextHoldsOnlyTheAllowlistedFields(t *testing.T) {
	t.Parallel()
	ev := Event{
		Kind: KindHTTP, Message: "request failed\nuser 7", Route: "GET /v1/x", Status: 500, Code: "internal",
		ErrorClass: "*pgconn.PgError", RequestID: "rid-1", Colour: "green", Release: "v1", Count: 2,
	}
	ev.Prefix = "svc alert: "
	text := ev.Text()
	for _, want := range []string{"svc alert: http", "route: GET /v1/x", "status: 500", "code: internal",
		"error class: *pgconn.PgError", "request: rid-1", "colour: green", "release: v1", "x2 in the window"} {
		if !strings.Contains(text, want) {
			t.Errorf("text lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "\nuser") {
		t.Errorf("a newline in a field broke out of its line:\n%s", text)
	}
	long := Event{Kind: KindHTTP, Message: strings.Repeat("я", 5000)}
	if n := len([]rune(long.Text())); n > maxTextLen {
		t.Errorf("text has %d characters, limit %d", n, maxTextLen)
	}
}
