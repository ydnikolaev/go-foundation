# cron

An in-process scheduled-task runner: a task runs on a fixed interval (`Every`) or once a day at HH:MM (`Daily`), driven
by an injected `Clock` and a `slog` logger. A task that is still running at its next slot skips it, a process-wide
permit limit bounds the runs at once, a panic is an error, `Stop` waits a bounded time, and a run that will not return
halts the runner. Standard library only.

## Install

```sh
go get github.com/ydnikolaev/go-foundation/units/cron@v0.1.0
```

go-foundation is public: no GOPRIVATE, no token, no vendoring.

## Wire

1. Build the runner: `cron.New(logger, clock, opts...)`. The logger and the clock are required; pass `cron.RealClock{}`
   in production and a fake clock in tests.
2. Register each task with `Add`: a unique `Key`, exactly one of `Every` (an interval) and `Daily` (`&cron.Daily{Hour,
   Minute}`), a `Run(ctx, slot)` that honours its context, and an optional `Timeout` (default one minute). `Add`
   refuses a bad task, a repeated key, and any call after `Start`.
3. `Start(ctx)` begins every task: it tries the task's current slot at once (a daily task only if today's slot has
   passed), then sleeps to each next slot boundary plus one second.
4. On shutdown call `Stop(ctx)`: it cancels every run's context and waits until the runs return, `ctx` ends or the stop
   timeout passes; it returns `ErrLeaked` when a run is still out. A `Run` that sees a context error can ask
   `Stopping()` to tell a stop from a failure.
5. The runner knows only its own process. When two replicas both run it, both run the task; make `Run` claim the slot
   (a database row, a lock) and return `cron.ErrHeld{RetryAt: ...}` when another holder has it unfinished: the runner
   calls `Run` again for the same slot at `RetryAt`, while the slot is still current.

A slot is the interval counted from the Unix epoch for `Every`, and that day's HH:MM for `Daily`. The runner computes
the slot from the clock when it wakes, so a late wake-up runs the slot it is in and skips none.

## Configure

Options of `cron.New`; with none, the defaults apply.

| option | meaning | default |
|---|---|---|
| `WithLocation(loc)` | the zone `Daily` is read in; nil keeps the default | `time.UTC` |
| `WithPermits(n)` | runs at once, process-wide; a task that waits for a permit past its slot skips it | 2 |
| `WithStopTimeout(d)` | how long `Stop` waits for the runs | 5s |
| `WithGrace(d)` | how long after its timeout a run may take to return before it is leaked | 10s |
| `WithOnLeak(f)` | called once with the key of each leaked run, after the runner halted | none |

A leaked run (Go cannot kill a goroutine) is logged as `job leaked` and halts the runner: no task of it starts again
(`jobs halted`). Other log lines: `job busy`, `job skipped`, `job failed` (with the stack of a panic), `job cancelled`.
For a zone without daylight saving time, `time.FixedZone` needs no tzdata in the image.

## API

<!-- api: devkit foundation api; do not edit -->
```go
CONSTANTS
const DefaultTimeout = time.Minute
VARIABLES
var (
	// ErrLeaked is returned by Stop when a run was still out at its bound.
	ErrLeaked = errors.New("cron: run still out")
	// ErrStarted is returned by Add and Start once the runner has started.
	ErrStarted = errors.New("cron: runner started")
)
TYPES
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}
type Daily struct {
	Hour, Minute int
}
type ErrHeld struct {
	RetryAt time.Time
}
func (e ErrHeld) Error() string
type Option func(*Runner)
func WithGrace(d time.Duration) Option
func WithLocation(loc *time.Location) Option
func WithOnLeak(f func(key string)) Option
func WithPermits(n int) Option
func WithStopTimeout(d time.Duration) Option
type PanicError struct {
	Value any
	Stack []byte
}
func (e *PanicError) Error() string
type RealClock struct{}
func (RealClock) After(d time.Duration) <-chan time.Time
func (RealClock) Now() time.Time
type Runner struct {
	// Has unexported fields.
}
func New(logger *slog.Logger, clock Clock, opts ...Option) *Runner
func (r *Runner) Add(t Task) error
func (r *Runner) Start(ctx context.Context) error
func (r *Runner) Stop(ctx context.Context) error
func (r *Runner) Stopping() bool
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
```

## Example

One interval task and one daily task, a `Daily` read in UTC+3 (this is `Example`, compiled and run by the unit's tests):

```go
logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
runner := cron.New(logger, cron.RealClock{},
	cron.WithLocation(time.FixedZone("UTC+3", 3*3600)), // Daily is read in this zone; default UTC
)

ran := make(chan struct{}, 1)
_ = runner.Add(cron.Task{
	Key:   "sweep",
	Every: 15 * time.Minute,
	Run: func(ctx context.Context, slot time.Time) error {
		ran <- struct{}{}
		return nil
	},
})
_ = runner.Add(cron.Task{
	Key:   "report",
	Daily: &cron.Daily{Hour: 9, Minute: 30},
	Run:   func(ctx context.Context, slot time.Time) error { return nil },
})

_ = runner.Start(context.Background())
<-ran
fmt.Println("sweep ran")
_ = runner.Stop(context.Background())
// Output: sweep ran
```

## Extension points

- `Clock` is an interface (`Now() time.Time`, `After(d time.Duration) <-chan time.Time`): any type with those two
  methods works, a hand-driven fake in tests included. The `clock` unit's `Real` and `Fake` have the same methods.
- `Task.Run` is yours: a database claim, a lock or an `ErrHeld` retry belong there, not in the runner.
- `WithOnLeak` is the hook for a leaked run (alert, exit the process).

## Do not

- Do not rely on it across replicas: it is in-process only, and two replicas both run every task unless `Run` claims
  its slot itself.
- Do not return from `Run` late: a run that ignores its context past `Timeout` plus the grace is leaked and halts the
  runner.
- Do not expect a missed slot to be replayed: after a restart the runner tries only the slot it is in.
- Do not call `Add` after `Start`, and do not reuse a `Key`.
- Do not copy this package. Extend it here; a copy carries `go-foundation-copy: cron@<sha>` and a removal row.
