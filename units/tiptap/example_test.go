package tiptap_test

import (
	"errors"
	"fmt"

	"github.com/ydnikolaev/go-foundation/units/tiptap"
)

// Example validates an editor document against the built-in Post profile: it
// reports the text length and the mention ids, and refuses a node outside the
// profile.
func Example() {
	body := []byte(`{"type":"doc","content":[{"type":"paragraph","content":[
		{"type":"text","text":"Hi "},
		{"type":"mention","attrs":{"user_id":"u_42"}}
	]}]}`)

	res, err := tiptap.Validate(body, tiptap.Post, 5000)
	fmt.Println(res.Runes, res.Mentions, err)

	_, err = tiptap.Validate([]byte(`{"type":"doc","content":[{"type":"heading"}]}`), tiptap.Post, 5000)
	fmt.Println(errors.Is(err, tiptap.ErrNotAllowed), err)
	// Output:
	// 3 [u_42] <nil>
	// true tiptap: body not allowed: $.content[0]: node type "heading" is not allowed
}
