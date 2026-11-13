package keyset

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)

// row is a fake list row.
type row struct {
	key  Key
	head bool // the list's head item: the store leaves it out
}

// fakeStore pages rows in memory in its order, as an SQL store does: filter after the cursor, sort, cut.
type fakeStore struct {
	order Order
	rows  []row
	calls []int // the limit of each Fetch
}

func (f *fakeStore) Fetch(_ context.Context, after *Key, limit int) ([]row, error) {
	f.calls = append(f.calls, limit)
	var out []row
	for _, r := range f.rows {
		if r.head || (after != nil && !f.order.After(r.key, *after)) {
			continue
		}
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b row) int {
		if f.order == OldestFirst {
			return a.key.Compare(b.key)
		}
		return b.key.Compare(a.key)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// add appends n rows, one a minute from start, with ids "<prefix>-<i>".
func (f *fakeStore) add(n int, start time.Time, prefix string) {
	for i := range n {
		f.rows = append(f.rows, row{key: Key{Time: start.Add(time.Duration(i) * time.Minute), ID: fmt.Sprintf("%s-%02d", prefix, i)}})
	}
}

func newList(store *fakeStore) List[row] {
	return List[row]{
		Codec:  NewCodec("feed"),
		Limits: Limits{Default: 20, Max: 50},
		Store:  store,
		Key:    func(r row) Key { return r.key },
	}
}

// walk reads every page and returns the sizes and the ids in order.
func walk(t *testing.T, l List[row], between func(page int)) (sizes []int, ids []string) {
	t.Helper()
	cursor := ""
	for page := 0; ; page++ {
		p, err := l.Page(t.Context(), cursor, 0)
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		sizes = append(sizes, len(p.Items))
		for _, r := range p.Items {
			ids = append(ids, r.key.ID)
		}
		if p.Next == "" {
			return sizes, ids
		}
		if between != nil {
			between(page)
		}
		cursor = p.Next
		if page > 100 {
			t.Fatal("no last page")
		}
	}
}

func noRepeats(t *testing.T, ids []string) {
	t.Helper()
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Errorf("%s shown twice", id)
		}
		seen[id] = true
	}
}

// TestPages: 45 rows read 20/20/5, newest first, each once, and the store is asked for limit+1.
func TestPages(t *testing.T) {
	t.Parallel()
	store := &fakeStore{order: NewestFirst}
	store.add(45, t0, "r")
	sizes, ids := walk(t, newList(store), nil)
	if !slices.Equal(sizes, []int{20, 20, 5}) {
		t.Fatalf("sizes %v, want [20 20 5]", sizes)
	}
	noRepeats(t, ids)
	if ids[0] != "r-44" || ids[44] != "r-00" {
		t.Errorf("order: first %s last %s, want r-44 … r-00", ids[0], ids[44])
	}
	if !slices.Equal(store.calls, []int{21, 21, 21}) {
		t.Errorf("fetch limits %v, want 21 each", store.calls)
	}
}

// TestExactPage: 40 rows are two full pages and no third: the next cursor appears only when more rows exist.
func TestExactPage(t *testing.T) {
	t.Parallel()
	store := &fakeStore{order: NewestFirst}
	store.add(40, t0, "r")
	if sizes, _ := walk(t, newList(store), nil); !slices.Equal(sizes, []int{20, 20}) {
		t.Fatalf("sizes %v, want [20 20]", sizes)
	}
	empty := &fakeStore{order: NewestFirst}
	p, err := newList(empty).Page(t.Context(), "", 0)
	if err != nil || len(p.Items) != 0 || p.Next != "" || p.Items == nil {
		t.Fatalf("empty list: %+v %v, want no items, no next, a non-nil slice", p, err)
	}
}

// TestOldestFirst: the same list read ascending.
func TestOldestFirst(t *testing.T) {
	t.Parallel()
	store := &fakeStore{order: OldestFirst}
	store.add(45, t0, "r")
	sizes, ids := walk(t, newList(store), nil)
	if !slices.Equal(sizes, []int{20, 20, 5}) || ids[0] != "r-00" || ids[44] != "r-44" {
		t.Fatalf("sizes %v first %s last %s, want 20/20/5 from r-00 to r-44", sizes, ids[0], ids[44])
	}
	noRepeats(t, ids)
}

// TestInsertBetweenReads: rows inserted after page 1 (a newer one, and one in the middle of the unread part) neither
// repeat nor skip any row the walk started with; ties on time are broken by id.
func TestInsertBetweenReads(t *testing.T) {
	t.Parallel()
	store := &fakeStore{order: NewestFirst}
	store.add(45, t0, "r")
	// Two rows at one time: the id breaks the tie, so the cursor between them is exact.
	store.rows = append(store.rows, row{key: Key{Time: t0.Add(30 * time.Minute), ID: "r-30b"}})
	sizes, ids := walk(t, newList(store), func(page int) {
		if page == 0 {
			store.rows = append(store.rows,
				row{key: Key{Time: t0.Add(time.Hour), ID: "new-top"}},
				row{key: Key{Time: t0.Add(10*time.Minute + time.Second), ID: "new-mid"}})
		}
	})
	noRepeats(t, ids)
	for i := range 45 {
		if id := fmt.Sprintf("r-%02d", i); !slices.Contains(ids, id) {
			t.Errorf("%s skipped", id)
		}
	}
	if !slices.Contains(ids, "r-30b") || !slices.Contains(ids, "new-mid") {
		t.Errorf("ids %v: want r-30b and the row inserted below the cursor", ids)
	}
	if slices.Contains(ids, "new-top") {
		t.Error("a row inserted above the cursor showed on a later page")
	}
	if got := sizes[0] + sizes[1] + sizes[2]; got != 47 || len(sizes) != 3 {
		t.Errorf("sizes %v", sizes)
	}
}

// TestHead: one head among 45 rows reads 21/20/4, the head once and first.
func TestHead(t *testing.T) {
	t.Parallel()
	store := &fakeStore{order: NewestFirst}
	store.add(45, t0, "r")
	store.rows[7].head = true
	headRow := store.rows[7]
	l := newList(store)
	heads := 0
	l.Head = func(context.Context) (row, bool, error) { heads++; return headRow, true, nil }
	sizes, ids := walk(t, l, nil)
	if !slices.Equal(sizes, []int{21, 20, 4}) {
		t.Fatalf("sizes %v, want [21 20 4]", sizes)
	}
	if ids[0] != headRow.key.ID {
		t.Errorf("first item %s, want the head %s", ids[0], headRow.key.ID)
	}
	noRepeats(t, ids)
	if heads != 1 {
		t.Errorf("Head called %d times, want once (first page only)", heads)
	}

	l.Head = func(context.Context) (row, bool, error) { return row{}, false, nil }
	if p, _ := l.Page(t.Context(), "", 0); len(p.Items) != 20 {
		t.Errorf("no head: %d items, want 20", len(p.Items))
	}
	boom := errors.New("boom")
	l.Head = func(context.Context) (row, bool, error) { return row{}, false, boom }
	if _, err := l.Page(t.Context(), "", 0); !errors.Is(err, boom) {
		t.Errorf("head error: %v, want it returned", err)
	}
}

// TestLimits: the clamp, and a page of the asked size.
func TestLimits(t *testing.T) {
	t.Parallel()
	l := Limits{Default: 20, Max: 50}
	for in, want := range map[int]int{0: 20, -3: 20, 1: 1, 35: 35, 50: 50, 51: 50, 1000: 50} {
		if got := l.Clamp(in); got != want {
			t.Errorf("Clamp(%d) = %d, want %d", in, got, want)
		}
	}
	store := &fakeStore{order: NewestFirst}
	store.add(45, t0, "r")
	p, err := newList(store).Page(t.Context(), "", 7)
	if err != nil || len(p.Items) != 7 || p.Next == "" {
		t.Fatalf("limit 7: %d items next %q %v", len(p.Items), p.Next, err)
	}
}

// TestCursorRoundTrip: a key comes back exactly, nanoseconds and all.
func TestCursorRoundTrip(t *testing.T) {
	t.Parallel()
	c := NewCodec("feed")
	k := Key{Time: time.Date(2026, 11, 13, 9, 1, 2, 123456789, time.FixedZone("x", 3*3600)), ID: "s/0f-ü"}
	got, err := c.Decode(c.Encode(k))
	if err != nil || got.Compare(k) != 0 || got.ID != k.ID {
		t.Fatalf("round trip: %+v %v, want %+v", got, err, k)
	}
}

// TestCursorRefused: malformed, altered, foreign and other-version cursors are ErrInvalidCursor, and a list refuses
// them on Page without reading the store.
func TestCursorRefused(t *testing.T) {
	t.Parallel()
	c := NewCodec("feed")
	good := c.Encode(Key{Time: t0, ID: "6f1c2a4e-1d1b-4b8e-9a0e-2f1f0f7e1a01"})
	raw, _ := base64.RawURLEncoding.DecodeString(good)

	altered := slices.Clone(raw)
	altered[3] ^= 0x01 // the time moved
	alteredID := slices.Clone(raw)
	alteredID[1+timeLen] ^= 0x01                    // '6' becomes '7': another row
	oldVersion := c.seal(0, Key{Time: t0, ID: "x"}) // a well-formed, correctly summed cursor of another layout
	nextVersion := c.seal(2, Key{Time: t0, ID: "x"})

	cases := map[string]string{
		"not base64":        "!!!",
		"too short":         base64.RawURLEncoding.EncodeToString(raw[:10]),
		"altered time":      base64.RawURLEncoding.EncodeToString(altered),
		"altered id":        base64.RawURLEncoding.EncodeToString(alteredID),
		"truncated sum":     base64.RawURLEncoding.EncodeToString(raw[:len(raw)-1]),
		"another list":      NewCodec("archive").Encode(Key{Time: t0, ID: "x"}),
		"another parent":    NewCodec("feedx").Encode(Key{Time: t0, ID: "x"}),
		"old version":       base64.RawURLEncoding.EncodeToString(oldVersion),
		"next version":      base64.RawURLEncoding.EncodeToString(nextVersion),
		"padded base64":     base64.URLEncoding.EncodeToString(raw),
		"standard alphabet": base64.StdEncoding.EncodeToString(raw) + "+/",
	}
	store := &fakeStore{order: NewestFirst}
	l := newList(store)
	for name, s := range cases {
		if _, err := c.Decode(s); !errors.Is(err, ErrInvalidCursor) {
			t.Errorf("%s: Decode %v, want ErrInvalidCursor", name, err)
		}
		if _, err := l.Page(t.Context(), s, 0); !errors.Is(err, ErrInvalidCursor) {
			t.Errorf("%s: Page %v, want ErrInvalidCursor", name, err)
		}
	}
	if len(store.calls) != 0 {
		t.Errorf("the store was read %d times for refused cursors", len(store.calls))
	}
}

// TestValidKey: the checksum is unkeyed, so a cursor sealed by this very codec around a key the store cannot take is
// ErrInvalidCursor on Page when ValidKey refuses it, before the store is read; a key it accepts pages as usual.
func TestValidKey(t *testing.T) {
	t.Parallel()
	store := &fakeStore{order: NewestFirst}
	store.add(3, t0, "id")
	l := newList(store)
	l.ValidKey = func(k Key) bool { return len(k.ID) == len("id-00") }

	forged := l.Codec.Encode(Key{Time: t0, ID: "not-an-id"})
	if _, err := l.Page(t.Context(), forged, 0); !errors.Is(err, ErrInvalidCursor) {
		t.Errorf("forged key: Page %v, want ErrInvalidCursor", err)
	}
	if len(store.calls) != 0 {
		t.Errorf("the store was read %d times for a refused key", len(store.calls))
	}
	page, err := l.Page(t.Context(), l.Codec.Encode(Key{Time: t0.Add(2 * time.Minute), ID: "id-02"}), 0)
	if err != nil {
		t.Fatalf("valid key: %v", err)
	}
	if len(page.Items) != 2 {
		t.Errorf("valid key: %d items, want the 2 rows after it", len(page.Items))
	}
}
