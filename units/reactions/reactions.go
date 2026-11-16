// Package reactions holds the rules of a one-per-user reaction (a heart) on a target: putting it and taking it back
// are idempotent, and each answers the target's resulting state, its count and whether the user's reaction is in it.
//
// The package is a unit: standard library only, no project imports. Tables, queries and who may react to what are
// the caller's; the rules reach the stored reactions through the Store port, which the caller implements.
package reactions

import (
	"context"
	"errors"
	"fmt"
)

// The refusals the rules give. A caller maps each onto its own error codes.
var (
	// ErrInvalidTarget: the target has no kind or no id.
	ErrInvalidTarget = errors.New("reactions: invalid target")
	// ErrNoUser: the call names no user.
	ErrNoUser = errors.New("reactions: a user id is required")
)

// Target is what a reaction is on: a kind the caller names ("post", "comment") and the item's id. Targets of two kinds
// with the same id are two targets.
type Target struct {
	Kind string
	ID   string
}

func (t Target) valid() bool { return t.Kind != "" && t.ID != "" }

// State is a target's reactions as one user reads them: how many there are and whether the user's is one of them.
type State struct {
	Count int
	Mine  bool
}

// Store reads and changes the stored reactions. Add and Remove are idempotent: adding a reaction the user holds, or
// removing one they do not, changes nothing and is no error (a caller backs Add with a unique key on user and target).
type Store interface {
	Add(ctx context.Context, t Target, userID string) error
	Remove(ctx context.Context, t Target, userID string) error
	State(ctx context.Context, t Target, userID string) (State, error)
}

func check(t Target, userID string) error {
	switch {
	case !t.valid():
		return ErrInvalidTarget
	case userID == "":
		return ErrNoUser
	}
	return nil
}

// Put gives userID's reaction to t, once however often it is called, and answers t's state after it.
func Put(ctx context.Context, s Store, t Target, userID string) (State, error) {
	if err := check(t, userID); err != nil {
		return State{}, err
	}
	if err := s.Add(ctx, t, userID); err != nil {
		return State{}, fmt.Errorf("reactions: add to %s %s: %w", t.Kind, t.ID, err)
	}
	return state(ctx, s, t, userID)
}

// Remove takes userID's reaction off t, if it is there, and answers t's state after it.
func Remove(ctx context.Context, s Store, t Target, userID string) (State, error) {
	if err := check(t, userID); err != nil {
		return State{}, err
	}
	if err := s.Remove(ctx, t, userID); err != nil {
		return State{}, fmt.Errorf("reactions: remove from %s %s: %w", t.Kind, t.ID, err)
	}
	return state(ctx, s, t, userID)
}

func state(ctx context.Context, s Store, t Target, userID string) (State, error) {
	st, err := s.State(ctx, t, userID)
	if err != nil {
		return State{}, fmt.Errorf("reactions: state of %s %s: %w", t.Kind, t.ID, err)
	}
	return st, nil
}
