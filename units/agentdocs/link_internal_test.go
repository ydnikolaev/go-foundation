package agentdocs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLinkSymlinkFallbackToStub is ported from a2ahub
// internal/surface/registry_test.go. It is intentionally NOT t.Parallel(): it
// mutates the package-level symlink var, so its mutate/restore window runs
// inside testing's sequential phase, before any parallel test resumes.
func TestLinkSymlinkFallbackToStub(t *testing.T) {
	root := t.TempDir()
	const ssot = ".mytool/skill"
	if err := os.MkdirAll(filepath.Join(root, ssot), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ssot, "SKILL.md"), []byte("---\nname: mytool\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	claude, _ := ByID("claude")

	orig := symlink
	symlink = func(string, string) error { return errors.New("simulated: symlinks unavailable") }
	t.Cleanup(func() { symlink = orig })

	result, err := Link(root, claude, "mytool", ssot, false)
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if result.Mode != LinkStub {
		t.Fatalf("Mode = %q, want %q", result.Mode, LinkStub)
	}
	entry := filepath.Join(root, claude.SkillsHome, "mytool")
	info, err := os.Lstat(entry)
	if err != nil || !info.IsDir() {
		t.Fatalf("stub entry is not a directory: %v %v", info, err)
	}
	data, err := os.ReadFile(filepath.Join(entry, "SKILL.md"))
	if err != nil {
		t.Fatalf("read stub: %v", err)
	}
	content := string(data)
	if !strings.HasPrefix(content, "---\n") {
		t.Errorf("stub does not start with frontmatter: %q", content)
	}
	for _, want := range []string{"name: mytool\n", "description:", "<!-- mytool-skill-link -->", ssot + "/SKILL.md"} {
		if !strings.Contains(content, want) {
			t.Errorf("stub missing %q: %q", want, content)
		}
	}

	// A re-link recognizes its own stub as owned and refreshes it unforced.
	result2, err := Link(root, claude, "mytool", ssot, false)
	if err != nil || result2.Mode != LinkStub {
		t.Fatalf("stub refresh = %+v, %v", result2, err)
	}
}

// The a2a-derived name reproduces a2a's own link marker byte for byte, so an
// a2a stub already in a repository stays owned when a2a moves to this unit.
func TestLinkMarkerMatchesA2a(t *testing.T) {
	t.Parallel()
	if got := linkMarker("a2ahub"); got != "<!-- a2ahub-skill-link -->" {
		t.Fatalf("linkMarker(a2ahub) = %q, want a2a's %q", got, "<!-- a2ahub-skill-link -->")
	}
}
