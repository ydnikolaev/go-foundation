// Package clock is the kit's time seam: every kit timer goes through a Clock, so a test drives time by hand with
// Fake instead of sleeping.
//
// Use it when code reads the time or waits and a test must drive that time: take a Clock, pass Real in production
// and Fake in tests.
package clock

import (
	"sync"
	"time"
)

// Clock tells the time and makes timers.
type Clock interface {
	// Now is the current time.
	Now() time.Time
	// After returns a channel that receives the clock's time once d has passed (at once for d <= 0).
	After(d time.Duration) <-chan time.Time
}

// Real is the wall clock.
type Real struct{}

// Now returns time.Now().
func (Real) Now() time.Time { return time.Now() }

// After returns time.After(d).
func (Real) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Fake is a clock that moves only when Advance or Set is called. Its zero value is not usable: build it with
// NewFake. It is safe for concurrent use.
type Fake struct {
	mu      sync.Mutex
	now     time.Time
	waiters []waiter
}

type waiter struct {
	at time.Time
	ch chan time.Time
}

// NewFake returns a Fake set to start.
func NewFake(start time.Time) *Fake { return &Fake{now: start} }

// Now returns the fake time.
func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// After returns a channel that fires when the fake time reaches now+d; d <= 0 fires at once. The channel has a
// buffer of one, so a timer nobody reads never blocks Advance.
func (f *Fake) After(d time.Duration) <-chan time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan time.Time, 1)
	if d <= 0 {
		ch <- f.now
		return ch
	}
	f.waiters = append(f.waiters, waiter{at: f.now.Add(d), ch: ch})
	return ch
}

// Advance moves the fake time forward by d and fires every timer that is due. A negative d is ignored.
func (f *Fake) Advance(d time.Duration) {
	if d < 0 {
		return
	}
	f.mu.Lock()
	f.set(f.now.Add(d))
	f.mu.Unlock()
}

// Set moves the fake time to t, forwards or backwards. Moving forwards fires every timer that is due; moving
// backwards fires none, because every timer still waiting is due after the current time.
func (f *Fake) Set(t time.Time) {
	f.mu.Lock()
	f.set(t)
	f.mu.Unlock()
}

// Waiters is the number of timers not yet fired: a test waits for it to reach n before it advances, so the code
// under test has armed its timer.
func (f *Fake) Waiters() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.waiters)
}

func (f *Fake) set(t time.Time) {
	f.now = t
	kept := f.waiters[:0]
	for _, w := range f.waiters {
		if w.at.After(t) {
			kept = append(kept, w)
			continue
		}
		w.ch <- t
	}
	clear(f.waiters[len(kept):])
	f.waiters = kept
}
