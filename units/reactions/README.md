# reactions

The rules of a one-per-user reaction (a heart) on a target: putting it and taking it back are idempotent, and each
answers the target's resulting state, its count and whether the user's reaction is in it. Tables, queries and who may
react to what are the caller's; the rules reach the stored reactions through the `Store` port, which the caller
implements. Standard library only.

## Install

```sh
go get github.com/ydnikolaev/go-foundation/units/reactions@v0.1.0
```

go-foundation is public: no GOPRIVATE, no token, no vendoring.

## Wire

1. Implement `Store` (`Add`, `Remove`, `State`). `Add` and `Remove` must be idempotent: adding a reaction the user holds,
   or removing one they do not, changes nothing and is no error. Back `Add` with a unique key on user and target.
2. Call `Put(ctx, store, target, userID)` and `Remove(ctx, store, target, userID)`; each answers the `State` after it.
3. Map `ErrInvalidTarget` and `ErrNoUser` onto the caller's own error codes at the boundary. Do the access check (who may
   react to what) before the call.

## Configure

Nothing to configure: no options, no environment. A `Target` is a kind the caller names (`"post"`, `"comment"`) and the
item's id; two kinds with the same id are two targets.

## API

<!-- api: devkit foundation api; do not edit -->
```go
VARIABLES
var (
	// ErrInvalidTarget: the target has no kind or no id.
	ErrInvalidTarget = errors.New("reactions: invalid target")
	// ErrNoUser: the call names no user.
	ErrNoUser = errors.New("reactions: a user id is required")
)
TYPES
type State struct {
	Count int
	Mine  bool
}
func Put(ctx context.Context, s Store, t Target, userID string) (State, error)
func Remove(ctx context.Context, s Store, t Target, userID string) (State, error)
type Store interface {
	Add(ctx context.Context, t Target, userID string) error
	Remove(ctx context.Context, t Target, userID string) error
	State(ctx context.Context, t Target, userID string) (State, error)
}
type Target struct {
	Kind string
	ID   string
}
```

## Example

Put twice, put by another user, remove (this is `Example`, compiled and run by the unit's tests; `memStore` there is a
map standing in for the caller's table):

```go
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
```

## Extension points

- `Store` is the one port: the caller decides the table, the transaction and the unique key.
- `Target.Kind` is the caller's own string, so one `Store` serves posts, comments or anything else.

## Do not

- Do not put storage, queries or access rules here: they are the caller's.
- Do not make `Add` or `Remove` non-idempotent: the rules call them once per request and trust the answer.
- Do not use it for several reaction kinds per user on one target: it holds exactly one.
- Do not copy this package. Extend it here; a copy carries `go-foundation-copy: reactions@<sha>` and a removal row.
