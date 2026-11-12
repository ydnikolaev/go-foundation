# agentdocs

A CLI's agent documentation, shipped inside its binary and put in front of the agent working in a consuming
repository: `<tool> docs <topic>` reads the embed, `<tool> skill install` mirrors it into the repository under a
provenance stamp, `<tool> skill link` points each agent runtime at it, a pointer block in AGENTS.md names it, and a
doctor row says when the installed copy drifted from the binary.

## Install

```sh
go get github.com/ydnikolaev/go-foundation/units/agentdocs@v0.2.0
```

go-foundation is public (R-6): no GOPRIVATE, no token, no vendoring.

## Wire

Embed the tree (no `all:` prefix) with a manifest at a path you choose:

```go
//go:embed mytool
var Docs embed.FS

var tool = agentdocs.Tool{ID: "mytool", Name: "mytool", Version: version, Comparable: semverOrEmpty(version),
	Source: "github.com/acme/mytool", Refresh: "mytool skill install"}
```

```json
{"schema": "agentdocs-manifest/v1", "groups": ["Start", "Reference"],
 "sections": [{"id": "pushing", "group": "Reference", "title": "Pushing", "file": "mytool/push.md", "verbs": ["env push"]}]}
```

Then the verbs, in cobra:

```go
m, err := agentdocs.LoadManifest(Docs, "mytool/docs-manifest.json") // a test asserts err == nil

docs := &cobra.Command{Use: "docs <topic>", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, a []string) error {
	page, err := m.Read(Docs, a[0]) // an unknown topic names every valid one
	if err == nil { _, err = c.OutOrStdout().Write(page) }
	return err
}}
install := &cobra.Command{Use: "install", RunE: func(c *cobra.Command, _ []string) error {
	_, err := agentdocs.Install(Docs, "mytool", root, ".mytool/skill", tool, force)
	if err == nil { _, err = agentdocs.UpsertPointer(root, "AGENTS.md", tool.ID, pointerBody) }
	return err // errors.Is(err, agentdocs.ErrForeignTarget): say "pass --force"
}}
link := &cobra.Command{Use: "link", RunE: func(c *cobra.Command, _ []string) error {
	for _, s := range agentdocs.Detect(root) {
		if _, err := agentdocs.Link(root, s, tool.ID, ".mytool/skill", force); err != nil { return err }
	}
	return nil
}}
// A usage error points at the page: agentdocs.WorkflowLine(tool, topic), topic from
// m.TopicFor(strings.TrimPrefix(cmd.CommandPath(), "mytool ")).
```

`root` is the repository root, and every path after it is relative to it. Nothing the unit touches leaves that
root, symlinks included: a CLAUDE.md that links to AGENTS.md is followed, one that links to `~/.bashrc` is refused
with an error naming it, and so is a path that climbs out with `..`.

Doctor rows: `st := agentdocs.Installed(root, ".mytool/skill", tool)`, report `st.Severity()` and
word it from `st.State` and `st.Version` (Behind and Ahead name both versions; Dev names the dev build). Then
`d, err := agentdocs.Drift(Docs, "mytool", root, ".mytool/skill")` for the tree itself. Its severity is yours; a2a's is:

- Current with `d.HasDiff()` is **Fail**: the stamp names this binary and the tree contradicts it (a hand edit,
  a partial copy). Name the counts and the first paths, and say the refresh overwrites local edits.
- Dev (a dev-build stamp) with a diff is advisory, never Fail: there is no version claim to contradict.
- Behind and Ahead do not walk the tree; the version line is the whole verdict.
- `d.Undecidable != ""` names a path that could not be read: advisory, named, never a pass or a fail.

## Configure

- `Tool.ID` is the marker namespace (`<!-- <ID>-skill-install -->`, `<!-- <ID>-skill-link -->`) and the `name`
  you pass to `Link` and `UpsertPointer`; `Tool.Name` is the binary, used in prose. They may differ: a2a's binary is
  `a2a` and its id is `a2ahub`. `Install` refuses an empty ID.
- `Tool.Comparable` is the only version `Installed` compares; leave it `""` on a dev build. `Tool.Version` is display
  only and lands in PROVENANCE.md.
- The manifest path, the tree's `dir` and the install target are yours.
- Who calls `Install` is yours: `init`, an explicit verb, or both.

## API

<!-- api: devkit foundation api; do not edit -->
```go
CONSTANTS
const ManifestSchema = "agentdocs-manifest/v1"
const ProvenanceFile = "PROVENANCE.md"
VARIABLES
var (
	// ErrManifestInvalid is a manifest that cannot be read or decoded, names
	// an unknown schema, declares no groups or no sections, or lists one
	// command path twice.
	ErrManifestInvalid = errors.New("docs manifest is invalid")
	// ErrUnknownTopic is a topic no section carries. The message names every
	// valid topic.
	ErrUnknownTopic = errors.New("no such topic")
	// ErrPageMissing is a section whose page the tree does not hold: a
	// corpus/binary mismatch, not a caller mistake.
	ErrPageMissing = errors.New("the manifest names a page the tree does not hold")
	// ErrTreeMissing is an Install whose dir is not a directory in the tree.
	// Nothing is written or removed.
	ErrTreeMissing = errors.New("the tree holds no such directory")
	// ErrToolID is an Install whose Tool has no ID, so the install marker
	// would have no namespace. Nothing is written or removed.
	ErrToolID = errors.New("tool has no ID; the install marker needs a namespace")
	// ErrForeignTarget is an Install target or a Link entry that exists, is not
	// empty, and is not this tool's. Nothing is written unless forced.
	ErrForeignTarget = errors.New("target exists and is not this tool's")
	// ErrInvalidName is a Link name that is not one path element.
	ErrInvalidName = errors.New("name must be one path element")
	// ErrPointerMalformed is a context file whose markers do not delimit
	// exactly one block, or a body that carries the markers itself. Nothing is
	// written.
	ErrPointerMalformed = errors.New("pointer markers do not delimit exactly one block")
)
FUNCTIONS
func Install(tree fs.FS, dir, root, target string, t Tool, force bool) (written int, err error)
func Stale(tree fs.FS, generated map[string][]byte) []string
func UpsertPointer(root, path, name, body string) (changed bool, err error)
func WorkflowLine(t Tool, topic string) string
TYPES
type Error struct {
	Op    string
	Input string
	Err   error
}
func (e *Error) Error() string
func (e *Error) Unwrap() error
type InstallStatus struct {
	State   State
	Version string
}
func Installed(root, target string, t Tool) InstallStatus
func (s InstallStatus) Severity() Severity
type LinkMode string
const (
	// LinkSymlink is a relative symlink to the installed tree.
	LinkSymlink LinkMode = "symlink"
	// LinkStub is a stub SKILL.md pointing at the installed tree, written
	// where a symlink cannot be created (Windows without Developer Mode). The
	// runtime fallback is the cross-platform mechanism; there is no build tag.
	LinkStub LinkMode = "stub"
)
type LinkResult struct {
	// Surface is the row the entry was installed for.
	Surface Surface
	// Path is the repo-relative entry, "<SkillsHome>/<name>".
	Path string
	// Mode is how it was installed.
	Mode LinkMode
}
func Link(root string, s Surface, name, target string, force bool) (LinkResult, error)
type Manifest struct {
	Schema   string    `json:"schema"`
	Groups   []string  `json:"groups"`
	Sections []Section `json:"sections"`
}
func LoadManifest(tree fs.FS, path string) (Manifest, error)
func (m Manifest) Read(tree fs.FS, topic string) ([]byte, error)
func (m Manifest) Resolve(topic string) (Section, bool)
func (m Manifest) TopicFor(path string) (string, bool)
func (m Manifest) Topics() []string
type Section struct {
	ID    string   `json:"id"`
	Group string   `json:"group"`
	Title string   `json:"title"`
	File  string   `json:"file"`
	Verbs []string `json:"verbs,omitempty"`
}
type Severity int
const (
	// SeverityOK needs nothing.
	SeverityOK Severity = iota
	// SeverityWarn is advisory: worth a line, never a failing row.
	SeverityWarn
	// SeverityFail is a failing row. No InstallStatus maps to it in this
	// version; it is defined so the scale matches adoption's.
	SeverityFail
)
type State int
const (
	// StateNone is no install: the target is absent, empty or unreadable.
	StateNone State = iota
	// StateForeign is a non-empty target without this tool's marker. It is
	// someone else's and is not judged.
	StateForeign
	// StateDev is this tool's install where either side has no comparable
	// version (a dev build on either side, a stamp that records none, or a
	// value that is not a version), so nothing can be compared.
	StateDev
	// StateBehind is an install older than the running binary.
	StateBehind
	// StateCurrent is an install of the running binary's version.
	StateCurrent
	// StateAhead is an install newer than the running binary: the binary is
	// older than the copy, and a reinstall would write an older tree.
	StateAhead
)
func (s State) String() string
type Surface struct {
	// ID is the stable identifier ("claude", "codex", "dsh").
	ID string
	// SkillsHome is the repo-relative directory the runtime scans for skills.
	SkillsHome string
	// ContextFile is the repo-relative file the runtime reads as always-on
	// context.
	ContextFile string
	// ReadsAgentsMD is true when the runtime reads AGENTS.md as always-on
	// context. Claude Code does not; it reads CLAUDE.md.
	ReadsAgentsMD bool
	// SourceURL is the page the row was read from.
	SourceURL string
	// VerifiedOn is the date (YYYY-MM-DD) the row was last verified there.
	VerifiedOn string
}
func ByID(id string) (Surface, bool)
func Detect(root string) []Surface
func Surfaces() []Surface
func (s Surface) MarkerDir() string
type Tool struct{ ID, Name, Version, Comparable, Source, Refresh string }
type TreeDrift struct {
	// Total is the number of tree files compared.
	Total int
	// Missing are tree files absent from the target.
	Missing []string
	// Modified are tree files whose bytes on disk differ.
	Modified []string
	// Unexpected are disk files the tree does not hold. The target's own
	// root PROVENANCE.md is the one exemption; a PROVENANCE.md anywhere
	// deeper is unexpected like any other file.
	Unexpected []string
	// Undecidable is the path the comparison could not judge, or "".
	Undecidable string
}
func Drift(tree fs.FS, dir, root, target string) (TreeDrift, error)
func (d TreeDrift) Count() int
func (d TreeDrift) HasDiff() bool
```

## Example

Read a topic from the embedded docs and install the tree into a repository (compiled and run as a test of this snippet):

```go
docs := fstest.MapFS{
	"mytool/docs-manifest.json": {Data: []byte(`{"schema": "agentdocs-manifest/v1", "groups": ["Start"],
 "sections": [{"id": "start", "group": "Start", "title": "Start", "file": "mytool/start.md", "verbs": ["init"]}]}`)},
	"mytool/start.md": {Data: []byte("# Start\n")},
}
tool := agentdocs.Tool{ID: "mytool", Name: "mytool", Version: "1.2.0", Comparable: "1.2.0",
	Source: "github.com/acme/mytool", Refresh: "mytool skill install"}

m, err := agentdocs.LoadManifest(docs, "mytool/docs-manifest.json")
if err != nil {
	panic(err)
}
page, err := m.Read(docs, "start") // what `mytool docs start` prints
if err != nil {
	panic(err)
}
fmt.Printf("%s", page)

root, _ := os.MkdirTemp("", "repo")
defer os.RemoveAll(root)
if _, err := agentdocs.Install(docs, "mytool", root, ".mytool/skill", tool, false); err != nil {
	panic(err)
}
fmt.Println(agentdocs.Installed(root, ".mytool/skill", tool).State) // current
```

## Extension points

- Every function takes an `fs.FS`: a test passes an `fstest.MapFS`, production the embed.
- `Stale(tree, generated)`: give it what your reference generator writes now (cobra/doc output) to fail a test or a
  doctor row on a committed copy that drifted.
- A new agent runtime is a new row in `Surfaces()`, verified against that runtime's current docs, made here.

## Do not

- Never pass a root wider than the repository (`/`, the home directory): the root is the containment, and a wider
  one lets a cloned repository's symlinks reach whatever it contains. A user-level install names its own root
  (`~/.mytool/skill`, target `"."`).
- Do not embed with `all:`: it ships whatever the build machine left untracked (a `.DS_Store`) to every repository.
- Do not compare `Tool.Version`; compare through `Installed`.
- Do not warn on `StateNone`: a repository that never ran your install verb owes nothing.
- Do not hand-edit an installed tree or the pointer block; refresh with the verb.
- Do not copy this package. Extend it here; a copy carries `go-foundation-copy: agentdocs@<sha>` and a removal row.
