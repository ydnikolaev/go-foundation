package gitfixture

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestArgs_PrependsFlagsBeforeSubcommand(t *testing.T) {
	t.Parallel()

	in := []string{"commit", "-m", "msg"}
	snapshot := append([]string(nil), in...)

	got := Args(in...)

	want := []string{"-c", "gc.auto=0", "-c", "maintenance.auto=false", "commit", "-m", "msg"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Args(%v) = %v, want %v", in, got, want)
	}
	if !reflect.DeepEqual(in, snapshot) {
		t.Fatalf("Args mutated its input: got %v, want %v", in, snapshot)
	}
}

func TestArgs_EmptyInput(t *testing.T) {
	t.Parallel()

	got := Args()
	want := []string{"-c", "gc.auto=0", "-c", "maintenance.auto=false"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Args() = %v, want %v", got, want)
	}
}

// gitConfigEnv is every GIT_CONFIG_* entry of the process, sorted.
func gitConfigEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GIT_CONFIG_") {
			out = append(out, kv)
		}
	}
	sort.Strings(out)
	return out
}

// isolateGitConfigEnv clears every GIT_CONFIG_* variable and GIT_CEILING_DIRECTORIES for the test and restores the
// previous state at its end. t.Setenv cannot unset, and the process may already carry entries from a TestMain.
func isolateGitConfigEnv(t *testing.T) {
	t.Helper()
	t.Setenv(ceilingEnv, "") // registers the restore of the ceiling variable
	if err := os.Unsetenv(ceilingEnv); err != nil {
		t.Fatalf("unset %s: %v", ceilingEnv, err)
	}
	original := gitConfigEnv()
	wipe := func() {
		for _, kv := range gitConfigEnv() {
			name, _, _ := strings.Cut(kv, "=")
			_ = os.Unsetenv(name)
		}
	}
	wipe()
	t.Cleanup(func() {
		wipe()
		for _, kv := range original {
			name, val, _ := strings.Cut(kv, "=")
			_ = os.Setenv(name, val)
		}
	})
}

// The HardenEnv tests change process-wide environment, so none of them runs in parallel.

func TestHardenEnv_ComposesOverPresetCount(t *testing.T) {
	isolateGitConfigEnv(t)
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "user.name")
	t.Setenv("GIT_CONFIG_VALUE_0", "someone")

	HardenEnv()

	want := []string{
		"GIT_CONFIG_COUNT=3",
		"GIT_CONFIG_KEY_0=user.name",
		"GIT_CONFIG_KEY_1=gc.auto",
		"GIT_CONFIG_KEY_2=maintenance.auto",
		"GIT_CONFIG_VALUE_0=someone",
		"GIT_CONFIG_VALUE_1=0",
		"GIT_CONFIG_VALUE_2=false",
	}
	if got := gitConfigEnv(); !reflect.DeepEqual(got, want) {
		t.Fatalf("env after HardenEnv over a preset count of 1:\n got %v\nwant %v", got, want)
	}
}

func TestHardenEnv_FromEmptyAndBadCount(t *testing.T) {
	for _, bad := range []string{"", "x", "-4"} {
		isolateGitConfigEnv(t)
		t.Setenv("GIT_CONFIG_COUNT", bad)

		HardenEnv()

		if got := os.Getenv("GIT_CONFIG_COUNT"); got != "2" {
			t.Fatalf("GIT_CONFIG_COUNT = %q after HardenEnv over %q, want 2", got, bad)
		}
		if os.Getenv("GIT_CONFIG_KEY_0") != "gc.auto" || os.Getenv("GIT_CONFIG_VALUE_1") != "false" {
			t.Fatalf("entries after HardenEnv over %q: %v", bad, gitConfigEnv())
		}
	}
}

func TestHardenEnv_Idempotent(t *testing.T) {
	isolateGitConfigEnv(t)

	HardenEnv()
	first := gitConfigEnv()
	HardenEnv()
	second := gitConfigEnv()

	if !reflect.DeepEqual(first, second) {
		t.Fatalf("second HardenEnv changed the env:\nbefore %v\nafter  %v", first, second)
	}
	if got := os.Getenv("GIT_CONFIG_COUNT"); got != "2" {
		t.Fatalf("GIT_CONFIG_COUNT after two calls = %q, want 2", got)
	}
}

// A real git sees the setting: HardenEnv alone, with no Args, makes `git config --get gc.auto` say 0.
func TestHardenEnv_EndToEnd(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("gitfixture: git is not installed")
	}
	isolateGitConfigEnv(t)
	HardenEnv()

	dir := t.TempDir()
	if out, err := exec.Command("git", Args("init", "-q", dir)...).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	for key, want := range map[string]string{"gc.auto": "0", "maintenance.auto": "false"} {
		out, err := exec.Command("git", "-C", dir, "config", "--get", key).Output()
		if err != nil {
			t.Fatalf("git config --get %s: %v", key, err)
		}
		if got := strings.TrimSpace(string(out)); got != want {
			t.Fatalf("git config --get %s = %q, want %q", key, got, want)
		}
	}
}

// git runs one git through Args in dir with env (nil is the process environment) and returns its trimmed stdout.
func git(t *testing.T, dir string, env []string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("git", Args(args...)...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// realTempDir is t.TempDir with symlinks resolved, the form git reports.
func realTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	return dir
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("gitfixture: git is not installed")
	}
}

// initRepo makes dir (created when missing) a repository of its own.
func initRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := git(t, dir, nil, "init", "-q"); err != nil {
		t.Fatalf("git init %s: %v", dir, err)
	}
}

func mkdir(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The leak itself: a fixture dir with no .git under a real work tree. git discovers the enclosing repository until the
// ceiling is set, and cannot after. The test's working directory is the work tree, as a package dir is in real use.
func TestHardenEnv_CeilingStopsDiscoveryIntoTheEnclosingWorkTree(t *testing.T) {
	requireGit(t)
	isolateGitConfigEnv(t)
	repo := realTempDir(t)
	initRepo(t, repo)
	fixture := mkdir(t, filepath.Join(repo, "sub", "fixture"))
	own := filepath.Join(repo, "sub", "own")
	initRepo(t, own)
	t.Chdir(repo)

	if top, err := git(t, fixture, nil, "rev-parse", "--show-toplevel"); err != nil || top != repo {
		t.Fatalf("control: git in the fixture should find the enclosing repo %q without the ceiling, got %q, %v", repo, top, err)
	}

	HardenEnv()

	if top, err := git(t, fixture, nil, "rev-parse", "--show-toplevel"); err == nil {
		t.Fatalf("git in a fixture with no .git discovered %q after HardenEnv, want a failure", top)
	}
	if top, err := git(t, own, nil, "rev-parse", "--show-toplevel"); err != nil || top != own {
		t.Fatalf("a fixture with its own git init: toplevel %q, %v, want %q", top, err, own)
	}
}

// The temp dir is a ceiling too: a TMPDIR kept inside a repository (a go tmp dir under the main checkout) does not let a
// fixture under it reach that repository, even when the working directory is elsewhere.
func TestHardenEnv_CeilingCoversTheTempDir(t *testing.T) {
	requireGit(t)
	isolateGitConfigEnv(t)
	elsewhere := realTempDir(t)
	repo := realTempDir(t)
	initRepo(t, repo)
	tmp := mkdir(t, filepath.Join(repo, ".cache", "tmp"))
	fixture := mkdir(t, filepath.Join(tmp, "TestX", "001"))
	t.Chdir(elsewhere)
	t.Setenv("TMPDIR", tmp)

	HardenEnv()

	if top, err := git(t, fixture, nil, "rev-parse", "--show-toplevel"); err == nil {
		t.Fatalf("git in a fixture under TMPDIR discovered %q after HardenEnv, want a failure", top)
	}
}

func TestHardenEnv_KeepsAPresetCeilingAndIsIdempotent(t *testing.T) {
	isolateGitConfigEnv(t)
	t.Setenv(ceilingEnv, "/preset/a"+string(os.PathListSeparator)+"/preset/b")

	HardenEnv()
	first := os.Getenv(ceilingEnv)
	HardenEnv()
	second := os.Getenv(ceilingEnv)

	if want := "/preset/a" + string(os.PathListSeparator) + "/preset/b" + string(os.PathListSeparator); !strings.HasPrefix(first, want) {
		t.Fatalf("%s = %q, want the preset kept first (prefix %q)", ceilingEnv, first, want)
	}
	if first != second {
		t.Fatalf("second HardenEnv changed %s:\nbefore %q\nafter  %q", ceilingEnv, first, second)
	}
	tmp := resolve(os.TempDir())
	if tmp != "" && !containsEntry(first, tmp) {
		t.Fatalf("%s = %q does not list the temp dir %q", ceilingEnv, first, tmp)
	}
}

func TestHardenEnv_CeilingListsTheWorkTreeTop(t *testing.T) {
	isolateGitConfigEnv(t)
	repo := realTempDir(t)
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil { // a .git entry is all the walk looks for
		t.Fatal(err)
	}
	t.Chdir(mkdir(t, filepath.Join(repo, "pkg", "deep")))

	HardenEnv()

	if got := os.Getenv(ceilingEnv); !containsEntry(got, repo) {
		t.Fatalf("%s = %q does not list the work tree top %q", ceilingEnv, got, repo)
	}
}

func containsEntry(list, entry string) bool {
	for _, have := range strings.Split(list, string(os.PathListSeparator)) {
		if have == entry {
			return true
		}
	}
	return false
}

func TestEnv_LeavesBaseAndProcessEnvironmentAlone(t *testing.T) {
	isolateGitConfigEnv(t)
	base := []string{"A=1", "GIT_AUTHOR_NAME=other", "GIT_AUTHOR_NAME=last"}
	baseSnapshot := append([]string(nil), base...)
	processSnapshot := os.Environ()

	got := Env(base)

	if !reflect.DeepEqual(base, baseSnapshot) {
		t.Fatalf("Env mutated base: got %v, want %v", base, baseSnapshot)
	}
	if after := os.Environ(); !reflect.DeepEqual(after, processSnapshot) {
		t.Fatalf("Env touched the process environment:\nbefore %v\nafter  %v", processSnapshot, after)
	}
	if len(got) > 0 && len(base) > 0 && &got[0] == &base[0] {
		t.Fatal("Env returned base's backing array, want a fresh slice")
	}
	counts := map[string]int{}
	for _, kv := range got {
		k, _, _ := strings.Cut(kv, "=")
		counts[k]++
	}
	for k, n := range counts {
		if n != 1 {
			t.Fatalf("Env left %d entries of %s in %v, want 1", n, k, got)
		}
	}
	for _, want := range []string{
		"A=1", "GIT_AUTHOR_NAME=" + testName, "GIT_AUTHOR_EMAIL=" + testEmail,
		"GIT_COMMITTER_NAME=" + testName, "GIT_COMMITTER_EMAIL=" + testEmail,
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_COUNT=2",
	} {
		if !sliceHas(got, want) {
			t.Fatalf("Env result lacks %q: %v", want, got)
		}
	}
}

func sliceHas(list []string, want string) bool {
	for _, have := range list {
		if have == want {
			return true
		}
	}
	return false
}

func TestEnv_ComposesOverAPresetCountAndCeiling(t *testing.T) {
	isolateGitConfigEnv(t)
	base := []string{
		"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=user.name", "GIT_CONFIG_VALUE_0=someone",
		ceilingEnv + "=/preset/a",
	}

	got := Env(base)

	for _, want := range []string{
		"GIT_CONFIG_COUNT=3", "GIT_CONFIG_KEY_0=user.name", "GIT_CONFIG_VALUE_0=someone",
		"GIT_CONFIG_KEY_1=gc.auto", "GIT_CONFIG_VALUE_1=0",
		"GIT_CONFIG_KEY_2=maintenance.auto", "GIT_CONFIG_VALUE_2=false",
	} {
		if !sliceHas(got, want) {
			t.Fatalf("Env over a preset count of 1 lacks %q: %v", want, got)
		}
	}
	var ceiling string
	for _, kv := range got {
		if v, ok := strings.CutPrefix(kv, ceilingEnv+"="); ok {
			ceiling = v
		}
	}
	if !strings.HasPrefix(ceiling, "/preset/a"+string(os.PathListSeparator)) {
		t.Fatalf("%s = %q, want the preset kept first", ceilingEnv, ceiling)
	}
	if again := Env(got); !reflect.DeepEqual(again, got) {
		t.Fatalf("Env of Env's own result changed it:\nfirst  %v\nsecond %v", got, again)
	}
}

// A real git, with no user config and no identity in the environment, commits in a fresh repo through Env, and sees the
// maintenance settings, a composed preset entry and the ceiling.
func TestEnv_EndToEnd(t *testing.T) {
	requireGit(t)
	isolateGitConfigEnv(t)
	env := Env(append(os.Environ(), "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.abbrev", "GIT_CONFIG_VALUE_0=9"))
	dir := realTempDir(t)

	for _, args := range [][]string{{"init", "-q"}, {"commit", "--allow-empty", "-q", "-m", "seed"}} {
		if out, err := git(t, dir, env, args...); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	for args, want := range map[string]string{
		"log -1 --format=%an":      testName,
		"log -1 --format=%ce":      testEmail,
		"config --get gc.auto":     "0",
		"config --get core.abbrev": "9",
	} {
		if got, err := git(t, dir, env, strings.Fields(args)...); err != nil || got != want {
			t.Fatalf("git %s = %q, %v, want %q", args, got, err, want)
		}
	}
}
