package agentdocs_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/go-foundation/units/agentdocs"
)

// Ported: internal/surface/registry_test.go TestRegistryRowsAreProvenanced.
func TestSurfacesAreProvenanced(t *testing.T) {
	t.Parallel()
	for _, s := range agentdocs.Surfaces() {
		if s.SourceURL == "" {
			t.Errorf("surface %q: SourceURL is empty (every row must carry the doc it was verified against)", s.ID)
		}
		if s.VerifiedOn == "" {
			t.Errorf("surface %q: VerifiedOn is empty (every row must carry the date it was verified)", s.ID)
		}
	}
}

// Ported: TestRegistryDeterministicOrder plus the three row tests, and the
// provenance values carried exactly from a2ahub internal/surface/registry.go.
func TestSurfacesAreA2asVerifiedRows(t *testing.T) {
	t.Parallel()
	want := []agentdocs.Surface{
		{ID: "claude", SkillsHome: ".claude/skills", ContextFile: "CLAUDE.md", ReadsAgentsMD: false,
			SourceURL: "https://code.claude.com/docs/en/skills", VerifiedOn: "2026-07-23"},
		{ID: "codex", SkillsHome: ".codex/skills", ContextFile: "AGENTS.md", ReadsAgentsMD: true,
			SourceURL: "https://developers.openai.com/codex/skills", VerifiedOn: "2026-07-23"},
		{ID: "dsh", SkillsHome: ".dsh/skills", ContextFile: "AGENTS.md", ReadsAgentsMD: true,
			SourceURL:  "https://github.com/deepseek-ai/deepseek-harness/blob/0a53fb55bea101816fa226bb964ae2bed71c343b/packages/skill/skill-filesystem/src/index.ts",
			VerifiedOn: "2026-08-30"},
	}
	got := agentdocs.Surfaces()
	if len(got) != len(want) {
		t.Fatalf("Surfaces() = %d rows, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Surfaces()[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// A caller mutating the returned slice cannot change the registry.
func TestSurfacesReturnsACopy(t *testing.T) {
	t.Parallel()
	rows := agentdocs.Surfaces()
	rows[0].SkillsHome = "/tmp/elsewhere"
	if agentdocs.Surfaces()[0].SkillsHome != ".claude/skills" {
		t.Fatal("mutating Surfaces()'s result changed the registry")
	}
}

// Ported: TestByIDUnknown and the ByID lookups of the row tests.
func TestByID(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"claude", "codex", "dsh"} {
		if s, ok := agentdocs.ByID(id); !ok || s.ID != id {
			t.Errorf("ByID(%q) = %+v, %v", id, s, ok)
		}
	}
	if _, ok := agentdocs.ByID("does-not-exist"); ok {
		t.Error("ByID(unknown) ok = true, want false")
	}
}

// Ported: TestMarkerDir.
func TestMarkerDir(t *testing.T) {
	t.Parallel()
	for id, want := range map[string]string{"claude": ".claude", "codex": ".codex", "dsh": ".dsh"} {
		s, _ := agentdocs.ByID(id)
		if got := s.MarkerDir(); got != want {
			t.Errorf("%s.MarkerDir() = %q, want %q", id, got, want)
		}
	}
}

// --- Detect (ported: the detect.go half of registry_test.go) ---

func TestDetectNeitherPresent(t *testing.T) {
	t.Parallel()
	if got := agentdocs.Detect(t.TempDir()); len(got) != 0 {
		t.Errorf("Detect(empty root) = %v, want empty", got)
	}
}

func TestDetectClaudeOnly(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mustMkdirAll(t, filepath.Join(root, ".claude"))
	if got := agentdocs.Detect(root); len(got) != 1 || got[0].ID != "claude" {
		t.Fatalf("Detect(.claude only) = %v, want [claude]", got)
	}
}

// A bare ".agents/" is a shared convention, not dsh's marker.
func TestDetectDshNotDetectedByBareAgents(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mustMkdirAll(t, filepath.Join(root, ".agents"))
	for _, s := range agentdocs.Detect(root) {
		if s.ID == "dsh" {
			t.Fatal("Detect(bare .agents/) reported dsh")
		}
	}
}

func TestDetectDshOnly(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mustMkdirAll(t, filepath.Join(root, ".dsh"))
	if got := agentdocs.Detect(root); len(got) != 1 || got[0].ID != "dsh" {
		t.Fatalf("Detect(.dsh only) = %v, want [dsh]", got)
	}
}

func TestDetectBothPresentOrder(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mustMkdirAll(t, filepath.Join(root, ".codex"))
	mustMkdirAll(t, filepath.Join(root, ".claude"))
	if got := agentdocs.Detect(root); len(got) != 2 || got[0].ID != "claude" || got[1].ID != "codex" {
		t.Fatalf("Detect(both) = %v, want [claude, codex]", got)
	}
}

func TestDetectMissingRoot(t *testing.T) {
	t.Parallel()
	if got := agentdocs.Detect(filepath.Join(t.TempDir(), "does-not-exist")); len(got) != 0 {
		t.Errorf("Detect(missing root) = %v, want empty", got)
	}
}

func TestDetectMarkerIsAFileNotADir(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".claude"), "not a dir")
	if got := agentdocs.Detect(root); len(got) != 0 {
		t.Errorf("Detect(.claude as file) = %v, want empty", got)
	}
}

// --- errors (ported: the errors.go half) ---

func TestErrorString(t *testing.T) {
	t.Parallel()
	err := &agentdocs.Error{Op: "Link", Input: "/some/target", Err: agentdocs.ErrForeignTarget}
	if got, want := err.Error(), "agentdocs: Link: /some/target: "+agentdocs.ErrForeignTarget.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if !errors.Is(err, agentdocs.ErrForeignTarget) {
		t.Error("errors.Is(err, ErrForeignTarget) = false")
	}
	bare := &agentdocs.Error{Op: "Link", Err: agentdocs.ErrForeignTarget}
	if got, want := bare.Error(), "agentdocs: Link: "+agentdocs.ErrForeignTarget.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

// --- Link (ported: the link.go half) ---

const ssotRel = ".mytool/skill"

func setupSSOT(t *testing.T, root string) {
	t.Helper()
	mustWrite(t, filepath.Join(root, ssotRel, "SKILL.md"), "---\nname: mytool\ndescription: test\n---\n# mytool\n")
}

func mustMkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func assertResolvesToSSOT(t *testing.T, root, entry string) {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(entry)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", entry, err)
	}
	want, err := filepath.EvalSymlinks(filepath.Join(root, ssotRel))
	if err != nil {
		t.Fatalf("EvalSymlinks(ssot): %v", err)
	}
	if resolved != want {
		t.Errorf("entry resolves to %s, want %s", resolved, want)
	}
}

func TestLinkFreshCreatesRelativeSymlinkResolvingToSSOT(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	setupSSOT(t, root)
	claude, _ := agentdocs.ByID("claude")

	result, err := agentdocs.Link(root, claude, "mytool", ssotRel, false)
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if result.Mode != agentdocs.LinkSymlink {
		t.Fatalf("Mode = %q, want %q", result.Mode, agentdocs.LinkSymlink)
	}
	if want := filepath.Join(".claude/skills", "mytool"); result.Path != want {
		t.Errorf("Path = %q, want %q (repo-relative)", result.Path, want)
	}
	if result.Surface.ID != "claude" {
		t.Errorf("Surface.ID = %q", result.Surface.ID)
	}

	entry := filepath.Join(root, claude.SkillsHome, "mytool")
	// The link text must be RELATIVE: an absolute link breaks when the repo is
	// checked out elsewhere. EvalSymlinks alone passes for both, so Readlink.
	dest, err := os.Readlink(entry)
	if err != nil {
		t.Fatalf("Readlink: %v", err)
	}
	if filepath.IsAbs(dest) {
		t.Errorf("symlink dest %q is absolute, want relative", dest)
	}
	if !strings.HasSuffix(dest, ssotRel) {
		t.Errorf("symlink dest %q does not end in %q", dest, ssotRel)
	}
	assertResolvesToSSOT(t, root, entry)
	if b, err := os.ReadFile(filepath.Join(entry, "SKILL.md")); err != nil || !strings.Contains(string(b), "name: mytool") {
		t.Errorf("linked SKILL.md = %q, %v", b, err)
	}
}

func TestLinkRefreshIsIdempotent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	setupSSOT(t, root)
	claude, _ := agentdocs.ByID("claude")
	if _, err := agentdocs.Link(root, claude, "mytool", ssotRel, false); err != nil {
		t.Fatal(err)
	}
	result, err := agentdocs.Link(root, claude, "mytool", ssotRel, false)
	if err != nil || result.Mode != agentdocs.LinkSymlink {
		t.Fatalf("refresh = %+v, %v", result, err)
	}
	assertResolvesToSSOT(t, root, filepath.Join(root, claude.SkillsHome, "mytool"))
	// RemoveAll on a symlink removes the link, never the tree it points to.
	if _, err := os.Stat(filepath.Join(root, ssotRel, "SKILL.md")); err != nil {
		t.Errorf("SSOT SKILL.md missing after refresh: %v", err)
	}
}

func TestLinkForeignTargetRefusedWithoutForce(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	setupSSOT(t, root)
	claude, _ := agentdocs.ByID("claude")
	entry := filepath.Join(root, claude.SkillsHome, "mytool")
	foreignFile := filepath.Join(entry, "SKILL.md")
	foreign := "---\nname: someone-else\ndescription: not ours\n---\n"
	mustWrite(t, foreignFile, foreign)

	_, err := agentdocs.Link(root, claude, "mytool", ssotRel, false)
	if !errors.Is(err, agentdocs.ErrForeignTarget) {
		t.Fatalf("Link(foreign) = %v, want ErrForeignTarget", err)
	}
	if b, _ := os.ReadFile(foreignFile); string(b) != foreign {
		t.Errorf("foreign entry changed: %q", b)
	}

	result, err := agentdocs.Link(root, claude, "mytool", ssotRel, true)
	if err != nil || result.Mode != agentdocs.LinkSymlink {
		t.Fatalf("Link(force) = %+v, %v", result, err)
	}
	info, err := os.Lstat(entry)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("entry after force is not a symlink: %v %v", info, err)
	}
}

// Another tool's stub carries another tool's marker: foreign.
func TestLinkAnotherToolsStubIsForeign(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	setupSSOT(t, root)
	claude, _ := agentdocs.ByID("claude")
	mustWrite(t, filepath.Join(root, claude.SkillsHome, "mytool", "SKILL.md"), "---\nname: x\n---\n\n<!-- othertool-skill-link -->\n")
	if _, err := agentdocs.Link(root, claude, "mytool", ssotRel, false); !errors.Is(err, agentdocs.ErrForeignTarget) {
		t.Fatalf("Link over another tool's stub = %v, want ErrForeignTarget", err)
	}
}

func TestLinkForeignSymlinkWithMatchingSuffixIsNotOwned(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	setupSSOT(t, root)
	claude, _ := agentdocs.ByID("claude")

	foreignSSOT := filepath.Join(root, "foreign", ".mytool", "skill")
	foreignMarker := filepath.Join(foreignSSOT, "KEEP")
	mustWrite(t, foreignMarker, "foreign")
	entry := filepath.Join(root, claude.SkillsHome, "mytool")
	mustMkdirAll(t, filepath.Dir(entry))
	if err := os.Symlink(foreignSSOT, entry); err != nil {
		t.Fatal(err)
	}

	if _, err := agentdocs.Link(root, claude, "mytool", ssotRel, false); !errors.Is(err, agentdocs.ErrForeignTarget) {
		t.Fatalf("Link = %v, want ErrForeignTarget", err)
	}
	resolved, _ := filepath.EvalSymlinks(entry)
	wantForeign, _ := filepath.EvalSymlinks(foreignSSOT)
	if resolved != wantForeign {
		t.Fatalf("foreign link changed to %q, want %q", resolved, wantForeign)
	}
	if b, err := os.ReadFile(foreignMarker); err != nil || string(b) != "foreign" {
		t.Fatalf("foreign target changed: %q %v", b, err)
	}

	if _, err := agentdocs.Link(root, claude, "mytool", ssotRel, true); err != nil {
		t.Fatalf("Link(force): %v", err)
	}
	assertResolvesToSSOT(t, root, entry)
}

// A name that is not one path element could make the wipe-before-relink
// reach outside the skills home. It is refused and nothing is touched.
func TestLinkRefusesANameThatIsNotOneElement(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	setupSSOT(t, root)
	claude, _ := agentdocs.ByID("claude")
	keep := filepath.Join(root, ".claude", "keep.md")
	mustWrite(t, keep, "keep\n")
	for _, name := range []string{"", ".", "..", "a/b", "../x", `a\b`} {
		if _, err := agentdocs.Link(root, claude, name, ssotRel, true); !errors.Is(err, agentdocs.ErrInvalidName) {
			t.Errorf("Link(name=%q) = %v, want ErrInvalidName", name, err)
		}
	}
	if b, err := os.ReadFile(keep); err != nil || string(b) != "keep\n" {
		t.Fatalf(".claude/keep.md changed: %q %v", b, err)
	}
}
