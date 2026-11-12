# adoption

What a tool's releases still ask of the repository that uses the tool, and the record that it was done.
The binary is per machine and adoption is per repository, so a tool that adopts only inside its update verb leaves every
other repository on the machine unadopted. This unit lets every verb ask at use.

## Install

```sh
go get github.com/ydnikolaev/go-foundation/units/adoption@v0.3.1
```

go-foundation is public (R-6): no GOPRIVATE, no token, no vendoring.

## Wire

1. Embed your corpus: one YAML per release (`version`, `released`: a date or `unreleased`, `changes[]` with `action`).
2. Store the `adoption.Clock` (`adapted_through`, `adapted_ahead`) in a file the repository COMMITS.
3. Read verb: `adoption.Pending(all, standing, clock, binaryVersion, policy)`; sort items by `Group`, then `ImpactOrder`.
   A change whose `Actor` is `adoption.ActorOwner` needs a person (a credential, a setting, a decision): show it as
   a hand-over to the owner, never as a step the session runs. An empty `Actor` is an agent step.
4. Record verb: refuse while `adoption.CheckDetects(ctx, proj.Items, nil)` is non-nil; then set `Through` to the binary
   version and append `proj.AuthoredAhead` to `Ahead`.
   A template detect (`tool submit <id>`) is never run; `adoption.TemplateOnly(proj.Items)` names the obligations that
   carry only templates, so a "verified" count can leave them out.
5. After every successful verb: print `adoption.Notice(...)` on stderr when it is not empty. Never stdout, never the exit code.
6. Doctor: report `adoption.ClockFile(...).Severity()`. `SeverityWarn` is an untracked clock in a repository with no
   commit yet (owed at the first commit); `SeverityFail` is an ignored clock (quote `IgnoredBy`) or an untracked one
   once the repository has commits.

## Configure

- `Policy.PartyScopes`: the scope values that oblige another party (a2a: `space`). Unnamed scopes oblige nothing.
- `Verbs`: your binary name and your read and record commands, as the notice prints them.
- `Load(fsys, pattern)` and `LoadStanding(fsys, path)`: where your corpus lives.

## API

<!-- api: devkit foundation api; do not edit -->
```go
CONSTANTS
const (
	ScopeNone  = "none"
	ScopeLocal = "local"
)
const (
	ActorAgent = "agent"
	ActorOwner = "owner"
)
const KindKnownIssue = "known-issue"
const Unreleased = "unreleased"
VARIABLES
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
FUNCTIONS
func DefaultDetectRunner(ctx context.Context, command string) (bool, error)
func ImpactOrder(impact string) int
func IsTemplateDetect(command string) bool
func LastReleased(all []Release) (string, error)
func Notice(all []Release, standing []Change, clock Clock, binaryVersion, execPath string, pol Policy, v Verbs) string
func OlderThan(a, b string) (bool, error)
func TemplateOnly(items []PendingItem) []string
func ValidateClock(c Clock, binaryVersion string) error
TYPES
type Action struct {
	Scope string `yaml:"scope" json:"scope"`
	Why   string `yaml:"why" json:"why"`
	// Detect commands exit 0 while the obligation is satisfied and non-zero
	// while it is still owed. A dry-run that exits 0 with work pending is not
	// a detect.
	Detect []string `yaml:"detect,omitempty" json:"detect,omitempty"`
	Run    []string `yaml:"run,omitempty" json:"run,omitempty"`
}
type Change struct {
	ID      string   `yaml:"id" json:"id"`
	Kind    string   `yaml:"kind" json:"kind"`
	Impact  string   `yaml:"impact" json:"impact"`
	Subject string   `yaml:"subject" json:"subject"`
	Detail  string   `yaml:"detail" json:"detail"`
	Affects []string `yaml:"affects,omitempty" json:"affects,omitempty"`
	// Actor is ActorAgent, ActorOwner or empty; Parse refuses anything else.
	Actor  string `yaml:"actor,omitempty" json:"actor,omitempty"`
	Action Action `yaml:"action" json:"action"`
}
func LoadStanding(fsys fs.FS, path string) ([]Change, error)
type Clock struct {
	Through string   `yaml:"adapted_through,omitempty" json:"adapted_through,omitempty"`
	Ahead   []string `yaml:"adapted_ahead,omitempty" json:"adapted_ahead,omitempty"`
}
type ClockFileStatus struct {
	Tracked   bool
	IgnoredBy string
	Unborn    bool
}
func ClockFile(ctx context.Context, root, path string) (ClockFileStatus, error)
func (s ClockFileStatus) Severity() Severity
type DetectResult struct {
	Item    PendingItem
	Command string
	Fired   bool
	Err     error
}
func CheckDetects(ctx context.Context, items []PendingItem, run DetectRunner) *DetectResult
type DetectRunner func(ctx context.Context, command string) (fired bool, err error)
type Error struct {
	Op    string
	Input string
	Err   error
}
func (e *Error) Error() string
func (e *Error) Unwrap() error
type Group int
const (
	// GroupLocalWithRun is a local obligation that carries a command.
	GroupLocalWithRun Group = iota
	// GroupLocalOther is a local obligation with a detect or prose only.
	GroupLocalOther
	// GroupParty is an obligation in a party scope: real, but it may need
	// someone else's access, so it never outranks unilateral work.
	GroupParty
	// GroupKnownIssue is a known issue that obliges something; kind decides
	// the group before scope does.
	GroupKnownIssue
)
type PendingItem struct {
	Version string
	Change  Change
	Group   Group
}
type Policy struct {
	PartyScopes []string
}
type Projection struct {
	Clock         Clock
	BinaryVersion string
	// Releases counts the releases in range plus the notes authored ahead,
	// before standing issues are attached.
	Releases int
	// StartedFromOldest is true when the clock was never recorded; the
	// caller must say so rather than present a guessed baseline.
	StartedFromOldest bool
	// Oldest is the oldest release in the corpus, set with StartedFromOldest.
	Oldest string
	// Items are the obligations in corpus order, not yet sorted by group.
	Items []PendingItem
	// AuthoredAhead is every obliging change id in a note newer than the
	// binary: the set a record verb adds to Clock.Ahead.
	AuthoredAhead []string
}
func Pending(all []Release, standing []Change, clock Clock, binaryVersion string, pol Policy) (Projection, error)
type Release struct {
	Schema   string   `yaml:"schema" json:"schema"`
	Version  string   `yaml:"version" json:"version"`
	Released string   `yaml:"released" json:"released"`
	Headline string   `yaml:"headline" json:"headline"`
	Changes  []Change `yaml:"changes" json:"changes"`
	// Raw is the exact bytes Parse was given, for a tool's schema validator.
	Raw []byte `yaml:"-" json:"-"`
}
func AttachStanding(selected, all []Release, issues []Change) []Release
func Exactly(all []Release, version string) (Release, bool)
func Load(fsys fs.FS, pattern string) ([]Release, error)
func Parse(raw []byte) (Release, error)
func Since(all []Release, from, upto string) []Release
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
type Verbs struct {
	Tool  string
	Adapt string
	Done  string
}
```

## Example

What a repository owes after a binary update (compiled and run as a test of this snippet):

```go
corpus := fstest.MapFS{"notes/0.2.0.yaml": {Data: []byte(`schema: mytool/releasenotes/v1
version: 0.2.0
released: 2026-02-01
headline: Config moved
changes:
  - id: MYTOOL-0200-1
    kind: breaking
    impact: high
    subject: The config file moved to .mytool/config.yaml
    detail: Move the old file.
    action:
      scope: local
      why: The tool no longer reads the old path.
      run: ["mytool migrate"]
`)}}
all, err := adoption.Load(corpus, "notes/*.yaml")
if err != nil {
	panic(err)
}
// The clock is read from the file the repository commits; empty means never recorded.
clock := adoption.Clock{Through: "0.1.0"}
proj, err := adoption.Pending(all, nil, clock, "0.2.0", adoption.Policy{})
if err != nil {
	panic(err)
}
for _, it := range proj.Items {
	fmt.Println(it.Version, it.Change.ID, it.Change.Action.Run) // 0.2.0 MYTOOL-0200-1 [mytool migrate]
}
```

## Extension points

- `DetectRunner`: replace the shell runner (tests, a sandbox).
- Schema validation is yours: `Release.Raw` keeps the exact bytes for your validator.

## Do not

- Do not infer a baseline for a never-recorded clock: a guessed baseline skips work it wrongly believes done.
- Do not write a detect that exits 0 while work is pending (a dry-run is not a detect).
- Do not keep the clock in a gitignored file.
- Do not copy this package. Extend it here; a copy carries `go-foundation-copy: adoption@<sha>` and a removal row.
