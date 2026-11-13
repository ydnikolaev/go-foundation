package keyset_test

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ydnikolaev/go-foundation/units/keyset"
)

type entry struct {
	At time.Time
	ID string
}

// Example pages a three-entry feed two at a time, newest first. The store here is a slice; in a service it is one SQL
// query filtered by the cursor key.
func Example() {
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
	// Output:
	// c b
	// a
}
