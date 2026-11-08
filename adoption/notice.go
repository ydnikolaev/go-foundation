package adoption

import (
	"errors"
	"fmt"
)

// Verbs names the tool in the advisory. Tool is the binary's name ("a2a");
// Adapt and Done are the read and record commands ("a2a adapt",
// "a2a adapt --done").
type Verbs struct {
	Tool  string
	Adapt string
	Done  string
}

// Notice returns the one-line advisory a tool prints on stderr after every
// successful verb (never stdout, never the exit code), or "" when there is
// nothing to say. Behind: how many steps are owed and since when. Ahead: the
// clock names a newer release than this binary, which is almost always an
// older binary first in PATH, so it names execPath. Any answer Pending cannot
// give (a dev build, a broken corpus) is silent here; the tool's adapt and
// doctor verbs name those.
func Notice(all []Release, standing []Change, clock Clock, binaryVersion, execPath string, pol Policy, v Verbs) string {
	proj, err := Pending(all, standing, clock, binaryVersion, pol)
	if errors.Is(err, ErrClockAheadOfBinary) {
		return fmt.Sprintf("this repository adapted through v%s, newer than this binary (v%s at %s) — update %s, or check which %s comes first in PATH", clock.Through, binaryVersion, execPath, v.Tool, v.Tool)
	}
	if err != nil || len(proj.Items) == 0 {
		return ""
	}
	if proj.StartedFromOldest {
		return fmt.Sprintf("this repository has never recorded adoption: %d step(s) since v%s (this binary is v%s) — run `%s`, do or verify each, then `%s`", len(proj.Items), proj.Oldest, binaryVersion, v.Adapt, v.Done)
	}
	return fmt.Sprintf("this repository has %d adoption step(s) pending since v%s (this binary is v%s) — run `%s`, do them, then `%s`", len(proj.Items), clock.Through, binaryVersion, v.Adapt, v.Done)
}
