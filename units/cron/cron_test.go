package cron

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var base = time.Date(2026, time.November, 12, 12, 0, 30, 0, time.UTC)

// take receives the next slot and waits for the run goroutine to finish, so the
// task is no longer busy when the test moves the clock again.
func take(t *testing.T, g *rig, slots <-chan time.Time, done *int) time.Time {
	t.Helper()
	slot := recv(t, slots)
	*done++
	g.logs.await(t, "job done", *done)
	return slot
}

func TestRunnerEvery(t *testing.T) {
	tests := []struct {
		name  string
		every time.Duration
		start time.Time
	}{
		{"one minute", time.Minute, base},
		{"ten minutes", 10 * time.Minute, time.Date(2026, time.November, 12, 12, 3, 0, 0, time.UTC)},
		{"seven minutes is counted from the epoch, not the hour", 7 * time.Minute, base},
		{"one day", 24 * time.Hour, base},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := newRig(t, tt.start)
			slots := make(chan time.Time, 16)
			g.add(t, Task{Key: "k", Every: tt.every, Run: sendSlots(slots)})
			g.start(t)
			done := 0

			want := epochSlot(tt.start, tt.every)
			if got := take(t, g, slots, &done); !got.Equal(want) {
				t.Fatalf("slot at start = %v, want %v", got, want)
			}
			for i := range 3 {
				want = want.Add(tt.every)
				wake := want.Add(time.Second)
				g.clock.await(t, wake)
				g.clock.advance(wake)
				if got := take(t, g, slots, &done); !got.Equal(want) {
					t.Fatalf("slot %d = %v, want %v", i, got, want)
				}
			}

			// A late wake-up runs the slot it is in and skips none that it can name.
			g.clock.await(t, want.Add(tt.every+time.Second))
			late := want.Add(tt.every * 5 / 2)
			g.clock.advance(late)
			if got, wantLate := take(t, g, slots, &done), epochSlot(late, tt.every); !got.Equal(wantLate) {
				t.Fatalf("late slot = %v, want %v", got, wantLate)
			}
		})
	}
}

func TestRunnerDailyZone(t *testing.T) {
	type step struct {
		boundary  time.Time // the slot the sleeping runner is waiting for
		advanceTo time.Time // zero: boundary plus the 1 s lag
	}
	tests := []struct {
		name  string
		daily Daily
		start time.Time
		first time.Time // zero: nothing to catch up
		steps []step
	}{
		{
			name:  "slot of today passed at start",
			daily: Daily{0, 5},
			start: time.Date(2026, time.November, 12, 20, 0, 0, 0, time.UTC), // 23:00 +03
			first: plus3(time.November, 12, 0, 5),
			steps: []step{{boundary: plus3(time.November, 13, 0, 5)}, {boundary: plus3(time.November, 14, 0, 5)}},
		},
		{
			name:  "slot of today not yet",
			daily: Daily{23, 30},
			start: time.Date(2026, time.November, 12, 12, 0, 0, 0, time.UTC), // 15:00 +03
			steps: []step{{boundary: plus3(time.November, 12, 23, 30)}, {boundary: plus3(time.November, 13, 23, 30)}},
		},
		{
			name:  "late wake-up runs the day it is in",
			daily: Daily{12, 0},
			start: time.Date(2026, time.November, 12, 3, 0, 0, 0, time.UTC), // 06:00 +03
			steps: []step{
				{boundary: plus3(time.November, 12, 12, 0)},
				{boundary: plus3(time.November, 13, 12, 0), advanceTo: plus3(time.November, 13, 18, 0)},
			},
		},
		{
			name:  "month end",
			daily: Daily{0, 5},
			start: time.Date(2026, time.November, 30, 20, 0, 0, 0, time.UTC), // 23:00 +03
			first: plus3(time.November, 30, 0, 5),
			steps: []step{{boundary: plus3(time.December, 1, 0, 5)}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := newRig(t, tt.start, WithLocation(utcPlus3))
			slots := make(chan time.Time, 16)
			d := tt.daily
			g.add(t, Task{Key: "k", Daily: &d, Run: sendSlots(slots)})
			g.start(t)
			done := 0

			if !tt.first.IsZero() {
				if got := take(t, g, slots, &done); !got.Equal(tt.first) {
					t.Fatalf("slot at start = %v, want %v", got, tt.first)
				}
			}
			for i, s := range tt.steps {
				wake := s.boundary.Add(time.Second)
				g.clock.await(t, wake)
				to := s.advanceTo
				if to.IsZero() {
					to = wake
				}
				g.clock.advance(to)
				if got := take(t, g, slots, &done); !got.Equal(s.boundary) {
					t.Fatalf("slot %d = %v, want %v", i, got, s.boundary)
				}
			}
		})
	}
}

func TestRunnerDailyDefaultsToUTC(t *testing.T) {
	start := time.Date(2026, time.November, 12, 8, 0, 0, 0, time.UTC)
	g := newRig(t, start)
	slots := make(chan time.Time, 4)
	g.add(t, Task{Key: "k", Daily: &Daily{9, 0}, Run: sendSlots(slots)})
	g.start(t)
	done := 0

	boundary := time.Date(2026, time.November, 12, 9, 0, 0, 0, time.UTC)
	g.clock.await(t, boundary.Add(time.Second))
	g.clock.advance(boundary.Add(time.Second))
	if got := take(t, g, slots, &done); !got.Equal(boundary) {
		t.Fatalf("slot = %v, want %v", got, boundary)
	}
}

func TestWithLocationNilKeepsUTC(t *testing.T) {
	if r := New(slog.Default(), RealClock{}, WithLocation(nil)); r.loc != time.UTC {
		t.Errorf("zone = %v, want UTC", r.loc)
	}
}

func TestRunnerCatchUp(t *testing.T) {
	daily := func(h, m int) *Daily { return &Daily{h, m} }
	tests := []struct {
		name  string
		task  Task
		start time.Time
		want  time.Time // zero: no run at start
		sleep time.Time // the timer the idle runner waits on
	}{
		{
			name:  "every runs its current slot at start",
			task:  Task{Every: time.Hour},
			start: base,
			want:  time.Date(2026, time.November, 12, 12, 0, 0, 0, time.UTC),
		},
		{
			name:  "daily whose slot passed runs it at start",
			task:  Task{Daily: daily(9, 0)},
			start: time.Date(2026, time.November, 12, 12, 0, 0, 0, time.UTC), // 15:00 +03
			want:  plus3(time.November, 12, 9, 0),
		},
		{
			name:  "daily whose slot has not come does not",
			task:  Task{Daily: daily(18, 0)},
			start: time.Date(2026, time.November, 12, 12, 0, 0, 0, time.UTC),
			sleep: plus3(time.November, 12, 18, 0).Add(time.Second),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := newRig(t, tt.start, WithLocation(utcPlus3))
			var calls atomic.Int32
			slots := make(chan time.Time, 4)
			tt.task.Key = "k"
			tt.task.Run = func(_ context.Context, slot time.Time) error {
				calls.Add(1)
				slots <- slot
				return nil
			}
			g.add(t, tt.task)
			g.start(t)

			if tt.want.IsZero() {
				g.clock.await(t, tt.sleep)
				if n := calls.Load(); n != 0 {
					t.Fatalf("Run called %d times at start, want 0", n)
				}
				return
			}
			if got := recv(t, slots); !got.Equal(tt.want) {
				t.Fatalf("slot at start = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRunnerRecover(t *testing.T) {
	tests := []struct {
		name  string
		panic any
	}{
		{"string", "boom"},
		{"error value", errors.New("boom")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := newRig(t, base)
			var calls atomic.Int32
			slots := make(chan time.Time, 4)
			g.add(t, Task{Key: "k", Every: time.Minute, Run: func(_ context.Context, slot time.Time) error {
				if calls.Add(1) == 1 {
					panic(tt.panic)
				}
				slots <- slot
				return nil
			}})
			g.start(t)

			g.logs.await(t, "job failed", 1)
			g.logs.await(t, "job done", 1)
			rec, _ := g.logs.first("job failed")
			var pe *PanicError
			err, _ := rec.attrs["err"].(error)
			if !errors.As(err, &pe) {
				t.Fatalf("logged err = %v, want a *PanicError", rec.attrs["err"])
			}
			if !strings.Contains(string(pe.Stack), "TestRunnerRecover") {
				t.Errorf("stack does not name the panicking test:\n%s", pe.Stack)
			}
			if stack, _ := rec.attrs["stack"].(string); stack == "" {
				t.Error("the log line carries no stack")
			}

			// The runner lives on: the next slot runs.
			wake := time.Date(2026, time.November, 12, 12, 1, 1, 0, time.UTC)
			g.clock.await(t, wake)
			g.clock.advance(wake)
			recv(t, slots)
			if n := g.logs.count("job failed"); n != 1 {
				t.Errorf("job failed logged %d times, want 1", n)
			}
		})
	}
}

func TestRunnerSafeRun(t *testing.T) {
	err := safeRun(context.Background(), func(context.Context, time.Time) error { panic("x") }, base)
	var pe *PanicError
	if !errors.As(err, &pe) || pe.Value != "x" || len(pe.Stack) == 0 {
		t.Fatalf("safeRun = %v, want a *PanicError with value and stack", err)
	}
	want := errors.New("plain")
	if got := safeRun(context.Background(), func(context.Context, time.Time) error { return want }, base); !errors.Is(got, want) {
		t.Fatalf("safeRun returned %v, want %v", got, want)
	}
}

func TestRunnerTimeout(t *testing.T) {
	tests := []struct {
		name    string
		timeout time.Duration
	}{
		{"short", 5 * time.Millisecond},
		{"a bit longer", 40 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := newRig(t, base)
			errs := make(chan error, 1)
			g.add(t, Task{Key: "k", Every: time.Hour, Timeout: tt.timeout, Run: func(ctx context.Context, _ time.Time) error {
				<-ctx.Done()
				errs <- ctx.Err()
				return ctx.Err()
			}})
			g.start(t)

			if err := recv(t, errs); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("run context error = %v, want DeadlineExceeded", err)
			}
			g.logs.await(t, "job failed", 1)
			if n := g.logs.count("job leaked"); n != 0 {
				t.Errorf("a run that honoured its timeout was leaked %d times", n)
			}
		})
	}
}

func TestRunnerBusySkips(t *testing.T) {
	for _, skipped := range []int{1, 2} {
		t.Run(fmt.Sprintf("%d slots skipped", skipped), func(t *testing.T) {
			g := newRig(t, base)
			var calls atomic.Int32
			slots := make(chan time.Time, 8)
			release := make(chan struct{})
			t.Cleanup(func() { close(release) }) // runs before the rig's Stop
			g.add(t, Task{Key: "k", Every: time.Minute, Timeout: time.Hour, Run: func(_ context.Context, slot time.Time) error {
				calls.Add(1)
				slots <- slot
				<-release
				return nil
			}})
			g.start(t)
			recv(t, slots)

			minute := time.Date(2026, time.November, 12, 12, 0, 0, 0, time.UTC)
			for i := 1; i <= skipped; i++ {
				wake := minute.Add(time.Duration(i) * time.Minute).Add(time.Second)
				g.clock.await(t, wake)
				g.clock.advance(wake)
				g.logs.await(t, "job busy", i)
			}
			if n := calls.Load(); n != 1 {
				t.Fatalf("Run called %d times while busy, want 1", n)
			}
		})
	}
}

func TestRunnerBusyThenFree(t *testing.T) {
	g := newRig(t, base)
	slots := make(chan time.Time, 8)
	release := make(chan struct{})
	var calls atomic.Int32
	g.add(t, Task{Key: "k", Every: time.Minute, Timeout: time.Hour, Run: func(_ context.Context, slot time.Time) error {
		slots <- slot
		if calls.Add(1) == 1 {
			<-release
		}
		return nil
	}})
	g.start(t)
	recv(t, slots)

	g.clock.await(t, time.Date(2026, time.November, 12, 12, 1, 1, 0, time.UTC))
	g.clock.advance(time.Date(2026, time.November, 12, 12, 1, 1, 0, time.UTC))
	g.logs.await(t, "job busy", 1)
	close(release)
	g.logs.await(t, "job done", 1)

	wake := time.Date(2026, time.November, 12, 12, 2, 1, 0, time.UTC)
	g.clock.await(t, wake)
	g.clock.advance(wake)
	if got, want := recv(t, slots), time.Date(2026, time.November, 12, 12, 2, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("slot after the busy one = %v, want %v", got, want)
	}
}

func TestRunnerPermitSkips(t *testing.T) {
	g := newRig(t, base, WithPermits(1))
	started := make(chan string, 2)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	run := func(key string) func(context.Context, time.Time) error {
		return func(context.Context, time.Time) error {
			started <- key
			<-release
			return nil
		}
	}
	g.add(t, Task{Key: "a", Every: time.Minute, Timeout: time.Hour, Run: run("a")})
	g.add(t, Task{Key: "b", Every: time.Minute, Timeout: time.Hour, Run: run("b")})
	g.start(t)
	holder := recv(t, started)

	// The other task waits for a permit until its slot is over.
	end := time.Date(2026, time.November, 12, 12, 1, 0, 0, time.UTC)
	g.clock.await(t, end)
	g.clock.advance(end)
	g.logs.await(t, "job skipped", 1)
	rec, _ := g.logs.first("job skipped")
	if rec.attrs["key"] == holder {
		t.Errorf("the skipped job is the permit holder %q", holder)
	}
	select {
	case other := <-started:
		t.Errorf("job %q ran without a permit", other)
	default:
	}
}

func TestRunnerRetryHeld(t *testing.T) {
	daily := &Daily{12, 0}
	dailyStart := time.Date(2026, time.November, 12, 9, 30, 0, 0, time.UTC) // 12:30 +03
	tests := []struct {
		name  string
		task  Task
		start time.Time
		after func(start time.Time) time.Time // RetryAt
		wrap  bool
		retry bool
	}{
		{"every, retry inside the slot", Task{Every: time.Hour}, base, func(s time.Time) time.Time { return s.Add(20 * time.Second) }, false, true},
		{"every, wrapped ErrHeld", Task{Every: time.Hour}, base, func(s time.Time) time.Time { return s.Add(20 * time.Second) }, true, true},
		{"every, retry after the slot is over", Task{Every: time.Minute}, base, func(s time.Time) time.Time { return s.Add(31 * time.Second) }, false, false},
		{"every, retry time already past", Task{Every: time.Hour}, base, func(s time.Time) time.Time { return s.Add(-time.Second) }, false, false},
		{"daily, retry later today", Task{Daily: daily}, dailyStart, func(s time.Time) time.Time { return s.Add(time.Hour) }, false, true},
		{"daily, retry tomorrow", Task{Daily: daily}, dailyStart, func(s time.Time) time.Time { return s.Add(24 * time.Hour) }, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := newRig(t, tt.start, WithLocation(utcPlus3))
			retryAt := tt.after(tt.start)
			var calls atomic.Int32
			slots := make(chan time.Time, 4)
			tt.task.Key = "k"
			tt.task.Run = func(_ context.Context, slot time.Time) error {
				slots <- slot
				if calls.Add(1) > 1 {
					return nil
				}
				var err error = ErrHeld{RetryAt: retryAt}
				if tt.wrap {
					err = fmt.Errorf("claim: %w", err)
				}
				return err
			}
			g.add(t, tt.task)
			g.start(t)
			first := recv(t, slots)

			if !tt.retry {
				g.logs.await(t, "job done", 1)
				if n := calls.Load(); n != 1 {
					t.Fatalf("Run called %d times, want 1", n)
				}
				return
			}
			g.clock.await(t, retryAt)
			g.clock.advance(retryAt)
			if second := recv(t, slots); !second.Equal(first) {
				t.Fatalf("retried slot = %v, want the same slot %v", second, first)
			}
			g.logs.await(t, "job done", 1)
			if n := calls.Load(); n != 2 {
				t.Errorf("Run called %d times, want 2", n)
			}
		})
	}
}

func TestRunnerGraceRelease(t *testing.T) {
	g := newRig(t, base, WithPermits(1), WithGrace(10*time.Second))
	leaked := make(chan string, 4)
	g.r.onLeak = func(key string) { leaked <- key }
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	g.add(t, Task{Key: "hung", Every: time.Hour, Timeout: 30 * time.Second, Run: func(context.Context, time.Time) error {
		started <- struct{}{}
		<-release // ignores its context
		return nil
	}})
	g.start(t)
	recv(t, started)

	at := base.Add(40 * time.Second) // timeout plus grace
	g.clock.await(t, at)
	g.clock.advance(at)
	if key := recv(t, leaked); key != "hung" {
		t.Fatalf("OnLeak key = %q, want hung", key)
	}
	if n := g.logs.count("job leaked"); n != 1 {
		t.Errorf("job leaked logged %d times, want 1", n)
	}
	if n := g.logs.count("jobs halted"); n != 1 {
		t.Errorf("jobs halted logged %d times, want 1", n)
	}
	if n := len(g.r.sem); n != 1 {
		t.Errorf("permits held while the leaked run is out = %d, want 1", n)
	}

	// A run that returns after it was declared leaked gives its permit and its
	// entry back, and the runner says nothing more about it.
	close(release)
	ctx, cancel := context.WithTimeout(context.Background(), waitGuard)
	defer cancel()
	if err := g.r.Stop(ctx); err != nil {
		t.Fatalf("Stop after the leaked run returned: %v", err)
	}
	if n := len(g.r.sem); n != 0 {
		t.Errorf("permits held after the leaked run returned = %d, want 0", n)
	}
	if n := len(g.r.runs); n != 0 {
		t.Errorf("runs still registered = %d, want 0", n)
	}
	if n := g.logs.count("job leaked"); n != 1 {
		t.Errorf("job leaked logged %d times in all, want 1", n)
	}
	if len(leaked) != 0 {
		t.Error("OnLeak called twice for one run")
	}
}

func TestRunnerLeakHalts(t *testing.T) {
	g := newRig(t, base, WithGrace(10*time.Second))
	leaked := make(chan string, 4)
	g.r.onLeak = func(key string) { leaked <- key }
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	var okCalls atomic.Int32
	g.add(t, Task{Key: "hung", Every: time.Hour, Timeout: 5 * time.Second, Run: func(context.Context, time.Time) error {
		started <- struct{}{}
		<-release
		return nil
	}})
	g.add(t, Task{Key: "ok", Every: time.Minute, Run: func(context.Context, time.Time) error {
		okCalls.Add(1)
		return nil
	}})
	g.start(t)
	recv(t, started)
	g.logs.await(t, "job done", 1) // the healthy job's first run

	at := base.Add(15 * time.Second) // 12:00:45, before the healthy job's next slot
	g.clock.await(t, at)
	g.clock.advance(at)
	if key := recv(t, leaked); key != "hung" {
		t.Fatalf("OnLeak key = %q, want hung", key)
	}

	// Its next slot comes, and nothing starts. The halt must end both loops
	// (the hung job's and the healthy job's) before Stop cancels them: a loop
	// that is still out would be woken by the clock and decide to run.
	g.clock.advance(time.Date(2026, time.November, 12, 12, 1, 1, 0, time.UTC))
	g.clock.advance(time.Date(2026, time.November, 12, 12, 2, 1, 0, time.UTC))
	g.logs.await(t, "job loop stopped", 2)
	release <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), waitGuard)
	defer cancel()
	if err := g.r.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if n := okCalls.Load(); n != 1 {
		t.Errorf("healthy job ran %d times, want 1 (none after the leak)", n)
	}
	if n := g.logs.count("job leaked"); n != 1 {
		t.Errorf("job leaked logged %d times, want 1", n)
	}
}

func TestRunnerStopBounded(t *testing.T) {
	tests := []struct {
		name       string
		ignoresCtx bool
		cancelled  bool // Stop's own context is already done
		wantLeaked bool
	}{
		{"job ignores its context, the stop timeout ends the wait", true, false, true},
		{"job ignores its context, Stop's context is done", true, true, true},
		{"job honours its context", false, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const bound = 3 * time.Second
			g := newRig(t, base, WithStopTimeout(bound))
			started := make(chan struct{}, 1)
			sawStopping := make(chan bool, 1)
			release := make(chan struct{})
			t.Cleanup(func() { close(release) })
			g.add(t, Task{Key: "k", Every: time.Hour, Run: func(ctx context.Context, _ time.Time) error {
				started <- struct{}{}
				if tt.ignoresCtx {
					<-release
					return nil
				}
				<-ctx.Done()
				sawStopping <- g.r.Stopping()
				return ctx.Err()
			}})
			g.start(t)
			recv(t, started)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tt.cancelled {
				cancel()
			}
			result := make(chan error, 1)
			go func() { result <- g.r.Stop(ctx) }()
			if tt.ignoresCtx && !tt.cancelled {
				g.clock.await(t, base.Add(bound))
				g.clock.advance(base.Add(bound))
			}
			err := recv(t, result)

			if tt.wantLeaked {
				if !errors.Is(err, ErrLeaked) {
					t.Fatalf("Stop = %v, want ErrLeaked", err)
				}
				if n := g.logs.count("job leaked"); n != 1 {
					t.Errorf("job leaked logged %d times, want 1", n)
				}
				return
			}
			if err != nil {
				t.Fatalf("Stop = %v, want nil", err)
			}
			if !recv(t, sawStopping) {
				t.Error("Stopping() was false inside a run cancelled by Stop")
			}
			if n := g.logs.count("job failed"); n != 0 {
				t.Errorf("a run cancelled by Stop was logged as failed %d times", n)
			}
			if n := g.logs.count("job cancelled"); n != 1 {
				t.Errorf("job cancelled logged %d times, want 1", n)
			}
		})
	}
}

func TestRunnerAddAndStart(t *testing.T) {
	ok := func(context.Context, time.Time) error { return nil }
	tests := []struct {
		name string
		task Task
		ok   bool
	}{
		{"every", Task{Key: "a", Every: time.Minute, Run: ok}, true},
		{"daily", Task{Key: "a", Daily: &Daily{23, 59}, Run: ok}, true},
		{"no key", Task{Every: time.Minute, Run: ok}, false},
		{"no run", Task{Key: "a", Every: time.Minute}, false},
		{"neither every nor daily", Task{Key: "a", Run: ok}, false},
		{"both every and daily", Task{Key: "a", Every: time.Minute, Daily: &Daily{1, 0}, Run: ok}, false},
		{"hour out of range", Task{Key: "a", Daily: &Daily{24, 0}, Run: ok}, false},
		{"minute out of range", Task{Key: "a", Daily: &Daily{0, 60}, Run: ok}, false},
		{"negative timeout", Task{Key: "a", Every: time.Minute, Timeout: -1, Run: ok}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := newRig(t, base)
			if err := g.r.Add(tt.task); (err == nil) != tt.ok {
				t.Fatalf("Add = %v, want ok=%v", err, tt.ok)
			}
		})
	}

	t.Run("duplicate key and late Add", func(t *testing.T) {
		g := newRig(t, base)
		g.add(t, Task{Key: "a", Daily: &Daily{23, 59}, Run: ok})
		if err := g.r.Add(Task{Key: "a", Every: time.Minute, Run: ok}); err == nil {
			t.Error("a repeated key was accepted")
		}
		g.start(t)
		if err := g.r.Start(context.Background()); !errors.Is(err, ErrStarted) {
			t.Errorf("second Start = %v, want ErrStarted", err)
		}
		if err := g.r.Add(Task{Key: "b", Every: time.Minute, Run: ok}); !errors.Is(err, ErrStarted) {
			t.Errorf("Add after Start = %v, want ErrStarted", err)
		}
	})
}
