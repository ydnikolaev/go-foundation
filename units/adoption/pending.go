package adoption

import (
	"fmt"
	"slices"
)

// Clock is the repository's own adoption clock. Through is the release it
// adopted through ("" means never recorded). Ahead lists change ids adopted
// from notes authored ahead of a release: no version can name them yet, and a
// version watermark would hide every change added to that note afterwards.
//
// The clock lives in a file the repository commits; where is the tool's
// choice. A machine-local clock is lost to every clone and worktree.
type Clock struct {
	Through string   `yaml:"adapted_through,omitempty" json:"adapted_through,omitempty"`
	Ahead   []string `yaml:"adapted_ahead,omitempty" json:"adapted_ahead,omitempty"`
}

// Policy is what differs between tools. PartyScopes names the scope values
// that oblige another party (a2a: "space"); a scope that is neither local,
// none nor listed here obliges nothing.
type Policy struct {
	PartyScopes []string
}

// Group orders obligations so a reader can act alone first. Lower sorts
// first; sort within a group by ImpactOrder.
type Group int

const (
	// GroupLocalWithRun is a local obligation that carries a command.
	GroupLocalWithRun Group = iota
	// GroupLocalOther is a local obligation with a detect or prose only.
	GroupLocalOther
	// GroupParty is an obligation in a party scope: real, but it may need
	// someone else's access, so it never outranks unilateral work.
	GroupParty
	// GroupKnownIssue is a known issue that obliges something; kind decides
	// the group before scope does.
	GroupKnownIssue
)

// PendingItem is one obligation: the change, the release that introduced it,
// and its group.
type PendingItem struct {
	Version string
	Change  Change
	Group   Group
}

// Projection is the whole answer for one clock and one binary.
type Projection struct {
	Clock         Clock
	BinaryVersion string
	// Releases counts the releases in range plus the notes authored ahead,
	// before standing issues are attached.
	Releases int
	// StartedFromOldest is true when the clock was never recorded; the
	// caller must say so rather than present a guessed baseline.
	StartedFromOldest bool
	// Oldest is the oldest release in the corpus, set with StartedFromOldest.
	Oldest string
	// Items are the obligations in corpus order, not yet sorted by group.
	Items []PendingItem
	// AuthoredAhead is every obliging change id in a note newer than the
	// binary: the set a record verb adds to Clock.Ahead.
	AuthoredAhead []string
}

func (p Policy) group(ch Change) (Group, bool) {
	if ch.Kind == KindKnownIssue {
		if ch.Action.Scope == ScopeNone {
			return 0, false
		}
		return GroupKnownIssue, true
	}
	switch {
	case ch.Action.Scope == ScopeLocal && len(ch.Action.Run) > 0:
		return GroupLocalWithRun, true
	case ch.Action.Scope == ScopeLocal:
		return GroupLocalOther, true
	case ch.Action.Scope != "" && ch.Action.Scope != ScopeNone && slices.Contains(p.PartyScopes, ch.Action.Scope):
		return GroupParty, true
	default:
		return 0, false
	}
}

// ValidateClock refuses a clock newer than the binary. A never-recorded clock
// always validates.
func ValidateClock(c Clock, binaryVersion string) error {
	const op = "ValidateClock"
	if c.Through == "" {
		return nil
	}
	input := fmt.Sprintf("adapted_through=%s binary=%s", c.Through, binaryVersion)
	ahead, err := OlderThan(binaryVersion, c.Through)
	if err != nil {
		return &Error{Op: op, Input: input, Err: err}
	}
	if ahead {
		return &Error{Op: op, Input: input, Err: ErrClockAheadOfBinary}
	}
	return nil
}

func authoredAhead(all []Release, binaryVersion string) ([]Release, error) {
	var out []Release
	for _, r := range all {
		if r.Released != Unreleased {
			continue
		}
		ahead, err := OlderThan(binaryVersion, r.Version)
		if err != nil {
			// A note that cannot be compared was not judged; it must not
			// read as excluded.
			return nil, &Error{Op: "authoredAhead", Input: r.Version, Err: err}
		}
		if ahead {
			out = append(out, r)
		}
	}
	return out, nil
}

// Pending computes what the repository still owes: every obliging change in
// (clock.Through, binaryVersion], plus the notes authored ahead of the binary,
// plus the standing issues, minus the ids in clock.Ahead. all must be in
// Load's ascending order.
func Pending(all []Release, standing []Change, clock Clock, binaryVersion string, pol Policy) (Projection, error) {
	if _, err := canonical(binaryVersion); err != nil {
		return Projection{}, &Error{
			Op:    "Pending",
			Input: fmt.Sprintf("adapted_through=%s binary=%s", clock.Through, binaryVersion),
			Err:   ErrBinaryVersionUnusable,
		}
	}
	if err := ValidateClock(clock, binaryVersion); err != nil {
		return Projection{}, err
	}

	proj := Projection{Clock: clock, BinaryVersion: binaryVersion}
	if clock.Through == "" && len(all) > 0 {
		proj.StartedFromOldest = true
		proj.Oldest = all[0].Version
	}

	ahead, err := authoredAhead(all, binaryVersion)
	if err != nil {
		return Projection{}, err
	}
	for _, r := range ahead {
		for _, ch := range r.Changes {
			if _, ok := pol.group(ch); ok {
				proj.AuthoredAhead = append(proj.AuthoredAhead, ch.ID)
			}
		}
	}

	slice := Since(all, clock.Through, binaryVersion)
	slice = append(slice[:len(slice):len(slice)], ahead...)
	proj.Releases = len(slice)
	slice = AttachStanding(slice, all, standing)

	adopted := make(map[string]struct{}, len(clock.Ahead))
	for _, id := range clock.Ahead {
		adopted[id] = struct{}{}
	}
	for _, r := range slice {
		for _, ch := range r.Changes {
			g, ok := pol.group(ch)
			if !ok {
				continue
			}
			if _, done := adopted[ch.ID]; done {
				continue
			}
			proj.Items = append(proj.Items, PendingItem{Version: r.Version, Change: ch, Group: g})
		}
	}
	return proj, nil
}
