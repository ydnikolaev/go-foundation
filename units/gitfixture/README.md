# gitfixture

Make every git a test spawns die with the test that spawned it. git runs background maintenance (`gc --auto`) inside a
fixture repo and is still writing to `.git/objects` when `t.TempDir`'s cleanup removes the tree, so the test fails with
`unlinkat .../.git/objects: directory not empty`: a teardown race, not a failed assertion. `Args` fixes the spawn a
fixture helper makes, `HardenEnv` the spawn the code under test makes, and `Hygiene` fails the build on a spawn that
used neither. Standard library only.

## Install

```sh
go get github.com/ydnikolaev/go-foundation/units/gitfixture@v0.1.0
```

go-foundation is public: no GOPRIVATE, no token, no vendoring. It needs `git` on the PATH only where a test runs it.

## Wire

1. In every test helper that runs git, put the subcommand arguments through `Args`:
   `exec.Command("git", gitfixture.Args("commit", "-m", "seed")...)`. The flags land before the subcommand.
2. In a package whose production code spawns git while under test (a goroutine that clones or fetches), call
   `gitfixture.HardenEnv()` first in `TestMain`. git reads `GIT_CONFIG_COUNT` and `GIT_CONFIG_KEY_n` /
   `GIT_CONFIG_VALUE_n`, and every child inherits them, git's own `gc --auto` included.
3. Add one test that scans the repo: `gitfixture.Hygiene(t, root, allow...)`, with `root` the module root (for example
   from `runtime.Caller` or `go list -m -f '{{.Dir}}'`) and `allow` the paths where a raw spawn is meant. It reports
   `file:line` for each `exec.Command("git", ...)` or `exec.CommandContext(ctx, "git", ...)` that is not routed through
   `gitfixture.Args`.

## Configure

`Hygiene(t, root, allow ...string)`: `allow` lists slash-separated paths relative to `root`, each a file or a directory
(a directory allows every file under it). Directories named `testdata`, `vendor` and `node_modules` and any directory
starting with a dot are skipped. A `root` with no `.go` file fails too, so a moved tree cannot make the gate pass
vacuously. Nothing else is configurable: the flags are `gc.auto=0` and `maintenance.auto=false`.

## API

<!-- api: devkit foundation api; do not edit -->
```go
FUNCTIONS
func Args(args ...string) []string
func HardenEnv()
func Hygiene(t testing.TB, root string, allow ...string)
```

## Example

```go
package repo_test

import (
	"os"
	"os/exec"
	"testing"

	"github.com/ydnikolaev/go-foundation/units/gitfixture"
)

func TestMain(m *testing.M) {
	gitfixture.HardenEnv() // a git the code under test starts is hardened too
	os.Exit(m.Run())
}

func TestCommit(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q", dir}, {"-C", dir, "commit", "--allow-empty", "-q", "-m", "seed"}} {
		if out, err := exec.Command("git", gitfixture.Args(args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func TestNoRawGit(t *testing.T) {
	gitfixture.Hygiene(t, "..", "internal/githost") // the one place a raw spawn is meant
}
```

## Extension points

- `Hygiene` takes a `testing.TB`, so a test of your own wrapper can pass a recorder and assert what it reports.
- `allow` is the one exemption list: a package that spawns git on purpose, and nothing else, goes there.

## Do not

- Do not put `Args` after the subcommand: git reads `-c` only before it, and `Args` is meant to be the first argument.
- Do not pass a repo's own `-c user.name` or identity through `HardenEnv`: it sets maintenance only, so a repo's own
  config stays in force.
- Do not allow a test that merely forgot `Args`: fix the call. `allow` is for a spawn that must stay raw.
- Do not expect `Hygiene` to see a spawn through a variable or a wrapper: it reads literal `"git"` arguments of
  `exec.Command` and `exec.CommandContext`.
- Do not copy this package. Extend it here; a copy carries `go-foundation-copy: gitfixture@<sha>` and a removal row.
