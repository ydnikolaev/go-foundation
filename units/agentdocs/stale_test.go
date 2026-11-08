package agentdocs_test

import (
	"reflect"
	"testing"
	"testing/fstest"

	"github.com/ydnikolaev/go-foundation/units/agentdocs"
)

// Stale names every generated file the tree does not hold byte for byte:
// missing or different. Files the tree holds beyond the map are not judged.
func TestStale(t *testing.T) {
	t.Parallel()
	tree := fstest.MapFS{
		"mytool/reference/commands.md": {Data: []byte("# commands\n")},
		"mytool/reference/env.md":      {Data: []byte("# env, edited by hand\n")},
		"mytool/SKILL.md":              {Data: []byte("not generated, not judged\n")},
	}
	got := agentdocs.Stale(tree, map[string][]byte{
		"mytool/reference/env.md":      []byte("# env\n"),
		"mytool/reference/commands.md": []byte("# commands\n"),
		"mytool/reference/absent.md":   []byte("# absent\n"),
	})
	want := []string{"mytool/reference/absent.md", "mytool/reference/env.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Stale = %v, want %v (sorted)", got, want)
	}
}

func TestStaleCurrentTreeIsEmpty(t *testing.T) {
	t.Parallel()
	tree := fstest.MapFS{"a.md": {Data: []byte("a")}, "b/c.md": {Data: []byte("")}}
	if got := agentdocs.Stale(tree, map[string][]byte{"a.md": []byte("a"), "b/c.md": nil}); len(got) != 0 {
		t.Fatalf("Stale = %v, want none", got)
	}
	if got := agentdocs.Stale(tree, nil); len(got) != 0 {
		t.Fatalf("Stale(nil) = %v, want none", got)
	}
}

// A path that names a directory in the tree is not a matching file.
func TestStaleDirectoryIsNotAFile(t *testing.T) {
	t.Parallel()
	tree := fstest.MapFS{"b/c.md": {Data: []byte("c")}}
	if got := agentdocs.Stale(tree, map[string][]byte{"b": nil}); !reflect.DeepEqual(got, []string{"b"}) {
		t.Fatalf("Stale = %v, want [b]", got)
	}
}
