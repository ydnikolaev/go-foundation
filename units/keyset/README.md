# keyset

Keyset (seek) pagination for a list ordered by a time and then an id. A page resumes strictly after the last key of the
previous one, so a row inserted or deleted between two reads never repeats or skips another, and the next-page token is
an opaque (not secret, not signed) base64url cursor: a version byte, the key and a checksum over the list's scope and the payload. Standard
library only.

## Install

```sh
go get github.com/ydnikolaev/go-foundation/units/keyset@v0.1.1
```

go-foundation is public: no GOPRIVATE, no token, no vendoring.

## Wire

1. Describe the list: `keyset.List[T]{Codec, Limits, Store, Key}`. `Codec` is `keyset.NewCodec(scope)`; the scope names
   the list (and its parent, when one list serves several: `"items-of:" + parentID`), so a cursor of one list is
   refused by another. `Key` returns a row's `keyset.Key{Time, ID}`.
2. Implement `Store[T].Fetch(ctx, after *Key, limit int)` (or wrap a function in `keyset.StoreFunc`): return at most
   `limit` rows strictly after `after`, from the start when it is nil, in the list's order. In SQL that is
   `WHERE (t, id) < ($1, $2) ORDER BY t DESC, id DESC LIMIT $3`; `Order.After` is the same comparison for an
   in-memory store. The ID is compared byte by byte (a `"C"` collation, or a uuid).
3. Call `list.Page(ctx, cursor, limit)` with the cursor the client sent (`""` for the first page) and the requested
   size. It asks the store for one row more than the page, and sets `Page.Next` only when more rows exist.
4. Return `Page.Items` and `Page.Next` to the client; map `errors.Is(err, keyset.ErrInvalidCursor)` to a 400.

## Configure

Fields of `keyset.List`; the first four are required.

| field | meaning | default |
|---|---|---|
| `Codec` | writes and reads the cursors of this list | none |
| `Limits` | `Default` page size (also for a non-positive request) and `Max`, both positive | none |
| `Store` | reads the rows | none |
| `Key` | a row's position, the one the store orders on | none |
| `ValidKey` | refuses a decoded key the store cannot take (an id of the wrong shape) as `ErrInvalidCursor` | none |
| `Head` | an item shown before the first page's rows (a pinned row); the store must leave it out of every fetch | none |

## API

<!-- api: devkit foundation api; do not edit -->
```go
VARIABLES
var ErrInvalidCursor = errors.New("keyset: invalid cursor")
TYPES
type Codec struct {
	// Has unexported fields.
}
func NewCodec(scope string) Codec
func (c Codec) Decode(s string) (Key, error)
func (c Codec) Encode(k Key) string
type Key struct {
	Time time.Time
	ID   string
}
func (a Key) Compare(b Key) int
type Limits struct {
	Default int
	Max     int
}
func (l Limits) Clamp(n int) int
type List[T any] struct {
	Codec  Codec
	Limits Limits
	Store  Store[T]
	// Key is a row's position, the same the store orders on.
	Key func(T) Key
	// ValidKey, when set, refuses a decoded key the store cannot take (an id of the wrong shape) as
	// ErrInvalidCursor before the store reads it. The checksum is unkeyed, so anyone can seal a cursor around any key.
	ValidKey func(Key) bool
	// Head, when set, is an item shown before the first page's rows (that page then holds one more than the limit);
	// ok false shows none. The store never returns it.
	Head func(ctx context.Context) (item T, ok bool, err error)
}
func (l List[T]) Page(ctx context.Context, cursor string, limit int) (Page[T], error)
type Order int
const (
	// NewestFirst reads keys descending: the latest time first.
	NewestFirst Order = iota
	// OldestFirst reads keys ascending.
	OldestFirst
)
func (o Order) After(k, cursor Key) bool
type Page[T any] struct {
	Items []T
	Next  string
}
type Store[T any] interface {
	Fetch(ctx context.Context, after *Key, limit int) ([]T, error)
}
type StoreFunc[T any] func(ctx context.Context, after *Key, limit int) ([]T, error)
func (f StoreFunc[T]) Fetch(ctx context.Context, after *Key, limit int) ([]T, error)
```

## Example

A three-entry feed read two at a time, newest first, with an in-memory store (this is `Example`, compiled and run by the
unit's tests):

```go
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	feed := []entry{ // newest first, as the store returns them
		{at.Add(2 * time.Minute), "c"},
		{at.Add(1 * time.Minute), "b"},
		{at, "a"},
	}

	list := keyset.List[entry]{
		Codec:  keyset.NewCodec("feed"),
		Limits: keyset.Limits{Default: 2, Max: 50},
		Key:    func(e entry) keyset.Key { return keyset.Key{Time: e.At, ID: e.ID} },
		Store: keyset.StoreFunc[entry](func(_ context.Context, after *keyset.Key, limit int) ([]entry, error) {
			var out []entry
			for _, e := range feed {
				if after == nil || keyset.NewestFirst.After(keyset.Key{Time: e.At, ID: e.ID}, *after) {
					out = append(out, e)
				}
			}
			return out[:min(limit, len(out))], nil
		}),
	}

	cursor := ""
	for {
		page, err := list.Page(context.Background(), cursor, 0)
		if err != nil {
			fmt.Println(err)
			return
		}
		var ids []string
		for _, e := range page.Items {
			ids = append(ids, e.ID)
		}
		fmt.Println(strings.Join(ids, " "))
		if page.Next == "" {
			break
		}
		cursor = page.Next
	}
	// prints "c b", then "a"
```

## Extension points

- `Store[T]` is an interface: any type with `Fetch(ctx, *Key, int) ([]T, error)` is a store; `StoreFunc` adapts a
  function.
- `List.Head` and `List.ValidKey` are the two hooks: a pinned first item, and a check on the shape of a cursor's key.
- `Order.After` is the comparison for a store that pages in memory; an SQL store writes the same one in its query.

## Do not

- Do not treat the cursor as a secret, a signature or a permission: it is opaque, not protected. Anyone can decode it
  and seal another around any key, and the checksum refuses only a malformed, wrong-version or other-list cursor.
- Do not put a secret in the `Key`, and do not enforce visibility after `Fetch`: filter by who may see a row inside the
  store's query (the `WHERE`), because a forged cursor can position the query anywhere. Set `ValidKey` when the id has
  a shape.
- Do not offer "page N" or a total count on top of it: a cursor only moves forward from the last row seen.
- Do not let the store return the `Head` item, and do not reorder rows after `Fetch`: `Next` is the key of the last row
  `Fetch` returned within the page.
- Do not copy this package. Extend it here; a copy carries `go-foundation-copy: keyset@<sha>` and a removal row.
