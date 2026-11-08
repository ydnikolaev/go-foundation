# 01 — agentdocs API

Read from a2ahub `802fc17a`:
- `skill/manifest.go`: the tree and its manifest;
- `internal/cli/cmd_skill.go`: the install and its provenance marker;
- `internal/surface`: the provider rows and the link;
- `cmd_doctor.go` "skill manual current": the drift row;
- `cmd_init.go`: the pointer block;
- `cmd_docs.go` and `cli.go`: `docs <topic>` and the workflow line.

The first consumer, a private cobra CLI, reviewed this sketch; its answers are folded in below.

```go
type Tool struct{ ID, Name, Version, Comparable, Source, Refresh string } // "acme", "acme", "4.0.0 (local, 1a2b3c)", "4.0.0" ("" for a dev build), "example.com/acme", "acme skill install"

// Tree and topics
type Section struct{ ID, Group, Title, File string; Verbs []string } // Verbs: full command paths, "env push"
type Manifest struct{ Schema string; Groups []string; Sections []Section } // schema "agentdocs-manifest/v1"
func LoadManifest(tree fs.FS, path string) (Manifest, error)  // refuses an unknown schema or an empty manifest
func (m Manifest) Topics() []string
func (m Manifest) Resolve(topic string) (Section, bool)
func (m Manifest) TopicFor(path string) (string, bool)          // longest listed word-prefix of path wins
func (m Manifest) Read(tree fs.FS, topic string) ([]byte, error) // an unknown topic names the valid ones
func WorkflowLine(t Tool, topic string) string                   // "workflow: run `acme docs <topic>` for the walkthrough"

// Install, and the drift a doctor row reports
func Install(tree fs.FS, dir, target string, t Tool, force bool) (written int, err error) // ErrForeignTarget
type InstallStatus struct{ State State; Version string }        // None, Foreign, Dev, Behind, Current, Ahead
func Installed(target string, t Tool) InstallStatus              // .Severity(): OK, Warn, Fail, as adoption.ClockFile

// Agent surfaces
type Surface struct{ ID, SkillsHome, ContextFile string; ReadsAgentsMD bool; SourceURL, VerifiedOn string }
func Surfaces() []Surface                // claude, codex, dsh: a2a's verified rows
func Detect(root string) []Surface
func Link(root string, s Surface, name, target string, force bool) (LinkResult, error) // symlink, else a stub

// Context-file pointer
func UpsertPointer(path, name, body string) (changed bool, err error) // replaces only its own delimited block

// A generated file in the tree must match (for cobra/doc output and the like)
func Stale(tree fs.FS, generated map[string][]byte) []string
```


## Rules carried from a2a

- **Install** is a mirror. It refuses a non-empty target without the tool's marker unless forced, and wipes only a target that is its own. It writes only under the target. `PROVENANCE.md` opens with the marker `<!-- <Tool.ID>-skill-install -->` and names `Tool.Name Tool.Version`, `Tool.Source` and `Tool.Refresh`.
- **Installed** compares `Tool.Comparable` with the comparable version the stamp records; `Tool.Version` is display only and goes into `PROVENANCE.md` beside it. An empty Comparable on either side is Dev. The states:
  - Behind or Dev is Warn;
  - Foreign is not judged;
  - None is OK, with its own test: a tool whose install is an explicit verb must not warn on a repo that never ran it;
  - Ahead is Warn: the binary is older than the copy.
- **Tool.ID** is the marker namespace, separate from the binary name: a2a's binary is `a2a` and its tree, link and pointer are `a2ahub`. Install refuses an empty ID. The same id names the link marker `<!-- <ID>-skill-link -->` and is the `name` callers pass to Link and UpsertPointer.
- **A legacy stamp** without the comparable line (every a2a install before its switch) reads as Dev, so Warn: the next install heals it. No legacy parser: it would be custom code kept for one migration.
- **Link** tries a relative symlink first, then falls back to a stub `SKILL.md` carrying the tool's link marker. It refuses a foreign entry unless forced.
- **UpsertPointer** delimits its block with `<!-- <name>:pointer:start -->` and `<!-- <name>:pointer:end -->`. It replaces only that block, and it never touches the file outside it.
- **TopicFor** takes a command path without the root (cobra's `CommandPath()` minus its first word) and matches the longest listed word prefix: `env push x` tries `env push x`, then `env push`, then `env`. A parent covers its subcommands; a subcommand listed on its own topic wins. A path listed on two sections is refused by `LoadManifest`.
- **Read** of an unknown topic returns an error naming every valid topic.
- **Install** is a function, so whether `init` calls it or a verb does is the tool's choice. The first consumer installs on an explicit verb only (`acme skill install`, never on init), and its `acme docs <topic>` reads the embed uninstalled.

## Verification

- Every rule above has a test, ported from a2ahub's tests where they exist.
- Parity check: install a2ahub's embedded tree through the unit and through `a2a skill install` (in a temporary directory), then compare file sets and bytes. Only `PROVENANCE.md` may differ, and not in its marker line: `Installed` on a real a2a install must not be Foreign.
