package alert

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// Defaults of the Alerter. Pace keeps a single chat inside Telegram's limit for groups (20 messages a minute); the queue and the fingerprint table are bounded so a storm costs memory once.
const (
	DefaultQueueSize       = 256
	DefaultWindow          = 10 * time.Minute
	DefaultMaxFingerprints = 1024
	DefaultWebBudget       = 20
	DefaultPace            = 3 * time.Second
	DefaultSendTimeout     = 10 * time.Second
	// maxRetryAfter caps how long a Telegram 429 may park the sender.
	maxRetryAfter = 60 * time.Second
)

// Timer is what Clock.AfterFunc returns (*time.Timer satisfies it).
type Timer interface{ Stop() bool }

// Clock is the time source of the Alerter; tests inject a fake one.
type Clock interface {
	Now() time.Time
	AfterFunc(d time.Duration, f func()) Timer
}

type realClock struct{}

func (realClock) Now() time.Time                            { return time.Now() }
func (realClock) AfterFunc(d time.Duration, f func()) Timer { return time.AfterFunc(d, f) }

// RateLimitError is what a Notifier returns when the provider asked it to slow down.
type RateLimitError struct{ RetryAfter time.Duration }

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("alert: rate limited, retry after %s", e.RetryAfter)
}

// Config tunes an Alerter; the zero value of every field means its default.
type Config struct {
	QueueSize       int           // pending events; more are dropped and counted
	Window          time.Duration // dedup window per fingerprint
	MaxFingerprints int           // open windows kept; the oldest is evicted past this
	WebBudget       int           // KindWeb events sent per window across all clients; the rest fold into one count
	Pace            time.Duration // minimum gap between two sends
	SendTimeout     time.Duration // per delivery attempt

	Colour  string // the deployment colour, stamped on every event that carries none
	Release string // the build version, stamped on every event that carries none
	Prefix  string // the opening of every message, up to the kind, stamped on every event that carries none; "" = DefaultPrefix

	// Log receives the Alerter's own failures. It must be the base logger, not the one fronted by Handler, or a
	// failing notifier would alert about itself. nil discards them.
	Log *slog.Logger

	Clock Clock                                            // nil = the wall clock
	Sleep func(ctx context.Context, d time.Duration) error // nil = a timer; tests inject an instant one
}

// Stats are the Alerter's counters.
type Stats struct {
	Sent    int64 // events handed to the notifier successfully
	Dropped int64 // events lost to a full queue or a closed Alerter, or abandoned on shutdown
	Failed  int64 // events the notifier could not deliver (after the one 429 retry)
}

// Alerter sits between the producers and a Notifier: a bounded queue, one sender goroutine, deduplication per
// fingerprint per window and a global send pace. The first event of a fingerprint is sent at once; later ones only
// count, and the window's closing message carries the total. State is in-process (two colours may each send one
// message for one fault during a flip).
type Alerter struct {
	n   Notifier
	cfg Config
	log *slog.Logger
	clk Clock

	queue chan Event

	mu       sync.Mutex
	closed   bool
	entries  map[string]*entry
	order    *list.List // *entry, oldest first
	webTimer Timer
	webSent  int
	webFold  int

	sendCtx context.Context
	cancel  context.CancelFunc
	done    chan struct{}

	sent, dropped, failed atomic.Int64
}

type entry struct {
	key   string
	ev    Event
	count int
	timer Timer
	elem  *list.Element
}

// New starts an Alerter delivering through n. Close it on shutdown.
func New(n Notifier, cfg Config) *Alerter {
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = DefaultQueueSize
	}
	if cfg.Window <= 0 {
		cfg.Window = DefaultWindow
	}
	if cfg.MaxFingerprints <= 0 {
		cfg.MaxFingerprints = DefaultMaxFingerprints
	}
	if cfg.WebBudget <= 0 {
		cfg.WebBudget = DefaultWebBudget
	}
	if cfg.Pace <= 0 {
		cfg.Pace = DefaultPace
	}
	if cfg.SendTimeout <= 0 {
		cfg.SendTimeout = DefaultSendTimeout
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	if cfg.Clock == nil {
		cfg.Clock = realClock{}
	}
	if cfg.Sleep == nil {
		cfg.Sleep = sleep
	}
	ctx, cancel := context.WithCancel(context.Background())
	a := &Alerter{
		n: n, cfg: cfg, log: cfg.Log, clk: cfg.Clock,
		queue:   make(chan Event, cfg.QueueSize),
		entries: map[string]*entry{}, order: list.New(),
		sendCtx: ctx, cancel: cancel, done: make(chan struct{}),
	}
	go a.run()
	return a
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("alert: wait cancelled: %w", ctx.Err())
	}
}

// Stats returns the counters.
func (a *Alerter) Stats() Stats {
	return Stats{Sent: a.sent.Load(), Dropped: a.dropped.Load(), Failed: a.failed.Load()}
}

// Submit implements Sink. It never blocks and never fails.
func (a *Alerter) Submit(ev Event) {
	ev.Count = 1
	ev = a.stamped(ev)
	key := ev.Key()

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		a.dropped.Add(1)
		return
	}
	if e, open := a.entries[key]; open {
		e.count++
		return
	}
	if ev.Kind == KindWeb && !a.takeWebBudgetLocked() {
		return
	}
	if !a.enqueueLocked(ev) {
		return // not registered: the next event of this fingerprint tries again
	}
	if a.order.Len() >= a.cfg.MaxFingerprints {
		a.evictOldestLocked()
	}
	e := &entry{key: key, ev: ev, count: 1}
	e.elem = a.order.PushBack(e)
	a.entries[key] = e
	e.timer = a.clk.AfterFunc(a.cfg.Window, func() { a.closeWindow(e) })
}

// enqueueLocked puts ev on the queue unless it is full, in which case the drop is counted.
func (a *Alerter) enqueueLocked(ev Event) bool {
	select {
	case a.queue <- ev:
		return true
	default:
		if n := a.dropped.Add(1); n == 1 || n%100 == 0 {
			a.log.Warn("alert: queue full, events dropped", "dropped", n)
		}
		return false
	}
}

func (a *Alerter) evictOldestLocked() {
	front := a.order.Front()
	if front == nil {
		return
	}
	a.removeLocked(front.Value.(*entry))
}

func (a *Alerter) removeLocked(e *entry) {
	if e.timer != nil {
		e.timer.Stop()
	}
	a.order.Remove(e.elem)
	delete(a.entries, e.key)
}

// takeWebBudgetLocked counts one web event against the window's budget: web events come from anonymous clients,
// so random fingerprints must not starve the service's own alerts. It reports whether the event may be sent.
func (a *Alerter) takeWebBudgetLocked() bool {
	if a.webTimer == nil {
		a.webSent, a.webFold = 0, 0
		a.webTimer = a.clk.AfterFunc(a.cfg.Window, a.closeWebWindow)
	}
	if a.webSent < a.cfg.WebBudget {
		a.webSent++
		return true
	}
	a.webFold++
	return false
}

func (a *Alerter) closeWebWindow() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return
	}
	a.flushWebLocked()
}

func (a *Alerter) flushWebLocked() {
	if a.webFold > 0 {
		a.enqueueLocked(a.stamped(Event{Kind: KindWeb, Message: "web errors over the window budget", Count: a.webFold}))
	}
	a.webTimer, a.webSent, a.webFold = nil, 0, 0
}

func (a *Alerter) stamped(ev Event) Event {
	if ev.Colour == "" {
		ev.Colour = a.cfg.Colour
	}
	if ev.Release == "" {
		ev.Release = a.cfg.Release
	}
	if ev.Prefix == "" {
		ev.Prefix = a.cfg.Prefix
	}
	return ev
}

// closeWindow ends e's window: a fingerprint that repeated sends its total as a closing message.
func (a *Alerter) closeWindow(e *entry) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || a.entries[e.key] != e {
		return
	}
	a.removeLocked(e)
	if e.count > 1 {
		closing := e.ev
		closing.Count = e.count
		a.enqueueLocked(closing)
	}
}

// Close stops accepting events, queues the closing counts of the open windows, and waits until the queue is
// delivered or ctx ends (then the delivery in flight is cancelled and what remains is counted as dropped). Call it
// after the HTTP server has shut down, so the last requests' alerts are in. Events submitted after Close are
// dropped.
func (a *Alerter) Close(ctx context.Context) error {
	a.mu.Lock()
	if !a.closed {
		a.closed = true
		for e := a.order.Front(); e != nil; e = a.order.Front() {
			ent := e.Value.(*entry)
			a.removeLocked(ent)
			if ent.count > 1 {
				closing := ent.ev
				closing.Count = ent.count
				a.enqueueLocked(closing)
			}
		}
		if a.webTimer != nil {
			a.webTimer.Stop()
		}
		a.flushWebLocked()
		close(a.queue)
	}
	a.mu.Unlock()

	select {
	case <-a.done:
		a.cancel()
		return nil
	case <-ctx.Done():
		a.cancel()
		<-a.done
		return fmt.Errorf("alert: close before the queue drained: %w", ctx.Err())
	}
}

func (a *Alerter) run() {
	defer close(a.done)
	var last time.Time
	for ev := range a.queue {
		if a.sendCtx.Err() != nil {
			a.dropped.Add(1)
			continue
		}
		if !last.IsZero() {
			if wait := a.cfg.Pace - a.clk.Now().Sub(last); wait > 0 {
				_ = a.cfg.Sleep(a.sendCtx, wait) // a cancelled wait is caught by the next send's context
			}
		}
		a.deliver(ev)
		last = a.clk.Now()
	}
}

// deliver sends ev, waiting out one provider rate-limit answer (capped), then gives up on it.
func (a *Alerter) deliver(ev Event) {
	err := a.notify(ev)
	var rl *RateLimitError
	if errors.As(err, &rl) {
		if wait := min(rl.RetryAfter, maxRetryAfter); wait > 0 {
			_ = a.cfg.Sleep(a.sendCtx, wait)
		}
		err = a.notify(ev)
	}
	if err != nil {
		a.failed.Add(1)
		a.log.Error("alert: send failed", "kind", ev.Kind, "error", err)
		return
	}
	a.sent.Add(1)
}

func (a *Alerter) notify(ev Event) (err error) {
	ctx, cancel := context.WithTimeout(a.sendCtx, a.cfg.SendTimeout)
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("alert: notifier panicked: %v", r)
		}
	}()
	if err := a.n.Notify(ctx, ev); err != nil {
		return fmt.Errorf("alert: notify: %w", err)
	}
	return nil
}
