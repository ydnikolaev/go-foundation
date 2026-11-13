// Package tiptap validates a user's rich-text JSON (the ProseMirror/Tiptap
// document shape) against an allowlist profile and reports the mention ids and
// the text length it found.
//
// The package is a unit: standard library only, no project imports. The
// profile is plain data so a caller can build it from any source (a JSON
// file, a literal); Post is one built-in profile written as a Go value.
package tiptap

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"regexp"
	"slices"
	"sync"
	"unicode/utf16"
	"unicode/utf8"
)

// ErrNotAllowed is wrapped by every refusal of a body: malformed JSON, a node,
// mark or attribute outside the profile, a broken attribute rule, a size cap.
// A caller maps errors.Is(err, ErrNotAllowed) to its "body not allowed" code.
// An error that does not match it is a defect in the profile itself.
var ErrNotAllowed = errors.New("tiptap: body not allowed")

// Node is one node of the document tree.
type Node struct {
	Type    string
	Attrs   map[string]any
	Content []Node
	Text    string
	Marks   []Mark
}

// Mark is one mark applied to a text node.
type Mark struct {
	Type  string
	Attrs map[string]any
}

// AttrRule restricts one attribute. Only Type "string" is supported.
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

// NodeRule lists the attributes a node or mark type may carry. An attribute
// not listed is refused.
type NodeRule struct {
	Attrs map[string]AttrRule `json:"attrs"`
}

// Profile is an allowlist: a node or mark type absent from it is refused.
// MaxNodes and MaxDepth of 0 mean no limit. The doc node counts as node 1 at
// depth 1.
type Profile struct {
	MaxNodes int                 `json:"maxNodes"`
	MaxDepth int                 `json:"maxDepth"`
	Nodes    map[string]NodeRule `json:"nodes"`
	Marks    map[string]NodeRule `json:"marks"`
}

// Post is a built-in profile for short posts: paragraphs, hard breaks and user
// mentions, no marks.
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

// Result is what a valid body reports.
type Result struct {
	// Runes counts Unicode code points across all text nodes.
	Runes int
	// Mentions lists the user_id of each mention node, in document order,
	// without repeats.
	Mentions []string
}

// Validate parses body and checks it against p. The root must be a doc node.
// The text of all text nodes together may hold at most maxRunes code points;
// the cap belongs to the caller (posts and comments differ). Every refusal
// wraps ErrNotAllowed and names the path of the offending node.
func Validate(body []byte, p Profile, maxRunes int) (Result, error) {
	var raw any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return Result{}, refuse("$", "invalid JSON: %v", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Result{}, refuse("$", "data after the JSON document")
	}

	st := &state{profile: p}
	root, err := st.parse(raw, "$", 1)
	if err != nil {
		return Result{}, err
	}
	if root.Type != "doc" {
		return Result{}, refuse("$", "root is %q, want doc", root.Type)
	}
	if err := st.check(root, "$", 1); err != nil {
		return Result{}, err
	}
	if st.runes > maxRunes {
		return Result{}, refuse("$", "text is %d characters, at most %d allowed", st.runes, maxRunes)
	}
	return Result{Runes: st.runes, Mentions: st.mentions}, nil
}

type state struct {
	profile  Profile
	nodes    int
	runes    int
	mentions []string
	seen     map[string]bool
}

// parse turns the decoded JSON value into a Node, refusing malformed nodes and
// enforcing the node and depth limits as it descends, so that a hostile tree
// is cut off early.
func (s *state) parse(v any, path string, depth int) (Node, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return Node{}, refuse(path, "node is not an object")
	}
	s.nodes++
	if s.profile.MaxNodes > 0 && s.nodes > s.profile.MaxNodes {
		return Node{}, refuse(path, "more than %d nodes", s.profile.MaxNodes)
	}
	if s.profile.MaxDepth > 0 && depth > s.profile.MaxDepth {
		return Node{}, refuse(path, "deeper than %d levels", s.profile.MaxDepth)
	}
	for _, k := range sortedKeys(m) {
		switch k {
		case "type", "attrs", "content", "text", "marks":
		default:
			return Node{}, refuse(path, "unknown node key %q", k)
		}
	}

	var n Node
	t, ok := m["type"].(string)
	if !ok || t == "" {
		return Node{}, refuse(path, "node has no type")
	}
	n.Type = t

	if a, has := m["attrs"]; has {
		attrs, ok := a.(map[string]any)
		if !ok {
			return Node{}, refuse(path, "attrs is not an object")
		}
		n.Attrs = attrs
	}
	if x, has := m["text"]; has {
		txt, ok := x.(string)
		if !ok || txt == "" {
			return Node{}, refuse(path, "text is not a non-empty string")
		}
		n.Text = txt
	}
	if x, has := m["marks"]; has {
		list, ok := x.([]any)
		if !ok {
			return Node{}, refuse(path, "marks is not an array")
		}
		for i, e := range list {
			mp := fmt.Sprintf("%s.marks[%d]", path, i)
			mark, err := parseMark(e, mp)
			if err != nil {
				return Node{}, err
			}
			n.Marks = append(n.Marks, mark)
		}
	}
	if x, has := m["content"]; has {
		list, ok := x.([]any)
		if !ok {
			return Node{}, refuse(path, "content is not an array")
		}
		for i, e := range list {
			child, err := s.parse(e, fmt.Sprintf("%s.content[%d]", path, i), depth+1)
			if err != nil {
				return Node{}, err
			}
			n.Content = append(n.Content, child)
		}
	}
	return n, nil
}

func parseMark(v any, path string) (Mark, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return Mark{}, refuse(path, "mark is not an object")
	}
	for _, k := range sortedKeys(m) {
		if k != "type" && k != "attrs" {
			return Mark{}, refuse(path, "unknown mark key %q", k)
		}
	}
	t, ok := m["type"].(string)
	if !ok || t == "" {
		return Mark{}, refuse(path, "mark has no type")
	}
	mk := Mark{Type: t}
	if a, has := m["attrs"]; has {
		attrs, ok := a.(map[string]any)
		if !ok {
			return Mark{}, refuse(path, "attrs is not an object")
		}
		mk.Attrs = attrs
	}
	return mk, nil
}

// check applies the profile's allowlist to a parsed node and its subtree and
// accumulates the text length and mention ids.
func (s *state) check(n Node, path string, depth int) error {
	rule, ok := s.profile.Nodes[n.Type]
	if !ok {
		return refuse(path, "node type %q is not allowed", n.Type)
	}
	if n.Type == "doc" && depth > 1 {
		return refuse(path, "doc inside another node")
	}
	if err := checkAttrs(n.Attrs, rule, path+".attrs"); err != nil {
		return err
	}

	if n.Type == "text" {
		if n.Text == "" {
			return refuse(path, "text node without text")
		}
		if len(n.Content) > 0 {
			return refuse(path, "text node with content")
		}
		s.runes += utf8.RuneCountInString(n.Text)
	} else if n.Text != "" {
		return refuse(path, "text on a %s node", n.Type)
	}

	if len(n.Marks) > 0 && n.Type != "text" {
		return refuse(path, "marks on a %s node", n.Type)
	}
	for i, mk := range n.Marks {
		mp := fmt.Sprintf("%s.marks[%d]", path, i)
		mr, ok := s.profile.Marks[mk.Type]
		if !ok {
			return refuse(mp, "mark type %q is not allowed", mk.Type)
		}
		if err := checkAttrs(mk.Attrs, mr, mp+".attrs"); err != nil {
			return err
		}
	}

	if n.Type == "mention" {
		if id, ok := n.Attrs["user_id"].(string); ok && !s.seen[id] {
			if s.seen == nil {
				s.seen = map[string]bool{}
			}
			s.seen[id] = true
			s.mentions = append(s.mentions, id)
		}
	}

	for i, c := range n.Content {
		if err := s.check(c, fmt.Sprintf("%s.content[%d]", path, i), depth+1); err != nil {
			return err
		}
	}
	return nil
}

func checkAttrs(attrs map[string]any, rule NodeRule, path string) error {
	for _, name := range sortedKeys(attrs) {
		if _, ok := rule.Attrs[name]; !ok {
			return refuse(path+"."+name, "attribute is not allowed")
		}
	}
	for _, name := range sortedKeys(rule.Attrs) {
		ar := rule.Attrs[name]
		ap := path + "." + name
		v, present := attrs[name]
		if v == nil {
			present = false
		}
		if !present {
			if ar.Required {
				return refuse(ap, "required attribute is missing")
			}
			continue
		}
		if ar.Type != "string" {
			return fmt.Errorf("tiptap: attribute %s: unsupported profile type %q", ap, ar.Type)
		}
		str, ok := v.(string)
		if !ok {
			return refuse(ap, "attribute is not a string")
		}
		if ar.Max > 0 {
			if n := len(utf16.Encode([]rune(str))); n > ar.Max {
				return refuse(ap, "attribute is %d long, at most %d allowed", n, ar.Max)
			}
		}
		if ar.Pattern != "" {
			re, err := compile(ar.Pattern)
			if err != nil {
				return fmt.Errorf("tiptap: attribute %s: bad profile pattern: %w", ap, err)
			}
			if !re.MatchString(str) {
				return refuse(ap, "attribute does not match %s", ar.Pattern)
			}
		}
	}
	return nil
}

var patterns sync.Map // pattern string -> *regexp.Regexp

// compile returns the compiled pattern, compiling each distinct one once.
func compile(pattern string) (*regexp.Regexp, error) {
	if re, ok := patterns.Load(pattern); ok {
		return re.(*regexp.Regexp), nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("compile pattern: %w", err)
	}
	actual, _ := patterns.LoadOrStore(pattern, re)
	return actual.(*regexp.Regexp), nil
}

func refuse(path, format string, args ...any) error {
	return fmt.Errorf("%w: %s: %s", ErrNotAllowed, path, fmt.Sprintf(format, args...))
}

func sortedKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}
