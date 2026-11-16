// Package comments holds the rules of a comment thread one level deep: where a reply attaches, how long its author
// may edit it, what a comment must and may carry, the honeypot, what a delete leaves behind, and which mentions an
// edit adds.
//
// The package is a unit: standard library only, no project imports. Tables, queries and access rules are the
// caller's; the rules that read stored comments do so through the Store port, which the caller implements on its
// own transaction.
package comments

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// The refusals the rules give. A caller maps each onto its own error codes.
var (
	// ErrNotFound: the comment does not exist, or it is a deleted stub where a live comment is needed.
	ErrNotFound = errors.New("comments: not found")
	// ErrEditWindowClosed: the edit window after the comment's creation has passed.
	ErrEditWindowClosed = errors.New("comments: edit window closed")
	// ErrTooManyPhotos: more photos than the cap.
	ErrTooManyPhotos = errors.New("comments: too many photos")
	// ErrEmpty: a comment with neither text nor a photo.
	ErrEmpty = errors.New("comments: empty comment")
)

// Comment is what the rules read of a stored comment. ParentID is "" for a top comment. Deleted marks a stub: the
// trace a deleted comment with replies leaves, with no body or photos, its replies kept.
type Comment struct {
	ID        string
	ParentID  string
	AuthorID  string
	CreatedAt time.Time
	Deleted   bool
}

// Top reports whether c is a top comment (not a reply).
func (c Comment) Top() bool { return c.ParentID == "" }

// Store reads and changes the stored comments, on the caller's transaction. Get answers ErrNotFound (or an error
// wrapping it) for a comment that does not exist; a caller that serialises deletes takes the row's lock in Get.
type Store interface {
	Get(ctx context.Context, id string) (Comment, error)
	// Replies counts the comments whose parent is id.
	Replies(ctx context.Context, id string) (int, error)
	// Stub turns id into a stub: marks it deleted and drops its body, photos and reactions; its replies stay.
	Stub(ctx context.Context, id string) error
	// Remove deletes id outright.
	Remove(ctx context.Context, id string) error
}

// Attach is where a reply to parent is stored: under parent when it is a top comment, under parent's own parent
// when it is a reply, so a thread is never deeper than one level.
func Attach(parent Comment) string {
	if parent.Top() {
		return parent.ID
	}
	return parent.ParentID
}

// ReplyTo is the parent a reply answering answered is stored under (Attach). A stub cannot be answered: ErrNotFound.
func ReplyTo(answered Comment) (parentID string, err error) {
	if answered.Deleted {
		return "", fmt.Errorf("answer comment %s: a stub: %w", answered.ID, ErrNotFound)
	}
	return Attach(answered), nil
}

// CheckEdit allows an edit at now while now is within window of createdAt (the boundary included); after it, it is
// ErrEditWindowClosed. now is the caller's clock, so a test drives it.
func CheckEdit(createdAt, now time.Time, window time.Duration) error {
	if now.Sub(createdAt) > window {
		return ErrEditWindowClosed
	}
	return nil
}

// CheckContent applies the content caps: at most maxPhotos photos (ErrTooManyPhotos), and at least text or one
// photo (ErrEmpty). hasText is whether the body holds any text, a mention included.
func CheckContent(hasText bool, photos, maxPhotos int) error {
	if photos > maxPhotos {
		return ErrTooManyPhotos
	}
	if !hasText && photos == 0 {
		return ErrEmpty
	}
	return nil
}

// Honeypot reports whether the hidden form field a person never fills (website) carries anything: the request is a
// bot's, and the caller answers as if it succeeded and stores nothing.
func Honeypot(website string) bool { return strings.TrimSpace(website) != "" }

// Added lists the ids of after that before lacks, in after's order and without repeats: the mentions an edit adds,
// the only ones it notifies.
func Added(before, after []string) []string {
	had := make(map[string]bool, len(before))
	for _, id := range before {
		had[id] = true
	}
	var out []string
	for _, id := range after {
		if !had[id] {
			had[id] = true
			out = append(out, id)
		}
	}
	return out
}

// Deletion is what Delete did: Stubbed when the comment became a stub (it has replies), else Removed lists the ids
// removed, the comment first, then its parent when that was a stub left with no reply.
type Deletion struct {
	Stubbed bool
	Removed []string
}

// Delete deletes comment id: a comment with replies becomes a stub and its replies live; one without is removed,
// and a stub whose last reply this was goes with it. A stub itself is ErrNotFound: it was deleted already.
func Delete(ctx context.Context, s Store, id string) (Deletion, error) {
	c, err := s.Get(ctx, id)
	if err != nil {
		return Deletion{}, fmt.Errorf("delete comment %s: get: %w", id, err)
	}
	if c.Deleted {
		return Deletion{}, fmt.Errorf("delete comment %s: a stub: %w", id, ErrNotFound)
	}
	n, err := s.Replies(ctx, id)
	if err != nil {
		return Deletion{}, fmt.Errorf("delete comment %s: count replies: %w", id, err)
	}
	if n > 0 {
		if err := s.Stub(ctx, id); err != nil {
			return Deletion{}, fmt.Errorf("delete comment %s: stub: %w", id, err)
		}
		return Deletion{Stubbed: true}, nil
	}
	if err := s.Remove(ctx, id); err != nil {
		return Deletion{}, fmt.Errorf("delete comment %s: remove: %w", id, err)
	}
	out := Deletion{Removed: []string{id}}
	if c.Top() {
		return out, nil
	}
	parent, err := s.Get(ctx, c.ParentID)
	if err != nil {
		return Deletion{}, fmt.Errorf("delete comment %s: get parent %s: %w", id, c.ParentID, err)
	}
	if !parent.Deleted {
		return out, nil
	}
	left, err := s.Replies(ctx, parent.ID)
	if err != nil {
		return Deletion{}, fmt.Errorf("delete comment %s: count replies of stub %s: %w", id, parent.ID, err)
	}
	if left > 0 {
		return out, nil
	}
	if err := s.Remove(ctx, parent.ID); err != nil {
		return Deletion{}, fmt.Errorf("delete comment %s: remove stub %s: %w", id, parent.ID, err)
	}
	out.Removed = append(out.Removed, parent.ID)
	return out, nil
}
