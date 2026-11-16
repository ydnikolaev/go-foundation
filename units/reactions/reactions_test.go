package reactions

import (
	"context"
	"errors"
	"testing"
)

// fakeStore is an in-memory Store: one set of user ids per target, the writes recorded in order. failOn makes the
// named operation fail.
type fakeStore struct {
	rows   map[Target]map[string]bool
	writes []string
	failOn string
}

var errStore = errors.New("test: store down")

func newFakeStore() *fakeStore { return &fakeStore{rows: map[Target]map[string]bool{}} }

func (s *fakeStore) Add(_ context.Context, t Target, userID string) error {
	if s.failOn == "add" {
		return errStore
	}
	if s.rows[t] == nil {
		s.rows[t] = map[string]bool{}
	}
	s.rows[t][userID] = true
	s.writes = append(s.writes, "add "+userID)
	return nil
}

func (s *fakeStore) Remove(_ context.Context, t Target, userID string) error {
	if s.failOn == "remove" {
		return errStore
	}
	delete(s.rows[t], userID)
	s.writes = append(s.writes, "remove "+userID)
	return nil
}

func (s *fakeStore) State(_ context.Context, t Target, userID string) (State, error) {
	if s.failOn == "state" {
		return State{}, errStore
	}
	return State{Count: len(s.rows[t]), Mine: s.rows[t][userID]}, nil
}

var (
	post    = Target{Kind: "post", ID: "p1"}
	comment = Target{Kind: "comment", ID: "p1"} // the same id on another kind is another target
)

// Donor TestService_Toggle "success like" and TestRepository_ToggleAndCount: a put answers the target's count with
// the user's heart in it; a second put changes nothing (one heart per user, idempotent where the donor toggled).
func TestPutIsIdempotent(t *testing.T) {
	t.Parallel()
	s := newFakeStore()
	ctx := context.Background()
	for i := range 2 {
		st, err := Put(ctx, s, post, "u1")
		if err != nil {
			t.Fatalf("put %d: %v", i+1, err)
		}
		if st != (State{Count: 1, Mine: true}) {
			t.Errorf("put %d: %+v, want one heart, mine", i+1, st)
		}
	}
	st, err := Put(ctx, s, post, "u2")
	if err != nil || st != (State{Count: 2, Mine: true}) {
		t.Errorf("another user's put: %+v %v, want two hearts, mine", st, err)
	}
}

// Donor TestService_Toggle "success unlike": a remove answers the count without the user's heart; a second remove,
// or a remove of a heart never given, changes nothing.
func TestRemoveIsIdempotent(t *testing.T) {
	t.Parallel()
	s := newFakeStore()
	ctx := context.Background()
	for _, u := range []string{"u1", "u2"} {
		if _, err := Put(ctx, s, post, u); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 2 {
		st, err := Remove(ctx, s, post, "u1")
		if err != nil {
			t.Fatalf("remove %d: %v", i+1, err)
		}
		if st != (State{Count: 1, Mine: false}) {
			t.Errorf("remove %d: %+v, want the other heart only, not mine", i+1, st)
		}
	}
	st, err := Remove(ctx, s, post, "u3")
	if err != nil || st != (State{Count: 1, Mine: false}) {
		t.Errorf("remove of a heart never given: %+v %v", st, err)
	}
}

// Targets of different kinds with one id are counted apart.
func TestTargetsAreApart(t *testing.T) {
	t.Parallel()
	s := newFakeStore()
	ctx := context.Background()
	if _, err := Put(ctx, s, post, "u1"); err != nil {
		t.Fatal(err)
	}
	st, err := Put(ctx, s, comment, "u2")
	if err != nil || st != (State{Count: 1, Mine: true}) {
		t.Errorf("the comment's state %+v %v, want its own heart only", st, err)
	}
}

// Donor TestInvalidEntityType and TestValidEntityType: a target without a kind or an id, or a call without a user, is
// refused before the store is reached. The kinds themselves are the caller's.
func TestRefusesIncompleteCalls(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, c := range []struct {
		name string
		t    Target
		user string
		want error
	}{
		{"no kind", Target{ID: "p1"}, "u1", ErrInvalidTarget},
		{"no id", Target{Kind: "post"}, "u1", ErrInvalidTarget},
		{"no user", post, "", ErrNoUser},
	} {
		s := newFakeStore()
		if _, err := Put(ctx, s, c.t, c.user); !errors.Is(err, c.want) {
			t.Errorf("put, %s: %v, want %v", c.name, err, c.want)
		}
		if _, err := Remove(ctx, s, c.t, c.user); !errors.Is(err, c.want) {
			t.Errorf("remove, %s: %v, want %v", c.name, err, c.want)
		}
		if len(s.writes) != 0 {
			t.Errorf("%s: the store was written: %v", c.name, s.writes)
		}
	}
}

// Donor TestService_Toggle "repo error": a store failure comes back wrapped, still matching the store's error.
func TestStoreErrorsPassThrough(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, op := range []string{"add", "remove", "state"} {
		s := newFakeStore()
		s.failOn = op
		_, errPut := Put(ctx, s, post, "u1")
		_, errRemove := Remove(ctx, s, post, "u1")
		if op != "remove" && !errors.Is(errPut, errStore) {
			t.Errorf("put with %s failing: %v, want the store's error", op, errPut)
		}
		if op != "add" && !errors.Is(errRemove, errStore) {
			t.Errorf("remove with %s failing: %v, want the store's error", op, errRemove)
		}
	}
}
