package tiptap

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Manifest is a node manifest: allowlist profiles by name, in the shape
// Profile, NodeRule and AttrRule read.
type Manifest struct {
	Version  int                `json:"version"`
	Profiles map[string]Profile `json:"profiles"`
}

// manifestJSON is the default manifest: a neutral node set (post, article,
// email) that lists no video provider. A product with providers of its own
// loads its manifest with LoadManifest.
//
//go:embed manifest.json
var manifestJSON []byte

var embedded = mustLoad(manifestJSON)

// The profiles of the embedded manifest. Post is paragraphs, hard breaks and
// user mentions, no marks; Article is the rich article set (headings, lists,
// quotes, images, galleries, callouts, marks) with no video node, because the
// default lists no provider; Email is the set RenderEmail reads. Article and
// Email have no text cap: pass NoRuneCap. For video, load a manifest that
// lists providers with LoadManifest.
var (
	Post    = embedded.mustProfile("post")
	Article = embedded.mustProfile("article")
	Email   = embedded.mustProfile("email")
)

func mustLoad(b []byte) *Manifest {
	m, err := LoadManifest(b)
	if err != nil {
		panic(err)
	}
	return m
}

func (m *Manifest) mustProfile(name string) Profile {
	p, ok := m.Profiles[name]
	if !ok {
		panic(fmt.Sprintf("tiptap: embedded manifest has no %q profile", name))
	}
	return p
}

// LoadManifest decodes a manifest and refuses one the validator would read
// wrongly or fail open on: an unknown key, a rule type other than string,
// integer or array, a pattern that is not RE2, a patternBy naming an
// attribute the entry does not list, an array rule without items (or items
// on another type), a pattern on a non-string, and any bound that is not
// positive: maxNodes and maxDepth must be present and at least 1, a min or
// max, when present, at least 1 (0 reads as "no limit", so it would fail
// open).
func LoadManifest(b []byte) (*Manifest, error) {
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("tiptap: decode manifest: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("tiptap: decode manifest: data after the JSON document")
	}
	// A second, presence-aware read: an omitempty int cannot tell 0 from absent.
	var bounds struct {
		Profiles map[string]profileBounds `json:"profiles"`
	}
	if err := json.Unmarshal(b, &bounds); err != nil {
		return nil, fmt.Errorf("tiptap: decode manifest bounds: %w", err)
	}
	for _, name := range sortedKeys(m.Profiles) {
		if err := checkProfile(m.Profiles[name], bounds.Profiles[name]); err != nil {
			return nil, fmt.Errorf("tiptap: manifest profile %s: %w", name, err)
		}
	}
	return &m, nil
}

type profileBounds struct {
	MaxNodes *int                   `json:"maxNodes"`
	MaxDepth *int                   `json:"maxDepth"`
	Nodes    map[string]entryBounds `json:"nodes"`
	Marks    map[string]entryBounds `json:"marks"`
}

type entryBounds struct {
	Attrs map[string]ruleBounds `json:"attrs"`
}

type ruleBounds struct {
	Min   *int         `json:"min"`
	Max   *int         `json:"max"`
	Items *entryBounds `json:"items"`
}

func checkProfile(p Profile, b profileBounds) error {
	if b.MaxNodes == nil || *b.MaxNodes < 1 {
		return errors.New("maxNodes must be present and at least 1")
	}
	if b.MaxDepth == nil || *b.MaxDepth < 1 {
		return errors.New("maxDepth must be present and at least 1")
	}
	for _, name := range sortedKeys(p.Nodes) {
		if err := checkEntry(p.Nodes[name], b.Nodes[name], "nodes."+name); err != nil {
			return err
		}
	}
	for _, name := range sortedKeys(p.Marks) {
		if err := checkEntry(p.Marks[name], b.Marks[name], "marks."+name); err != nil {
			return err
		}
	}
	return nil
}

func checkEntry(e NodeRule, b entryBounds, path string) error {
	for _, name := range sortedKeys(e.Attrs) {
		if err := checkRule(e.Attrs[name], b.Attrs[name], e, path+".attrs."+name); err != nil {
			return err
		}
	}
	return nil
}

func checkRule(r AttrRule, b ruleBounds, entry NodeRule, path string) error {
	switch r.Type {
	case "string", "integer", "array":
	default:
		return fmt.Errorf("%s: unknown rule type %q", path, r.Type)
	}
	if b.Min != nil && *b.Min < 1 {
		return fmt.Errorf("%s: min must be at least 1 or omitted", path)
	}
	if b.Max != nil && *b.Max < 1 {
		return fmt.Errorf("%s: max must be at least 1 or omitted", path)
	}
	if (r.Pattern != "" || r.PatternBy != nil) && r.Type != "string" {
		return fmt.Errorf("%s: pattern or patternBy on a rule of type %s", path, r.Type)
	}
	if r.Pattern != "" {
		if _, err := compile(r.Pattern); err != nil {
			return fmt.Errorf("%s: pattern: %w", path, err)
		}
	}
	if pb := r.PatternBy; pb != nil {
		if _, ok := entry.Attrs[pb.Attr]; !ok {
			return fmt.Errorf("%s: patternBy names %q, which the entry does not list", path, pb.Attr)
		}
		for _, key := range sortedKeys(pb.Patterns) {
			if _, err := compile(pb.Patterns[key]); err != nil {
				return fmt.Errorf("%s: patternBy %s: %w", path, key, err)
			}
		}
	}
	if (r.Type == "array") != (r.Items != nil) {
		return fmt.Errorf("%s: items go with an array rule, and an array rule needs items", path)
	}
	if r.Items != nil {
		var ib entryBounds
		if b.Items != nil {
			ib = *b.Items
		}
		return checkEntry(*r.Items, ib, path+".items")
	}
	return nil
}
