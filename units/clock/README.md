# clock

The time seam: code that reads the time or waits takes a `Clock`, production passes `Real`, a test passes `Fake` and
drives time by hand instead of sleeping. Standard library only.

## Install

```sh
go get github.com/ydnikolaev/go-foundation/units/clock@v0.1.3
```

go-foundation is public: no GOPRIVATE, no token, no vendoring.

## Wire

1. Take the narrowest interface the code needs: `clock.Nower` when it only reads the time, `clock.Clock` when it also
   waits (`After`).
2. Pass `clock.Real{}` in production and `clock.NewFake(start)` in tests.
3. In a test, wait until `Fake.Waiters()` reaches the number of timers the code under test arms, then move time:
   `Advance(d)` moves forward by `d`; `Set(t)` moves to `t` in either direction.
4. A timer fires once, when the fake time reaches its due time: `Advance` and a forward `Set` fire every timer that is
   due, a backward `Set` fires none, and a negative `Advance` is ignored.

## Configure

Nothing to configure. `NewFake(start)` takes the first time the fake reports.

## API

<!-- api: devkit foundation api; do not edit -->
```go
TYPES
type Clock interface {
	// Now is the current time.
	Now() time.Time
	// After returns a channel that receives the clock's time once d has passed (at once for d <= 0).
	After(d time.Duration) <-chan time.Time
}
type Fake struct {
	// Has unexported fields.
}
func NewFake(start time.Time) *Fake
func (f *Fake) Advance(d time.Duration)
func (f *Fake) After(d time.Duration) <-chan time.Time
func (f *Fake) Now() time.Time
func (f *Fake) Set(t time.Time)
func (f *Fake) Waiters() int
type Nower interface {
	// Now is the current time.
	Now() time.Time
}
type Real struct{}
func (Real) After(d time.Duration) <-chan time.Time
func (Real) Now() time.Time
```

## Example

Test code that waits, without sleeping (compiled and run as a test of this snippet):

```go
// waitThenStamp is the code under test: it takes a Clock instead of calling time.After.
func waitThenStamp(c clock.Clock, d time.Duration) time.Time {
	<-c.After(d)
	return c.Now()
}

func main() {
	fake := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	done := make(chan time.Time)
	go func() { done <- waitThenStamp(fake, time.Minute) }()

	for fake.Waiters() < 1 { // wait until the code under test has armed its timer
		runtime.Gosched()
	}
	fake.Advance(time.Minute) // fires the timer without sleeping
	fmt.Println(<-done)       // 2026-01-01 00:01:00 +0000 UTC
}
```

## Extension points

- `Nower` and `Clock` are interfaces: a type of your own with `Now() time.Time` satisfies `Nower`, and one that also has
  `After(time.Duration) <-chan time.Time` satisfies `Clock`. Keep a consumer-side `Clock` if you like; the unit does
  not ask you to rename it.

## Do not

- Do not call `time.Now` or `time.After` in code that takes a `Clock`; the test then cannot drive it.
- Do not use the zero value of `Fake`: build it with `NewFake`.
- Do not rely on a timer that was armed before a backward `Set` to fire earlier: it keeps its due time.
- Do not copy this package. Extend it here; a copy carries `go-foundation-copy: clock@<sha>` and a removal row.
