// Package moderation holds the rules of a members' community's moderation over a generic Target, so a new kind of
// item (a post, a comment) joins with no change here: reports (one open per reporter and target, never on one's own
// item, resolved as actioned or dismissed), hiding an item, the sanctions ladder (a warning, a mute for a period with
// an end, a mute forever; always with a non-empty reason) and the append-only sanction log, where every hide, unhide,
// warning, mute and lift is one entry with its actor and reason.
//
// Each operation reads and writes through a Store port that the host binds to one database transaction, so a change
// and its log entry commit or roll back together. The host owns the tables, the visibility rules, the reason
// vocabulary and the notifications; this package owns the order of the steps and the refusals.
//
// Standard library only.
package moderation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Kind is the kind of item a Target names.
type Kind string

// The item kinds.
const (
	KindPost    Kind = "post"
	KindComment Kind = "comment"
)

// Target is one moderated item.
type Target struct {
	Kind Kind
	ID   string
}

func (t Target) valid() bool { return (t.Kind == KindPost || t.Kind == KindComment) && t.ID != "" }

// Status is a report's state.
type Status string

// The report states: open until the item is actioned (hidden) or the reports dismissed.
const (
	Open      Status = "open"
	Actioned  Status = "actioned"
	Dismissed Status = "dismissed"
)

// Report is a member's report on an item. Reason is the host's category; Note is optional.
type Report struct {
	Target     Target
	ReporterID string
	Reason     string
	Note       string
	At         time.Time
}

// Item is what the rules read of a moderated item.
type Item struct {
	Target       Target
	AuthorID     string
	Hidden       bool
	HiddenReason string
}

// Hiding is the moderator's label on a hidden item.
type Hiding struct {
	Reason  string
	ActorID string
	At      time.Time
}

// Step is a step of the sanctions ladder. The ladder is the moderator's judgement: steps are not enforced in order.
type Step string

// The steps: a warning restricts nothing; a period ends at Until; forever lasts until lifted.
const (
	Warning Step = "warning"
	Period  Step = "period"
	Forever Step = "forever"
)

// Mute is one rung given to a member (a warning is a Mute row that is never active).
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

// Active reports whether m restricts its member at now: not a warning, not lifted, and forever or before Until.
// It is the rule `step <> 'warning' AND lifted_at IS NULL AND (step = 'forever' OR until > now)`.
func (m *Mute) Active(now time.Time) bool {
	if m == nil || m.Step == Warning || m.LiftedAt != nil {
		return false
	}
	return m.Step == Forever || (m.Until != nil && m.Until.After(now))
}

// Order is a moderator's request to give a member a step of the ladder.
type Order struct {
	UserID  string
	Step    Step
	Reason  string
	Until   *time.Time // a period's end; nil for a warning and forever
	ActorID string
}

// SanctionKind is the kind of a sanction log entry.
type SanctionKind string

// The sanction kinds.
const (
	SanctionHide    SanctionKind = "hide"
	SanctionUnhide  SanctionKind = "unhide"
	SanctionWarning SanctionKind = "warning"
	SanctionMute    SanctionKind = "mute"
	SanctionLift    SanctionKind = "lift"
)

// Entry is one line of the append-only sanction log: what was done (Kind) to whom (UserID), on which item (Target,
// for hide and unhide), why and by whom.
type Entry struct {
	Kind    SanctionKind
	UserID  string
	Target  *Target
	Reason  string
	ActorID string
	At      time.Time
}

// Store is the host's storage, bound to one transaction: every operation of this package runs on one Store, so the
// change and its log entry commit together. The log has no update or delete: it is append-only by its port.
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

// The refusals. A host maps each onto its own error answer.
var (
	ErrNotFound         = errors.New("moderation: not found")
	ErrOwnItem          = errors.New("moderation: own item")
	ErrAlreadyReported  = errors.New("moderation: already reported")
	ErrBlankReason      = errors.New("moderation: the reason is empty")
	ErrInvalidTarget    = errors.New("moderation: invalid target")
	ErrInvalidOutcome   = errors.New("moderation: invalid outcome")
	ErrInvalidStep      = errors.New("moderation: invalid step")
	ErrEndRequired      = errors.New("moderation: a period needs an end")
	ErrEndNotAllowed    = errors.New("moderation: only a period has an end")
	ErrEndInPast        = errors.New("moderation: the end is not in the future")
	ErrNotActive        = errors.New("moderation: the mute is not active")
	ErrInvalidEntry     = errors.New("moderation: invalid sanction entry")
	errMissingPrincipal = errors.New("moderation: a user id is required")
)

func blank(s string) bool { return strings.TrimSpace(s) == "" }

// wrap names the Store operation that failed; errors.Is still finds the host's error and the refusals above.
func wrap(op string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("moderation: %s: %w", op, err)
}

// FileReport records a member's report on an item: the item must exist and not be hidden (ErrNotFound), must not be
// the reporter's own (ErrOwnItem), and the reporter may hold one open report on it (ErrAlreadyReported). Whether the
// reporter may see the item is the host's rule, checked before.
func FileReport(ctx context.Context, s Store, r Report) error {
	switch {
	case !r.Target.valid():
		return ErrInvalidTarget
	case r.ReporterID == "":
		return errMissingPrincipal
	case blank(r.Reason):
		return ErrBlankReason
	}
	item, err := s.Item(ctx, r.Target)
	if err != nil {
		return wrap("item", err)
	}
	if item.Hidden {
		return ErrNotFound
	}
	if item.AuthorID == r.ReporterID {
		return ErrOwnItem
	}
	open, err := s.HasOpenReport(ctx, r.ReporterID, r.Target)
	if err != nil {
		return wrap("has open report", err)
	}
	if open {
		return ErrAlreadyReported
	}
	return wrap("add report", s.AddReport(ctx, r))
}

// Resolve closes every open report on t as outcome (Actioned or Dismissed) and answers how many it closed. It writes
// no log entry: a dismissal sanctions nobody, and an action is Hide's.
func Resolve(ctx context.Context, s Store, t Target, outcome Status, actorID string, at time.Time) (int, error) {
	if !t.valid() {
		return 0, ErrInvalidTarget
	}
	if outcome != Actioned && outcome != Dismissed {
		return 0, ErrInvalidOutcome
	}
	if _, err := s.Item(ctx, t); err != nil {
		return 0, wrap("item", err)
	}
	n, err := s.ResolveReports(ctx, t, outcome, actorID, at)
	return n, wrap("resolve reports", err)
}

// Hide hides t with a non-empty reason, logs a hide entry against its author and resolves its open reports as
// actioned. Hiding a hidden item changes nothing and logs nothing, but still actions any open report. changed tells
// the host whether to tell the author.
func Hide(ctx context.Context, s Store, t Target, h Hiding) (item Item, changed bool, err error) {
	if !t.valid() {
		return Item{}, false, ErrInvalidTarget
	}
	if blank(h.Reason) {
		return Item{}, false, ErrBlankReason
	}
	if item, err = s.Item(ctx, t); err != nil {
		return Item{}, false, wrap("item", err)
	}
	if !item.Hidden {
		if err := s.SetHidden(ctx, t, &h); err != nil {
			return Item{}, false, wrap("set hidden", err)
		}
		err := appendEntry(ctx, s, Entry{
			Kind: SanctionHide, UserID: item.AuthorID, Target: &t, Reason: h.Reason, ActorID: h.ActorID, At: h.At,
		})
		if err != nil {
			return Item{}, false, err
		}
		changed = true
	}
	if _, err := s.ResolveReports(ctx, t, Actioned, h.ActorID, h.At); err != nil {
		return Item{}, false, wrap("resolve reports", err)
	}
	return item, changed, nil
}

// Unhide shows a hidden item again and logs an unhide entry carrying the reason it had been hidden for. Its resolved
// reports stay resolved. Showing a visible item changes nothing and logs nothing.
func Unhide(ctx context.Context, s Store, t Target, actorID string, at time.Time) (item Item, changed bool, err error) {
	if !t.valid() {
		return Item{}, false, ErrInvalidTarget
	}
	if item, err = s.Item(ctx, t); err != nil {
		return Item{}, false, wrap("item", err)
	}
	if !item.Hidden {
		return item, false, nil
	}
	if err := s.SetHidden(ctx, t, nil); err != nil {
		return Item{}, false, wrap("set hidden", err)
	}
	err = appendEntry(ctx, s, Entry{
		Kind: SanctionUnhide, UserID: item.AuthorID, Target: &t, Reason: item.HiddenReason, ActorID: actorID, At: at,
	})
	if err != nil {
		return Item{}, false, err
	}
	return item, true, nil
}

// Check applies the ladder's rules to o at now: a known step, a non-empty reason, an end in the future for a period
// and none for a warning or forever.
func (o Order) Check(now time.Time) error {
	switch {
	case o.UserID == "":
		return errMissingPrincipal
	case o.Step != Warning && o.Step != Period && o.Step != Forever:
		return ErrInvalidStep
	case blank(o.Reason):
		return ErrBlankReason
	case o.Step == Period && o.Until == nil:
		return ErrEndRequired
	case o.Step != Period && o.Until != nil:
		return ErrEndNotAllowed
	case o.Step == Period && !o.Until.After(now):
		return ErrEndInPast
	}
	return nil
}

// Issue gives o's member its step at now and logs it (a warning entry, or a mute entry). A mute (a period or
// forever) replaces the member's active one: that one is lifted in the same transaction with no lift entry, because
// the new mute's entry records the change. A warning leaves an active mute in force.
func Issue(ctx context.Context, s Store, o Order, now time.Time) (Mute, error) {
	if err := o.Check(now); err != nil {
		return Mute{}, err
	}
	if o.Step != Warning {
		prev, err := s.ActiveMute(ctx, o.UserID, now)
		if err != nil {
			return Mute{}, wrap("active mute", err)
		}
		if prev != nil {
			if err := s.SetLifted(ctx, prev.ID, now); err != nil {
				return Mute{}, wrap("set lifted", err)
			}
		}
	}
	m := Mute{UserID: o.UserID, Step: o.Step, Reason: o.Reason, Until: o.Until, ActorID: o.ActorID, CreatedAt: now}
	id, err := s.AddMute(ctx, m)
	if err != nil {
		return Mute{}, wrap("add mute", err)
	}
	m.ID = id
	kind := SanctionMute
	if o.Step == Warning {
		kind = SanctionWarning
	}
	if err := appendEntry(ctx, s, Entry{Kind: kind, UserID: o.UserID, Reason: o.Reason, ActorID: o.ActorID, At: now}); err != nil {
		return Mute{}, err
	}
	return m, nil
}

// Lift ends mute id at now and logs a lift entry carrying the reason the mute was given for. Only an active mute
// can be lifted: a warning, a lapsed period or a lifted mute is ErrNotActive, and nothing is written.
func Lift(ctx context.Context, s Store, id, actorID string, now time.Time) (Mute, error) {
	if id == "" {
		return Mute{}, ErrNotFound
	}
	m, err := s.Mute(ctx, id)
	if err != nil {
		return Mute{}, wrap("mute", err)
	}
	if !m.Active(now) {
		return Mute{}, ErrNotActive
	}
	if err := s.SetLifted(ctx, id, now); err != nil {
		return Mute{}, wrap("set lifted", err)
	}
	m.LiftedAt = &now
	err = appendEntry(ctx, s, Entry{Kind: SanctionLift, UserID: m.UserID, Reason: m.Reason, ActorID: actorID, At: now})
	if err != nil {
		return Mute{}, err
	}
	return m, nil
}

// appendEntry checks e and appends it: a known kind, the member, the actor, an item for hide and unhide, and a
// non-empty reason for the acts that need one (hide, warning, mute).
func appendEntry(ctx context.Context, s Store, e Entry) error {
	if err := e.check(); err != nil {
		return err
	}
	return wrap("append", s.Append(ctx, e))
}

func (e Entry) check() error {
	onItem := e.Kind == SanctionHide || e.Kind == SanctionUnhide
	switch {
	case e.Kind != SanctionHide && e.Kind != SanctionUnhide && e.Kind != SanctionWarning && e.Kind != SanctionMute &&
		e.Kind != SanctionLift:
		return fmt.Errorf("%w: kind %q", ErrInvalidEntry, e.Kind)
	case e.UserID == "" || e.ActorID == "":
		return fmt.Errorf("%w: %s needs the member and the actor", ErrInvalidEntry, e.Kind)
	case onItem && (e.Target == nil || !e.Target.valid()):
		return fmt.Errorf("%w: %s needs an item", ErrInvalidEntry, e.Kind)
	case !onItem && e.Target != nil:
		return fmt.Errorf("%w: %s is on a member, not an item", ErrInvalidEntry, e.Kind)
	case (e.Kind == SanctionHide || e.Kind == SanctionWarning || e.Kind == SanctionMute) && blank(e.Reason):
		return ErrBlankReason
	}
	return nil
}
