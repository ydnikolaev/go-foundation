package agentdocs_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ydnikolaev/go-foundation/units/agentdocs"
)

// tool is the release build every install test stamps with.
var tool = agentdocs.Tool{
	ID:         "mytool",
	Name:       "mytool",
	Version:    "1.4.0 (local, 1a2b3c)",
	Comparable: "1.4.0",
	Source:     "github.com/acme/mytool",
	Refresh:    "mytool skill install",
}

// docTree is a small tree rooted at "mytool/", with a nested directory and an
// unrelated sibling root the install must not pick up.
func docTree() fstest.MapFS {
	return fstest.MapFS{
		"mytool/SKILL.md":                 {Data: []byte("---\nname: mytool\n---\n# mytool\n")},
		"mytool/loops.md":                 {Data: []byte("loops\n")},
		"mytool/reference/commands.md":    {Data: []byte("commands\n")},
		"mytool/reference/authoring/a.md": {Data: []byte("a\n")},
		"other/SKILL.md":                  {Data: []byte("not ours to install\n")},
	}
}

func withTool(mut func(*agentdocs.Tool)) agentdocs.Tool {
	t := tool
	mut(&t)
	return t
}

// assertTreeMatches walks tree under dir and asserts every file was written
// under target byte-identically (root prefix stripped), and that target holds
// nothing else but PROVENANCE.md.
func assertTreeMatches(t *testing.T, tree fs.FS, dir, target string) {
	t.Helper()
	want := map[string]string{}
	err := fs.WalkDir(tree, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, _ := fs.ReadFile(tree, p)
		want[strings.TrimPrefix(p, dir+"/")] = string(b)
		return nil
	})
	if err != nil {
		t.Fatalf("walk tree: %v", err)
	}
	got := map[string]string{}
	err = filepath.WalkDir(target, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(target, p)
		if rel == agentdocs.ProvenanceFile {
			return nil
		}
		b, _ := os.ReadFile(p)
		got[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatalf("walk target: %v", err)
	}
	for rel, w := range want {
		if g, ok := got[rel]; !ok {
			t.Errorf("installed file missing: %s", rel)
		} else if g != w {
			t.Errorf("installed %s differs from the tree", rel)
		}
	}
	for rel := range got {
		if _, ok := want[rel]; !ok {
			t.Errorf("installed tree holds %s, which the source tree does not ship", rel)
		}
	}
}

func mustInstall(t *testing.T, target string, tl agentdocs.Tool) int {
	t.Helper()
	n, err := agentdocs.Install(docTree(), "mytool", target, tl, false)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	return n
}

// Ported: cmd_skill_test.go TestSkillInstall_CleanRepo. Install into a repo
// that already has a harness: the harness is untouched, the tree lands
// byte-identical under the target, and PROVENANCE.md carries the marker.
func TestInstallCleanRepo(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	agents := filepath.Join(repo, "AGENTS.md")
	mustWrite(t, agents, "# My AGENTS\nexisting content\n")
	mine := filepath.Join(repo, ".claude", "skills", "mine", "SKILL.md")
	mustWrite(t, mine, "my own skill\n")

	target := filepath.Join(repo, ".mytool", "skill")
	n := mustInstall(t, target, tool)
	if n != 4 {
		t.Errorf("written = %d, want 4 (the tree's files; PROVENANCE.md is not counted)", n)
	}

	if b, _ := os.ReadFile(agents); string(b) != "# My AGENTS\nexisting content\n" {
		t.Fatalf("AGENTS.md was modified: %q", b)
	}
	if b, _ := os.ReadFile(mine); string(b) != "my own skill\n" {
		t.Fatalf(".claude skill was modified: %q", b)
	}
	assertTreeMatches(t, docTree(), "mytool", target)
	if _, err := os.Stat(filepath.Join(target, "SKILL.md")); err != nil {
		t.Errorf("the tree's root prefix was not stripped: %v", err)
	}
}

// PROVENANCE.md opens with the marker and names Name Version, Source and
// Refresh (spec 01 "Install").
func TestInstallProvenanceNamesTheTool(t *testing.T) {
	t.Parallel()
	target := t.TempDir()
	mustInstall(t, target, tool)
	b, err := os.ReadFile(filepath.Join(target, agentdocs.ProvenanceFile))
	if err != nil {
		t.Fatalf("read provenance: %v", err)
	}
	s := string(b)
	if !strings.HasPrefix(s, "<!-- mytool-skill-install -->\n") {
		t.Fatalf("PROVENANCE.md does not open with the marker; head=%q", s[:min(60, len(s))])
	}
	for _, want := range []string{"mytool 1.4.0 (local, 1a2b3c)", "github.com/acme/mytool", "`mytool skill install`"} {
		if !strings.Contains(s, want) {
			t.Errorf("PROVENANCE.md missing %q:\n%s", want, s)
		}
	}
}

// Ported: TestSkillInstall_RefusesForeignTarget. A non-empty target without
// the tool's marker is someone else's: refuse, write nothing, unless forced.
func TestInstallRefusesForeignTarget(t *testing.T) {
	t.Parallel()
	target := t.TempDir()
	foreign := filepath.Join(target, "user-file.md")
	mustWrite(t, foreign, "do not touch\n")

	_, err := agentdocs.Install(docTree(), "mytool", target, tool, false)
	if !errors.Is(err, agentdocs.ErrForeignTarget) {
		t.Fatalf("Install(foreign) = %v, want ErrForeignTarget", err)
	}
	if b, _ := os.ReadFile(foreign); string(b) != "do not touch\n" {
		t.Fatalf("foreign file was modified: %q", b)
	}
	if _, err := os.Stat(filepath.Join(target, "SKILL.md")); !os.IsNotExist(err) {
		t.Fatal("SKILL.md written into a refused target")
	}

	if _, err := agentdocs.Install(docTree(), "mytool", target, tool, true); err != nil {
		t.Fatalf("Install(force) = %v", err)
	}
	assertTreeMatches(t, docTree(), "mytool", target)
}

// Another tool's install is foreign too: the marker is per tool.
func TestInstallRefusesAnotherToolsInstall(t *testing.T) {
	t.Parallel()
	target := t.TempDir()
	mustInstall(t, target, withTool(func(t *agentdocs.Tool) { t.ID = "othertool" }))
	_, err := agentdocs.Install(docTree(), "mytool", target, tool, false)
	if !errors.Is(err, agentdocs.ErrForeignTarget) {
		t.Fatalf("Install over another tool's install = %v, want ErrForeignTarget", err)
	}
}

// The marker namespace is Tool.ID, separate from the binary name: a2a's
// binary is "a2a" and its tree is "a2ahub". The marker line is a2a's own
// byte for byte; Name appears only in the prose.
func TestInstallMarkerIsTheToolID(t *testing.T) {
	t.Parallel()
	target := t.TempDir()
	a2a := withTool(func(t *agentdocs.Tool) { t.ID, t.Name, t.Refresh = "a2ahub", "a2a", "a2a skill install" })
	mustInstall(t, target, a2a)
	s := read(t, filepath.Join(target, agentdocs.ProvenanceFile))
	if !strings.HasPrefix(s, "<!-- a2ahub-skill-install -->\n") {
		t.Fatalf("marker line is not a2a's; head=%q", s[:min(60, len(s))])
	}
	if strings.Contains(s, "a2a-skill-install") || !strings.Contains(s, "a2a 1.4.0 (local, 1a2b3c)") {
		t.Fatalf("Name must be the prose, never the marker:\n%s", s)
	}
	// A refresh by the same tool is owned through the ID, not the Name.
	if _, err := agentdocs.Install(docTree(), "mytool", target, a2a, false); err != nil {
		t.Fatalf("refresh by the same ID = %v, want nil", err)
	}
	// Same Name, different ID: not ours.
	if _, err := agentdocs.Install(docTree(), "mytool", target, withTool(func(t *agentdocs.Tool) { t.Name = "a2a" }), false); !errors.Is(err, agentdocs.ErrForeignTarget) {
		t.Fatalf("Install with the same Name and another ID = %v, want ErrForeignTarget", err)
	}
}

// An empty ID would stamp a marker no install could own. Refused, nothing
// written.
func TestInstallRefusesAnEmptyID(t *testing.T) {
	t.Parallel()
	target := filepath.Join(t.TempDir(), "skill")
	_, err := agentdocs.Install(docTree(), "mytool", target, withTool(func(t *agentdocs.Tool) { t.ID = "" }), false)
	if !errors.Is(err, agentdocs.ErrToolID) {
		t.Fatalf("Install(empty ID) = %v, want ErrToolID", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("a refused install created the target")
	}
}

// Ported: TestSkillInstall_IdempotentRefresh.
func TestInstallIdempotentRefresh(t *testing.T) {
	t.Parallel()
	target := t.TempDir()
	mustInstall(t, target, tool)
	mustInstall(t, target, tool)
	assertTreeMatches(t, docTree(), "mytool", target)
}

// Ported: TestSkillInstall_RefreshPrunesStaleFiles. A refresh mirrors.
func TestInstallRefreshPrunesStaleFiles(t *testing.T) {
	t.Parallel()
	target := t.TempDir()
	mustInstall(t, target, tool)
	stale := filepath.Join(target, "reference", "authoring", "obsolete.md")
	mustWrite(t, stale, "stale\n")
	mustInstall(t, target, tool)
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("stale file survived the refresh: the tree is not mirrored")
	}
	assertTreeMatches(t, docTree(), "mytool", target)
}

// Ported: TestSkillInstall_EmptyDirOK.
func TestInstallEmptyDirOK(t *testing.T) {
	t.Parallel()
	mustInstall(t, t.TempDir(), tool)
}

// Install writes only under its target: a sibling of the target survives an
// install, a refresh and a forced install.
func TestInstallWritesOnlyUnderTarget(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	sibling := filepath.Join(repo, "keep.md")
	mustWrite(t, sibling, "keep\n")
	target := filepath.Join(repo, "skill")
	mustInstall(t, target, tool)
	mustInstall(t, target, tool)
	if _, err := agentdocs.Install(docTree(), "mytool", target, tool, true); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("repo holds %d entries, want 2 (keep.md and skill/)", len(entries))
	}
	if b, _ := os.ReadFile(sibling); string(b) != "keep\n" {
		t.Fatalf("sibling was modified: %q", b)
	}
}

// A dir the tree does not hold is refused BEFORE the target is wiped: a typo
// in dir must never cost a good install.
func TestInstallUnknownDirKeepsTheInstall(t *testing.T) {
	t.Parallel()
	target := t.TempDir()
	mustInstall(t, target, tool)
	_, err := agentdocs.Install(docTree(), "no-such-dir", target, tool, false)
	if !errors.Is(err, agentdocs.ErrTreeMissing) {
		t.Fatalf("Install(unknown dir) = %v, want ErrTreeMissing", err)
	}
	assertTreeMatches(t, docTree(), "mytool", target)
}

// dir "." installs the whole tree.
func TestInstallWholeTree(t *testing.T) {
	t.Parallel()
	target := t.TempDir()
	tree := fstest.MapFS{"SKILL.md": {Data: []byte("x\n")}, "ref/a.md": {Data: []byte("a\n")}}
	n, err := agentdocs.Install(tree, ".", target, tool, false)
	if err != nil || n != 2 {
		t.Fatalf("Install(.) = %d, %v; want 2 files", n, err)
	}
	if b, _ := os.ReadFile(filepath.Join(target, "ref", "a.md")); string(b) != "a\n" {
		t.Fatalf("ref/a.md = %q", b)
	}
}

func mustWrite(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}
