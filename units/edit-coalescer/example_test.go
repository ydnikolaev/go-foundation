package editcoalescer_test

import (
	"context"
	"fmt"
	"time"

	editcoalescer "github.com/ydnikolaev/go-foundation/units/edit-coalescer"
)

// printEditor is an Editor that prints what it is asked to send.
type printEditor struct{}

func (printEditor) Edit(_ context.Context, object, data string) error {
	fmt.Println("edit", object, data)
	return nil
}

// Example puts three drafts of one message inside a window: only the last is sent, once.
func Example() {
	open := make(chan struct{})
	c := editcoalescer.New(context.Background(), editcoalescer.Config{
		// The test holds the window shut until every Put is in; a real caller leaves Wait nil.
		Wait: func(ctx context.Context, _ time.Duration) error {
			select {
			case <-open:
				return nil
			case <-ctx.Done():
				return fmt.Errorf("wait: %w", ctx.Err())
			}
		},
		Hooks: editcoalescer.Hooks{
			Edited: func(r editcoalescer.Result) { fmt.Println("sent", r.Object, "ok:", r.OK) },
		},
	})

	var ed printEditor
	c.Put(ed, "sender-1", "draft", "msg-7", "he")
	c.Put(ed, "sender-1", "draft", "msg-7", "hello")
	c.Put(ed, "sender-1", "draft", "msg-7", "hello world")
	fmt.Println("live objects:", c.Len())

	close(open)
	c.Wait()
	c.Close(context.Background())
	// Output:
	// live objects: 1
	// edit msg-7 hello world
	// sent msg-7 ok: true
}
