package adoption

import (
	"context"
	"errors"
	"os/exec"
)

// DetectRunner runs one detect command and reports whether the obligation
// still fires (the command ran and exited non-zero). A non-nil err means the
// command could not run at all, and must never read as "still fires":
// "could not measure" and "measured and owed" are different facts.
type DetectRunner func(ctx context.Context, command string) (fired bool, err error)

// DefaultDetectRunner runs command through sh -c. Detects are one-liners with
// pipes and tests, authored by the tool's maintainers and embedded in its
// binary at build time; they are never content from outside the binary.
func DefaultDetectRunner(ctx context.Context, command string) (bool, error) {
	err := exec.CommandContext(ctx, "sh", "-c", command).Run() //nolint:gosec // reason: command is a maintainer-authored detect embedded in the calling tool's binary, not external input.
	if err == nil {
		return false, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return true, nil
	}
	return false, err
}

// DetectResult names the first item whose detect still fires or could not run.
type DetectResult struct {
	Item    PendingItem
	Command string
	Fired   bool
	Err     error
}

// CheckDetects runs every detect across items in order and stops at the first
// that fires or cannot run; nil means every detect ran and exited zero. A
// record verb refuses to move the clock while it returns non-nil.
func CheckDetects(ctx context.Context, items []PendingItem, run DetectRunner) *DetectResult {
	if run == nil {
		run = DefaultDetectRunner
	}
	for _, item := range items {
		for _, cmd := range item.Change.Action.Detect {
			fired, err := run(ctx, cmd)
			if err != nil {
				return &DetectResult{Item: item, Command: cmd, Err: err}
			}
			if fired {
				return &DetectResult{Item: item, Command: cmd, Fired: true}
			}
		}
	}
	return nil
}
