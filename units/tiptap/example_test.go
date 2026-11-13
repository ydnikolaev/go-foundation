package tiptap_test

import (
	"errors"
	"fmt"
	"os"

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

// ExampleLoadManifest builds the profiles from a manifest the caller owns: here
// one that lists two made-up video providers, each with its own id pattern.
func ExampleLoadManifest() {
	data, err := os.ReadFile("testdata/manifest.example.json")
	if err != nil {
		panic(err)
	}
	m, err := tiptap.LoadManifest(data)
	if err != nil {
		panic(err)
	}
	article := m.Profiles["article"]

	ok := []byte(`{"type":"doc","content":[{"type":"video","attrs":{"provider":"example","id":"123456"}}]}`)
	_, err = tiptap.Validate(ok, article, tiptap.NoRuneCap)
	fmt.Println(err)

	bad := []byte(`{"type":"doc","content":[{"type":"video","attrs":{"provider":"example","id":"abcdefgh"}}]}`)
	_, err = tiptap.Validate(bad, article, tiptap.NoRuneCap)
	fmt.Println(errors.Is(err, tiptap.ErrNotAllowed))
	// Output:
	// <nil>
	// true
}

// ExampleRenderEmail renders a body as an HTML fragment and a plain-text part. The
// caller supplies every address, word and class name; an image with no address
// becomes a link to the article (the text part always names the article).
func ExampleRenderEmail() {
	body := []byte(`{"type":"doc","content":[
		{"type":"heading","attrs":{"level":2},"content":[{"type":"text","text":"News"}]},
		{"type":"paragraph","content":[{"type":"text","text":"Read <this>"}]},
		{"type":"image","attrs":{"mediaId":"m-1","width":1200,"height":800}}
	]}`)

	letter, err := tiptap.RenderEmail(body, tiptap.EmailOptions{
		ImageURL: func(id string, width int) (string, bool) {
			return fmt.Sprintf("https://example.com/m/%s?w=%d", id, width), true
		},
		ArticleURL:  "https://example.com/a/1",
		Labels:      tiptap.EmailLabels{Photo: "Photo", Gallery: "Gallery"},
		ColumnWidth: 600,
		ClassPrefix: "x-",
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(letter.HTML)
	fmt.Println("--")
	fmt.Println(letter.Text)
	// Output:
	// <h2 class="x-h2">News</h2>
	// <p class="x-p">Read &lt;this&gt;</p>
	// <p class="x-figure"><img class="x-img" src="https://example.com/m/m-1?w=600" alt="" width="600" height="400"></p>
	// --
	// News
	//
	// Read <this>
	//
	// Photo: https://example.com/a/1
}
