package tiptap

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return b
}

// nestedParagraphs returns a doc whose deepest node sits at the given depth
// (doc is depth 1) and is a paragraph.
func nestedParagraphs(depth int) []byte {
	s := `{"type":"paragraph"}`
	for i := 2; i < depth; i++ {
		s = `{"type":"paragraph","content":[` + s + `]}`
	}
	return []byte(`{"type":"doc","content":[` + s + `]}`)
}

// docWithNodes returns a doc of exactly n nodes: the doc and n-1 paragraphs.
func docWithNodes(n int) []byte {
	return []byte(`{"type":"doc","content":[` +
		strings.TrimSuffix(strings.Repeat(`{"type":"paragraph"},`, n-1), ",") + `]}`)
}

func textDoc(text string) []byte {
	b, _ := json.Marshal(map[string]any{
		"type": "doc",
		"content": []any{map[string]any{
			"type":    "paragraph",
			"content": []any{map[string]any{"type": "text", "text": text}},
		}},
	})
	return b
}

func mentionDoc(id string) []byte {
	b, _ := json.Marshal(map[string]any{
		"type": "doc",
		"content": []any{map[string]any{
			"type": "paragraph",
			"content": []any{map[string]any{
				"type": "mention", "attrs": map[string]any{"user_id": id},
			}},
		}},
	})
	return b
}

func TestValidateAccepts(t *testing.T) {
	cases := []struct {
		name         string
		body         []byte
		maxRunes     int
		wantRunes    int
		wantMentions []string
	}{
		{"allowlisted tree", fixture(t, "valid.json"), 5000, 11, []string{"u_alice-1", "bob"}},
		{"empty doc", []byte(`{"type":"doc"}`), 5000, 0, nil},
		{"depth 4", nestedParagraphs(4), 5000, 0, nil},
		{"1000 nodes", docWithNodes(1000), 5000, 0, nil},
		{"user_id of 64 characters", mentionDoc(strings.Repeat("a", 64)), 5000, 0, []string{strings.Repeat("a", 64)}},
		{"5000 emoji at cap 5000", textDoc(strings.Repeat("😀", 5000)), 5000, 5000, nil},
		{"runes, not bytes or UTF-16 units", textDoc("Привет😀"), 7, 7, nil},
		{"duplicate key keeps the last", []byte(`{"type":"paragraph","type":"doc"}`), 5000, 0, nil},
		{"empty attrs object", []byte(`{"type":"doc","content":[{"type":"paragraph","attrs":{}}]}`), 5000, 0, nil},
		{"surrounding whitespace", []byte("\n {\"type\":\"doc\"} \n"), 5000, 0, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Validate(c.body, Post, c.maxRunes)
			if err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if got.Runes != c.wantRunes {
				t.Errorf("Runes = %d, want %d", got.Runes, c.wantRunes)
			}
			if !reflect.DeepEqual(got.Mentions, c.wantMentions) {
				t.Errorf("Mentions = %v, want %v", got.Mentions, c.wantMentions)
			}
		})
	}
}

func TestValidateRefuses(t *testing.T) {
	cases := []struct {
		name     string
		body     []byte
		maxRunes int
		wantPath string // substring of the error: where the node is
	}{
		{"heading", fixture(t, "heading.json"), 5000, "$.content[0]"},
		{"bold mark", fixture(t, "mark_bold.json"), 5000, "$.content[0].content[0].marks[0]"},
		{"mention without user_id", fixture(t, "mention_no_user_id.json"), 5000, "$.content[0].content[0].attrs.user_id"},
		{"user_id with a space", fixture(t, "mention_space.json"), 5000, "$.content[0].content[0].attrs.user_id"},
		{"user_id of 65 characters", mentionDoc(strings.Repeat("a", 65)), 5000, "attrs.user_id"},
		{"user_id null", []byte(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"mention","attrs":{"user_id":null}}]}]}`), 5000, "attrs.user_id"},
		{"user_id not a string", []byte(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"mention","attrs":{"user_id":7}}]}]}`), 5000, "attrs.user_id"},
		{"unknown attribute", fixture(t, "unknown_attr.json"), 5000, "attrs.label"},
		{"attribute on a node with none", []byte(`{"type":"doc","content":[{"type":"paragraph","attrs":{"align":"left"}}]}`), 5000, "attrs.align"},
		{"1001 nodes", docWithNodes(1001), 5000, "1000 nodes"},
		{"depth 5", nestedParagraphs(5), 5000, "4 levels"},
		{"root is not doc", fixture(t, "root_paragraph.json"), 5000, "root"},
		{"doc below the root", []byte(`{"type":"doc","content":[{"type":"doc"}]}`), 5000, "$.content[0]"},
		{"bad JSON", []byte(`{"type":"doc"`), 5000, "invalid JSON"},
		{"empty body", nil, 5000, "invalid JSON"},
		{"data after the document", []byte(`{"type":"doc"} {"type":"doc"}`), 5000, "after"},
		{"JSON array root", []byte(`[]`), 5000, "not an object"},
		{"5001 emoji at cap 5000", textDoc(strings.Repeat("😀", 5001)), 5000, "5001"},
		{"text node empty", []byte(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":""}]}]}`), 5000, "text"},
		{"text node without text", []byte(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text"}]}]}`), 5000, "text node without text"},
		{"text on a paragraph", []byte(`{"type":"doc","content":[{"type":"paragraph","text":"x"}]}`), 5000, "text on a paragraph"},
		{"content on a text node", []byte(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"x","content":[{"type":"hardBreak"}]}]}]}`), 5000, "text node with content"},
		{"marks on a paragraph", []byte(`{"type":"doc","content":[{"type":"paragraph","marks":[{"type":"bold"}]}]}`), 5000, "marks on a paragraph"},
		{"unknown node key", []byte(`{"type":"doc","foo":1}`), 5000, `"foo"`},
		{"node key in another case", []byte(`{"type":"doc","Content":[]}`), 5000, `"Content"`},
		{"type missing", []byte(`{"content":[]}`), 5000, "no type"},
		{"attrs as an array", []byte(`{"type":"doc","content":[{"type":"paragraph","attrs":[]}]}`), 5000, "attrs is not an object"},
		{"content not an array", []byte(`{"type":"doc","content":{}}`), 5000, "content is not an array"},
		{"content entry not an object", []byte(`{"type":"doc","content":["x"]}`), 5000, "$.content[0]"},
		{"mark with an unknown key", []byte(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"x","marks":[{"type":"bold","x":1}]}]}]}`), 5000, `"x"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Validate(c.body, Post, c.maxRunes)
			if !errors.Is(err, ErrNotAllowed) {
				t.Fatalf("err = %v, want ErrNotAllowed", err)
			}
			if !strings.Contains(err.Error(), c.wantPath) {
				t.Errorf("err = %q, want it to contain %q", err, c.wantPath)
			}
		})
	}
}

// A custom profile exercises what Post does not use: optional attributes and
// the UTF-16 length rule.
func TestValidateCustomProfile(t *testing.T) {
	p := Profile{
		MaxNodes: 10,
		MaxDepth: 3,
		Nodes: map[string]NodeRule{
			"doc":       {Attrs: map[string]AttrRule{}},
			"paragraph": {Attrs: map[string]AttrRule{"note": {Type: "string", Max: 4}}},
			"text":      {Attrs: map[string]AttrRule{}},
		},
		Marks: map[string]NodeRule{
			"link": {Attrs: map[string]AttrRule{"href": {Type: "string", Required: true, Pattern: `^https://`}}},
		},
	}
	doc := func(note, mark string) []byte {
		return []byte(`{"type":"doc","content":[{"type":"paragraph","attrs":{"note":"` + note + `"},` +
			`"content":[{"type":"text","text":"x"` + mark + `}]}]}`)
	}
	link := func(href string) string {
		return `,"marks":[{"type":"link","attrs":{"href":"` + href + `"}}]`
	}

	t.Run("2 emoji are 4 UTF-16 units: pass", func(t *testing.T) {
		if _, err := Validate(doc("😀😀", ""), p, 10); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("3 emoji are 6 UTF-16 units: refused", func(t *testing.T) {
		_, err := Validate(doc("😀😀😀", ""), p, 10)
		if !errors.Is(err, ErrNotAllowed) {
			t.Fatalf("err = %v, want ErrNotAllowed", err)
		}
	})
	t.Run("optional attribute null is absent", func(t *testing.T) {
		body := []byte(`{"type":"doc","content":[{"type":"paragraph","attrs":{"note":null}}]}`)
		if _, err := Validate(body, p, 10); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("allowed mark passes", func(t *testing.T) {
		if _, err := Validate(doc("", link("https://x.ru")), p, 10); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("mark attribute breaking its pattern is refused", func(t *testing.T) {
		_, err := Validate(doc("", link("javascript:alert(1)")), p, 10)
		if !errors.Is(err, ErrNotAllowed) {
			t.Fatalf("err = %v, want ErrNotAllowed", err)
		}
		if !strings.Contains(err.Error(), "marks[0].attrs.href") {
			t.Errorf("err = %q, want the mark path", err)
		}
	})
	t.Run("a profile defect is not ErrNotAllowed", func(t *testing.T) {
		bad := Profile{Nodes: map[string]NodeRule{
			"doc": {Attrs: map[string]AttrRule{"n": {Type: "integer"}}},
		}}
		_, err := Validate([]byte(`{"type":"doc","attrs":{"n":1}}`), bad, 10)
		if err == nil || errors.Is(err, ErrNotAllowed) {
			t.Fatalf("err = %v, want a non-ErrNotAllowed error", err)
		}
	})
}

// Post must equal its JSON form in testdata/post_profile.json, so a profile
// kept as a file and the built-in Go value cannot drift apart.
func TestPostEqualsJSONProfile(t *testing.T) {
	var want Profile
	dec := json.NewDecoder(bytes.NewReader(fixture(t, "post_profile.json")))
	dec.DisallowUnknownFields() // a rule the unit cannot express is a mismatch, not a skip
	if err := dec.Decode(&want); err != nil {
		t.Fatalf("decode post profile: %v", err)
	}
	if !reflect.DeepEqual(Post, want) {
		t.Errorf("Post differs from testdata/post_profile.json:\n got  %+v\n want %+v", Post, want)
	}
}
