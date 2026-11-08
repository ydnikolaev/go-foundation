package agentdocs_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"testing/fstest"

	"github.com/ydnikolaev/go-foundation/units/agentdocs"
)

// Ported from a2ahub internal/cli/cmd_doctor_skilltree_test.go: the tree walk
// behind "skill manual current". Wording and severity stay with the caller;
// these pin the classification the doctor row is built from.

// driftTree is docTree plus enough files for the output-stability fixture.
func driftTree() fstest.MapFS {
	tree := docTree()
	for i := range 12 {
		tree[fmt.Sprintf("mytool/reference/page%02d.md", i)] = &fstest.MapFile{Data: []byte(fmt.Sprintf("page %d\n", i))}
	}
	return tree
}

func installedDrift(t *testing.T) string {
	t.Helper()
	target := filepath.Join(t.TempDir(), "skill")
	if _, err := agentdocs.Install(driftTree(), "mytool", target, tool, false); err != nil {
		t.Fatal(err)
	}
	return target
}

func mustDrift(t *testing.T, target string) agentdocs.TreeDrift {
	t.Helper()
	d, err := agentdocs.Drift(driftTree(), "mytool", target)
	if err != nil {
		t.Fatalf("Drift: %v", err)
	}
	return d
}

func assertDrift(t *testing.T, got agentdocs.TreeDrift, missing, modified, unexpected []string) {
	t.Helper()
	if !reflect.DeepEqual(got.Missing, missing) || !reflect.DeepEqual(got.Modified, modified) || !reflect.DeepEqual(got.Unexpected, unexpected) {
		t.Errorf("Drift = missing %v, modified %v, unexpected %v; want %v, %v, %v",
			got.Missing, got.Modified, got.Unexpected, missing, modified, unexpected)
	}
	if want := len(missing) + len(modified) + len(unexpected); got.Count() != want || got.HasDiff() != (want > 0) {
		t.Errorf("Count() = %d, HasDiff() = %v; want %d, %v", got.Count(), got.HasDiff(), want, want > 0)
	}
}

// Ported: RoundTrip_CleanZeroDifferences. A tree Install wrote is clean,
// which proves the walk and the installer agree on the prefix strip and the
// PROVENANCE.md exemption.
func TestDriftRoundTripIsClean(t *testing.T) {
	t.Parallel()
	got := mustDrift(t, installedDrift(t))
	assertDrift(t, got, nil, nil, nil)
	if got.Total != 16 || got.Undecidable != "" {
		t.Errorf("Total = %d, Undecidable = %q; want 16, \"\"", got.Total, got.Undecidable)
	}
}

// dir "." walks the whole tree.
func TestDriftWholeTree(t *testing.T) {
	t.Parallel()
	tree := fstest.MapFS{"SKILL.md": {Data: []byte("x\n")}, "ref/a.md": {Data: []byte("a\n")}}
	target := t.TempDir()
	if _, err := agentdocs.Install(tree, ".", target, tool, false); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(target, "ref", "a.md"), "edited\n")
	got, err := agentdocs.Drift(tree, ".", target)
	if err != nil {
		t.Fatal(err)
	}
	assertDrift(t, got, nil, []string{"ref/a.md"}, nil)
}

// Ported: LiveDefectReplayed. One file removed and two behind, seeded on disk.
func TestDriftLiveDefectReplayed(t *testing.T) {
	t.Parallel()
	target := installedDrift(t)
	if err := os.Remove(filepath.Join(target, "reference", "commands.md")); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"loops.md", "SKILL.md"} {
		mustWrite(t, filepath.Join(target, rel), "stale content, behind the embed\n")
	}
	assertDrift(t, mustDrift(t, target), []string{"reference/commands.md"}, []string{"SKILL.md", "loops.md"}, nil)
}

// Ported: ModifiedAlone. Same name, one flipped byte.
func TestDriftModifiedAlone(t *testing.T) {
	t.Parallel()
	target := installedDrift(t)
	p := filepath.Join(target, "SKILL.md")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	b[0] ^= 0xFF
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	assertDrift(t, mustDrift(t, target), nil, []string{"SKILL.md"}, nil)
}

// Ported: UnexpectedFile. Only the ROOT PROVENANCE.md is exempt; a
// reference/PROVENANCE.md is an unexpected file like any other.
func TestDriftUnexpectedFile(t *testing.T) {
	t.Parallel()
	t.Run("root PROVENANCE.md is exempt", func(t *testing.T) {
		t.Parallel()
		assertDrift(t, mustDrift(t, installedDrift(t)), nil, nil, nil)
	})
	t.Run("reference/PROVENANCE.md is not", func(t *testing.T) {
		t.Parallel()
		target := installedDrift(t)
		mustWrite(t, filepath.Join(target, "reference", "PROVENANCE.md"), "not exempt\n")
		mustWrite(t, filepath.Join(target, "extra.md"), "mine\n")
		assertDrift(t, mustDrift(t, target), nil, nil, []string{"extra.md", "reference/PROVENANCE.md"})
	})
}

// Ported: HandEditedOwnedTreeFails. A hand edit is a modification; that the
// caller turns it into a failing row on a Current install is the caller's.
func TestDriftHandEditedOwnedTree(t *testing.T) {
	t.Parallel()
	target := installedDrift(t)
	mustWrite(t, filepath.Join(target, "SKILL.md"), "hand-edited, ignoring the do-not-edit notice\n")
	got := mustDrift(t, target)
	assertDrift(t, got, nil, []string{"SKILL.md"}, nil)
	if st := agentdocs.Installed(target, tool); st.State != agentdocs.StateCurrent {
		t.Fatalf("Installed = %v; the stamp still names this binary, which is what makes the drift a contradiction", st.State)
	}
}

// Ported: OutputStability. Twelve differing files come back sorted, and two
// runs are identical (map iteration must not leak into the result).
func TestDriftOutputStability(t *testing.T) {
	t.Parallel()
	target := installedDrift(t)
	var want []string
	for i := range 12 {
		rel := fmt.Sprintf("reference/page%02d.md", i)
		want = append(want, rel)
		mustWrite(t, filepath.Join(target, filepath.FromSlash(rel)), "modified for output-stability test\n")
	}
	first, second := mustDrift(t, target), mustDrift(t, target)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("two consecutive runs differ:\n%+v\n%+v", first, second)
	}
	assertDrift(t, first, nil, want, nil)
}

// Ported: Undecidable. A disk file that is present but cannot be read is
// undecidable, named, and never classified; the walk stops there. A dangling
// symlink stands in for the unreadable file, so the test needs no
// permission bits and holds when run as root.
func TestDriftUndecidable(t *testing.T) {
	t.Parallel()
	target := installedDrift(t)
	// A file the walk reaches first is modified, so a result that kept
	// classifying past the undecidable one would show it.
	mustWrite(t, filepath.Join(target, "SKILL.md"), "edited\n")
	p := filepath.Join(target, "reference", "page05.md")
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(target, "does-not-exist"), p); err != nil {
		t.Fatal(err)
	}
	got := mustDrift(t, target)
	if got.Undecidable != "reference/page05.md" {
		t.Fatalf("Undecidable = %q, want reference/page05.md", got.Undecidable)
	}
	if got.Total == 0 || got.Total >= 16 {
		t.Errorf("Total = %d; want the files reached before the undecidable one", got.Total)
	}
	if got.HasDiff() {
		t.Errorf("an undecidable walk classified files: %+v", got)
	}
}

// A target that cannot be walked at all is undecidable at its root.
func TestDriftMissingTargetIsUndecidable(t *testing.T) {
	t.Parallel()
	got := mustDrift(t, filepath.Join(t.TempDir(), "absent"))
	if got.Undecidable != "." || got.HasDiff() {
		t.Fatalf("Drift(absent) = %+v, want Undecidable \".\" and nothing classified", got)
	}
}

// The error is reserved for the tree side: a dir the tree does not hold.
func TestDriftUnknownDirIsAnError(t *testing.T) {
	t.Parallel()
	_, err := agentdocs.Drift(driftTree(), "no-such-dir", installedDrift(t))
	if !errors.Is(err, agentdocs.ErrTreeMissing) {
		t.Fatalf("Drift(unknown dir) = %v, want ErrTreeMissing", err)
	}
}

// Every class comes back sorted by path, not in walk or map order. The two
// orders differ where a directory and a file share a stem: the walk visits
// "reference/x.md" before "reference.md", and the sort puts it after.
func TestDriftSortsEveryClass(t *testing.T) {
	t.Parallel()
	tree := fstest.MapFS{}
	for _, stem := range []string{"a", "b"} {
		tree["d/"+stem+"/x.md"] = &fstest.MapFile{Data: []byte("x\n")}
		tree["d/"+stem+".md"] = &fstest.MapFile{Data: []byte("y\n")}
	}
	target := t.TempDir()
	if _, err := agentdocs.Install(tree, "d", target, tool, false); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"a.md", "a/x.md"} {
		mustWrite(t, filepath.Join(target, filepath.FromSlash(rel)), "edited\n")
	}
	for _, rel := range []string{"b.md", "b/x.md"} {
		if err := os.Remove(filepath.Join(target, filepath.FromSlash(rel))); err != nil {
			t.Fatal(err)
		}
	}
	var unexpected []string
	for i := range 12 {
		rel := fmt.Sprintf("extra%02d.md", i)
		unexpected = append(unexpected, rel)
		mustWrite(t, filepath.Join(target, rel), "mine\n")
	}
	got, err := agentdocs.Drift(tree, "d", target)
	if err != nil {
		t.Fatal(err)
	}
	assertDrift(t, got, []string{"b.md", "b/x.md"}, []string{"a.md", "a/x.md"}, unexpected)
}
