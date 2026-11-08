package adoption

import (
	"strings"

	"golang.org/x/mod/semver"
)

// canonical turns "0.27", "v0.27.1" or "0.28.0-rc.1" into the form
// golang.org/x/mod/semver compares, or fails.
func canonical(v string) (string, error) {
	s := strings.TrimSpace(v)
	if s == "" {
		return "", ErrVersionInvalid
	}
	if !strings.HasPrefix(s, "v") {
		s = "v" + s
	}
	if !semver.IsValid(s) {
		return "", ErrVersionInvalid
	}
	return s, nil
}

// OlderThan reports whether a is strictly older than b. Either side may omit
// the leading "v" and trailing zero components.
func OlderThan(a, b string) (bool, error) {
	ca, err := canonical(a)
	if err != nil {
		return false, err
	}
	cb, err := canonical(b)
	if err != nil {
		return false, err
	}
	return semver.Compare(ca, cb) < 0, nil
}
