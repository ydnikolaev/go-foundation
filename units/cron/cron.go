// Package cron runs scheduled tasks in one process, one slot at a time.
//
// A task has a slot: an interval counted from the Unix epoch (Every) or a time
// of day in the runner's zone (Daily; WithLocation, default UTC). The runner
// sleeps by its Clock to the next slot boundary plus one second, never a
// ticker, and computes the slot from the clock when it wakes, so a late wake-up
// runs the slot it is in and skips none. The task's Run decides whether the slot is really its own (for example a database
// claim); the runner only calls it.
//
// Isolation: a task that is still running at its next slot skips that slot
// ("job busy"); a process-wide semaphore bounds the runs at once, and a task
// that waits for a permit past its slot skips it ("job skipped"); a panic is an
// error; a run that has not returned at its timeout plus a grace is leaked (Go
// cannot kill it): the runner logs "job leaked" once, calls OnLeak and halts, so
// no task of this runner starts again ("jobs halted").
//
// Stop cancels every run's context and waits a bounded time. A Run that returns
// an error after that cancel is no failed run: it can ask Runner.Stopping.
//
// The package imports only the standard library.
package cron

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// wakeLag is added to a slot boundary before the runner wakes.
	wakeLag = time.Second

	defaultStopTimeout = 5 * time.Second
	defaultGrace       = 10 * time.Second
	defaultPermits     = 2
)

// Errors of the runner.
var (
	// ErrLeaked is returned by Stop when a run was still out at its bound.
	ErrLeaked = errors.New("cron: run still out")
	// ErrStarted is returned by Add and Start once the runner has started.
	ErrStarted = errors.New("cron: runner started")
)

// Clock is the runner's time. After fires once, d from Now.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

// RealClock is the wall clock.
type RealClock struct{}

// Now is time.Now.
func (RealClock) Now() time.Time { return time.Now() }

// After is time.After.
func (RealClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Option configures a Runner.
type Option func(*Runner)

// WithStopTimeout bounds how long Stop waits for the runs (default 5s).
func WithStopTimeout(d time.Duration) Option { return func(r *Runner) { r.stopTimeout = d } }

// WithGrace is how long after its timeout a run may take to return before it is
// leaked (default 10s).
func WithGrace(d time.Duration) Option { return func(r *Runner) { r.grace = d } }

// WithLocation sets the zone Daily tasks are read in (default time.UTC). A nil
// zone keeps the default. Use a fixed zone (time.FixedZone) when the image has no
// tzdata or the zone has no daylight saving time; a zone that shifts its clock
// makes a daily slot fall on the shifted local time.
func WithLocation(loc *time.Location) Option {
	return func(r *Runner) {
		if loc != nil {
			r.loc = loc
		}
	}
}

// WithPermits sets the process-wide number of runs at once (default 2).
func WithPermits(n int) Option { return func(r *Runner) { r.permits = n } }

// WithOnLeak sets the function called once for each leaked run, with its key.
// It is called after the runner halted, and not for a run Stop finds out.
func WithOnLeak(f func(key string)) Option { return func(r *Runner) { r.onLeak = f } }

type task struct {
	Task
	busy atomic.Bool
}

// run is a run that has been started and has not returned.
type run struct {
	key    string
	slot   time.Time
	leaked bool // logged as leaked
}

// Runner runs tasks. Build it with New, Add the tasks, Start it, Stop it.
type Runner struct {
	log         *slog.Logger
	clock       Clock
	stopTimeout time.Duration
	grace       time.Duration
	permits     int
	onLeak      func(string)
	loc         *time.Location

	sem chan struct{}

	mu      sync.Mutex
	tasks   []*task
	started bool
	cancel  context.CancelFunc
	runs    map[uint64]*run
	nextID  uint64

	stopping atomic.Bool
	halted   atomic.Bool
	haltOnce sync.Once
	haltCh   chan struct{}
	abandon  chan struct{}
	live     tracker
}

// New builds a Runner. The logger and the clock are required.
func New(logger *slog.Logger, clock Clock, opts ...Option) *Runner {
	r := &Runner{
		log:         logger,
		clock:       clock,
		stopTimeout: defaultStopTimeout,
		grace:       defaultGrace,
		permits:     defaultPermits,
		loc:         time.UTC,
		runs:        map[uint64]*run{},
		haltCh:      make(chan struct{}),
		abandon:     make(chan struct{}),
	}
	for _, o := range opts {
		o(r)
	}
	if r.permits < 1 {
		r.permits = 1
	}
	r.sem = make(chan struct{}, r.permits)
	return r
}

// Add registers a task. It fails after Start, on a bad task or a repeated key.
func (r *Runner) Add(t Task) error {
	if err := t.validate(); err != nil {
		return fmt.Errorf("cron: task %q: %w", t.Key, err)
	}
	if t.Timeout == 0 {
		t.Timeout = DefaultTimeout
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started {
		return ErrStarted
	}
	for _, o := range r.tasks {
		if o.Key == t.Key {
			return fmt.Errorf("cron: task %q: duplicate key", t.Key)
		}
	}
	r.tasks = append(r.tasks, &task{Task: t})
	return nil
}

// Start begins every task: it tries the task's current slot at once (a daily
// task only if today's slot has passed), then sleeps to each next boundary. The
// runner stops when ctx ends or Stop is called.
func (r *Runner) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started {
		return ErrStarted
	}
	r.started = true
	ctx, r.cancel = context.WithCancel(ctx)
	for _, t := range r.tasks {
		r.live.add()
		go r.loop(ctx, t)
	}
	return nil
}

// Stopping reports that Stop has been called. A Run that returns a context
// error while it is true was cancelled by the stop, not failed.
func (r *Runner) Stopping() bool { return r.stopping.Load() }

// Stop cancels every run's context and waits until the runs return, ctx ends or
// the stop timeout passes. It logs "job leaked" for each run still out and
// returns ErrLeaked then.
func (r *Runner) Stop(ctx context.Context) error {
	if !r.stopping.CompareAndSwap(false, true) {
		return nil
	}
	r.mu.Lock()
	cancel := r.cancel
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	select {
	case <-r.live.idle():
		return nil
	case <-ctx.Done():
	case <-r.clock.After(r.stopTimeout):
	}
	close(r.abandon)

	r.mu.Lock()
	defer r.mu.Unlock()
	for _, o := range r.runs {
		if !o.leaked {
			o.leaked = true
			r.log.Error("job leaked", "key", o.key, "slot", o.slot, "stop", true)
		}
	}
	if len(r.runs) > 0 {
		return ErrLeaked
	}
	return nil
}

func (r *Runner) loop(ctx context.Context, t *task) {
	defer r.live.done()
	defer r.log.Debug("job loop stopped", "key", t.Key)
	if slot, ok := t.slotAt(r.clock.Now(), r.loc); ok {
		r.dispatch(ctx, t, slot)
	}
	for {
		now := r.clock.Now()
		wake := t.next(now, r.loc).Add(wakeLag).Sub(now)
		select {
		case <-ctx.Done():
			return
		case <-r.haltCh:
			return
		case <-r.clock.After(wake):
		}
		if slot, ok := t.slotAt(r.clock.Now(), r.loc); ok {
			r.dispatch(ctx, t, slot)
		}
	}
}

// dispatch starts the slot's run unless the runner halted or the task is busy.
func (r *Runner) dispatch(ctx context.Context, t *task, slot time.Time) {
	if r.halted.Load() || ctx.Err() != nil {
		return
	}
	if !t.busy.CompareAndSwap(false, true) {
		r.log.Warn("job busy", "key", t.Key, "slot", slot)
		return
	}
	r.live.add()
	go func() {
		defer r.live.done()
		r.runSlot(ctx, t, slot)
		t.busy.Store(false)
		r.log.Debug("job done", "key", t.Key, "slot", slot)
	}()
}

func (r *Runner) runSlot(ctx context.Context, t *task, slot time.Time) {
	for {
		if !r.acquire(ctx, t, slot) {
			return
		}
		leaked, err := r.exec(ctx, t, slot)
		if leaked {
			return
		}
		var held ErrHeld
		switch {
		case err == nil:
			return
		case errors.As(err, &held):
			if !r.waitRetry(ctx, t, slot, held.RetryAt) {
				return
			}
		case r.stopping.Load() && errors.Is(err, context.Canceled):
			r.log.Info("job cancelled", "key", t.Key, "slot", slot)
			return
		default:
			attrs := []any{"key", t.Key, "slot", slot, "err", err}
			var p *PanicError
			if errors.As(err, &p) {
				attrs = append(attrs, "stack", string(p.Stack))
			}
			r.log.Error("job failed", attrs...)
			return
		}
	}
}

// acquire takes a permit, or skips the slot when none frees up before the slot
// is over. A permit is released by the run goroutine when its Run returns.
func (r *Runner) acquire(ctx context.Context, t *task, slot time.Time) bool {
	got := false
	select {
	case r.sem <- struct{}{}:
		got = true
	default:
		wait := slot.Add(t.span()).Sub(r.clock.Now())
		if wait <= 0 {
			r.log.Warn("job skipped", "key", t.Key, "slot", slot)
			return false
		}
		select {
		case r.sem <- struct{}{}:
			got = true
		case <-r.clock.After(wait):
			r.log.Warn("job skipped", "key", t.Key, "slot", slot)
		case <-ctx.Done():
		case <-r.haltCh:
		}
	}
	if got && (r.halted.Load() || ctx.Err() != nil) {
		<-r.sem
		return false
	}
	return got
}

// waitRetry sleeps to retryAt and reports whether the slot is to run again.
func (r *Runner) waitRetry(ctx context.Context, t *task, slot, retryAt time.Time) bool {
	now := r.clock.Now()
	if !retryAt.After(now) || !t.current(slot, retryAt, r.loc) {
		return false
	}
	r.log.Debug("job held", "key", t.Key, "slot", slot, "retry", retryAt)
	select {
	case <-r.clock.After(retryAt.Sub(now)):
		return !r.halted.Load()
	case <-ctx.Done():
	case <-r.haltCh:
	}
	return false
}

// exec runs the task's Run under its timeout on a goroutine of its own and waits
// for it up to timeout plus grace. The run goroutine gives the permit back and
// forgets the run when Run really returns, even after it was declared leaked.
func (r *Runner) exec(parent context.Context, t *task, slot time.Time) (leaked bool, err error) {
	runCtx, cancel := context.WithTimeout(parent, t.Timeout)
	id := r.register(t.Key, slot)
	done := make(chan error, 1)
	r.live.add()
	go func() {
		defer r.live.done()
		err := safeRun(runCtx, t.Run, slot)
		cancel()
		<-r.sem
		r.forget(id)
		done <- err
	}()

	select {
	case err := <-done:
		return false, err
	case <-r.clock.After(t.Timeout + r.grace):
		select {
		case err := <-done:
			return false, err
		default:
		}
		r.leak(id, t.Key, slot)
	case <-r.abandon:
	}
	return true, nil
}

func (r *Runner) register(key string, slot time.Time) uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	r.runs[r.nextID] = &run{key: key, slot: slot}
	return r.nextID
}

func (r *Runner) forget(id uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.runs, id)
}

// leak declares a run leaked: one log line and one OnLeak call per run, then
// the halt.
func (r *Runner) leak(id uint64, key string, slot time.Time) {
	r.mu.Lock()
	o, ok := r.runs[id]
	first := ok && !o.leaked
	if first {
		o.leaked = true
	}
	r.mu.Unlock()
	if !first {
		return
	}
	r.log.Error("job leaked", "key", key, "slot", slot)
	r.haltOnce.Do(func() {
		r.halted.Store(true)
		close(r.haltCh)
		r.log.Error("jobs halted", "key", key)
	})
	if r.onLeak != nil {
		r.onLeak(key)
	}
}

// tracker counts goroutines and tells when there are none.
type tracker struct {
	mu      sync.Mutex
	n       int
	waiters []chan struct{}
}

func (t *tracker) add() {
	t.mu.Lock()
	t.n++
	t.mu.Unlock()
}

func (t *tracker) done() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.n--
	if t.n == 0 {
		for _, w := range t.waiters {
			close(w)
		}
		t.waiters = nil
	}
}

// idle is closed when the count is zero.
func (t *tracker) idle() <-chan struct{} {
	t.mu.Lock()
	defer t.mu.Unlock()
	ch := make(chan struct{})
	if t.n == 0 {
		close(ch)
	} else {
		t.waiters = append(t.waiters, ch)
	}
	return ch
}
