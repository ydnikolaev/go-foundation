package comments

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

// fakeStore is an in-memory Store: comments by id, the calls that change them recorded in order.
type fakeStore struct {
	rows  map[string]Comment
	calls []string
}

func newFakeStore(cs ...Comment) *fakeStore {
	s := &fakeStore{rows: map[string]Comment{}}
	for _, c := range cs {
		s.rows[c.ID] = c
	}
	return s
}

func (s *fakeStore) Get(_ context.Context, id string) (Comment, error) {
	c, ok := s.rows[id]
	if !ok {
		return Comment{}, ErrNotFound
	}
	return c, nil
}

func (s *fakeStore) Replies(_ context.Context, id string) (int, error) {
	n := 0
	for _, c := range s.rows {
		if c.ParentID == id {
			n++
		}
	}
	return n, nil
}

func (s *fakeStore) Stub(_ context.Context, id string) error {
	c := s.rows[id]
	c.Deleted = true
	s.rows[id] = c
	s.calls = append(s.calls, "stub "+id)
	return nil
}

func (s *fakeStore) Remove(_ context.Context, id string) error {
	delete(s.rows, id)
	s.calls = append(s.calls, "remove "+id)
	return nil
}

var t0 = time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)

// Donor TestService_CreateComment_NoParent and _WithParent: a top comment attaches nowhere, a reply to a top comment
// under it.
func TestReplyToTop(t *testing.T) {
	t.Parallel()
	if got, err := ReplyTo(Comment{ID: "top"}); err != nil || got != "top" {
		t.Errorf("reply to a top comment attaches to %q (%v), want top", got, err)
	}
}

// Donor TestService_CreateComment_ParentPastMaxDepth and TestBuildTree_MaxDepth: the donor refused a reply past its
// depth; here a reply to a reply attaches to the top comment, so the thread stays one level deep.
func TestReplyToReplyAttachesToTop(t *testing.T) {
	t.Parallel()
	if got, err := ReplyTo(Comment{ID: "r1", ParentID: "top"}); err != nil || got != "top" {
		t.Errorf("reply to r1 attaches to %q (%v), want top", got, err)
	}
}

// Donor TestHandler_Create_ThreadingNotFound: a stub cannot be answered (an unknown parent is the caller's read).
func TestReplyRefusesStub(t *testing.T) {
	t.Parallel()
	if _, err := ReplyTo(Comment{ID: "stub", Deleted: true}); !errors.Is(err, ErrNotFound) {
		t.Errorf("stub parent: %v, want ErrNotFound", err)
	}
}

// Donor TestService_EditComment "success" and "edit window expired", TestHandler_Edit_ExpiredWindow: the window is
// read from the passed now, the boundary inside it.
func TestCheckEditWindow(t *testing.T) {
	t.Parallel()
	w := 15 * time.Minute
	cases := []struct {
		at   time.Duration
		want error
	}{
		{14*time.Minute + 59*time.Second, nil},
		{15 * time.Minute, nil},
		{15*time.Minute + time.Minute, ErrEditWindowClosed},
		{time.Hour, ErrEditWindowClosed},
	}
	for _, c := range cases {
		if err := CheckEdit(t0, t0.Add(c.at), w); err != c.want {
			t.Errorf("edit at +%v: %v, want %v", c.at, err, c.want)
		}
	}
	if err := CheckEdit(t0, t0, 0); err != nil {
		t.Errorf("a zero window at once: %v, want allowed", err)
	}
}

// CheckContent: the photo cap and text-or-photo (no donor test: photos are new, R-148).
func TestCheckContent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		text    bool
		photos  int
		max     int
		wantErr error
	}{
		{"text only", true, 0, 4, nil},
		{"photo only", false, 1, 4, nil},
		{"four photos", true, 4, 4, nil},
		{"five photos", true, 5, 4, ErrTooManyPhotos},
		{"nothing", false, 0, 4, ErrEmpty},
		{"a photo with the cap at 0", false, 1, 0, ErrTooManyPhotos},
	}
	for _, c := range cases {
		if err := CheckContent(c.text, c.photos, c.max); err != c.wantErr {
			t.Errorf("%s: %v, want %v", c.name, err, c.wantErr)
		}
	}
}

// Donor TestHandler_Create_Honeypot: a filled website marks a bot.
func TestHoneypot(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]bool{"": false, "   ": false, "http://spam.example": true, "x": true} {
		if got := Honeypot(in); got != want {
			t.Errorf("Honeypot(%q) = %v, want %v", in, got, want)
		}
	}
}

// Donor TestExtractMentions_Deduplication and TestService_CreateComment_Mentions: only ids an edit adds are new, once
// each, in order.
func TestAdded(t *testing.T) {
	t.Parallel()
	cases := []struct {
		before, after, want []string
	}{
		{nil, nil, nil},
		{nil, []string{"a", "b", "a"}, []string{"a", "b"}},
		{[]string{"a"}, []string{"a"}, nil},
		{[]string{"a"}, []string{"b", "a", "c"}, []string{"b", "c"}},
		{[]string{"a", "b"}, []string{"b"}, nil},
	}
	for _, c := range cases {
		if got := Added(c.before, c.after); !slices.Equal(got, c.want) {
			t.Errorf("Added(%v, %v) = %v, want %v", c.before, c.after, got, c.want)
		}
	}
}

// Donor TestService_DeleteOwn "success": a comment without replies is removed outright.
func TestDeleteWithoutReplies(t *testing.T) {
	t.Parallel()
	s := newFakeStore(Comment{ID: "c"})
	d, err := Delete(t.Context(), s, "c")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if d.Stubbed || !slices.Equal(d.Removed, []string{"c"}) || !slices.Equal(s.calls, []string{"remove c"}) {
		t.Errorf("delete without replies: %+v, calls %v", d, s.calls)
	}
}

// R-156 (the donor soft-deleted, TestService_AdminSoftDelete): a top comment with replies becomes a stub and keeps
// them; deleting the last reply removes the reply and the now childless stub; a stub with a reply left stays.
func TestDeleteStubOrRemove(t *testing.T) {
	t.Parallel()
	s := newFakeStore(Comment{ID: "top"}, Comment{ID: "r1", ParentID: "top"}, Comment{ID: "r2", ParentID: "top"})
	d, err := Delete(t.Context(), s, "top")
	if err != nil || !d.Stubbed || len(d.Removed) != 0 {
		t.Fatalf("delete top with replies: %+v %v, want a stub", d, err)
	}
	if !s.rows["top"].Deleted || len(s.rows) != 3 {
		t.Fatalf("after the stub: %+v", s.rows)
	}
	if _, err := Delete(t.Context(), s, "top"); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete a stub: %v, want ErrNotFound", err)
	}
	d, err = Delete(t.Context(), s, "r1")
	if err != nil || d.Stubbed || !slices.Equal(d.Removed, []string{"r1"}) {
		t.Fatalf("delete r1 with r2 left: %+v %v, want r1 alone", d, err)
	}
	d, err = Delete(t.Context(), s, "r2")
	if err != nil || !slices.Equal(d.Removed, []string{"r2", "top"}) {
		t.Fatalf("delete the last reply: %+v %v, want r2 and the stub", d, err)
	}
	if len(s.rows) != 0 {
		t.Errorf("rows left: %+v", s.rows)
	}
}

// A reply under a live top comment is removed alone; the top stays.
func TestDeleteReplyUnderLiveTop(t *testing.T) {
	t.Parallel()
	s := newFakeStore(Comment{ID: "top"}, Comment{ID: "r1", ParentID: "top"})
	d, err := Delete(t.Context(), s, "r1")
	if err != nil || !slices.Equal(d.Removed, []string{"r1"}) {
		t.Fatalf("delete reply: %+v %v", d, err)
	}
	if _, ok := s.rows["top"]; !ok {
		t.Error("the live top comment went with its last reply")
	}
}

// Donor TestHandler_DeleteOwn_NotFound: an unknown comment.
func TestDeleteUnknown(t *testing.T) {
	t.Parallel()
	if _, err := Delete(t.Context(), newFakeStore(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete unknown: %v, want ErrNotFound", err)
	}
}
