package clock_test

import (
	"testing"
	"time"

	"github.com/ydnikolaev/go-foundation/units/clock"
)

// TestFakeTimerFiresAtItsDeadline pins the go-foundation units/clock v0.1.0
// defect: a Fake timer crossed by a jump must deliver its own deadline, as a
// real timer does, not the end time of the jump.
func TestFakeTimerFiresAtItsDeadline(t *testing.T) {
	t0 := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	deadline := t0.Add(time.Second)

	tests := []struct {
		name string
		jump func(f *clock.Fake)
	}{
		{"Advance past the deadline", func(f *clock.Fake) { f.Advance(3 * time.Second) }},
		{"Set past the deadline", func(f *clock.Fake) { f.Set(t0.Add(3 * time.Second)) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := clock.NewFake(t0)
			ch := f.After(time.Second)
			tc.jump(f)

			done := time.After(5 * time.Second)
			select {
			case got := <-ch:
				if !got.Equal(deadline) {
					t.Errorf("fired at %v, want its deadline %v", got, deadline)
				}
			case <-done:
				t.Fatal("timer did not fire after the jump")
			}
		})
	}
}
