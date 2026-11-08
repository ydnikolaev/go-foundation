package adoption

import (
	"context"
	"errors"
	"os/exec"
	"regexp"
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

// templatePlaceholder matches a `<word ...>` placeholder: a value the reader
// was meant to fill in, as in `tool submit <id>` or `<XC-id>`. It ends on a
// non-space before `>`, so `cat <<EOF > out` is not one. A shell reads `<id>`
// as a redirect from a file named "id" and fails, and that failure would read
// as "still fires" for an obligation nobody can ever discharge.
var templatePlaceholder = regexp.MustCompile(`<[A-Za-z](?:[^<>]*[^<>\s])?>`)

// IsTemplateDetect reports whether a detect is a template for the reader to
// fill in rather than a probe a tool can run. It proves nothing either way, so
// CheckDetects never runs it, and a record verb records its obligation on the
// agent's word, as for an obligation with no detect at all.
func IsTemplateDetect(command string) bool {
	return templatePlaceholder.MatchString(command)
}

// TemplateOnly returns the ids of items that carry detects, none of them
// runnable (every one IsTemplateDetect). A record verb names them, so that
// "verified" never counts an obligation no command checked.
func TemplateOnly(items []PendingItem) []string {
	var ids []string
	for _, item := range items {
		detects := item.Change.Action.Detect
		if len(detects) == 0 {
			continue
		}
		runnable := false
		for _, cmd := range detects {
			if !IsTemplateDetect(cmd) {
				runnable = true
				break
			}
		}
		if !runnable {
			ids = append(ids, item.Change.ID)
		}
	}
	return ids
}

// DetectResult names the first item whose detect still fires or could not run.
type DetectResult struct {
	Item    PendingItem
	Command string
	Fired   bool
	Err     error
}

// CheckDetects runs every detect across items in order, skipping a template
// (IsTemplateDetect), and stops at the first that fires or cannot run; nil means every detect ran and exited zero. A
// record verb refuses to move the clock while it returns non-nil.
func CheckDetects(ctx context.Context, items []PendingItem, run DetectRunner) *DetectResult {
	if run == nil {
		run = DefaultDetectRunner
	}
	for _, item := range items {
		for _, cmd := range item.Change.Action.Detect {
			if IsTemplateDetect(cmd) {
				continue
			}
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
