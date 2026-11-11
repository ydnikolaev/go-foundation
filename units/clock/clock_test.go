package clock_test

import (
	"testing"
	"time"

	"github.com/ydnikolaev/go-foundation/units/clock"
)

var start = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

var (
	_ clock.Clock = clock.Real{}
	_ clock.Clock = (*clock.Fake)(nil)

	_ clock.Nower = clock.Real{}
	_ clock.Nower = (*clock.Fake)(nil)
	_ clock.Nower = nowOnly{}
)

// nowOnly stands for a consumer's own type that has Now and no After.
type nowOnly struct{}

func (nowOnly) Now() time.Time { return start }

func fired(ch <-chan time.Time) (time.Time, bool) {
	select {
	case t := <-ch:
		return t, true
	default:
		return time.Time{}, false
	}
}

func TestFakeNowMovesOnlyByHand(t *testing.T) {
	t.Parallel()
	f := clock.NewFake(start)
	if !f.Now().Equal(start) {
		t.Fatalf("Now = %v, want %v", f.Now(), start)
	}
	f.Advance(90 * time.Second)
	if want := start.Add(90 * time.Second); !f.Now().Equal(want) {
		t.Errorf("after Advance: %v, want %v", f.Now(), want)
	}
	f.Advance(-time.Hour)
	if want := start.Add(90 * time.Second); !f.Now().Equal(want) {
		t.Errorf("a negative Advance moved the time: %v, want %v", f.Now(), want)
	}
	f.Set(start.Add(time.Hour))
	if want := start.Add(time.Hour); !f.Now().Equal(want) {
		t.Errorf("after Set: %v, want %v", f.Now(), want)
	}
}

func TestFakeAfter(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		wait     time.Duration
		steps    []time.Duration
		wantFire bool
	}{
		{"zero fires at once", 0, nil, true},
		{"negative fires at once", -time.Second, nil, true},
		{"not due", time.Minute, []time.Duration{59 * time.Second}, false},
		{"exactly due", time.Minute, []time.Duration{time.Minute}, true},
		{"due over two steps", time.Minute, []time.Duration{30 * time.Second, 30 * time.Second}, true},
		{"overshoot", time.Minute, []time.Duration{time.Hour}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := clock.NewFake(start)
			ch := f.After(tt.wait)
			for _, s := range tt.steps {
				f.Advance(s)
			}
			got, ok := fired(ch)
			if ok != tt.wantFire {
				t.Fatalf("fired = %v, want %v", ok, tt.wantFire)
			}
			if ok && !got.Equal(f.Now()) {
				t.Errorf("fired with %v, want the clock's time %v", got, f.Now())
			}
			if ok {
				if _, again := fired(ch); again {
					t.Error("a timer fired twice")
				}
			}
		})
	}
}

func TestFakeFiresOnlyDueTimers(t *testing.T) {
	t.Parallel()
	f := clock.NewFake(start)
	soon, late := f.After(time.Second), f.After(time.Hour)
	if n := f.Waiters(); n != 2 {
		t.Fatalf("Waiters = %d, want 2", n)
	}
	f.Advance(time.Minute)
	if _, ok := fired(soon); !ok {
		t.Error("the due timer did not fire")
	}
	if _, ok := fired(late); ok {
		t.Error("the later timer fired early")
	}
	if n := f.Waiters(); n != 1 {
		t.Errorf("Waiters = %d, want 1", n)
	}
}

func TestRealClock(t *testing.T) {
	t.Parallel()
	var c clock.Clock = clock.Real{}
	before := time.Now()
	if now := c.Now(); now.Before(before) {
		t.Errorf("Real.Now %v is before %v", now, before)
	}
	select {
	case <-c.After(0):
	case <-time.After(5 * time.Second):
		t.Fatal("Real.After(0) did not fire")
	}
}
