package cron

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"time"
)

// DefaultTimeout is the run timeout of a Task that sets none.
const DefaultTimeout = time.Minute

// Daily is a time of day in the Runner's zone (WithLocation, default UTC).
type Daily struct {
	Hour, Minute int
}

// on is this time of day on the calendar day of day in loc.
func (d Daily) on(day time.Time, loc *time.Location) time.Time {
	y, m, dd := day.In(loc).Date()
	return time.Date(y, m, dd, d.Hour, d.Minute, 0, 0, loc)
}

// Task is one scheduled job. Exactly one of Every and Daily is set.
type Task struct {
	// Key names the task in logs and in OnLeak; it is unique in a Runner.
	Key string
	// Every runs the task once per interval. A slot is the time truncated to
	// the interval counted from the Unix epoch.
	Every time.Duration
	// Daily runs the task once a day at HH:MM in the Runner's zone. A slot is
	// that day's HH:MM.
	Daily *Daily
	// Timeout bounds one run (context.WithTimeout, wall time); 0 means
	// DefaultTimeout.
	Timeout time.Duration
	// Run does the work of one slot. It must honour its context. It may return
	// ErrHeld to ask for the same slot again.
	Run func(ctx context.Context, slot time.Time) error
}

func (t Task) validate() error {
	switch {
	case t.Key == "":
		return errors.New("empty key")
	case t.Run == nil:
		return errors.New("nil Run")
	case t.Every > 0 && t.Daily != nil, t.Every <= 0 && t.Daily == nil:
		return errors.New("exactly one of Every and Daily is required")
	case t.Daily != nil && (t.Daily.Hour < 0 || t.Daily.Hour > 23 || t.Daily.Minute < 0 || t.Daily.Minute > 59):
		return fmt.Errorf("daily %02d:%02d out of range", t.Daily.Hour, t.Daily.Minute)
	case t.Timeout < 0:
		return errors.New("negative timeout")
	}
	return nil
}

// slotAt is the slot now falls in. For a daily task it is today's slot, and
// false while that has not come yet.
func (t Task) slotAt(now time.Time, loc *time.Location) (time.Time, bool) {
	if t.Daily != nil {
		s := t.Daily.on(now, loc)
		return s, !s.After(now)
	}
	n, e := now.UnixNano(), int64(t.Every)
	return time.Unix(0, n-((n%e)+e)%e).UTC(), true
}

// next is the first slot boundary after now.
func (t Task) next(now time.Time, loc *time.Location) time.Time {
	if t.Daily != nil {
		s := t.Daily.on(now, loc)
		if s.After(now) {
			return s
		}
		return t.Daily.on(now.In(loc).AddDate(0, 0, 1), loc)
	}
	s, _ := t.slotAt(now, loc)
	return s.Add(t.Every)
}

// span is how long a slot is current: the interval, or a day.
func (t Task) span() time.Duration {
	if t.Daily != nil {
		return 24 * time.Hour
	}
	return t.Every
}

// current reports whether at lies in the slot: inside the interval, or on the
// slot's calendar day in loc.
func (t Task) current(slot, at time.Time, loc *time.Location) bool {
	if t.Daily != nil {
		sy, sm, sd := slot.In(loc).Date()
		ay, am, ad := at.In(loc).Date()
		return sy == ay && sm == am && sd == ad
	}
	return !at.Before(slot) && at.Before(slot.Add(t.Every))
}

// PanicError is a recovered panic of a Run.
type PanicError struct {
	Value any
	Stack []byte
}

func (e *PanicError) Error() string { return fmt.Sprintf("panic: %v", e.Value) }

// safeRun calls run and turns a panic into a *PanicError carrying the stack.
func safeRun(ctx context.Context, run func(context.Context, time.Time) error, slot time.Time) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = &PanicError{Value: p, Stack: debug.Stack()}
		}
	}()
	return run(ctx, slot)
}

// ErrHeld is what a Run returns when another holder has the slot unfinished
// until RetryAt: the runner calls Run again for the same slot at RetryAt, while
// the slot is still current (every) or today (daily).
type ErrHeld struct {
	RetryAt time.Time
}

func (e ErrHeld) Error() string {
	return "slot held until " + e.RetryAt.Format(time.RFC3339)
}
