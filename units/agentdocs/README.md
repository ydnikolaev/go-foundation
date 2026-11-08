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
