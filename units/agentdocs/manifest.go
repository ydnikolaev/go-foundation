package agentdocs

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

// ManifestSchema is the only schema LoadManifest accepts. A manifest carrying
// anything else is refused rather than decoded on a guess.
const ManifestSchema = "agentdocs-manifest/v1"

// Section is one documentation page: its stable id (the topic), its
// navigation group, its title, the page's path inside the tree, and the full
// command paths (without the root, "env push") it documents.
type Section struct {
	ID    string   `json:"id"`
	Group string   `json:"group"`
	Title string   `json:"title"`
	File  string   `json:"file"`
	Verbs []string `json:"verbs,omitempty"`
}

// Manifest is the curated list of pages. Slice order is the author's order.
type Manifest struct {
	Schema   string    `json:"schema"`
	Groups   []string  `json:"groups"`
	Sections []Section `json:"sections"`
}

// LoadManifest reads and validates the JSON manifest at path in tree. It
// refuses an unknown schema, a manifest with no groups or no sections, and a
// command path listed twice (on two sections or on one), because TopicFor
// could not choose between them. Every refusal wraps ErrManifestInvalid.
func LoadManifest(tree fs.FS, path string) (Manifest, error) {
	fail := func(format string, args ...any) (Manifest, error) {
		return Manifest{}, &Error{Op: "LoadManifest", Input: path,
			Err: fmt.Errorf("%w: "+format, append([]any{ErrManifestInvalid}, args...)...)}
	}
	b, err := fs.ReadFile(tree, path)
	if err != nil {
		return fail("read: %v", err)
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return fail("decode: %v", err)
	}
	if m.Schema != ManifestSchema {
		return fail("declares schema %q, want %q", m.Schema, ManifestSchema)
	}
	if len(m.Groups) == 0 || len(m.Sections) == 0 {
		return fail("carries %d groups and %d sections; a manifest declaring no documentation is a broken tree, not an empty one",
			len(m.Groups), len(m.Sections))
	}
	owner := map[string]string{}
	for _, s := range m.Sections {
		for _, v := range s.Verbs {
			key := normalizeVerb(v)
			if prev, dup := owner[key]; dup {
				return fail("command path %q is listed on section %q and on section %q", key, prev, s.ID)
			}
			owner[key] = s.ID
		}
	}
	return m, nil
}

// Topics returns every section id, in manifest order.
func (m Manifest) Topics() []string {
	ids := make([]string, 0, len(m.Sections))
	for _, s := range m.Sections {
		ids = append(ids, s.ID)
	}
	return ids
}

// Resolve returns the section whose id is topic.
func (m Manifest) Resolve(topic string) (Section, bool) {
	for _, s := range m.Sections {
		if s.ID == topic {
			return s, true
		}
	}
	return Section{}, false
}

// TopicFor returns the topic documenting a command path given without its
// root (cobra's CommandPath() minus its first word). The longest listed word
// prefix wins: "env push x" tries "env push x", then "env push", then "env".
// A parent covers its subcommands; a subcommand listed on its own topic wins.
func (m Manifest) TopicFor(path string) (string, bool) {
	words := strings.Fields(path)
	for n := len(words); n > 0; n-- {
		key := strings.Join(words[:n], " ")
		for _, s := range m.Sections {
			for _, v := range s.Verbs {
				if normalizeVerb(v) == key {
					return s.ID, true
				}
			}
		}
	}
	return "", false
}

// Read returns topic's page from tree verbatim. An unknown topic wraps
// ErrUnknownTopic and names every valid topic; a page the tree does not hold
// wraps ErrPageMissing and names the section and the file.
func (m Manifest) Read(tree fs.FS, topic string) ([]byte, error) {
	s, ok := m.Resolve(topic)
	if !ok {
		ids := m.Topics()
		sort.Strings(ids)
		return nil, &Error{Op: "Read", Input: fmt.Sprintf("%q", topic),
			Err: fmt.Errorf("%w; valid topics: %s", ErrUnknownTopic, strings.Join(ids, ", "))}
	}
	b, err := fs.ReadFile(tree, s.File)
	if err != nil {
		return nil, &Error{Op: "Read", Input: s.ID,
			Err: fmt.Errorf("%w: section %q names %s: %v", ErrPageMissing, s.ID, s.File, err)}
	}
	return b, nil
}

func normalizeVerb(v string) string { return strings.Join(strings.Fields(v), " ") }
