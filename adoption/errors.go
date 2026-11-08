package adoption

import "errors"

var (
	// ErrReleaseInvalid is a release-notes document that is not valid YAML or
	// carries a malformed released value.
	ErrReleaseInvalid = errors.New("adoption: release notes document is invalid")
	// ErrStandingInvalid is a standing-issues document that is not valid YAML.
	ErrStandingInvalid = errors.New("adoption: standing issues document is not valid yaml")
	// ErrCorpusLoad is a corpus that could not be read or ordered.
	ErrCorpusLoad = errors.New("adoption: corpus failed to load")
	// ErrVersionInvalid is a version string that is not comparable.
	ErrVersionInvalid = errors.New("adoption: invalid version string")
	// ErrNoReleased is a corpus with no dated release.
	ErrNoReleased = errors.New("adoption: no dated release note")
	// ErrClockAheadOfBinary is a clock that names a release newer than the
	// running binary — almost always an older binary first in PATH. Adoption
	// never walks backwards; the caller names both versions.
	ErrClockAheadOfBinary = errors.New("adoption: the repository's clock is newer than the running binary")
	// ErrBinaryVersionUnusable is a binary version that cannot bound the walk
	// (a dev build, an empty stamp). Without this refusal an empty answer would
	// read as "nothing owed", a claim nothing measured.
	ErrBinaryVersionUnusable = errors.New("adoption: the running binary's version is not comparable, so the obligation range cannot be computed")
	// ErrNotAGitRepo is ClockFile's answer outside a git work tree.
	ErrNotAGitRepo = errors.New("adoption: not inside a git work tree")
)

// Error carries the operation and the input that failed.
type Error struct {
	Op    string
	Input string
	Err   error
}

func (e *Error) Error() string {
	if e.Input == "" {
		return "adoption: " + e.Op + ": " + e.Err.Error()
	}
	return "adoption: " + e.Op + ": " + e.Input + ": " + e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }
