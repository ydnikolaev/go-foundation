package reactions_test

import (
	"context"
	"fmt"

	"github.com/ydnikolaev/go-foundation/units/reactions"
)

// memStore is the Store a caller would back with its own table and a unique key on user and target.
type memStore map[reactions.Target]map[string]bool

func (m memStore) Add(_ context.Context, t reactions.Target, userID string) error {
	if m[t] == nil {
		m[t] = map[string]bool{}
	}
	m[t][userID] = true
	return nil
}

func (m memStore) Remove(_ context.Context, t reactions.Target, userID string) error {
	delete(m[t], userID)
	return nil
}

func (m memStore) State(_ context.Context, t reactions.Target, userID string) (reactions.State, error) {
	return reactions.State{Count: len(m[t]), Mine: m[t][userID]}, nil
}

func Example() {
	ctx := context.Background()
	store := memStore{}
	post := reactions.Target{Kind: "post", ID: "p1"}

	st, _ := reactions.Put(ctx, store, post, "ann")
	fmt.Println(st.Count, st.Mine)
	st, _ = reactions.Put(ctx, store, post, "ann") // again: still one
	fmt.Println(st.Count, st.Mine)
	st, _ = reactions.Put(ctx, store, post, "bob")
	fmt.Println(st.Count, st.Mine)
	st, _ = reactions.Remove(ctx, store, post, "ann")
	fmt.Println(st.Count, st.Mine)
	// Output:
	// 1 true
	// 1 true
	// 2 true
	// 1 false
}
