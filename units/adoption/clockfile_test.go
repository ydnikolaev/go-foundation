package adoption

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestClockFileAgainstRealGit runs the probe once per answer: its exit-code
// reading is the part a fake cannot prove.
func TestClockFileAgainstRealGit(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()
	outside := t.TempDir()
	if _, err := ClockFile(ctx, outside, filepath.Join(outside, "c.yaml")); !errors.Is(err, ErrNotAGitRepo) {
		// A temp dir nested in some work tree would make this answer differ.
		if err == nil {
			t.Skip("the temp dir is inside a git work tree")
		}
		t.Fatalf("outside a work tree: %v, want ErrNotAGitRepo", err)
	}

	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "user.name=t", "-c", "user.email=t@t", "-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	clock := filepath.Join(root, ".tool", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(clock), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(clock, []byte("adapted_through: 0.1.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// No commit yet: untracked is owed at the first commit, not broken.
	if got, err := ClockFile(ctx, root, clock); err != nil || got.Tracked || got.IgnoredBy != "" || !got.Unborn || got.Severity() != SeverityWarn {
		t.Fatalf("untracked before the first commit: %+v, %v; want Unborn and a warning", got, err)
	}
	ignore := filepath.Join(root, ".gitignore")
	if err := os.WriteFile(ignore, []byte("/.tool/*\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// An ignore rule survives the first commit, so it fails even now.
	got, err := ClockFile(ctx, root, clock)
	if err != nil || !strings.HasPrefix(got.IgnoredBy, ".gitignore:1:") || !got.Unborn || got.Severity() != SeverityFail {
		t.Fatalf("ignored before the first commit: %+v, %v; want the rule .gitignore:1: and a failure", got, err)
	}
	git("add", "--", ignore)
	git("commit", "-q", "-m", "first")
	got, err = ClockFile(ctx, root, clock)
	if err != nil || got.Unborn || !strings.HasPrefix(got.IgnoredBy, ".gitignore:1:") || got.Severity() != SeverityFail {
		t.Fatalf("ignored after the first commit: %+v, %v", got, err)
	}
	if err := os.Remove(ignore); err != nil {
		t.Fatal(err)
	}
	if got, err := ClockFile(ctx, root, clock); err != nil || got.Unborn || got.IgnoredBy != "" || got.Severity() != SeverityFail {
		t.Fatalf("untracked after the first commit: %+v, %v; want a failure", got, err)
	}
	git("add", "--", clock)
	if got, err := ClockFile(ctx, root, clock); err != nil || !got.Tracked || got.Severity() != SeverityOK {
		t.Fatalf("tracked: %+v, %v", got, err)
	}
}
