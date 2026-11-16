# comments

The rules of a comment thread one level deep: where a reply attaches, how long its author may edit it, what a comment
must and may carry, the honeypot, what a delete leaves behind, and which mentions an edit adds. Tables, queries and
access rules are the caller's; the rules that read stored comments do so through the `Store` port, which the caller
implements on its own transaction. Standard library only.

## Install

```sh
go get github.com/ydnikolaev/go-foundation/units/comments@v0.1.0
```

go-foundation is public: no GOPRIVATE, no token, no vendoring.

## Wire

1. Implement `Store` (`Get`, `Replies`, `Stub`, `Remove`) on the transaction that holds the comment rows. `Get` answers
   `ErrNotFound`, or an error wrapping it, for a comment that does not exist.
2. On create: `Honeypot(website)` first (answer as if it succeeded and store nothing), then `CheckContent`, then store
   the reply under `ReplyTo(answered)` or `Attach(parent)`.
3. On edit: `CheckEdit(createdAt, now, window)` with the caller's clock, then notify only `Added(before, after)`.
4. On delete: `Delete(ctx, store, id)` and read the `Deletion` it returns.
5. Map each `Err...` value onto the caller's own error codes at the boundary.

## Configure

Nothing global. The edit window and the photo cap are arguments of `CheckEdit` and `CheckContent`; the clock is the
caller's `now`, so a test drives it.

## API

<!-- api: devkit foundation api; do not edit -->
```go
VARIABLES
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
FUNCTIONS
func Added(before, after []string) []string
func Attach(parent Comment) string
func CheckContent(hasText bool, photos, maxPhotos int) error
func CheckEdit(createdAt, now time.Time, window time.Duration) error
func Honeypot(website string) bool
func ReplyTo(answered Comment) (parentID string, err error)
TYPES
type Comment struct {
	ID        string
	ParentID  string
	AuthorID  string
	CreatedAt time.Time
	Deleted   bool
}
func (c Comment) Top() bool
type Deletion struct {
	Stubbed bool
	Removed []string
}
func Delete(ctx context.Context, s Store, id string) (Deletion, error)
type Store interface {
	Get(ctx context.Context, id string) (Comment, error)
	// Replies counts the comments whose parent is id.
	Replies(ctx context.Context, id string) (int, error)
	// Stub turns id into a stub: marks it deleted and drops its body, photos and reactions; its replies stay.
	Stub(ctx context.Context, id string) error
	// Remove deletes id outright.
	Remove(ctx context.Context, id string) error
}
```

## Example

The pure rules in one place (this is `Example`, compiled and run by the unit's tests):

```go
top := comments.Comment{ID: "c1"}
reply := comments.Comment{ID: "c2", ParentID: "c1"}

// A reply to a reply is stored under the top comment: a thread is one level deep.
fmt.Println(comments.Attach(top), comments.Attach(reply))

// The author may edit within the window, the boundary included.
created := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
fmt.Println(comments.CheckEdit(created, created.Add(15*time.Minute), 15*time.Minute))
err := comments.CheckEdit(created, created.Add(16*time.Minute), 15*time.Minute)
fmt.Println(errors.Is(err, comments.ErrEditWindowClosed))

// An edit notifies only the mentions it adds.
fmt.Println(comments.Added([]string{"u1"}, []string{"u1", "u2", "u2"}))
// Output:
// c1 c1
// <nil>
// true
// [u2]
```

## Extension points

- `Store` is the one port: the caller decides the tables, the transaction and any row lock taken in `Get`.
- The refusals are plain `errors.Is` values, so a caller wraps them and maps them where it likes.

## Do not

- Do not put storage, queries or access rules here: they are the caller's.
- Do not nest deeper than one level: `Attach` is the rule, so do not store a reply under a reply.
- Do not answer a honeypot hit with an error: answer as if it succeeded, so a bot learns nothing.
- Do not copy this package. Extend it here; a copy carries `go-foundation-copy: comments@<sha>` and a removal row.
