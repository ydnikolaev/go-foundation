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
// Hygiene is the gate that keeps both honest: it scans a tree and fails on every raw git spawn.
//
// Standard library only.
package gitfixture

import (
	"fmt"
	"os"
	"strconv"
)

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
func HardenEnv() {
	count := presetCount()
	if hasOwnEntries(count) {
		return
	}
	// os.Setenv fails only on a malformed name, and every name here is a constant prefix plus a decimal number.
	next := count
	for _, kv := range gitConfig {
		_ = os.Setenv("GIT_CONFIG_KEY_"+strconv.Itoa(next), kv[0])
		_ = os.Setenv("GIT_CONFIG_VALUE_"+strconv.Itoa(next), kv[1])
		next++
	}
	_ = os.Setenv("GIT_CONFIG_COUNT", strconv.Itoa(next))
}

// presetCount reads GIT_CONFIG_COUNT. An absent, empty, non-numeric or negative value is 0: git itself rejects a bad
// count, so there is no valid state to keep.
func presetCount() int {
	n, err := strconv.Atoi(os.Getenv("GIT_CONFIG_COUNT"))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// hasOwnEntries reports whether every pair of gitConfig is already among the first count entries: the signature a
// previous HardenEnv leaves.
func hasOwnEntries(count int) bool {
	for _, want := range gitConfig {
		found := false
		for i := range count {
			key := os.Getenv(fmt.Sprintf("GIT_CONFIG_KEY_%d", i))
			val := os.Getenv(fmt.Sprintf("GIT_CONFIG_VALUE_%d", i))
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
