// Package gitfixture makes every git process a test spawns die with the test that spawned it.
//
// A test that shells out to git is one flake away from a red build it did not earn. git runs background maintenance
// (gc --auto) inside a fixture repo, and is still writing to .git/objects when t.TempDir's cleanup removes the same tree:
// the test fails with "unlinkat .../.git/objects: directory not empty", a teardown race and not an assertion. Two spawn
// paths need two mechanisms:
//
//  1. A fixture helper spawns git directly. Fix it at the argument list: Args puts the flags that turn git's own
//     background maintenance off before the subcommand.
//  2. Production code spawns git while under test (a goroutine that clones or fetches). No fixture argument list reaches
//     it, so fix it at the test binary's environment: git reads GIT_CONFIG_COUNT and GIT_CONFIG_KEY_n / GIT_CONFIG_VALUE_n,
//     and every child inherits them, including a git the code under test starts and git's own gc --auto child. HardenEnv
//     sets them; call it from TestMain.
//
// A third leak is not maintenance but discovery. A fixture directory with no .git of its own that sits under a real work
// tree (a go tmp dir kept inside the main checkout, a scratch dir inside a repo) makes git climb out of it, find the
// enclosing repository and read or write that one. HardenEnv also sets GIT_CEILING_DIRECTORIES so no git a test spawns
// can climb into the enclosing work tree or the temp dir.
//
// A test that runs t.Parallel cannot use HardenEnv's process-wide Setenv. Env is the same hardening, plus global-config
// and identity isolation, as a fresh environment slice for one spawn's cmd.Env.
//
// Hygiene is the gate that keeps both honest: it scans a tree and fails on every raw git spawn.
//
// Standard library only.
package gitfixture

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ceilingEnv is git's list of directories repository discovery will not chdir up into (git(1)).
const ceilingEnv = "GIT_CEILING_DIRECTORIES"

// gitConfig is the one list of settings that turn git's background maintenance off. Args spells it as -c flags and
// HardenEnv as GIT_CONFIG_* entries, so the two cannot drift.
var gitConfig = [][2]string{
	{"gc.auto", "0"},
	{"maintenance.auto", "false"},
}

// Args returns args prefixed with the flags that disable git's background maintenance for one invocation. The flags
// must come before the subcommand, so pass the result straight to the spawn:
//
//	exec.Command("git", gitfixture.Args("commit", "-m", "msg")...)
//
// The input is never mutated; the result is a fresh slice.
func Args(args ...string) []string {
	out := make([]string, 0, 2*len(gitConfig)+len(args))
	for _, kv := range gitConfig {
		out = append(out, "-c", kv[0]+"="+kv[1])
	}
	return append(out, args...)
}

// HardenEnv sets the GIT_CONFIG_COUNT / GIT_CONFIG_KEY_n / GIT_CONFIG_VALUE_n entries (git's documented environment
// config, see git-config(1)) on the current process, so every git spawned for the rest of the run, by the test or by
// the code under test, has background maintenance off. Call it first in TestMain.
//
// It composes: when GIT_CONFIG_COUNT is already N, the entries are appended at N and above and the count is raised, so a
// preset entry is left untouched. It is idempotent: a second call adds nothing, so TestMain in several packages need
// not coordinate.
//
// It also sets GIT_CEILING_DIRECTORIES, so repository discovery cannot climb out of a fixture. git(1): the variable is a
// list of directories git "should not chdir up into while looking for a repository directory"; it does not exclude the
// current working directory. Two directories go on the list, both symlink-resolved because git compares them with its own
// resolved cwd: the top of the work tree that holds the test's working directory (the nearest ancestor with a .git entry,
// found by walking up, no git spawned), and os.TempDir(). A fixture in t.TempDir, or in a tmp dir kept inside the main
// checkout, therefore never discovers the enclosing repository, while a fixture's own git init (its .git is in the
// fixture, below the ceiling) works as before. A preset GIT_CEILING_DIRECTORIES is kept as is and ours are appended;
// an entry already on the list is not added twice.
//
// The cost: a test that runs git in its package directory against the enclosing repository no longer finds it. Give such
// a test its own fixture repo, or run that git with GIT_CEILING_DIRECTORIES emptied.
func HardenEnv() {
	harden(processEnv{})
}

// Env returns base with the same hardening HardenEnv applies, plus the isolation a fixture repo wants, as a fresh slice;
// base and the process environment are never touched, so it is safe in a test that runs t.Parallel and cannot use
// HardenEnv's process-wide Setenv. Pass it to one spawn:
//
//	cmd := exec.Command("git", gitfixture.Args("commit", "-m", "seed")...)
//	cmd.Env = gitfixture.Env(os.Environ())
//
// It adds, on top of base (a variable it sets replaces the one in base):
//   - the maintenance-off entries as GIT_CONFIG_COUNT / KEY_n / VALUE_n, composed with a count already in base;
//   - the GIT_CEILING_DIRECTORIES rule of HardenEnv, composed with a list already in base;
//   - GIT_CONFIG_GLOBAL=/dev/null and GIT_CONFIG_NOSYSTEM=1, so no user or system config leaks into the fixture;
//   - GIT_AUTHOR_NAME / GIT_AUTHOR_EMAIL / GIT_COMMITTER_NAME / GIT_COMMITTER_EMAIL set to a fixed test identity, so a
//     commit needs no user.name or user.email.
//
// HardenEnv leaves the global config and the identity alone: it covers production code under test, whose own config is
// not the fixture's to replace.
func Env(base []string) []string {
	e := &sliceEnv{entries: append([]string(nil), base...)}
	harden(e)
	e.Set("GIT_CONFIG_GLOBAL", "/dev/null")
	e.Set("GIT_CONFIG_NOSYSTEM", "1")
	e.Set("GIT_AUTHOR_NAME", testName)
	e.Set("GIT_AUTHOR_EMAIL", testEmail)
	e.Set("GIT_COMMITTER_NAME", testName)
	e.Set("GIT_COMMITTER_EMAIL", testEmail)
	return e.entries
}

// The fixed identity Env gives a fixture commit.
const (
	testName  = "gitfixture"
	testEmail = "noreply@gitfixture.invalid"
)

// environ is the environment HardenEnv and Env write to: the process's, or a slice's.
type environ interface {
	Get(key string) string
	Set(key, value string)
}

// processEnv is the process environment. os.Setenv fails only on a malformed name, and every name set here is a constant
// or a constant prefix plus a decimal number, so the error is dropped.
type processEnv struct{}

func (processEnv) Get(key string) string { return os.Getenv(key) }
func (processEnv) Set(key, value string) { _ = os.Setenv(key, value) }

// sliceEnv is a KEY=VALUE slice with exec.Cmd.Env semantics: the last entry of a key wins.
type sliceEnv struct{ entries []string }

func (e *sliceEnv) Get(key string) string {
	for i := len(e.entries) - 1; i >= 0; i-- {
		if k, v, ok := strings.Cut(e.entries[i], "="); ok && k == key {
			return v
		}
	}
	return ""
}

// Set replaces the first entry of key in place and drops its duplicates, or appends one: a second pass over the same
// slice changes nothing, order included.
func (e *sliceEnv) Set(key, value string) {
	kept := e.entries[:0]
	placed := false
	for _, kv := range e.entries {
		if k, _, _ := strings.Cut(kv, "="); k != key {
			kept = append(kept, kv)
		} else if !placed {
			kept = append(kept, key+"="+value)
			placed = true
		}
	}
	if !placed {
		kept = append(kept, key+"="+value)
	}
	e.entries = kept
}

// harden writes the maintenance-off entries and the discovery ceilings to e.
func harden(e environ) {
	e.Set(ceilingEnv, mergeCeilings(e.Get(ceilingEnv), ceilings()))
	count := presetCount(e)
	if hasOwnEntries(e, count) {
		return
	}
	next := count
	for _, kv := range gitConfig {
		e.Set("GIT_CONFIG_KEY_"+strconv.Itoa(next), kv[0])
		e.Set("GIT_CONFIG_VALUE_"+strconv.Itoa(next), kv[1])
		next++
	}
	e.Set("GIT_CONFIG_COUNT", strconv.Itoa(next))
}

// presetCount reads GIT_CONFIG_COUNT. An absent, empty, non-numeric or negative value is 0: git itself rejects a bad
// count, so there is no valid state to keep.
func presetCount(e environ) int {
	n, err := strconv.Atoi(e.Get("GIT_CONFIG_COUNT"))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// hasOwnEntries reports whether every pair of gitConfig is already among the first count entries: the signature a
// previous HardenEnv leaves.
func hasOwnEntries(e environ, count int) bool {
	for _, want := range gitConfig {
		found := false
		for i := range count {
			key := e.Get(fmt.Sprintf("GIT_CONFIG_KEY_%d", i))
			val := e.Get(fmt.Sprintf("GIT_CONFIG_VALUE_%d", i))
			if key == want[0] && val == want[1] {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// ceilings returns the directories discovery must not climb into: the work tree top above the working directory, then the
// temp dir, both with symlinks resolved. A directory that cannot be resolved, or is the filesystem root, is left out.
func ceilings() []string {
	var out []string
	if wd, err := os.Getwd(); err == nil {
		if top := workTreeTop(wd); top != "" {
			out = append(out, top)
		}
	}
	if tmp := resolve(os.TempDir()); tmp != "" {
		out = append(out, tmp)
	}
	return out
}

// workTreeTop returns the nearest ancestor of dir, dir itself included, that holds a .git entry (a directory for a
// clone, a file for a linked work tree or a submodule), or "" when there is none.
func workTreeTop(dir string) string {
	dir = resolve(dir)
	for dir != "" {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
	return ""
}

// resolve returns the absolute, symlink-free form of path, or "" when it cannot be had or is the filesystem root: a
// ceiling at the root would only say what git already knows.
func resolve(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil || filepath.Dir(real) == real {
		return ""
	}
	return real
}

// mergeCeilings returns preset with every entry of add that it does not already list appended, joined by the platform's
// list separator (git's own PATH_SEP). The preset is kept verbatim, so a second call returns it unchanged.
func mergeCeilings(preset string, add []string) string {
	var entries []string
	if preset != "" {
		entries = strings.Split(preset, string(os.PathListSeparator))
	}
	for _, want := range add {
		found := false
		for _, have := range entries {
			if have == want {
				found = true
				break
			}
		}
		if !found {
			entries = append(entries, want)
		}
	}
	return strings.Join(entries, string(os.PathListSeparator))
}
