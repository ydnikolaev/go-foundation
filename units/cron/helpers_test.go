package cron

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// waitGuard only bounds a wait that would otherwise hang a failing test; no
// passing test waits for it.
const waitGuard = 10 * time.Second

// signal wakes the waiters of a condition each time it is bumped.
type signal struct {
	mu sync.Mutex
	ch chan struct{}
}

func newSignal() *signal { return &signal{ch: make(chan struct{})} }

func (s *signal) bump() {
	s.mu.Lock()
	defer s.mu.Unlock()
	close(s.ch)
	s.ch = make(chan struct{})
}

func (s *signal) wait() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ch
}

// await blocks until cond holds, re-checking it after every bump of sig.
func await(t *testing.T, sig *signal, what string, cond func() bool) {
	t.Helper()
	guard := time.After(waitGuard)
	for {
		ch := sig.wait()
		if cond() {
			return
		}
		select {
		case <-ch:
		case <-guard:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// recv takes one value from ch.
func recv[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(waitGuard):
		t.Fatal("timed out waiting for a value")
	}
	var zero T
	return zero
}

type fakeTimer struct {
	at time.Time
	ch chan time.Time
}

// fakeClock is a Clock the test moves by hand.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []fakeTimer
	sig    *signal
}

func newFakeClock(now time.Time) *fakeClock {
	return &fakeClock{now: now, sig: newSignal()}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	ch := make(chan time.Time, 1)
	if d <= 0 {
		ch <- c.now
	} else {
		c.timers = append(c.timers, fakeTimer{at: c.now.Add(d), ch: ch})
	}
	c.mu.Unlock()
	c.sig.bump()
	return ch
}

// advance moves the clock to to and fires every timer due by then.
func (c *fakeClock) advance(to time.Time) {
	c.mu.Lock()
	c.now = to
	rest := c.timers[:0]
	for _, tm := range c.timers {
		if tm.at.After(to) {
			rest = append(rest, tm)
			continue
		}
		tm.ch <- to
	}
	c.timers = rest
	c.mu.Unlock()
	c.sig.bump()
}

func (c *fakeClock) has(at time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, tm := range c.timers {
		if tm.at.Equal(at) {
			return true
		}
	}
	return false
}

// await blocks until a timer is pending for the instant at.
func (c *fakeClock) await(t *testing.T, at time.Time) {
	t.Helper()
	await(t, c.sig, "a timer at "+at.Format(time.RFC3339), func() bool { return c.has(at) })
}

type logRec struct {
	msg   string
	attrs map[string]any
}

// logSink is a slog.Handler that keeps the records.
type logSink struct {
	mu   sync.Mutex
	recs []logRec
	sig  *signal
}

func newLogSink() *logSink { return &logSink{sig: newSignal()} }

func (l *logSink) Enabled(context.Context, slog.Level) bool { return true }

func (l *logSink) Handle(_ context.Context, r slog.Record) error {
	attrs := map[string]any{}
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.Any()
		return true
	})
	l.mu.Lock()
	l.recs = append(l.recs, logRec{msg: r.Message, attrs: attrs})
	l.mu.Unlock()
	l.sig.bump()
	return nil
}

func (l *logSink) WithAttrs([]slog.Attr) slog.Handler { return l }

func (l *logSink) WithGroup(string) slog.Handler { return l }

func (l *logSink) count(msg string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, r := range l.recs {
		if r.msg == msg {
			n++
		}
	}
	return n
}

func (l *logSink) first(msg string) (logRec, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, r := range l.recs {
		if r.msg == msg {
			return r, true
		}
	}
	return logRec{}, false
}

// await blocks until at least n records carry msg.
func (l *logSink) await(t *testing.T, msg string, n int) {
	t.Helper()
	await(t, l.sig, msg, func() bool { return l.count(msg) >= n })
}

// rig is a Runner on a fake clock with a log sink.
type rig struct {
	clock *fakeClock
	logs  *logSink
	r     *Runner
}

func newRig(t *testing.T, start time.Time, opts ...Option) *rig {
	t.Helper()
	g := &rig{clock: newFakeClock(start), logs: newLogSink()}
	g.r = New(slog.New(g.logs), g.clock, opts...)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), waitGuard)
		defer cancel()
		if err := g.r.Stop(ctx); err != nil {
			t.Errorf("cleanup Stop: %v", err)
		}
	})
	return g
}

func (g *rig) add(t *testing.T, task Task) {
	t.Helper()
	if err := g.r.Add(task); err != nil {
		t.Fatalf("Add(%q): %v", task.Key, err)
	}
}

func (g *rig) start(t *testing.T) {
	t.Helper()
	if err := g.r.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
}

// sendSlots is a Run that reports each slot it gets.
func sendSlots(ch chan<- time.Time) func(context.Context, time.Time) error {
	return func(_ context.Context, slot time.Time) error {
		ch <- slot
		return nil
	}
}

// epochSlot is now truncated to every from the Unix epoch, by seconds.
func epochSlot(now time.Time, every time.Duration) time.Time {
	secs := int64(every / time.Second)
	u := now.Unix()
	return time.Unix(u-u%secs, 0)
}

// utcPlus3 is a fixed zone three hours ahead of UTC with no daylight saving time;
// the tests pass it to WithLocation.
var utcPlus3 = time.FixedZone("+03", 3*3600)

func plus3(month time.Month, day, hour, minute int) time.Time {
	return time.Date(2026, month, day, hour, minute, 0, 0, utcPlus3)
}
