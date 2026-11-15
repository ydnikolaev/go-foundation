# gitfixture

Make every git a test spawns die with the test that spawned it. git runs background maintenance (`gc --auto`) inside a
fixture repo and is still writing to `.git/objects` when `t.TempDir`'s cleanup removes the tree, so the test fails with
`unlinkat .../.git/objects: directory not empty`: a teardown race, not a failed assertion. `Args` fixes the spawn a
fixture helper makes, `HardenEnv` the spawn the code under test makes, and `Hygiene` fails the build on a spawn that
used neither. `HardenEnv` and `Env` also set `GIT_CEILING_DIRECTORIES`, so a fixture directory with no `.git` of its own
cannot discover the repository above it. Standard library only.

## Install

```sh
go get github.com/ydnikolaev/go-foundation/units/gitfixture@v0.2.0
```

go-foundation is public: no GOPRIVATE, no token, no vendoring. It needs `git` on the PATH only where a test runs it.

## Wire

1. In every test helper that runs git, put the subcommand arguments through `Args`:
   `exec.Command("git", gitfixture.Args("commit", "-m", "seed")...)`. The flags land before the subcommand.
2. In a package whose production code spawns git while under test (a goroutine that clones or fetches), call
   `gitfixture.HardenEnv()` first in `TestMain`. git reads `GIT_CONFIG_COUNT` and `GIT_CONFIG_KEY_n` /
   `GIT_CONFIG_VALUE_n`, and every child inherits them, git's own `gc --auto` included.
3. A test that runs `t.Parallel` cannot use `HardenEnv` (it sets the process environment). Give each spawn its own:
   `cmd.Env = gitfixture.Env(os.Environ())`. It returns a fresh slice, and adds global-config and identity isolation.
4. Add one test that scans the repo: `gitfixture.Hygiene(t, root, allow...)`, with `root` the module root (for example
   from `runtime.Caller` or `go list -m -f '{{.Dir}}'`) and `allow` the paths where a raw spawn is meant. It reports
   `file:line` for each `exec.Command("git", ...)` or `exec.CommandContext(ctx, "git", ...)` that is not routed through
   `gitfixture.Args`.

## The ceiling: a fixture cannot reach the repository above it

A fixture directory with no `.git` of its own that sits under a real work tree makes git climb out of it, find the
enclosing repository and read or write that one. It happens when the test temp dir lives inside the checkout (a
`GOTMPDIR` such as `<checkout>/.cache/go/tmp`) or a scratch dir is made under a repo: `git add` or `git commit` in the
fixture lands in the checkout.

`HardenEnv` and `Env` set `GIT_CEILING_DIRECTORIES`, which git(1) describes as the directories git "should not chdir up
into while looking for a repository directory"; it does not exclude the current working directory. Two entries go on the
list, symlink-resolved because git compares them with its own resolved working directory:

- the top of the work tree that holds the test's working directory (the nearest ancestor with a `.git` entry, found by
  walking up, no git spawned), so nothing under the checkout reaches the checkout;
- `os.TempDir()`, so a fixture under the temp dir reaches nothing above it.

A fixture's own `git init` keeps working: its `.git` is in the fixture, below the ceiling. A preset
`GIT_CEILING_DIRECTORIES` is kept and ours are appended after it, each once (the platform list separator, `:` on Unix).

Behaviour to know: a test that deliberately runs git in its package directory against the enclosing repository no longer
finds it, because that repository's top is now a ceiling. Give such a test a fixture repo of its own, or run that one git
with `GIT_CEILING_DIRECTORIES=` emptied. A `GIT_DIR` in the environment is not affected by any ceiling (git(1)).

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
func Env(base []string) []string
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

// A parallel test sets the environment on its own spawn: Env never touches the process.
func TestParallelCommit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"commit", "--allow-empty", "-q", "-m", "seed"}} {
		cmd := exec.Command("git", gitfixture.Args(args...)...)
		cmd.Dir = dir
		cmd.Env = gitfixture.Env(os.Environ()) // maintenance off, ceiling, no user config, a fixed identity
		if out, err := cmd.CombinedOutput(); err != nil {
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
- Do not call `HardenEnv` from a test that runs `t.Parallel`: it writes the process environment. Use `Env` per spawn.
- Do not expect `HardenEnv` to isolate the global config or set an identity: it covers production code under test, whose
  config is not the fixture's to replace. `Env` does, for a spawn a fixture helper makes.
- Do not pass a repo's own `-c user.name` or identity through `HardenEnv`: it sets maintenance only, so a repo's own
  config stays in force.
- Do not allow a test that merely forgot `Args`: fix the call. `allow` is for a spawn that must stay raw.
- Do not expect `Hygiene` to see a spawn through a variable or a wrapper: it reads literal `"git"` arguments of
  `exec.Command` and `exec.CommandContext`.
- Do not copy this package. Extend it here; a copy carries `go-foundation-copy: gitfixture@<sha>` and a removal row.
