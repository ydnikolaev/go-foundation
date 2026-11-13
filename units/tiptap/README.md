# tiptap

A server-side validator for rich-text JSON in the ProseMirror/Tiptap document shape: `Validate` checks a document
against an allowlist `Profile` (node and mark types, attributes with a length and a pattern, a node cap and a depth cap)
and returns the text length and the mention ids it found. `Post` is one built-in profile as a Go value. Standard library
only.

## Install

```sh
go get github.com/ydnikolaev/go-foundation/units/tiptap@v0.1.1
```

go-foundation is public: no GOPRIVATE, no token, no vendoring.

## Wire

1. Pick a profile: `tiptap.Post` (paragraphs, hard breaks and `mention` nodes with a required `user_id`, no marks), or
   build a `Profile` in Go or decode one from JSON (the struct has `json` tags).
2. Call `tiptap.Validate(body, profile, maxRunes)` with the raw JSON bytes the client sent, before you store or render
   them. The root must be a `doc` node. `maxRunes` is the cap on all text together, counted in Unicode code points and
   chosen by the caller (a post and a comment may differ).
3. On success read `Result.Runes` (the text length) and `Result.Mentions` (each mention's `user_id`, in document order,
   without repeats), for example to notify the people mentioned.
4. On failure `errors.Is(err, tiptap.ErrNotAllowed)` means the body was refused: map it to your "bad request" answer. The
   message names the path of the offending node (`$.content[0].content[1].attrs.user_id`). An error that does not match
   is a defect in the profile itself (an attribute type other than `string`, a pattern that does not compile).

What is refused: malformed JSON or data after the document, an unknown node key, a node or mark type outside the
profile, an attribute outside the profile, a required attribute that is missing, an attribute that is not a string, too
long or breaks its pattern, text on a non-text node, marks on a non-text node, a `doc` below the root, more nodes or
more levels than the profile allows, and text over `maxRunes`.

## Configure

A `Profile` is plain data; the unit reads no file and no environment.

| field | meaning | zero value |
|---|---|---|
| `MaxNodes` | the most nodes in a document; the `doc` node counts as node 1 | no limit |
| `MaxDepth` | the deepest level; the `doc` node is level 1 | no limit |
| `Nodes` | allowed node types and the attributes each may carry | none allowed |
| `Marks` | allowed mark types and the attributes each may carry | none allowed |

An `AttrRule` is `{Type: "string", Required, Max, Pattern}`. `Max` counts UTF-16 code units, what the web editor's
`String.length` counts, and 0 means no limit. `Pattern` is an RE2 expression with search semantics: anchor it with `^`
and `$` for a full match. A node type or an attribute that is not listed is refused.

## API

<!-- api: devkit foundation api; do not edit -->
```go
VARIABLES
var ErrNotAllowed = errors.New("tiptap: body not allowed")
var Post = Profile{
	MaxNodes: 1000,
	MaxDepth: 4,
	Nodes: map[string]NodeRule{
		"doc":       {Attrs: map[string]AttrRule{}},
		"paragraph": {Attrs: map[string]AttrRule{}},
		"text":      {Attrs: map[string]AttrRule{}},
		"hardBreak": {Attrs: map[string]AttrRule{}},
		"mention": {Attrs: map[string]AttrRule{
			"user_id": {Type: "string", Required: true, Max: 64, Pattern: `^[A-Za-z0-9_-]+$`},
		}},
	},
	Marks: map[string]NodeRule{},
}
TYPES
type AttrRule struct {
	Type     string `json:"type"`
	Required bool   `json:"required"`
	// Max is the longest allowed string in UTF-16 code units (what the web
	// editor's String.length counts); 0 means no limit.
	Max int `json:"max,omitempty"`
	// Pattern is an RE2 expression the string must match (search semantics,
	// like RegExp.test; anchor it with ^ and $ for a full match).
	Pattern string `json:"pattern,omitempty"`
}
type Mark struct {
	Type  string
	Attrs map[string]any
}
type Node struct {
	Type    string
	Attrs   map[string]any
	Content []Node
	Text    string
	Marks   []Mark
}
type NodeRule struct {
	Attrs map[string]AttrRule `json:"attrs"`
}
type Profile struct {
	MaxNodes int                 `json:"maxNodes"`
	MaxDepth int                 `json:"maxDepth"`
	Nodes    map[string]NodeRule `json:"nodes"`
	Marks    map[string]NodeRule `json:"marks"`
}
type Result struct {
	// Runes counts Unicode code points across all text nodes.
	Runes int
	// Mentions lists the user_id of each mention node, in document order,
	// without repeats.
	Mentions []string
}
func Validate(body []byte, p Profile, maxRunes int) (Result, error)
```

## Example

Validate a document against `Post` and refuse one that leaves the profile (this is `Example`, compiled and run by the
unit's tests):

```go
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
```

## Extension points

- `Profile`, `NodeRule` and `AttrRule` are exported data: build a profile in code, or decode it from a JSON file kept
  next to the editor's own schema. `Post` is one example of a profile, not a special case.
- `ErrNotAllowed` is the one error to match; map it to your transport's "bad request" code.
- `Node` and `Mark` are exported types describing the document tree, for code that works with a validated tree.

## Do not

- Do not treat a zero `MaxNodes` or `MaxDepth` as strict: 0 means no limit, and so does a zero `AttrRule.Max`. Set them
  for untrusted input.
- Do not rely on it for the editor's content model: whether a node may be a child of another node, and which nodes are
  leaves, are not enforced yet. It checks the allowlist, attributes and limits only.
- Do not count `Result.Runes` as UTF-16 units: text length is in Unicode code points, and only `AttrRule.Max` counts
  UTF-16 units.
- Do not use an attribute type other than `string`: it is a profile defect and returns an error that is not
  `ErrNotAllowed`.
- Do not store the raw request body after `Validate` passes: duplicate object keys and invalid UTF-8 are not refused
  yet, so another parser may read the stored bytes differently than the check did. Store a re-marshalled document.
- Do not copy this package. Extend it here; a copy carries `go-foundation-copy: tiptap@<sha>` and a removal row.
