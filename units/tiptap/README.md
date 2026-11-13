# tiptap

A server-side validator for rich-text JSON in the ProseMirror/Tiptap document shape: `Validate` checks a document
against an allowlist `Profile` (node and mark types, attributes with a length and a pattern, a node cap and a depth cap)
and returns the text length and the mention ids it found. `MediaIDs` lists the images a document uses, `LoadManifest`
reads your own allowlists from a JSON file, and `RenderEmail` turns a validated article body into an HTML fragment and a
plain-text part for a letter. `Post`, `Article` and `Email` are built-in profiles. Standard library only.

## Install

```sh
go get github.com/ydnikolaev/go-foundation/units/tiptap@v0.2.1
```

go-foundation is public: no GOPRIVATE, no token, no vendoring.

## Wire

1. Pick a profile: `tiptap.Post` (paragraphs, hard breaks and `mention` nodes with a required `user_id`, no marks),
   `tiptap.Article` (headings, lists, quotes, rules, images, galleries, callouts and bold, italic, code and link marks;
   no video) or `tiptap.Email` (what `RenderEmail` reads). For your own node set, or a video provider list of your own,
   call `tiptap.LoadManifest(data)` on a manifest file and take `m.Profiles["name"]`; or build a `Profile` in Go.
2. Call `tiptap.Validate(body, profile, maxRunes)` with the raw JSON bytes the client sent, before you store or render
   them. The root must be a `doc` node. `maxRunes` is the cap on all text together, counted in Unicode code points and
   chosen by the caller (a post and a comment may differ). `Article` and `Email` have no text cap of their own: pass
   `tiptap.NoRuneCap`.
3. On success read `Result.Runes` (the text length) and `Result.Mentions` (each mention's `user_id`, in document order,
   without repeats), for example to notify the people mentioned.
4. On failure `errors.Is(err, tiptap.ErrNotAllowed)` means the body was refused: map it to your "bad request" answer. The
   message names the path of the offending node (`$.content[0].content[1].attrs.user_id`). An error that does not match
   is a defect in the profile itself (an attribute type the unit does not know, a pattern that does not compile).
5. `tiptap.MediaIDs(body, profile)` validates the same way and returns the `mediaId` of every image and gallery item,
   in document order, repeats kept, for example to attach or release the uploads.
6. `tiptap.RenderEmail(body, opts)` validates against `Email` and returns a `Letter{HTML, Text}`. `EmailOptions` holds
   everything that is yours: `ImageURL(mediaID, width)` gives an absolute image address (no address renders a link to
   `ArticleURL`), `Labels` are the words of that link, `ColumnWidth` clamps images, `ClassPrefix` prefixes every class.
   `HTML` is a fragment with no style attribute, for your layout to wrap; the same input gives the same bytes.

What is refused: malformed JSON or data after the document, an unknown node key, a node or mark type outside the
profile, an attribute outside the profile, a required attribute that is missing, an attribute of the wrong type, out of
its bounds or enum, or breaking its pattern, content on a node the profile marks as a leaf, text on a non-text node,
marks on a non-text node, a `doc` below the root, more nodes or more levels than the profile allows, and text over
`maxRunes`.

## Configure

A `Profile` is plain data; the unit reads no file and no environment.

| field | meaning | zero value |
|---|---|---|
| `MaxNodes` | the most nodes in a document; the `doc` node counts as node 1 | no limit |
| `MaxDepth` | the deepest level; the `doc` node is level 1 | no limit |
| `Nodes` | allowed node types and the attributes each may carry | none allowed |
| `Marks` | allowed mark types and the attributes each may carry | none allowed |

An `AttrRule` has a `Type` of `string`, `integer` or `array`, `Required`, and `Min`, `Max`, `Enum`, `Pattern`,
`PatternBy` and `Items` as the type reads them. A string's `Min` and `Max` count UTF-16 code units, what the web
editor's `String.length` counts; an integer is a JSON number with no fractional part; an array's `Min` and `Max` count
items and `Items` is the rule of each. `Pattern` is an RE2 expression with search semantics: anchor it with `^` and `$`
for a full match. `PatternBy` picks the pattern by the value of a sibling attribute (a video id by its provider). A
`NodeRule` may be a `Leaf` (no content) and may list `Transient` attributes, which may only be null. A node type or an
attribute that is not listed is refused. A `Min` or `Max` of 0 means no bound in a `Profile` built in Go.

### The manifest

A manifest is JSON, `{"version": 1, "profiles": {"name": {"maxNodes", "maxDepth", "nodes", "marks"}}}`, the same
shape as `Profile`. `LoadManifest` refuses what the validator would read wrongly or fail open on: an unknown key, an
unknown rule type, a pattern that is not RE2, a `patternBy` naming an attribute the node does not list, an array rule
without `items`, a missing or zero `maxNodes` or `maxDepth`, and a `min` or `max` of 0 (it would read as "no limit").
`testdata/manifest.example.json` is a full example, with a `video` node and two made-up providers.

The manifest embedded in the unit lists no video provider, so `Article` has no `video` node: a product that embeds
videos owns its provider list and loads it with `LoadManifest`.

## API

<!-- api: devkit foundation api; do not edit -->
```go
CONSTANTS
const NoRuneCap = math.MaxInt
VARIABLES
var (
	Post    = embedded.mustProfile("post")
	Article = embedded.mustProfile("article")
	Email   = embedded.mustProfile("email")
)
var ErrNotAllowed = errors.New("tiptap: body not allowed")
FUNCTIONS
func MediaIDs(body []byte, p Profile) ([]string, error)
TYPES
type AttrRule struct {
	Type     string `json:"type"`
	Required bool   `json:"required"`
	Min      int    `json:"min,omitempty"`
	Max      int    `json:"max,omitempty"`
	Enum     []any  `json:"enum,omitempty"`
	// Pattern is an RE2 expression the string must match (search semantics,
	// like RegExp.test; anchor it with ^ and $ for a full match).
	Pattern string `json:"pattern,omitempty"`
	// PatternBy picks the string's pattern by the value of a sibling
	// attribute; a sibling that is absent, not a string or not a key of
	// Patterns is a refusal.
	PatternBy *PatternBy `json:"patternBy,omitempty"`
	// Items is the rule every element of an array must satisfy.
	Items *NodeRule `json:"items,omitempty"`
}
type EmailLabels struct {
	Photo, Gallery string
}
type EmailOptions struct {
	// ImageURL returns an absolute address of a rendition of the media item
	// about width pixels wide. A nil func, or ok == false (or an empty address),
	// means the photo has no address and renders as a placeholder.
	ImageURL func(mediaID string, width int) (src string, ok bool)
	// ArticleURL is the absolute address a placeholder links to; the text part
	// prints it after the photo label.
	ArticleURL string
	// Labels are the words of the placeholder and of the text part's photo line.
	Labels EmailLabels
	// ColumnWidth is the letter's column in pixels: an image is never rendered
	// wider than it, and its height keeps the ratio. 0 means no clamp.
	ColumnWidth int
	// ClassPrefix is put before every class name. Empty: no class attribute.
	ClassPrefix string
}
type Letter struct {
	HTML template.HTML
	Text string
}
func RenderEmail(body []byte, opts EmailOptions) (Letter, error)
type Manifest struct {
	Version  int                `json:"version"`
	Profiles map[string]Profile `json:"profiles"`
}
func LoadManifest(b []byte) (*Manifest, error)
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
	Attrs     map[string]AttrRule `json:"attrs"`
	Leaf      bool                `json:"leaf,omitempty"`
	Transient []string            `json:"transient,omitempty"`
}
type PatternBy struct {
	Attr     string            `json:"attr"`
	Patterns map[string]string `json:"patterns"`
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

- `Profile`, `NodeRule` and `AttrRule` are exported data: build a profile in code, or load a manifest file kept next to
  the editor's own schema with `LoadManifest`. `Post` is one example of a profile, not a special case.
- `EmailOptions` and `EmailLabels` carry every address, word and class name of a letter; the unit holds none.
- `ErrNotAllowed` is the one error to match; map it to your transport's "bad request" code.
- `Node` and `Mark` are exported types describing the document tree, for code that works with a validated tree.

## Do not

- Do not treat a zero `MaxNodes` or `MaxDepth` as strict in a `Profile` built in Go: 0 means no limit, and so does a
  zero `Min` or `Max`. Set them for untrusted input; `LoadManifest` refuses a manifest that leaves them out.
- Do not rely on it for the editor's content model: whether a node may be a child of another node is not enforced. It
  checks the allowlist, attributes, leaf nodes and limits only.
- Do not count `Result.Runes` as UTF-16 units: text length is in Unicode code points, and only `AttrRule.Max` counts
  UTF-16 units.
- Do not use an attribute type other than `string`, `integer` or `array` in a `Profile` built in Go: it is a profile
  defect and returns an error that is not `ErrNotAllowed`.
- Do not pass `Article` or `Email` a `maxRunes` of 0: it refuses every text node; pass `NoRuneCap`.
- Do not build a `Letter.HTML` from your own strings with `template.HTML`: only what `RenderEmail` returned has every
  text and attribute value escaped.
- Do not count `Result.Runes` as UTF-16 units: text length is in Unicode code points, and only `AttrRule.Max` counts
  UTF-16 units.
- Do not use an attribute type other than `string`, `integer` or `array` in a `Profile` built in Go: it is a profile
  defect and returns an error that is not `ErrNotAllowed`.
- Do not pass `Article` or `Email` a `maxRunes` of 0: it refuses every text node; pass `NoRuneCap`.
- Do not wrap `Letter.HTML` in a layout that adds the document's own text: it is escaped, and it is safe to embed only
  as the fragment `RenderEmail` returned. Do not build a `Letter` from your own strings with `template.HTML`.
- Do not store the raw request body after `Validate` passes: duplicate object keys and invalid UTF-8 are not refused
  yet, so another parser may read the stored bytes differently than the check did. Store a re-marshalled document.
- Do not expect a `rel` or `class` on a link: the embedded profiles accept none, and `target` only as `_blank`. A
  renderer sets `rel` itself (`RenderEmail` writes `ugc nofollow noopener noreferrer`); a client editor must not read it from the document.
- Do not copy this package. Extend it here; a copy carries `go-foundation-copy: tiptap@<sha>` and a removal row.
