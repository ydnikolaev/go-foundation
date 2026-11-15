package gitfixture

import (
	"os"
	"os/exec"
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

// isolateGitConfigEnv clears every GIT_CONFIG_* variable for the test and restores the previous state at its end.
// t.Setenv cannot unset, and the process may already carry entries from a TestMain.
func isolateGitConfigEnv(t *testing.T) {
	t.Helper()
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
