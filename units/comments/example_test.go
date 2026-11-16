package comments_test

import (
	"errors"
	"fmt"
	"time"

	"github.com/ydnikolaev/go-foundation/units/comments"
)

func Example() {
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
}
