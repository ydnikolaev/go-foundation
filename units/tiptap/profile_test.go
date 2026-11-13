package tiptap

import (
	"bytes"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

// The article profile these tests exercise is the one of testdata/manifest.example.json: the embedded Article
// minus a video node, so the example manifest adds the video node with two made-up providers. The golden documents
// and the shared rule cases live in testdata/fixtures.
var exampleManifest = func() *Manifest {
	b, err := os.ReadFile("testdata/manifest.example.json")
	if err != nil {
		panic(err)
	}
	m, err := LoadManifest(b)
	if err != nil {
		panic(err)
	}
	return m
}()

var exampleArticle = exampleManifest.Profiles["article"]

func goldenFixture(t *testing.T, name string) []byte {
	t.Helper()
	return fixture(t, "fixtures/"+name)
}

var profiles = map[string]Profile{"post": Post, "article": exampleArticle, "email": Email}

func TestFixturesPassTheirProfile(t *testing.T) {
	for _, name := range []string{"post", "article", "email"} {
		t.Run(name, func(t *testing.T) {
			if _, err := Validate(goldenFixture(t, name+".json"), profiles[name], NoRuneCap); err != nil {
				t.Fatal(err)
			}
		})
	}
	t.Run("post under the posts cap", func(t *testing.T) {
		if _, err := Validate(goldenFixture(t, "post.json"), Post, 5000); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("article fails post and email", func(t *testing.T) {
		article := goldenFixture(t, "article.json")
		for _, c := range []struct {
			p    Profile
			want string
		}{{Post, `deeper than 4 levels`}, {Email, `"video" is not allowed`}} { // parse's depth cut-off fires before post's node check
			_, err := Validate(article, c.p, NoRuneCap)
			if !errors.Is(err, ErrNotAllowed) || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want ErrNotAllowed containing %q", err, c.want)
			}
		}
	})
	t.Run("article's heading fails post", func(t *testing.T) {
		body := []byte(`{"type":"doc","content":[{"type":"heading","attrs":{"level":2}}]}`)
		if _, err := Validate(body, Post, NoRuneCap); !errors.Is(err, ErrNotAllowed) || !strings.Contains(err.Error(), `"heading" is not allowed`) {
			t.Errorf("err = %v, want a refused heading", err)
		}
	})
	t.Run("article's marks fail post", func(t *testing.T) {
		body := []byte(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"x","marks":[{"type":"bold"}]}]}]}`)
		if _, err := Validate(body, exampleArticle, NoRuneCap); err != nil {
			t.Fatal(err)
		}
		if _, err := Validate(body, Post, NoRuneCap); !errors.Is(err, ErrNotAllowed) || !strings.Contains(err.Error(), "mark type") {
			t.Errorf("err = %v, want a refused mark", err)
		}
	})
}

// doc wraps blocks (JSON objects, comma-separated) in a doc node.
func doc(blocks string) []byte {
	return []byte(`{"type":"doc","content":[` + blocks + `]}`)
}

func image(attrs string) string {
	return `{"type":"image","attrs":{"mediaId":"m-1","width":800,"height":600` + attrs + `}}`
}

const item = `{"mediaId":"m-1","width":800,"height":600}`

func gallery(items ...string) string {
	return `{"type":"gallery","attrs":{"items":[` + strings.Join(items, ",") + `]}}`
}

func link(href string) string {
	return `{"type":"paragraph","content":[{"type":"text","text":"x","marks":[{"type":"link","attrs":{"href":"` + href + `"}}]}]}`
}

// nested returns depth-1 blockquotes around one paragraph: the paragraph sits at the given depth (doc is 1).
func nested(depth int) string {
	s := `{"type":"paragraph"}`
	for i := 2; i < depth; i++ {
		s = `{"type":"blockquote","content":[` + s + `]}`
	}
	return s
}

func paragraphs(n int) string {
	return strings.TrimSuffix(strings.Repeat(`{"type":"paragraph"},`, n), ",")
}

func TestProfileAccepts(t *testing.T) {
	cases := []struct {
		name string
		p    Profile
		body []byte
	}{
		{"alt of 300 accented letters", exampleArticle, doc(image(`,"alt":"` + strings.Repeat("é", 300) + `"`))},
		{"alt of 150 emoji (300 UTF-16 units)", exampleArticle, doc(image(`,"alt":"` + strings.Repeat("😀", 150) + `"`))},
		{"width 2.0", exampleArticle, doc(`{"type":"image","attrs":{"mediaId":"m","width":2.0,"height":1}}`)},
		{"start 1e2", exampleArticle, doc(`{"type":"orderedList","attrs":{"start":1e2,"type":null}}`)},
		{"optional attributes null, as getJSON writes them", exampleArticle, doc(`{"type":"orderedList","attrs":{"start":null,"type":null}}`)},
		{"heading level 3", exampleArticle, doc(`{"type":"heading","attrs":{"level":3}}`)},
		{"link https with a path (search, not full match)", exampleArticle, doc(link("https://example.com/path"))},
		{"link mailto", exampleArticle, doc(link("mailto:a"))},
		{"link of 2048 characters", exampleArticle, doc(link("https://" + strings.Repeat("a", 2040)))},
		{"transient attributes null", exampleArticle, doc(image(`,"uploadId":null,"uploadStatus":null`))},
		{"transient null on a gallery item", exampleArticle, doc(gallery(item, `{"mediaId":"m-2","width":1,"height":1,"uploadId":null}`))},
		{"gallery of 20", exampleArticle, doc(gallery(slicesRepeat(item, 20)...))},
		{"video sample", exampleArticle, doc(`{"type":"video","attrs":{"provider":"sample","id":"abcdefgh"}}`)},
		{"video example", exampleArticle, doc(`{"type":"video","attrs":{"provider":"example","id":"123456"}}`)},
		{"callout tone", exampleArticle, doc(`{"type":"callout","attrs":{"tone":"warning"},"content":[{"type":"paragraph"}]}`)},
		{"paragraph inside a paragraph: no content model", exampleArticle, doc(`{"type":"paragraph","content":[{"type":"paragraph"}]}`)},
		{"depth 10", exampleArticle, doc(nested(10))},
		{"5000 nodes", exampleArticle, doc(paragraphs(4999))},
		{"email: everything but video", Email, doc(image(``) + `,` + gallery(item, item))},
		{"leaf without content", Post, doc(`{"type":"paragraph","content":[{"type":"hardBreak"},{"type":"mention","attrs":{"user_id":"u"}}]}`)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Validate(c.body, c.p, NoRuneCap); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func slicesRepeat(s string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = s
	}
	return out
}

func TestProfileRefuses(t *testing.T) {
	cases := []struct {
		name string
		p    Profile
		body []byte
		want string // substring of the error
	}{
		{"unknown node table", exampleArticle, doc(`{"type":"table"}`), `"table" is not allowed`},
		{"a mark on post", Post, doc(`{"type":"paragraph","content":[{"type":"text","text":"x","marks":[{"type":"italic"}]}]}`), `mark type "italic"`},
		{"unknown attribute", exampleArticle, doc(image(`,"src":"https://example.com/a.jpg"`)), "attrs.src: attribute is not allowed"},
		{"required missing", exampleArticle, doc(`{"type":"image","attrs":{"mediaId":"m","height":1}}`), "attrs.width: required"},
		{"required null", exampleArticle, doc(`{"type":"image","attrs":{"mediaId":"m","width":null,"height":1}}`), "attrs.width: required"},
		{"heading level 4", exampleArticle, doc(`{"type":"heading","attrs":{"level":4}}`), "attrs.level"},
		{"alt of 151 emoji (302 UTF-16 units)", exampleArticle, doc(image(`,"alt":"` + strings.Repeat("😀", 151) + `"`)), "302 long"},
		{"alt of 301 accented letters", exampleArticle, doc(image(`,"alt":"` + strings.Repeat("é", 301) + `"`)), "301 long"},
		{"width 2.5", exampleArticle, doc(`{"type":"image","attrs":{"mediaId":"m","width":2.5,"height":1}}`), "attrs.width: attribute is not an integer"},
		{"width 1e400 (infinite)", exampleArticle, doc(`{"type":"image","attrs":{"mediaId":"m","width":1e400,"height":1}}`), "not an integer"},
		{"width as a string", exampleArticle, doc(`{"type":"image","attrs":{"mediaId":"m","width":"800","height":1}}`), "not an integer"},
		{"start 0", exampleArticle, doc(`{"type":"orderedList","attrs":{"start":0}}`), "at least 1"},
		{"video sample with an example id", exampleArticle, doc(`{"type":"video","attrs":{"provider":"sample","id":"123456"}}`), "attrs.id: attribute does not match"},
		{"video with no provider", exampleArticle, doc(`{"type":"video","attrs":{"id":"123456"}}`), "provider"},
		{"video provider not a string", exampleArticle, doc(`{"type":"video","attrs":{"provider":5,"id":"123456"}}`), "provider"},
		{"video provider outside the enum", exampleArticle, doc(`{"type":"video","attrs":{"provider":"other","id":"123456"}}`), "provider"},
		{"link javascript:", exampleArticle, doc(link("javascript:alert(1)")), "marks[0].attrs.href"},
		{"link over 2048", exampleArticle, doc(link("https://" + strings.Repeat("a", 2041))), "2049 long"},
		{"callout tone outside the enum", exampleArticle, doc(`{"type":"callout","attrs":{"tone":"danger"}}`), "attrs.tone"},
		{"uploadStatus error", exampleArticle, doc(image(`,"uploadStatus":"error"`)), "attrs.uploadStatus: transient attribute is not null"},
		{"uploadId on a gallery item", exampleArticle, doc(gallery(item, `{"mediaId":"m-2","width":1,"height":1,"uploadId":"u1"}`)), "items[1].uploadId: transient"},
		{"transient name on a node that lists none", exampleArticle, doc(`{"type":"paragraph","attrs":{"uploadId":null}}`), "attrs.uploadId: attribute is not allowed"},
		{"gallery of 1", exampleArticle, doc(gallery(item)), "1 items, at least 2"},
		{"gallery of 21", exampleArticle, doc(gallery(slicesRepeat(item, 21)...)), "21 items, at most 20"},
		{"gallery item missing width", exampleArticle, doc(gallery(item, `{"mediaId":"m-2","height":1}`)), "items[1].width: required"},
		{"gallery item not an object", exampleArticle, doc(gallery(item, `"m-2"`)), "items[1]: item is not an object"},
		{"gallery items not an array", exampleArticle, doc(`{"type":"gallery","attrs":{"items":{"0":` + item + `}}}`), "not an array"},
		{"content on an image", exampleArticle, doc(`{"type":"image","attrs":{"mediaId":"m","width":1,"height":1},"content":[]}`), "leaf image node with content"},
		{"content on a mention (post)", Post, doc(`{"type":"paragraph","content":[{"type":"mention","attrs":{"user_id":"u"},"content":[{"type":"text","text":"x"}]}]}`), "leaf mention node with content"},
		{"content on a hardBreak (post)", Post, doc(`{"type":"paragraph","content":[{"type":"hardBreak","content":[]}]}`), "leaf hardBreak node with content"},
		{"marks on a paragraph", exampleArticle, doc(`{"type":"paragraph","marks":[{"type":"bold"}]}`), "marks on a paragraph"},
		{"text on a paragraph", exampleArticle, doc(`{"type":"paragraph","text":"x"}`), "text on a paragraph"},
		{"empty text", exampleArticle, doc(`{"type":"paragraph","content":[{"type":"text","text":""}]}`), "text is not a non-empty string"},
		{"Content key", exampleArticle, doc(`{"type":"paragraph","Content":[]}`), `"Content"`},
		{"extra foo key", exampleArticle, doc(`{"type":"paragraph","foo":1}`), `"foo"`},
		{"attrs as an array", exampleArticle, doc(`{"type":"heading","attrs":[2]}`), "attrs is not an object"},
		{"root paragraph", exampleArticle, []byte(`{"type":"paragraph"}`), "root"},
		{"nested doc", exampleArticle, doc(`{"type":"blockquote","content":[{"type":"doc"}]}`), "doc inside another node"},
		{"5001 nodes", exampleArticle, doc(paragraphs(5000)), "more than 5000 nodes"},
		{"depth 11", exampleArticle, doc(nested(11)), "deeper than 10 levels"},
		{"post depth 5", Post, doc(nested(5)), "deeper than 4 levels"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Validate(c.body, c.p, NoRuneCap)
			if !errors.Is(err, ErrNotAllowed) {
				t.Fatalf("err = %v, want ErrNotAllowed", err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %q, want it to contain %q", err, c.want)
			}
		})
	}
}

// A rule a hand-built profile can carry and the manifest does not use yet: a string min (the renderer ignores it,
// the server applies it), and Go numbers in an integer enum.
func TestHandBuiltRules(t *testing.T) {
	p := Profile{MaxNodes: 5, MaxDepth: 2, Nodes: map[string]NodeRule{
		"doc": {Attrs: map[string]AttrRule{
			"s": {Type: "string", Min: 2},
			"i": {Type: "integer", Enum: []any{1, 2.0}},
		}},
	}}
	for _, c := range []struct {
		attrs string
		ok    bool
	}{
		{`"s":"ab"`, true},
		{`"s":"😀"`, true}, // 2 UTF-16 units
		{`"s":"a"`, false},
		{`"i":1`, true},
		{`"i":2`, true},
		{`"i":3`, false},
	} {
		_, err := Validate([]byte(`{"type":"doc","attrs":{`+c.attrs+`}}`), p, NoRuneCap)
		if (err == nil) != c.ok {
			t.Errorf("%s: err = %v, want ok=%v", c.attrs, err, c.ok)
		}
	}
	t.Run("an array rule without items is a profile defect", func(t *testing.T) {
		bad := Profile{Nodes: map[string]NodeRule{"doc": {Attrs: map[string]AttrRule{"l": {Type: "array"}}}}}
		_, err := Validate([]byte(`{"type":"doc","attrs":{"l":[]}}`), bad, NoRuneCap)
		if err == nil || errors.Is(err, ErrNotAllowed) {
			t.Fatalf("err = %v, want a non-ErrNotAllowed error", err)
		}
	})
}

func TestNoRuneCap(t *testing.T) {
	long := `{"type":"paragraph","content":[{"type":"text","text":"` + strings.Repeat("é", 100_000) + `"}]}`
	got, err := Validate(doc(long), exampleArticle, NoRuneCap)
	if err != nil {
		t.Fatal(err)
	}
	if got.Runes != 100_000 {
		t.Errorf("Runes = %d, want 100000", got.Runes)
	}
	if _, err := Validate(doc(long), exampleArticle, 0); !errors.Is(err, ErrNotAllowed) {
		t.Errorf("maxRunes 0: err = %v, want ErrNotAllowed (0 is not \"no cap\")", err)
	}
}

func TestMediaIDs(t *testing.T) {
	t.Run("article fixture: every image and gallery item, in document order", func(t *testing.T) {
		got, err := MediaIDs(goldenFixture(t, "article.json"), exampleArticle)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"media-image-1", "media-gallery-1", "media-gallery-2"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("MediaIDs = %v, want %v", got, want)
		}
	})
	t.Run("nested and repeated ids are kept in order", func(t *testing.T) {
		body := doc(gallery(`{"mediaId":"g1","width":1,"height":1}`, `{"mediaId":"a","width":1,"height":1}`) + `,` +
			`{"type":"blockquote","content":[{"type":"image","attrs":{"mediaId":"a","width":1,"height":1}}]}`)
		got, err := MediaIDs(body, Email)
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"g1", "a", "a"}; !reflect.DeepEqual(got, want) {
			t.Errorf("MediaIDs = %v, want %v", got, want)
		}
	})
	t.Run("no text cap", func(t *testing.T) {
		body := doc(`{"type":"paragraph","content":[{"type":"text","text":"` + strings.Repeat("x", 50_000) + `"}]},` + image(``))
		got, err := MediaIDs(body, exampleArticle)
		if err != nil || !reflect.DeepEqual(got, []string{"m-1"}) {
			t.Errorf("MediaIDs = %v, %v", got, err)
		}
	})
	t.Run("a refused body is refused", func(t *testing.T) {
		got, err := MediaIDs(doc(image(`,"uploadStatus":"uploading"`)), exampleArticle)
		if !errors.Is(err, ErrNotAllowed) || got != nil {
			t.Errorf("MediaIDs = %v, %v; want nil, ErrNotAllowed", got, err)
		}
	})
	t.Run("post has none", func(t *testing.T) {
		got, err := MediaIDs(goldenFixture(t, "post.json"), Post)
		if err != nil || len(got) != 0 {
			t.Errorf("MediaIDs = %v, %v", got, err)
		}
	})
}

type ruleCases struct {
	Base map[string]map[string]any `json:"base"`
	Rows []struct {
		Profile string `json:"profile"`
		Node    string `json:"node"`
		Attr    string `json:"attr"`
		Value   any    `json:"value"`
		Valid   bool   `json:"valid"`
	} `json:"rows"`
}

func loadRuleCases(t *testing.T) ruleCases {
	t.Helper()
	var rc ruleCases
	dec := json.NewDecoder(bytes.NewReader(goldenFixture(t, "rule-cases.json")))
	dec.UseNumber() // keeps 2.0 and 1e2 as written
	if err := dec.Decode(&rc); err != nil {
		t.Fatalf("decode rule cases: %v", err)
	}
	if len(rc.Rows) == 0 {
		t.Fatal("no rule cases")
	}
	return rc
}

// The shared rule cases (design 6): Validate accepts exactly the valid rows. rule-cases.spec.ts holds the renderer to
// the same rows.
func TestRuleCases(t *testing.T) {
	rc := loadRuleCases(t)
	for _, row := range rc.Rows {
		p, ok := profiles[row.Profile]
		if !ok {
			t.Fatalf("unknown profile %q", row.Profile)
		}
		attrs := maps.Clone(rc.Base[row.Node])
		if attrs == nil {
			attrs = map[string]any{}
		}
		attrs[row.Attr] = row.Value
		var block map[string]any
		if _, isMark := p.Marks[row.Node]; isMark {
			block = map[string]any{"type": "paragraph", "content": []any{map[string]any{
				"type": "text", "text": "x", "marks": []any{map[string]any{"type": row.Node, "attrs": attrs}},
			}}}
		} else {
			block = map[string]any{"type": row.Node, "attrs": attrs}
		}
		body, err := json.Marshal(map[string]any{"type": "doc", "content": []any{block}})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		_, err = Validate(body, p, NoRuneCap)
		if err != nil && !errors.Is(err, ErrNotAllowed) {
			t.Errorf("%s %s.%s = %.40v: a profile defect: %v", row.Profile, row.Node, row.Attr, row.Value, err)
		}
		if (err == nil) != row.Valid {
			t.Errorf("%s %s.%s = %.40v: err = %v, want valid=%v", row.Profile, row.Node, row.Attr, row.Value, err, row.Valid)
		}
	}
}

// A string row at or past its rule's max sits exactly on the edge: a valid one at max, an invalid one a single
// character past it (Cyrillic and astral, design 6).
func TestRuleCasesSitOnTheEdges(t *testing.T) {
	rc := loadRuleCases(t)
	edges := 0
	for _, row := range rc.Rows {
		s, ok := row.Value.(string)
		rule := profiles[row.Profile].Nodes[row.Node].Attrs[row.Attr]
		if !ok || rule.Type != "string" || rule.Max == 0 || utf16Len(s) < rule.Max {
			continue
		}
		_, size := utf8.DecodeLastRuneInString(s)
		trimmed := utf16Len(s[:len(s)-size])
		if row.Valid && utf16Len(s) != rule.Max || !row.Valid && trimmed != rule.Max {
			t.Errorf("%s.%s: %d UTF-16 units (valid=%v) is not on the edge of max %d", row.Node, row.Attr, utf16Len(s), row.Valid, rule.Max)
		}
		edges++
	}
	if edges < 6 {
		t.Errorf("%d edge rows, want the mediaId, Cyrillic and emoji pairs", edges)
	}
}
