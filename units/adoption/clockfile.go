package adoption

import (
	"context"
	"errors"
	"os/exec"
	"strings"
)

// ClockFileStatus is what git says about the file that holds the clock.
// IgnoredBy is the rule as `git check-ignore -v` prints it,
// "<source>:<line>:<pattern>", when the file is untracked and ignored.
type ClockFileStatus struct {
	Tracked   bool
	IgnoredBy string
}

// ClockFile asks git, in two read-only calls, whether path is tracked in the
// work tree at root and, when it is not, which rule ignores it. A doctor
// should fail on an ignored or untracked clock: it is machine-local, so every
// clone and worktree owes every step again. Outside a work tree it returns
// ErrNotAGitRepo.
func ClockFile(ctx context.Context, root, path string) (ClockFileStatus, error) {
	err := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "--error-unmatch", "--", path).Run()
	if err == nil {
		return ClockFileStatus{Tracked: true}, nil
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return ClockFileStatus{}, err
	}
	if exit.ExitCode() == 128 {
		return ClockFileStatus{}, ErrNotAGitRepo
	}
	out, err := exec.CommandContext(ctx, "git", "-C", root, "check-ignore", "-v", "--", path).Output()
	if err == nil {
		rule, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\t")
		return ClockFileStatus{IgnoredBy: rule}, nil
	}
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return ClockFileStatus{}, nil
	}
	return ClockFileStatus{}, err
}
