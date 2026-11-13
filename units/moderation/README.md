# moderation

The rules of a members' community's moderation over a generic `Target` (a post, a comment): reports, hiding an item,
a sanctions ladder (a warning, a mute for a period, a mute forever) and an append-only sanction log. Every operation
reads and writes through a `Store` port that the host binds to one database transaction, so a change and its log entry
commit or roll back together. The host owns the tables, the visibility rules, the reason vocabulary and the
notifications; this package owns the order of the steps and the refusals. Standard library only.

## Install

```sh
go get github.com/ydnikolaev/go-foundation/units/moderation@v0.1.0
```

go-foundation is public: no GOPRIVATE, no token, no vendoring.

## Wire

1. Implement `moderation.Store` over your database, bound to one transaction. `Item`, `Mute` and `ActiveMute` read
   under a lock (`SELECT ... FOR UPDATE`) so two moderators never race; `ActiveMute` also serialises one member's mute
   changes. `Append` only inserts: give the log table no update or delete.
2. Open a transaction per request, build the `Store` on it, call one operation, commit on a nil error and roll back
   otherwise.
3. Map each refusal onto your own answer: `ErrNotFound`, `ErrOwnItem`, `ErrAlreadyReported`, `ErrBlankReason`,
   `ErrInvalidTarget`, `ErrInvalidOutcome`, `ErrInvalidStep`, `ErrEndRequired`, `ErrEndNotAllowed`, `ErrEndInPast`,
   `ErrNotActive` and `ErrInvalidEntry`. Test with `errors.Is`: an error your `Store` returns comes back wrapped with
   the name of the failed operation, and `errors.Is` still finds it.
4. Check who may moderate and who may see an item before the call: the package decides the rules of the step, not the
   caller's rights.
5. After a commit, use the results: `Hide` and `Unhide` answer the `Item` and `changed` (tell the author only when
   `changed`), `Issue` and `Lift` answer the `Mute`.

The operations:

| call | does |
|---|---|
| `FileReport` | records an open report; refuses a hidden item, the reporter's own item and a second open report |
| `Resolve` | closes every open report on an item as `Actioned` or `Dismissed`; writes no log entry |
| `Hide` / `Unhide` | hides or shows an item, logs it against its author, and (hide) actions its open reports; a repeat changes and logs nothing |
| `Issue` | gives a member a `Warning`, a `Period` (needs a future `Until`) or `Forever`; a mute lifts the member's active one with no lift entry |
| `Lift` | ends an active mute and logs the lift; a warning, a lapsed or a lifted mute is `ErrNotActive` |

## Configure

None. The package has no options: the rules are fixed, and the host's choices are the `Store` it binds, the reason
text it passes and the clock value `now` it hands to `Issue` and `Lift`.

## API

<!-- api: devkit foundation api; do not edit -->
```go
VARIABLES
var (
	ErrNotFound        = errors.New("moderation: not found")
	ErrOwnItem         = errors.New("moderation: own item")
	ErrAlreadyReported = errors.New("moderation: already reported")
	ErrBlankReason     = errors.New("moderation: the reason is empty")
	ErrInvalidTarget   = errors.New("moderation: invalid target")
	ErrInvalidOutcome  = errors.New("moderation: invalid outcome")
	ErrInvalidStep     = errors.New("moderation: invalid step")
	ErrEndRequired     = errors.New("moderation: a period needs an end")
	ErrEndNotAllowed   = errors.New("moderation: only a period has an end")
	ErrEndInPast       = errors.New("moderation: the end is not in the future")
	ErrNotActive       = errors.New("moderation: the mute is not active")
	ErrInvalidEntry    = errors.New("moderation: invalid sanction entry")
)
FUNCTIONS
func FileReport(ctx context.Context, s Store, r Report) error
func Resolve(ctx context.Context, s Store, t Target, outcome Status, actorID string, at time.Time) (int, error)
TYPES
type Entry struct {
	Kind    SanctionKind
	UserID  string
	Target  *Target
	Reason  string
	ActorID string
	At      time.Time
}
type Hiding struct {
	Reason  string
	ActorID string
	At      time.Time
}
type Item struct {
	Target       Target
	AuthorID     string
	Hidden       bool
	HiddenReason string
}
func Hide(ctx context.Context, s Store, t Target, h Hiding) (item Item, changed bool, err error)
func Unhide(ctx context.Context, s Store, t Target, actorID string, at time.Time) (item Item, changed bool, err error)
type Kind string
const (
	KindPost    Kind = "post"
	KindComment Kind = "comment"
)
type Mute struct {
	ID        string
	UserID    string
	Step      Step
	Reason    string
	Until     *time.Time // set for a period only
	ActorID   string
	CreatedAt time.Time
	LiftedAt  *time.Time
}
func Issue(ctx context.Context, s Store, o Order, now time.Time) (Mute, error)
func Lift(ctx context.Context, s Store, id, actorID string, now time.Time) (Mute, error)
func (m *Mute) Active(now time.Time) bool
type Order struct {
	UserID  string
	Step    Step
	Reason  string
	Until   *time.Time // a period's end; nil for a warning and forever
	ActorID string
}
func (o Order) Check(now time.Time) error
type Report struct {
	Target     Target
	ReporterID string
	Reason     string
	Note       string
	At         time.Time
}
type SanctionKind string
const (
	SanctionHide    SanctionKind = "hide"
	SanctionUnhide  SanctionKind = "unhide"
	SanctionWarning SanctionKind = "warning"
	SanctionMute    SanctionKind = "mute"
	SanctionLift    SanctionKind = "lift"
)
type Status string
const (
	Open      Status = "open"
	Actioned  Status = "actioned"
	Dismissed Status = "dismissed"
)
type Step string
const (
	Warning Step = "warning"
	Period  Step = "period"
	Forever Step = "forever"
)
type Store interface {
	// Item reads t, locked for this transaction; ErrNotFound when it does not exist.
	Item(ctx context.Context, t Target) (Item, error)
	// SetHidden hides t with h, or shows it again when h is nil.
	SetHidden(ctx context.Context, t Target, h *Hiding) error
	// HasOpenReport reports whether reporterID has an open report on t.
	HasOpenReport(ctx context.Context, reporterID string, t Target) (bool, error)
	// AddReport records r as open; ErrAlreadyReported when a concurrent report won the race.
	AddReport(ctx context.Context, r Report) error
	// ResolveReports closes every open report on t with outcome, by actorID at at, and answers how many it closed.
	ResolveReports(ctx context.Context, t Target, outcome Status, actorID string, at time.Time) (int, error)
	// ActiveMute is userID's mute active at now, or nil; it serialises the member's mute changes for this
	// transaction, so two concurrent mutes never both stay active.
	ActiveMute(ctx context.Context, userID string, now time.Time) (*Mute, error)
	// Mute reads mute id, locked for this transaction; ErrNotFound when it does not exist.
	Mute(ctx context.Context, id string) (Mute, error)
	// AddMute records m and answers its id; ErrNotFound when the member does not exist.
	AddMute(ctx context.Context, m Mute) (string, error)
	// SetLifted marks mute id lifted at at.
	SetLifted(ctx context.Context, id string, at time.Time) error
	// Append adds e to the sanction log.
	Append(ctx context.Context, e Entry) error
}
type Target struct {
	Kind Kind
	ID   string
}
```

## Example

Report a post, hide it as a moderator, then give its author a one-day mute over an in-memory `Store` (this is
`Example`, compiled and run by the unit's tests; `memStore` is in `example_test.go`):

```go
ctx := context.Background()
now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
post := moderation.Target{Kind: moderation.KindPost, ID: "p1"}
s := &memStore{items: map[moderation.Target]moderation.Item{post: {Target: post, AuthorID: "alice"}}}

err := moderation.FileReport(ctx, s, moderation.Report{Target: post, ReporterID: "bob", Reason: "spam", At: now})
fmt.Println("report:", err)

_, changed, _ := moderation.Hide(ctx, s, post, moderation.Hiding{Reason: "spam", ActorID: "mod", At: now})
fmt.Println("hidden:", changed, "reports:", s.reports[0].status)

end := now.Add(24 * time.Hour)
m, _ := moderation.Issue(ctx, s, moderation.Order{
	UserID: "alice", Step: moderation.Period, Reason: "spam", Until: &end, ActorID: "mod",
}, now)
fmt.Println("muted:", m.Active(now), "log:", s.log[0].Kind, s.log[1].Kind)
// Output:
// report: <nil>
// hidden: true reports: actioned
// muted: true log: hide mute
```

## Extension points

- `Store` is the one port: bind it to SQL, a test double or anything that gives one transaction's reads and writes.
- `Kind` has `KindPost` and `KindComment`; a `Target` of another kind is `ErrInvalidTarget` until the unit gains it
  (ask for it through an Improve request, do not patch a copy).
- Reasons and report categories are free strings of yours, and notifications are yours: the package checks only that a
  reason is not blank.

## Do not

- Do not run two operations on separate transactions: the change and its log entry must commit together.
- Do not give the log an update or a delete: it is append-only by its port.
- Do not expect enforcement: `Mute.Active` says whether a mute restricts its member at `now`; refusing the member's
  posts or logins is the host's.
- Do not expect the ladder to be enforced in order: a warning, a period and forever may come in any order, by the
  moderator's judgement.
- Do not copy this package. Extend it here; a copy carries `go-foundation-copy: moderation@<sha>` and a removal row.
