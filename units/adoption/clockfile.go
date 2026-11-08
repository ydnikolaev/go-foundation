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
// Unborn is set on an untracked file when the repository has no commit yet:
// nothing can be committed alongside it, so a fresh repository is not yet
// wrong. Severity() turns the three facts into the doctor's answer.
type ClockFileStatus struct {
	Tracked   bool
	IgnoredBy string
	Unborn    bool
}

// Severity is a doctor's verdict on a ClockFileStatus.
type Severity int

const (
	// SeverityOK is a tracked clock file.
	SeverityOK Severity = iota
	// SeverityWarn is an untracked, unignored clock in a repository with no
	// commit yet: it is owed at the first commit, not broken.
	SeverityWarn
	// SeverityFail is an ignored clock, whatever the history (the rule will
	// still ignore it after the first commit), or an untracked one in a
	// repository that has commits.
	SeverityFail
)

// Severity says how a doctor should report s.
func (s ClockFileStatus) Severity() Severity {
	switch {
	case s.Tracked:
		return SeverityOK
	case s.IgnoredBy == "" && s.Unborn:
		return SeverityWarn
	default:
		return SeverityFail
	}
}

// ClockFile asks git, in read-only calls, whether path is tracked in the work
// tree at root and, when it is not, which rule ignores it and whether the
// repository has a commit yet. An untracked clock is machine-local, so every
// clone and worktree owes every step again; report it by Severity. Outside a
// work tree it returns ErrNotAGitRepo.
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
	var st ClockFileStatus
	switch err := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--verify", "-q", "HEAD").Run(); {
	case err == nil:
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		st.Unborn = true
	default:
		return ClockFileStatus{}, err
	}
	out, err := exec.CommandContext(ctx, "git", "-C", root, "check-ignore", "-v", "--", path).Output()
	if err == nil {
		st.IgnoredBy, _, _ = strings.Cut(strings.TrimSpace(string(out)), "\t")
		return st, nil
	}
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return st, nil
	}
	return ClockFileStatus{}, err
}
