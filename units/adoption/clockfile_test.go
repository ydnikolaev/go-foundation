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

	if got, err := ClockFile(ctx, root, clock); err != nil || got.Tracked || got.IgnoredBy != "" {
		t.Fatalf("untracked: %+v, %v", got, err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("/.tool/*\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ClockFile(ctx, root, clock)
	if err != nil || !strings.HasPrefix(got.IgnoredBy, ".gitignore:1:") {
		t.Fatalf("ignored: %+v, %v; want the rule .gitignore:1:", got, err)
	}
	git("add", "-f", "--", clock)
	if got, err := ClockFile(ctx, root, clock); err != nil || !got.Tracked {
		t.Fatalf("tracked: %+v, %v", got, err)
	}
}
