package adoption

import (
	"fmt"
	"io/fs"
	"regexp"
	"sort"

	"gopkg.in/yaml.v3"
)

// ScopeNone and ScopeLocal are the two scope values every tool shares. A
// change with scope none, or with no action at all, obliges nothing. Any
// other scope is an obligation only when the tool names it as a party scope
// (Policy.PartyScopes): a positive match, never a default inclusion.
const (
	ScopeNone  = "none"
	ScopeLocal = "local"
)

// KindKnownIssue is the one Change.Kind the engine treats differently: it
// describes something broken right now, not something that changed. A
// standing issue that obliges something sorts after every other group.
const KindKnownIssue = "known-issue"

// Unreleased is the Released value of a note authored ahead of its tag. A
// build from a checkout embeds such notes while it is stamped with the last
// dated release, so the code they describe is in the binary.
const Unreleased = "unreleased"

// Action is what one change asks of the consuming repository.
type Action struct {
	Scope string `yaml:"scope" json:"scope"`
	Why   string `yaml:"why" json:"why"`
	// Detect commands exit 0 while the obligation is satisfied and non-zero
	// while it is still owed. A dry-run that exits 0 with work pending is not
	// a detect.
	Detect []string `yaml:"detect,omitempty" json:"detect,omitempty"`
	Run    []string `yaml:"run,omitempty" json:"run,omitempty"`
}

// Change is one entry in a release's changes list.
type Change struct {
	ID      string   `yaml:"id" json:"id"`
	Kind    string   `yaml:"kind" json:"kind"`
	Impact  string   `yaml:"impact" json:"impact"`
	Subject string   `yaml:"subject" json:"subject"`
	Detail  string   `yaml:"detail" json:"detail"`
	Affects []string `yaml:"affects,omitempty" json:"affects,omitempty"`
	Action  Action   `yaml:"action" json:"action"`
}

// Release is one version's release-notes document. Parsing is structural
// only; schema validity is the tool's own concern.
type Release struct {
	Schema   string   `yaml:"schema" json:"schema"`
	Version  string   `yaml:"version" json:"version"`
	Released string   `yaml:"released" json:"released"`
	Headline string   `yaml:"headline" json:"headline"`
	Changes  []Change `yaml:"changes" json:"changes"`

	// Raw is the exact bytes Parse was given, for a tool's schema validator.
	Raw []byte `yaml:"-" json:"-"`
}

var isoDate = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)

// Parse decodes one release-notes document. released must be empty, an ISO
// date or Unreleased.
func Parse(raw []byte) (Release, error) {
	const op = "Parse"
	var r Release
	if err := yaml.Unmarshal(raw, &r); err != nil {
		return Release{}, &Error{Op: op, Err: ErrReleaseInvalid}
	}
	if r.Released != "" && r.Released != Unreleased && !isoDate.MatchString(r.Released) {
		return Release{}, &Error{Op: op, Input: r.Version, Err: fmt.Errorf("%w: released: %q must be an ISO date (YYYY-MM-DD) or %q", ErrReleaseInvalid, r.Released, Unreleased)}
	}
	r.Raw = raw
	return r, nil
}

// Load parses every file of fsys matching pattern (for example "*.yaml") and
// returns them by version, oldest first. Any failure is an error; there is no
// partial result.
func Load(fsys fs.FS, pattern string) ([]Release, error) {
	const op = "Load"
	files, err := fs.Glob(fsys, pattern)
	if err != nil {
		return nil, &Error{Op: op, Err: fmt.Errorf("%w: %w", ErrCorpusLoad, err)}
	}
	out := make([]Release, 0, len(files))
	for _, f := range files {
		raw, err := fs.ReadFile(fsys, f)
		if err != nil {
			return nil, &Error{Op: op, Input: f, Err: fmt.Errorf("%w: %w", ErrCorpusLoad, err)}
		}
		r, err := Parse(raw)
		if err != nil {
			return nil, &Error{Op: op, Input: f, Err: err}
		}
		out = append(out, r)
	}
	var sortErr error
	sort.SliceStable(out, func(i, j int) bool {
		older, err := OlderThan(out[i].Version, out[j].Version)
		if err != nil && sortErr == nil {
			sortErr = fmt.Errorf("%q or %q: %w", out[i].Version, out[j].Version, err)
		}
		return older
	})
	if sortErr != nil {
		return nil, &Error{Op: op, Err: fmt.Errorf("%w: %w", ErrCorpusLoad, sortErr)}
	}
	return out, nil
}

type standingDocument struct {
	Schema string   `yaml:"schema"`
	Issues []Change `yaml:"issues"`
}

// LoadStanding reads the standing-issues document at path: limitations that
// hold now, whatever the release, under an `issues:` list.
func LoadStanding(fsys fs.FS, path string) ([]Change, error) {
	const op = "LoadStanding"
	raw, err := fs.ReadFile(fsys, path)
	if err != nil {
		return nil, &Error{Op: op, Input: path, Err: fmt.Errorf("%w: %w", ErrCorpusLoad, err)}
	}
	var doc standingDocument
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, &Error{Op: op, Input: path, Err: ErrStandingInvalid}
	}
	return append([]Change(nil), doc.Issues...), nil
}

// Since returns the releases strictly newer than from and no newer than upto;
// an empty bound is open. A release that cannot be compared is skipped, so a
// browse surface may use it; Pending refuses an unusable upper bound first.
func Since(all []Release, from, upto string) []Release {
	out := make([]Release, 0, len(all))
	for _, r := range all {
		if from != "" {
			newer, err := OlderThan(from, r.Version)
			if err != nil || !newer {
				continue
			}
		}
		if upto != "" {
			tooNew, err := OlderThan(upto, r.Version)
			if err != nil || tooNew {
				continue
			}
		}
		out = append(out, r)
	}
	return out
}

// Exactly returns the release with exactly this version string.
func Exactly(all []Release, version string) (Release, bool) {
	for _, r := range all {
		if r.Version == version {
			return r, true
		}
	}
	return Release{}, false
}

// LastReleased returns the newest version that carries a date rather than
// Unreleased: the stamp a build from a checkout gets.
func LastReleased(all []Release) (string, error) {
	const op = "LastReleased"
	best := ""
	for _, r := range all {
		if r.Released == Unreleased {
			continue
		}
		if _, err := canonical(r.Version); err != nil {
			return "", &Error{Op: op, Input: r.Version, Err: err}
		}
		if best == "" {
			best = r.Version
			continue
		}
		if older, _ := OlderThan(best, r.Version); older {
			best = r.Version
		}
	}
	if best == "" {
		return "", &Error{Op: op, Err: ErrNoReleased}
	}
	return best, nil
}

// AttachStanding appends the standing issues to the newest selected release,
// once each by id. An empty selection borrows the newest release of all as a
// carrier with no changes of its own, so a standing issue is never lost.
func AttachStanding(selected, all []Release, issues []Change) []Release {
	if len(issues) == 0 {
		return selected
	}
	out := append([]Release(nil), selected...)
	if len(out) == 0 {
		if len(all) == 0 {
			return out
		}
		carrier := all[len(all)-1]
		carrier.Changes = nil
		out = append(out, carrier)
	}
	last := len(out) - 1
	out[last].Changes = append([]Change(nil), out[last].Changes...)
	seen := make(map[string]struct{}, len(out[last].Changes))
	for _, ch := range out[last].Changes {
		seen[ch.ID] = struct{}{}
	}
	for _, issue := range issues {
		if _, dup := seen[issue.ID]; dup {
			continue
		}
		out[last].Changes = append(out[last].Changes, issue)
		seen[issue.ID] = struct{}{}
	}
	return out
}

var impactRank = map[string]int{"high": 0, "normal": 1, "low": 2}

// ImpactOrder ranks an impact for sorting within a group: high, normal, low,
// then anything else.
func ImpactOrder(impact string) int {
	if r, ok := impactRank[impact]; ok {
		return r
	}
	return len(impactRank)
}
