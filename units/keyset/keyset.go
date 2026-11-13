// Package keyset pages a list by a keyset cursor: each row has a Key (a time, then an id string), the list is ordered
// on it, and a page resumes strictly after the last key of the previous one. A row inserted or deleted between two
// reads therefore never repeats nor skips another.
//
// The cursor is opaque to clients: a base64url string holding a version byte, the key and a checksum over the list's
// scope and the payload. It carries no secret and grants nothing (it only positions a query that enforces its own
// rules), so the checksum is a plain hash: it refuses a cursor that is malformed, of another version, of another list
// or altered, never a forgery by someone who reads this package.
//
// A list reads its rows through a Store port; the package fetches one row more than the limit to know whether another
// page follows and hands out a next cursor only then. A list may show a head item first on its first page (a pinned or
// featured row); the store leaves it out of every Fetch, so it shows exactly once.
//
// Standard library only.
package keyset

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Key is a row's position: Time first, then ID compared byte by byte (in SQL, a "C" collation or a uuid).
type Key struct {
	Time time.Time
	ID   string
}

// Compare orders a before b ascending: -1, 0 or +1.
func (a Key) Compare(b Key) int {
	if c := a.Time.Compare(b.Time); c != 0 {
		return c
	}
	return strings.Compare(a.ID, b.ID)
}

// Order is the direction a list is read in.
type Order int

const (
	// NewestFirst reads keys descending: the latest time first.
	NewestFirst Order = iota
	// OldestFirst reads keys ascending.
	OldestFirst
)

// After reports whether k comes after cursor in o, that is whether a page resuming at cursor may hold k. A store
// that pages in memory filters with it; an SQL store writes the same comparison as `(t, id) < (…)` or `> (…)`.
func (o Order) After(k, cursor Key) bool {
	if o == OldestFirst {
		return k.Compare(cursor) > 0
	}
	return k.Compare(cursor) < 0
}

// ErrInvalidCursor is every refused cursor: malformed, of another version, of another list, or altered.
var ErrInvalidCursor = errors.New("keyset: invalid cursor")

// version is the cursor layout written today; Decode refuses any other.
const version byte = 1

const (
	timeLen = 8 // the key's time as Unix nanoseconds, big-endian
	sumLen  = 8 // the first bytes of a SHA-256
)

// Codec writes and reads the cursors of one list. Its scope names the list (and, when one list serves several
// parents, the parent: "items-of:<id>"), so a cursor of one list is refused by another.
type Codec struct {
	scope string
}

// NewCodec returns the codec of the list named scope.
func NewCodec(scope string) Codec { return Codec{scope: scope} }

// Encode renders k as the opaque cursor of this list.
func (c Codec) Encode(k Key) string {
	return base64.RawURLEncoding.EncodeToString(c.seal(version, k))
}

// seal lays out v, k and the checksum.
func (c Codec) seal(v byte, k Key) []byte {
	b := make([]byte, 0, 1+timeLen+len(k.ID)+sumLen)
	b = append(b, v)
	b = binary.BigEndian.AppendUint64(b, uint64(k.Time.UnixNano()))
	b = append(b, k.ID...)
	return append(b, c.sum(b)...)
}

// sum is the checksum of payload under the codec's scope.
func (c Codec) sum(payload []byte) []byte {
	h := sha256.New()
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(c.scope)))
	h.Write(n[:])
	h.Write([]byte(c.scope))
	h.Write(payload)
	return h.Sum(nil)[:sumLen]
}

// Decode reads a cursor of this list back into its key; anything else is ErrInvalidCursor.
func (c Codec) Decode(s string) (Key, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Key{}, fmt.Errorf("%w: not base64url", ErrInvalidCursor)
	}
	if len(b) < 1+timeLen+sumLen {
		return Key{}, fmt.Errorf("%w: too short", ErrInvalidCursor)
	}
	if b[0] != version {
		return Key{}, fmt.Errorf("%w: version %d", ErrInvalidCursor, b[0])
	}
	payload, sum := b[:len(b)-sumLen], b[len(b)-sumLen:]
	if !bytes.Equal(sum, c.sum(payload)) {
		return Key{}, fmt.Errorf("%w: not a cursor of this list", ErrInvalidCursor)
	}
	nanos := int64(binary.BigEndian.Uint64(payload[1 : 1+timeLen]))
	return Key{Time: time.Unix(0, nanos).UTC(), ID: string(payload[1+timeLen:])}, nil
}

// Limits are a list's page sizes: Default when the caller names none (or a non-positive one), at most Max. Both are
// positive.
type Limits struct {
	Default int
	Max     int
}

// Clamp is the page size for a requested n.
func (l Limits) Clamp(n int) int {
	switch {
	case n <= 0:
		return l.Default
	case n > l.Max:
		return l.Max
	}
	return n
}

// Store reads a list's rows in its order: at most limit rows strictly after the key after (from the start when nil).
// It leaves the list's head item, if any, out of every fetch.
type Store[T any] interface {
	Fetch(ctx context.Context, after *Key, limit int) ([]T, error)
}

// StoreFunc is a function as a Store.
type StoreFunc[T any] func(ctx context.Context, after *Key, limit int) ([]T, error)

// Fetch calls f.
func (f StoreFunc[T]) Fetch(ctx context.Context, after *Key, limit int) ([]T, error) {
	return f(ctx, after, limit)
}

// List is one paged list.
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

// Page is one page of a list: its items and the cursor of the next page, "" on the last.
type Page[T any] struct {
	Items []T
	Next  string
}

// Page reads the page after cursor ("" for the first) of at most Limits.Clamp(limit) rows, plus the head on the first
// page. A cursor this list did not hand out, or one whose key ValidKey refuses, is ErrInvalidCursor. Next is set only
// when more rows exist, and is the key of the page's last row, so a caller that drops rows from Items (a filter it
// cannot write in the store) still resumes after every row it was handed.
func (l List[T]) Page(ctx context.Context, cursor string, limit int) (Page[T], error) {
	n := l.Limits.Clamp(limit)
	var after *Key
	if cursor != "" {
		k, err := l.Codec.Decode(cursor)
		if err != nil {
			return Page[T]{}, err
		}
		if l.ValidKey != nil && !l.ValidKey(k) {
			return Page[T]{}, fmt.Errorf("%w: not a key of this list", ErrInvalidCursor)
		}
		after = &k
	}
	rows, err := l.Store.Fetch(ctx, after, n+1)
	if err != nil {
		return Page[T]{}, err
	}
	var out Page[T]
	if len(rows) > n {
		rows = rows[:n]
		out.Next = l.Codec.Encode(l.Key(rows[n-1]))
	}
	out.Items = make([]T, 0, len(rows)+1)
	if cursor == "" && l.Head != nil {
		head, ok, err := l.Head(ctx)
		if err != nil {
			return Page[T]{}, err
		}
		if ok {
			out.Items = append(out.Items, head)
		}
	}
	out.Items = append(out.Items, rows...)
	return out, nil
}
