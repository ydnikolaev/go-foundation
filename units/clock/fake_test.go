package clock_test

import (
	"testing"
	"time"

	"github.com/ydnikolaev/go-foundation/units/clock"
)

func TestFakeSetBackward(t *testing.T) {
	t.Parallel()

	t.Run("backward moves the time and fires no timer", func(t *testing.T) {
		t.Parallel()
		f := clock.NewFake(start)
		f.Advance(time.Hour)
		ch := f.After(time.Minute)
		back := start.Add(10 * time.Minute)
		f.Set(back)
		if !f.Now().Equal(back) {
			t.Errorf("Now = %v, want %v", f.Now(), back)
		}
		if _, ok := fired(ch); ok {
			t.Error("a timer fired when Set moved backwards")
		}
		if n := f.Waiters(); n != 1 {
			t.Errorf("Waiters = %d, want 1", n)
		}
	})

	t.Run("forward still fires the due timers", func(t *testing.T) {
		t.Parallel()
		f := clock.NewFake(start)
		due, later := f.After(time.Minute), f.After(time.Hour)
		f.Set(start.Add(-time.Hour))
		if _, ok := fired(due); ok {
			t.Fatal("a timer fired when Set moved backwards")
		}
		to := start.Add(2 * time.Minute)
		f.Set(to)
		if !f.Now().Equal(to) {
			t.Errorf("Now = %v, want %v", f.Now(), to)
		}
		if _, ok := fired(due); !ok {
			t.Error("the due timer did not fire when Set moved forwards")
		}
		if _, ok := fired(later); ok {
			t.Error("the later timer fired early")
		}
	})

	t.Run("a timer armed before a backward Set is measured from the moved time", func(t *testing.T) {
		t.Parallel()
		f := clock.NewFake(start)
		f.Set(start.Add(-time.Hour))
		ch := f.After(time.Minute)
		f.Advance(time.Minute)
		if _, ok := fired(ch); !ok {
			t.Error("a timer armed after the backward Set did not fire once due")
		}
	})
}
