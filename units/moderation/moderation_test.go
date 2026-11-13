package moderation

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)

// fakeStore keeps items, reports, mutes and the log in memory. Its log is a slice only Append grows, and every
// test compares it against the entries it expects, so an operation that rewrote or dropped an entry would fail.
type fakeStore struct {
	items   map[Target]*Item
	reports []Report
	status  []Status
	mutes   []Mute
	users   map[string]bool
	log     []Entry
	failOn  string // the method name that fails, when set
	locked  []string
}

var errStore = errors.New("test: store down")

func newFake() *fakeStore {
	return &fakeStore{items: map[Target]*Item{}, users: map[string]bool{"alice": true, "bob": true}}
}

func (f *fakeStore) fail(name string) error {
	if f.failOn == name {
		return errStore
	}
	return nil
}

func (f *fakeStore) add(t Target, author string) { f.items[t] = &Item{Target: t, AuthorID: author} }

func (f *fakeStore) Item(_ context.Context, t Target) (Item, error) {
	if err := f.fail("Item"); err != nil {
		return Item{}, err
	}
	it, ok := f.items[t]
	if !ok {
		return Item{}, ErrNotFound
	}
	return *it, nil
}

func (f *fakeStore) SetHidden(_ context.Context, t Target, h *Hiding) error {
	if err := f.fail("SetHidden"); err != nil {
		return err
	}
	it := f.items[t]
	it.Hidden = h != nil
	it.HiddenReason = ""
	if h != nil {
		it.HiddenReason = h.Reason
	}
	return nil
}

func (f *fakeStore) HasOpenReport(_ context.Context, reporterID string, t Target) (bool, error) {
	for i, r := range f.reports {
		if r.ReporterID == reporterID && r.Target == t && f.status[i] == Open {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeStore) AddReport(_ context.Context, r Report) error {
	if err := f.fail("AddReport"); err != nil {
		return err
	}
	f.reports = append(f.reports, r)
	f.status = append(f.status, Open)
	return nil
}

func (f *fakeStore) ResolveReports(_ context.Context, t Target, outcome Status, _ string, _ time.Time) (int, error) {
	n := 0
	for i, r := range f.reports {
		if r.Target == t && f.status[i] == Open {
			f.status[i] = outcome
			n++
		}
	}
	return n, nil
}

func (f *fakeStore) ActiveMute(_ context.Context, userID string, now time.Time) (*Mute, error) {
	f.locked = append(f.locked, userID)
	for i := range f.mutes {
		if m := f.mutes[i]; m.UserID == userID && m.Active(now) {
			return &m, nil
		}
	}
	return nil, nil
}

func (f *fakeStore) Mute(_ context.Context, id string) (Mute, error) {
	for _, m := range f.mutes {
		if m.ID == id {
			return m, nil
		}
	}
	return Mute{}, ErrNotFound
}

func (f *fakeStore) AddMute(_ context.Context, m Mute) (string, error) {
	if !f.users[m.UserID] {
		return "", ErrNotFound
	}
	m.ID = fmt.Sprintf("m%d", len(f.mutes)+1)
	f.mutes = append(f.mutes, m)
	return m.ID, nil
}

func (f *fakeStore) SetLifted(_ context.Context, id string, at time.Time) error {
	for i := range f.mutes {
		if f.mutes[i].ID == id {
			f.mutes[i].LiftedAt = &at
		}
	}
	return nil
}

func (f *fakeStore) Append(_ context.Context, e Entry) error {
	if err := f.fail("Append"); err != nil {
		return err
	}
	f.log = append(f.log, e)
	return nil
}

var (
	post  = Target{Kind: KindPost, ID: "p1"}
	other = Target{Kind: KindPost, ID: "p2"}
)

func ptr(t time.Time) *time.Time { return &t }

func TestFileReport(t *testing.T) {
	t.Parallel()
	f := newFake()
	f.add(post, "alice")
	report := Report{Target: post, ReporterID: "bob", Reason: "spam", At: t0}

	if err := FileReport(t.Context(), f, report); err != nil {
		t.Fatalf("first report: %v", err)
	}
	if err := FileReport(t.Context(), f, report); !errors.Is(err, ErrAlreadyReported) {
		t.Errorf("second open report by the same member: %v, want ErrAlreadyReported", err)
	}
	own := report
	own.ReporterID = "alice"
	if err := FileReport(t.Context(), f, own); !errors.Is(err, ErrOwnItem) {
		t.Errorf("report on one's own item: %v, want ErrOwnItem", err)
	}
	missing := report
	missing.Target = other
	if err := FileReport(t.Context(), f, missing); !errors.Is(err, ErrNotFound) {
		t.Errorf("report on a missing item: %v, want ErrNotFound", err)
	}
	blankReason := report
	blankReason.Reason = "  "
	if err := FileReport(t.Context(), f, blankReason); !errors.Is(err, ErrBlankReason) {
		t.Errorf("report with a blank reason: %v, want ErrBlankReason", err)
	}
	if err := FileReport(t.Context(), f, Report{Target: Target{Kind: "video", ID: "x"}, ReporterID: "bob",
		Reason: "spam"}); !errors.Is(err, ErrInvalidTarget) {
		t.Errorf("report on an unknown kind: %v, want ErrInvalidTarget", err)
	}
	if len(f.reports) != 1 {
		t.Errorf("%d reports stored, want 1", len(f.reports))
	}

	// Once resolved, the same member may report the item again.
	if n, err := Resolve(t.Context(), f, post, Dismissed, "admin", t0); err != nil || n != 1 {
		t.Fatalf("dismiss: %d, %v; want 1 closed", n, err)
	}
	if err := FileReport(t.Context(), f, report); err != nil {
		t.Errorf("report after the first was dismissed: %v", err)
	}

	// A hidden item takes no report.
	f.items[post].Hidden = true
	report.ReporterID = "carol"
	if err := FileReport(t.Context(), f, report); !errors.Is(err, ErrNotFound) {
		t.Errorf("report on a hidden item: %v, want ErrNotFound", err)
	}
	if len(f.log) != 0 {
		t.Errorf("reports wrote %d log entries, want none", len(f.log))
	}
}

func TestResolve(t *testing.T) {
	t.Parallel()
	f := newFake()
	f.add(post, "alice")
	f.add(other, "alice")
	for _, who := range []string{"bob", "carol", "dave"} {
		if err := FileReport(t.Context(), f, Report{Target: post, ReporterID: who, Reason: "spam"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := FileReport(t.Context(), f, Report{Target: other, ReporterID: "bob", Reason: "spam"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(t.Context(), f, post, Open, "admin", t0); !errors.Is(err, ErrInvalidOutcome) {
		t.Errorf("resolve as open: %v, want ErrInvalidOutcome", err)
	}
	if _, err := Resolve(t.Context(), f, Target{Kind: KindPost, ID: "nope"}, Dismissed, "admin", t0); !errors.Is(err,
		ErrNotFound) {
		t.Errorf("resolve a missing item: %v, want ErrNotFound", err)
	}
	n, err := Resolve(t.Context(), f, post, Dismissed, "admin", t0)
	if err != nil || n != 3 {
		t.Fatalf("resolve: %d, %v; want all 3 closed", n, err)
	}
	if want := []Status{Dismissed, Dismissed, Dismissed, Open}; !reflect.DeepEqual(f.status, want) {
		t.Errorf("statuses %v, want %v (the other item's report stays open)", f.status, want)
	}
	if len(f.log) != 0 {
		t.Errorf("a dismissal wrote %d log entries, want none", len(f.log))
	}
}

func TestHideAndUnhide(t *testing.T) {
	t.Parallel()
	f := newFake()
	f.add(post, "alice")
	for _, who := range []string{"bob", "carol"} {
		if err := FileReport(t.Context(), f, Report{Target: post, ReporterID: who, Reason: "spam"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := Hide(t.Context(), f, post, Hiding{Reason: " ", ActorID: "admin", At: t0}); !errors.Is(err,
		ErrBlankReason) {
		t.Errorf("hide with a blank reason: %v, want ErrBlankReason", err)
	}
	item, changed, err := Hide(t.Context(), f, post, Hiding{Reason: "rule 3", ActorID: "admin", At: t0})
	if err != nil || !changed || item.AuthorID != "alice" {
		t.Fatalf("hide: %+v %v %v", item, changed, err)
	}
	if !f.items[post].Hidden || f.items[post].HiddenReason != "rule 3" {
		t.Errorf("item after hide: %+v", f.items[post])
	}
	if want := []Status{Actioned, Actioned}; !reflect.DeepEqual(f.status, want) {
		t.Errorf("reports after hide: %v, want %v", f.status, want)
	}
	// Hiding again changes nothing and logs nothing.
	if _, changed, err := Hide(t.Context(), f, post, Hiding{Reason: "again", ActorID: "admin", At: t0}); err != nil ||
		changed {
		t.Errorf("hide a hidden item: changed %v, %v", changed, err)
	}
	if _, changed, err := Unhide(t.Context(), f, post, "admin", t0.Add(time.Hour)); err != nil || !changed {
		t.Errorf("unhide: changed %v, %v", changed, err)
	}
	if f.items[post].Hidden {
		t.Error("item still hidden after unhide")
	}
	if want := []Status{Actioned, Actioned}; !reflect.DeepEqual(f.status, want) {
		t.Errorf("reports after unhide: %v, want %v (unhiding reopens nothing)", f.status, want)
	}
	if _, changed, err := Unhide(t.Context(), f, post, "admin", t0); err != nil || changed {
		t.Errorf("unhide a visible item: changed %v, %v", changed, err)
	}
	want := []Entry{
		{Kind: SanctionHide, UserID: "alice", Target: &post, Reason: "rule 3", ActorID: "admin", At: t0},
		{Kind: SanctionUnhide, UserID: "alice", Target: &post, Reason: "rule 3", ActorID: "admin",
			At: t0.Add(time.Hour)},
	}
	if !reflect.DeepEqual(f.log, want) {
		t.Errorf("log %+v, want %+v", f.log, want)
	}
}

func TestHideFailureLeavesNoEntry(t *testing.T) {
	t.Parallel()
	f := newFake()
	f.add(post, "alice")
	f.failOn = "SetHidden"
	if _, _, err := Hide(t.Context(), f, post, Hiding{Reason: "r", ActorID: "admin", At: t0}); !errors.Is(err, errStore) {
		t.Fatalf("hide with the store down: %v", err)
	}
	if len(f.log) != 0 {
		t.Errorf("a failed hide logged %d entries", len(f.log))
	}
}

func TestOrderCheck(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		o    Order
		want error
	}{
		{"warning", Order{UserID: "u", Step: Warning, Reason: "r"}, nil},
		{"forever", Order{UserID: "u", Step: Forever, Reason: "r"}, nil},
		{"period", Order{UserID: "u", Step: Period, Reason: "r", Until: ptr(t0.Add(time.Hour))}, nil},
		{"blank reason", Order{UserID: "u", Step: Forever, Reason: "\t"}, ErrBlankReason},
		{"unknown step", Order{UserID: "u", Step: "ban", Reason: "r"}, ErrInvalidStep},
		{"period without end", Order{UserID: "u", Step: Period, Reason: "r"}, ErrEndRequired},
		{"period ending now", Order{UserID: "u", Step: Period, Reason: "r", Until: ptr(t0)}, ErrEndInPast},
		{"warning with end", Order{UserID: "u", Step: Warning, Reason: "r", Until: ptr(t0.Add(time.Hour))},
			ErrEndNotAllowed},
		{"forever with end", Order{UserID: "u", Step: Forever, Reason: "r", Until: ptr(t0.Add(time.Hour))},
			ErrEndNotAllowed},
		{"no member", Order{Step: Forever, Reason: "r"}, errMissingPrincipal},
	}
	for _, c := range cases {
		if err := c.o.Check(t0); !errors.Is(err, c.want) || (c.want == nil && err != nil) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
}

// TestActive is the database rule `step <> 'warning' AND lifted_at IS NULL AND (step = 'forever' OR until > now)`.
func TestActive(t *testing.T) {
	t.Parallel()
	lifted := t0.Add(-time.Minute)
	cases := []struct {
		name string
		m    *Mute
		want bool
	}{
		{"none", nil, false},
		{"warning", &Mute{Step: Warning}, false},
		{"forever", &Mute{Step: Forever}, true},
		{"forever lifted", &Mute{Step: Forever, LiftedAt: &lifted}, false},
		{"period running", &Mute{Step: Period, Until: ptr(t0.Add(time.Second))}, true},
		{"period ending now", &Mute{Step: Period, Until: ptr(t0)}, false},
		{"period lapsed", &Mute{Step: Period, Until: ptr(t0.Add(-time.Hour))}, false},
		{"period lifted", &Mute{Step: Period, Until: ptr(t0.Add(time.Hour)), LiftedAt: &lifted}, false},
	}
	for _, c := range cases {
		if got := c.m.Active(t0); got != c.want {
			t.Errorf("%s: Active = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestIssueAndLift(t *testing.T) {
	t.Parallel()
	f := newFake()
	until := t0.Add(24 * time.Hour)

	first, err := Issue(t.Context(), f, Order{UserID: "bob", Step: Period, Reason: "spam", Until: &until,
		ActorID: "admin"}, t0)
	if err != nil {
		t.Fatalf("mute: %v", err)
	}
	if !first.Active(t0) {
		t.Error("the new period mute is not active")
	}
	// A warning leaves the mute in force.
	if _, err := Issue(t.Context(), f, Order{UserID: "bob", Step: Warning, Reason: "tone", ActorID: "admin"},
		t0); err != nil {
		t.Fatalf("warning: %v", err)
	}
	if m, _ := f.Mute(t.Context(), first.ID); !m.Active(t0) {
		t.Error("a warning lifted the active mute")
	}
	// A new mute replaces the active one: lifted, with no lift entry.
	second, err := Issue(t.Context(), f, Order{UserID: "bob", Step: Forever, Reason: "again", ActorID: "admin"}, t0)
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if m, _ := f.Mute(t.Context(), first.ID); m.Active(t0) || m.LiftedAt == nil {
		t.Errorf("the replaced mute: %+v, want lifted", m)
	}
	// Lift ends it; lifting again, a warning or a lapsed mute is ErrNotActive and writes nothing.
	later := t0.Add(time.Hour)
	if _, err := Lift(t.Context(), f, second.ID, "admin", later); err != nil {
		t.Fatalf("lift: %v", err)
	}
	if m, _ := f.Mute(t.Context(), second.ID); m.Active(later) {
		t.Error("the lifted mute is still active")
	}
	if _, err := Lift(t.Context(), f, second.ID, "admin", later); !errors.Is(err, ErrNotActive) {
		t.Errorf("lift twice: %v, want ErrNotActive", err)
	}
	if _, err := Lift(t.Context(), f, "m2", "admin", later); !errors.Is(err, ErrNotActive) {
		t.Errorf("lift a warning: %v, want ErrNotActive", err)
	}
	if _, err := Lift(t.Context(), f, "m404", "admin", later); !errors.Is(err, ErrNotFound) {
		t.Errorf("lift a missing mute: %v, want ErrNotFound", err)
	}
	// A refused order and an unknown member write nothing.
	if _, err := Issue(t.Context(), f, Order{UserID: "bob", Step: Period, Reason: "x", ActorID: "admin"},
		t0); !errors.Is(err, ErrEndRequired) {
		t.Errorf("period without end: %v", err)
	}
	if _, err := Issue(t.Context(), f, Order{UserID: "ghost", Step: Forever, Reason: "x", ActorID: "admin"},
		t0); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown member: %v, want ErrNotFound", err)
	}

	want := []Entry{
		{Kind: SanctionMute, UserID: "bob", Reason: "spam", ActorID: "admin", At: t0},
		{Kind: SanctionWarning, UserID: "bob", Reason: "tone", ActorID: "admin", At: t0},
		{Kind: SanctionMute, UserID: "bob", Reason: "again", ActorID: "admin", At: t0},
		{Kind: SanctionLift, UserID: "bob", Reason: "again", ActorID: "admin", At: later},
	}
	if !reflect.DeepEqual(f.log, want) {
		t.Errorf("log\n %+v\nwant\n %+v", f.log, want)
	}
	if want := []string{"bob", "bob", "ghost"}; !reflect.DeepEqual(f.locked, want) {
		t.Errorf("ActiveMute asked for %v, want %v (mutes only, warnings never)", f.locked, want)
	}
}

func TestIssueFailureLeavesNoEntry(t *testing.T) {
	t.Parallel()
	f := newFake()
	f.failOn = "Append"
	if _, err := Issue(t.Context(), f, Order{UserID: "bob", Step: Forever, Reason: "x", ActorID: "admin"},
		t0); !errors.Is(err, errStore) {
		t.Fatalf("issue with the log down: %v", err)
	}
	if len(f.log) != 0 {
		t.Errorf("log %+v, want empty", f.log)
	}
}

func TestEntryCheck(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		e    Entry
		want error
	}{
		{"hide", Entry{Kind: SanctionHide, UserID: "a", Target: &post, Reason: "r", ActorID: "x"}, nil},
		{"unhide without reason", Entry{Kind: SanctionUnhide, UserID: "a", Target: &post, ActorID: "x"}, nil},
		{"lift without reason", Entry{Kind: SanctionLift, UserID: "a", ActorID: "x"}, nil},
		{"unknown kind", Entry{Kind: "ban", UserID: "a", ActorID: "x"}, ErrInvalidEntry},
		{"no actor", Entry{Kind: SanctionMute, UserID: "a", Reason: "r"}, ErrInvalidEntry},
		{"hide without item", Entry{Kind: SanctionHide, UserID: "a", Reason: "r", ActorID: "x"}, ErrInvalidEntry},
		{"mute on an item", Entry{Kind: SanctionMute, UserID: "a", Target: &post, Reason: "r", ActorID: "x"},
			ErrInvalidEntry},
		{"warning without reason", Entry{Kind: SanctionWarning, UserID: "a", ActorID: "x"}, ErrBlankReason},
	}
	for _, c := range cases {
		if err := c.e.check(); !errors.Is(err, c.want) || (c.want == nil && err != nil) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
}
