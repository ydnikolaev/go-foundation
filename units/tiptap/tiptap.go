// Package tiptap validates a user's rich-text JSON (the ProseMirror/Tiptap
// document shape) against an allowlist profile and reports the mention ids and
// the text length it found.
//
// The package is a unit: standard library only, no project imports. A
// profile is plain data so a caller can build it from any source (a manifest
// file, a literal); Post, Article and Email are the profiles of a small
// embedded node manifest (see LoadManifest for loading your own).
package tiptap

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"regexp"
	"slices"
	"strconv"
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

// AttrRule restricts one attribute. Type is "string", "integer" or "array";
// any other type is a defect of the profile, not a refusal of the body. A
// null value is an absent one. Min, Max and Enum read by type:
//   - string: Min and Max bound the length in UTF-16 code units (what the
//     web's String.length counts); Enum lists the allowed strings.
//   - integer: a JSON number with no fractional part (2, 2.0 and 1e2 are
//     integers, 2.5 is not); Min, Max and Enum compare by value.
//   - array: Min and Max bound the item count; every item is an object
//     checked against Items.
//
// A Min or Max of 0 means no bound.
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

// PatternBy maps each value of the sibling attribute Attr to the pattern the
// string must match (search semantics, as Pattern).
type PatternBy struct {
	Attr     string            `json:"attr"`
	Patterns map[string]string `json:"patterns"`
}

// NodeRule lists the attributes a node or mark type (or an array item) may
// carry. An attribute not listed is refused, except a Transient one, which
// may appear only as null. A Leaf node may not carry content.
type NodeRule struct {
	Attrs     map[string]AttrRule `json:"attrs"`
	Leaf      bool                `json:"leaf,omitempty"`
	Transient []string            `json:"transient,omitempty"`
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

// NoRuneCap is the maxRunes of a profile with no text cap of its own
// (article, email): every text length passes. A maxRunes of 0 refuses every
// text node.
const NoRuneCap = math.MaxInt

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
	_, st, err := validate(body, p)
	if err != nil {
		return Result{}, err
	}
	if st.runes > maxRunes {
		return Result{}, refuse("$", "text is %d characters, at most %d allowed", st.runes, maxRunes)
	}
	return Result{Runes: st.runes, Mentions: st.mentions}, nil
}

// MediaIDs validates body against p with no text cap (NoRuneCap) and returns
// the mediaId of every image node and every gallery item, in document order,
// repeats kept. A refusal wraps ErrNotAllowed, as Validate's.
func MediaIDs(body []byte, p Profile) ([]string, error) {
	root, _, err := validate(body, p)
	if err != nil {
		return nil, err
	}
	var ids []string
	var walk func(n Node)
	walk = func(n Node) {
		switch n.Type {
		case "image":
			if id, ok := n.Attrs["mediaId"].(string); ok {
				ids = append(ids, id)
			}
		case "gallery":
			items, _ := n.Attrs["items"].([]any)
			for _, item := range items {
				fields, _ := item.(map[string]any)
				if id, ok := fields["mediaId"].(string); ok {
					ids = append(ids, id)
				}
			}
		}
		for _, c := range n.Content {
			walk(c)
		}
	}
	walk(root)
	return ids, nil
}

// validate decodes body and checks it against p, with no text cap.
func validate(body []byte, p Profile) (Node, *state, error) {
	var raw any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return Node{}, nil, refuse("$", "invalid JSON: %v", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Node{}, nil, refuse("$", "data after the JSON document")
	}

	st := &state{profile: p}
	root, err := st.parse(raw, "$", 1)
	if err != nil {
		return Node{}, nil, err
	}
	if root.Type != "doc" {
		return Node{}, nil, refuse("$", "root is %q, want doc", root.Type)
	}
	if err := st.check(root, "$", 1); err != nil {
		return Node{}, nil, err
	}
	return root, st, nil
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
	if _, has := m["content"]; has && s.profile.Nodes[t].Leaf {
		return Node{}, refuse(path, "leaf %s node with content", t)
	}

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
		if _, ok := rule.Attrs[name]; ok {
			continue
		}
		if !slices.Contains(rule.Transient, name) {
			return refuse(path+"."+name, "attribute is not allowed")
		}
		if attrs[name] != nil {
			return refuse(path+"."+name, "transient attribute is not null")
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
		if err := checkValue(v, ar, attrs, ap); err != nil {
			return err
		}
	}
	return nil
}

// checkValue applies one rule to a present, non-null value; siblings are the
// other attributes of the same node, mark or array item.
func checkValue(v any, ar AttrRule, siblings map[string]any, path string) error {
	switch ar.Type {
	case "string":
		return checkString(v, ar, siblings, path)
	case "integer":
		return checkInteger(v, ar, path)
	case "array":
		return checkArray(v, ar, path)
	default:
		return fmt.Errorf("tiptap: attribute %s: unsupported profile type %q", path, ar.Type)
	}
}

func checkString(v any, ar AttrRule, siblings map[string]any, path string) error {
	str, ok := v.(string)
	if !ok {
		return refuse(path, "attribute is not a string")
	}
	n := utf16Len(str)
	if ar.Min > 0 && n < ar.Min {
		return refuse(path, "attribute is %d long, at least %d required", n, ar.Min)
	}
	if ar.Max > 0 && n > ar.Max {
		return refuse(path, "attribute is %d long, at most %d allowed", n, ar.Max)
	}
	if ar.Enum != nil && !slices.ContainsFunc(ar.Enum, func(e any) bool { s, ok := e.(string); return ok && s == str }) {
		return refuse(path, "attribute is not one of the allowed values")
	}
	if ar.Pattern != "" {
		if err := match(ar.Pattern, str, path); err != nil {
			return err
		}
	}
	if pb := ar.PatternBy; pb != nil {
		key, ok := siblings[pb.Attr].(string)
		if !ok {
			return refuse(path, "sibling %s is absent or not a string", pb.Attr)
		}
		pattern, ok := pb.Patterns[key]
		if !ok {
			return refuse(path, "sibling %s=%q has no pattern", pb.Attr, key)
		}
		if err := match(pattern, str, path); err != nil {
			return err
		}
	}
	return nil
}

// match refuses str unless the pattern finds a match in it (search semantics).
func match(pattern, str, path string) error {
	re, err := compile(pattern)
	if err != nil {
		return fmt.Errorf("tiptap: attribute %s: bad profile pattern: %w", path, err)
	}
	if !re.MatchString(str) {
		return refuse(path, "attribute does not match %s", pattern)
	}
	return nil
}

// checkInteger reads the value as JavaScript does: a float64 (so 1e2 is 100)
// that is finite and has no fractional part.
func checkInteger(v any, ar AttrRule, path string) error {
	num, ok := v.(json.Number)
	if !ok {
		return refuse(path, "attribute is not an integer")
	}
	f, ok := number(num)
	if !ok || f != math.Trunc(f) {
		return refuse(path, "attribute is not an integer")
	}
	if ar.Min > 0 && f < float64(ar.Min) {
		return refuse(path, "attribute is %s, at least %d required", num, ar.Min)
	}
	if ar.Max > 0 && f > float64(ar.Max) {
		return refuse(path, "attribute is %s, at most %d allowed", num, ar.Max)
	}
	if ar.Enum != nil && !slices.ContainsFunc(ar.Enum, func(e any) bool { g, ok := number(e); return ok && g == f }) {
		return refuse(path, "attribute is not one of the allowed values")
	}
	return nil
}

// number is the finite float64 value of a JSON number or a Go number from a
// hand-built profile's Enum.
func number(v any) (float64, bool) {
	switch n := v.(type) {
	case json.Number:
		f, err := strconv.ParseFloat(string(n), 64)
		return f, err == nil && !math.IsInf(f, 0) && !math.IsNaN(f)
	case float64:
		return n, true
	case int:
		return float64(n), true
	default:
		return 0, false
	}
}

func checkArray(v any, ar AttrRule, path string) error {
	if ar.Items == nil {
		return fmt.Errorf("tiptap: attribute %s: array rule without items", path)
	}
	list, ok := v.([]any)
	if !ok {
		return refuse(path, "attribute is not an array")
	}
	if ar.Min > 0 && len(list) < ar.Min {
		return refuse(path, "%d items, at least %d required", len(list), ar.Min)
	}
	if ar.Max > 0 && len(list) > ar.Max {
		return refuse(path, "%d items, at most %d allowed", len(list), ar.Max)
	}
	for i, item := range list {
		ip := fmt.Sprintf("%s[%d]", path, i)
		fields, ok := item.(map[string]any)
		if !ok {
			return refuse(ip, "item is not an object")
		}
		if err := checkAttrs(fields, *ar.Items, ip); err != nil {
			return err
		}
	}
	return nil
}

// utf16Len counts what JavaScript's String.length counts.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
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
