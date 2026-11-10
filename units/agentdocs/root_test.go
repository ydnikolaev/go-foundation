package agentdocs_test

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/go-foundation/units/agentdocs"
)

// The ported tests name one absolute path per fixture. These helpers hand it
// to the API the way it takes it: the parent directory is the root, the base
// name is the path inside it. The containment tests below call the API
// directly, with a repository root and a home outside it.

func install(tree fs.FS, dir, target string, tl agentdocs.Tool, force bool) (int, error) {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return 0, fmt.Errorf("creating the parent of %s: %w", target, err)
	}
	n, err := agentdocs.Install(tree, dir, filepath.Dir(target), filepath.Base(target), tl, force)
	if err != nil {
		return n, fmt.Errorf("installing %s: %w", target, err)
	}
	return n, nil
}

func installed(target string, tl agentdocs.Tool) agentdocs.InstallStatus {
	return agentdocs.Installed(filepath.Dir(target), filepath.Base(target), tl)
}

func drift(tree fs.FS, dir, target string) (agentdocs.TreeDrift, error) {
	d, err := agentdocs.Drift(tree, dir, filepath.Dir(target), filepath.Base(target))
	if err != nil {
		return d, fmt.Errorf("drift of %s: %w", target, err)
	}
	return d, nil
}

func upsertAt(path, name, body string) (bool, error) {
	changed, err := agentdocs.UpsertPointer(filepath.Dir(path), filepath.Base(path), name, body)
	if err != nil {
		return changed, fmt.Errorf("upserting the pointer in %s: %w", path, err)
	}
	return changed, nil
}

// home is a directory outside the repository root, standing in for the
// user's home: one file and one directory the unit must never touch.
func home(t *testing.T) string {
	t.Helper()
	h := t.TempDir()
	mustWrite(t, filepath.Join(h, ".bashrc"), "rc\n")
	mustWrite(t, filepath.Join(h, "d", "keep"), "keep\n")
	return h
}

// assertHomeUntouched fails when anything under h differs from home's
// fixture: a write, a removal or a new entry.
func assertHomeUntouched(t *testing.T, h string) {
	t.Helper()
	got := map[string]string{}
	err := filepath.WalkDir(h, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(h, p)
		b, _ := os.ReadFile(p)
		got[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatalf("walk home: %v", err)
	}
	want := map[string]string{".bashrc": "rc\n", "d/keep": "keep\n"}
	if len(got) != len(want) || got[".bashrc"] != want[".bashrc"] || got["d/keep"] != want["d/keep"] {
		t.Fatalf("the directory outside the root changed: %v", got)
	}
}

// assertEscapes wants a refusal that names the path and says it escapes.
func assertEscapes(t *testing.T, err error, path string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: no error, want a refusal that it escapes the root", path)
	}
	var ae *agentdocs.Error
	if !errors.As(err, &ae) || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("%s: error %q (%T) does not name the path and the escape", path, err, err)
	}
}

// relLink creates link as a relative symlink to dest, both absolute.
func relLink(t *testing.T, dest, link string) {
	t.Helper()
	rel, err := filepath.Rel(filepath.Dir(link), dest)
	if err != nil {
		t.Fatal(err)
	}
	mustMkdirAll(t, filepath.Dir(link))
	if err := os.Symlink(rel, link); err != nil {
		t.Fatal(err)
	}
}

func assertSymlink(t *testing.T, path, dest string) {
	t.Helper()
	got, err := os.Readlink(path)
	if err != nil || got != dest {
		t.Fatalf("%s: Readlink = %q, %v; want the symlink to %q kept", path, got, err, dest)
	}
}

// --- UpsertPointer ---

func TestUpsertPointerRefusesASymlinkOutOfTheRoot(t *testing.T) {
	t.Parallel()
	for name, setup := range map[string]func(t *testing.T, repo, h string){
		"relative": func(t *testing.T, repo, h string) {
			relLink(t, filepath.Join(h, ".bashrc"), filepath.Join(repo, "CLAUDE.md"))
		},
		"absolute": func(t *testing.T, repo, h string) {
			if err := os.Symlink(filepath.Join(h, ".bashrc"), filepath.Join(repo, "CLAUDE.md")); err != nil {
				t.Fatal(err)
			}
		},
		"second hop": func(t *testing.T, repo, h string) {
			if err := os.Symlink("hop.md", filepath.Join(repo, "CLAUDE.md")); err != nil {
				t.Fatal(err)
			}
			relLink(t, filepath.Join(h, ".bashrc"), filepath.Join(repo, "hop.md"))
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			repo, h := t.TempDir(), home(t)
			setup(t, repo, h)
			_, err := agentdocs.UpsertPointer(repo, "CLAUDE.md", "mytool", pointerBody)
			assertEscapes(t, err, "CLAUDE.md")
			assertHomeUntouched(t, h)
			if info, err := os.Lstat(filepath.Join(repo, "CLAUDE.md")); err != nil || info.Mode()&os.ModeSymlink == 0 {
				t.Fatalf("CLAUDE.md was replaced: %v %v", info, err)
			}
		})
	}
}

func TestUpsertPointerThroughAnEscapingParentIsRefused(t *testing.T) {
	t.Parallel()
	repo, h := t.TempDir(), home(t)
	relLink(t, h, filepath.Join(repo, "docs"))
	_, err := agentdocs.UpsertPointer(repo, filepath.Join("docs", "AGENTS.md"), "mytool", pointerBody)
	assertEscapes(t, err, "docs")
	assertHomeUntouched(t, h)
}

func TestUpsertPointerRefusesAPathThatEscapes(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	repo := filepath.Join(parent, "repo")
	mustMkdirAll(t, repo)
	for _, p := range []string{"../AGENTS.md", "a/../../AGENTS.md", filepath.Join(parent, "AGENTS.md")} {
		_, err := agentdocs.UpsertPointer(repo, p, "mytool", pointerBody)
		assertEscapes(t, err, p)
		if _, err := os.Stat(filepath.Join(parent, "AGENTS.md")); !os.IsNotExist(err) {
			t.Fatalf("%s: a file was created outside the root", p)
		}
	}
}

// An inside symlink still works: CLAUDE.md -> AGENTS.md gets the block into
// AGENTS.md and stays a symlink.
func TestUpsertPointerFollowsASymlinkInsideTheRoot(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, "AGENTS.md"), "# agents\n")
	mustWrite(t, filepath.Join(repo, "docs", "CONTEXT.md"), "# context\n")
	if err := os.Symlink("AGENTS.md", filepath.Join(repo, "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("docs/CONTEXT.md", filepath.Join(repo, "TWO.md")); err != nil {
		t.Fatal(err)
	}
	for link, dest := range map[string]string{"CLAUDE.md": "AGENTS.md", "TWO.md": "docs/CONTEXT.md"} {
		changed, err := agentdocs.UpsertPointer(repo, link, "mytool", pointerBody)
		if err != nil || !changed {
			t.Fatalf("UpsertPointer(%s) = %v, %v", link, changed, err)
		}
		if got := read(t, filepath.Join(repo, filepath.FromSlash(dest))); !strings.Contains(got, pointerStart) {
			t.Fatalf("%s does not carry the block:\n%s", dest, got)
		}
		assertSymlink(t, filepath.Join(repo, link), dest)
	}
	// Two hops: THREE.md -> TWO.md -> docs/CONTEXT.md. Both links stay.
	if err := os.Symlink("TWO.md", filepath.Join(repo, "THREE.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := agentdocs.UpsertPointer(repo, "THREE.md", "other", "## other\n"); err != nil {
		t.Fatalf("UpsertPointer(THREE.md) = %v", err)
	}
	if got := read(t, filepath.Join(repo, "docs", "CONTEXT.md")); !strings.Contains(got, "<!-- other:pointer:start -->") {
		t.Fatalf("docs/CONTEXT.md does not carry the second block:\n%s", got)
	}
	assertSymlink(t, filepath.Join(repo, "THREE.md"), "TWO.md")
	assertSymlink(t, filepath.Join(repo, "TWO.md"), "docs/CONTEXT.md")
}

// The write goes through a temporary file and a rename: the file keeps its
// mode, and no temporary file is left behind.
func TestUpsertPointerKeepsTheModeAndLeavesNoTempFile(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	path := filepath.Join(repo, "AGENTS.md")
	mustWrite(t, path, "# agents\n")
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if changed, err := agentdocs.UpsertPointer(repo, "AGENTS.md", "mytool", pointerBody); err != nil || !changed {
		t.Fatalf("UpsertPointer = %v, %v", changed, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode after the write = %v, %v; want 0600 kept", info.Mode().Perm(), err)
	}
	entries, err := os.ReadDir(repo)
	if err != nil || len(entries) != 1 {
		t.Fatalf("the directory holds %d entries after the write, want AGENTS.md alone: %v", len(entries), err)
	}
	// A new file gets 0644 and no leftover either.
	if _, err := agentdocs.UpsertPointer(repo, "NEW.md", "mytool", pointerBody); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(repo, "NEW.md")); err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("new file mode = %v, %v; want 0644", info.Mode().Perm(), err)
	}
	if entries, _ := os.ReadDir(repo); len(entries) != 2 {
		t.Fatalf("the directory holds %d entries, want 2", len(entries))
	}
}

// --- Install ---

func TestInstallRefusesATargetThroughAnEscapingParent(t *testing.T) {
	t.Parallel()
	repo, h := t.TempDir(), home(t)
	relLink(t, h, filepath.Join(repo, ".mytool"))
	_, err := agentdocs.Install(docTree(), "mytool", repo, filepath.Join(".mytool", "skill"), tool, true)
	assertEscapes(t, err, ".mytool")
	assertHomeUntouched(t, h)
}

func TestInstallRefusesATargetThatEscapes(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	repo := filepath.Join(parent, "repo")
	mustMkdirAll(t, repo)
	for _, target := range []string{"../skill", "a/../../skill", filepath.Join(parent, "skill"), ""} {
		_, err := agentdocs.Install(docTree(), "mytool", repo, target, tool, true)
		assertEscapes(t, err, target)
		if _, err := os.Stat(filepath.Join(parent, "skill")); !os.IsNotExist(err) {
			t.Fatalf("%q: an install landed outside the root", target)
		}
	}
}

// A forced install over a target holding a link out of the root removes the
// link, never what it points at.
func TestInstallForcedWipeKeepsWhatALinkPointsAt(t *testing.T) {
	t.Parallel()
	repo, h := t.TempDir(), home(t)
	mustWrite(t, filepath.Join(repo, "skill", "user.md"), "mine\n")
	relLink(t, filepath.Join(h, "d"), filepath.Join(repo, "skill", "out"))
	if _, err := agentdocs.Install(docTree(), "mytool", repo, "skill", tool, true); err != nil {
		t.Fatalf("Install(force) = %v", err)
	}
	assertHomeUntouched(t, h)
	assertTreeMatches(t, docTree(), "mytool", filepath.Join(repo, "skill"))
}

// A target that is a symlink inside the root is replaced as a link: a forced
// install never empties the directory it points at.
func TestInstallForcedOverAnInsideLinkKeepsItsDirectory(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, "docs", "user.md"), "mine\n")
	if err := os.Symlink("docs", filepath.Join(repo, "skill")); err != nil {
		t.Fatal(err)
	}
	if _, err := agentdocs.Install(docTree(), "mytool", repo, "skill", tool, true); err != nil {
		t.Fatalf("Install(force) = %v", err)
	}
	if got := read(t, filepath.Join(repo, "docs", "user.md")); got != "mine\n" {
		t.Fatalf("docs/user.md = %q", got)
	}
	if info, err := os.Lstat(filepath.Join(repo, "skill")); err != nil || !info.IsDir() {
		t.Fatalf("skill is not a directory after the install: %v %v", info, err)
	}
	assertTreeMatches(t, docTree(), "mytool", filepath.Join(repo, "skill"))
}

// A user-level install names its own root and installs into ".": the
// refresh mirrors (a stale file goes), and the root itself stays.
func TestInstallIntoTheRootItself(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if _, err := agentdocs.Install(docTree(), "mytool", root, ".", tool, false); err != nil {
		t.Fatalf("Install(.) = %v", err)
	}
	mustWrite(t, filepath.Join(root, "stale.md"), "stale\n")
	if _, err := agentdocs.Install(docTree(), "mytool", root, ".", tool, false); err != nil {
		t.Fatalf("refresh of . = %v", err)
	}
	assertTreeMatches(t, docTree(), "mytool", root)
	if st := agentdocs.Installed(root, ".", tool); st.State != agentdocs.StateCurrent {
		t.Fatalf("Installed(.) = %v, want current", st.State)
	}
	if d, err := agentdocs.Drift(docTree(), "mytool", root, "."); err != nil || d.HasDiff() || d.Undecidable != "" {
		t.Fatalf("Drift(.) = %+v, %v; want clean", d, err)
	}
}

// A PROVENANCE.md that links out of the root does not make the target this
// tool's, even when the file it points at carries the right marker.
func TestInstallProvenanceLinkingOutIsForeign(t *testing.T) {
	t.Parallel()
	repo, h := t.TempDir(), home(t)
	stamp := filepath.Join(t.TempDir(), "PROVENANCE.md")
	mustWrite(t, stamp, "<!-- mytool-skill-install -->\n<!-- comparable: 1.4.0 -->\n")
	mustWrite(t, filepath.Join(repo, "skill", "user.md"), "mine\n")
	relLink(t, stamp, filepath.Join(repo, "skill", agentdocs.ProvenanceFile))
	if st := agentdocs.Installed(repo, "skill", tool); st.State != agentdocs.StateForeign {
		t.Fatalf("Installed = %v, want foreign", st.State)
	}
	if _, err := agentdocs.Install(docTree(), "mytool", repo, "skill", tool, false); !errors.Is(err, agentdocs.ErrForeignTarget) {
		t.Fatalf("Install = %v, want ErrForeignTarget", err)
	}
	if got := read(t, filepath.Join(repo, "skill", "user.md")); got != "mine\n" {
		t.Fatalf("user.md = %q", got)
	}
	assertHomeUntouched(t, h)
}

// --- Installed and Drift read through the root too ---

func TestInstalledDoesNotJudgeAnInstallOutsideTheRoot(t *testing.T) {
	t.Parallel()
	repo, outside := t.TempDir(), t.TempDir()
	if _, err := agentdocs.Install(docTree(), "mytool", outside, "skill", tool, false); err != nil {
		t.Fatal(err)
	}
	relLink(t, outside, filepath.Join(repo, ".mytool"))
	if st := agentdocs.Installed(repo, filepath.Join(".mytool", "skill"), tool); st.State != agentdocs.StateNone {
		t.Fatalf("Installed through a link out of the root = %v, want none", st.State)
	}
	if st := agentdocs.Installed(repo, "../skill", tool); st.State != agentdocs.StateNone {
		t.Fatalf("Installed(../skill) = %v, want none", st.State)
	}
}

// A file in the install that links out of the root is undecidable, even when
// what it points at matches the tree byte for byte.
func TestDriftLinkOutOfTheRootIsUndecidable(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	if _, err := agentdocs.Install(driftTree(), "mytool", repo, "skill", tool, false); err != nil {
		t.Fatal(err)
	}
	copyOut := filepath.Join(t.TempDir(), "SKILL.md")
	mustWrite(t, copyOut, string(driftTree()["mytool/SKILL.md"].Data))
	p := filepath.Join(repo, "skill", "SKILL.md")
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	relLink(t, copyOut, p)
	d, err := agentdocs.Drift(driftTree(), "mytool", repo, "skill")
	if err != nil || d.Undecidable != "SKILL.md" {
		t.Fatalf("Drift = %+v, %v; want SKILL.md undecidable", d, err)
	}
}

func TestDriftTargetOutsideTheRoot(t *testing.T) {
	t.Parallel()
	repo, outside := t.TempDir(), t.TempDir()
	if _, err := agentdocs.Install(driftTree(), "mytool", outside, "skill", tool, false); err != nil {
		t.Fatal(err)
	}
	relLink(t, outside, filepath.Join(repo, ".mytool"))
	d, err := agentdocs.Drift(driftTree(), "mytool", repo, filepath.Join(".mytool", "skill"))
	if err != nil || d.Undecidable != "." {
		t.Fatalf("Drift through a link out of the root = %+v, %v; want undecidable at .", d, err)
	}
	_, err = agentdocs.Drift(driftTree(), "mytool", repo, "../skill")
	assertEscapes(t, err, "../skill")
}

// --- Link and Detect ---

func TestLinkRefusesASkillsHomeOutsideTheRoot(t *testing.T) {
	t.Parallel()
	repo, h := t.TempDir(), home(t)
	setupSSOT(t, repo)
	claude, _ := agentdocs.ByID("claude")
	relLink(t, h, filepath.Join(repo, claude.SkillsHome))
	_, err := agentdocs.Link(repo, claude, "mytool", ssotRel, true)
	assertEscapes(t, err, claude.SkillsHome)
	assertHomeUntouched(t, h)
	// The same through an absolute link.
	repo2, h2 := t.TempDir(), home(t)
	setupSSOT(t, repo2)
	mustMkdirAll(t, filepath.Join(repo2, ".claude"))
	if err := os.Symlink(h2, filepath.Join(repo2, claude.SkillsHome)); err != nil {
		t.Fatal(err)
	}
	_, err = agentdocs.Link(repo2, claude, "mytool", ssotRel, true)
	assertEscapes(t, err, claude.SkillsHome)
	assertHomeUntouched(t, h2)
}

func TestLinkRefusesATargetOutsideTheRoot(t *testing.T) {
	t.Parallel()
	claude, _ := agentdocs.ByID("claude")
	t.Run("lexically", func(t *testing.T) {
		t.Parallel()
		repo := t.TempDir()
		for _, target := range []string{"../elsewhere", "a/../../elsewhere", filepath.Join(t.TempDir(), "skill")} {
			_, err := agentdocs.Link(repo, claude, "mytool", target, true)
			assertEscapes(t, err, target)
		}
		if _, err := os.Lstat(filepath.Join(repo, claude.SkillsHome, "mytool")); !os.IsNotExist(err) {
			t.Fatal("an entry was created for a target outside the root")
		}
	})
	t.Run("through a link", func(t *testing.T) {
		t.Parallel()
		repo, h := t.TempDir(), home(t)
		relLink(t, h, filepath.Join(repo, ".mytool"))
		_, err := agentdocs.Link(repo, claude, "mytool", ssotRel, true)
		assertEscapes(t, err, ssotRel)
		if _, err := os.Lstat(filepath.Join(repo, claude.SkillsHome, "mytool")); !os.IsNotExist(err) {
			t.Fatal("an entry was created for a target that resolves outside the root")
		}
		assertHomeUntouched(t, h)
	})
}

// An existing entry that links out of the root is foreign, not an error, so
// force can heal it; the replacement removes the link, never its target.
func TestLinkEntryLinkingOutIsForeign(t *testing.T) {
	t.Parallel()
	repo, h := t.TempDir(), home(t)
	setupSSOT(t, repo)
	claude, _ := agentdocs.ByID("claude")
	entry := filepath.Join(repo, claude.SkillsHome, "mytool")
	relLink(t, filepath.Join(h, "d"), entry)
	if _, err := agentdocs.Link(repo, claude, "mytool", ssotRel, false); !errors.Is(err, agentdocs.ErrForeignTarget) {
		t.Fatalf("Link = %v, want ErrForeignTarget", err)
	}
	if _, err := agentdocs.Link(repo, claude, "mytool", ssotRel, true); err != nil {
		t.Fatalf("Link(force) = %v", err)
	}
	assertResolvesToSSOT(t, repo, entry)
	assertHomeUntouched(t, h)
}

// A skills home that is a symlink inside the root still works. It sits one
// level deeper than .claude/skills, so a relative link computed from the
// spelled path instead of the real one would dangle.
func TestLinkThroughASkillsHomeLinkInsideTheRoot(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	setupSSOT(t, repo)
	claude, _ := agentdocs.ByID("claude")
	shared := filepath.Join(repo, "harness", "shared", "skills")
	mustMkdirAll(t, shared)
	relLink(t, shared, filepath.Join(repo, claude.SkillsHome))
	if _, err := agentdocs.Link(repo, claude, "mytool", ssotRel, false); err != nil {
		t.Fatalf("Link = %v", err)
	}
	assertResolvesToSSOT(t, repo, filepath.Join(shared, "mytool"))
	// A second run owns what the first wrote.
	if _, err := agentdocs.Link(repo, claude, "mytool", ssotRel, false); err != nil {
		t.Fatalf("re-Link = %v", err)
	}
}

func TestDetectReadsOnlyInsideTheRoot(t *testing.T) {
	t.Parallel()
	repo, h := t.TempDir(), home(t)
	relLink(t, h, filepath.Join(repo, ".claude"))
	mustMkdirAll(t, filepath.Join(repo, "harness", "codex"))
	relLink(t, filepath.Join(repo, "harness", "codex"), filepath.Join(repo, ".codex"))
	if got := agentdocs.Detect(repo); len(got) != 1 || got[0].ID != "codex" {
		t.Fatalf("Detect = %v, want codex alone: .claude links out of the root, .codex links inside it", got)
	}
}
