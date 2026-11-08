package adoption

import (
	"strings"
	"testing"
)

var a2aVerbs = Verbs{Tool: "a2a", Adapt: "a2a adapt", Done: "a2a adapt --done"}

func TestNotice(t *testing.T) {
	t.Parallel()
	all := []Release{
		{Version: "0.1.0", Released: "2026-01-01", Changes: []Change{{ID: "A", Kind: "feat", Action: Action{Scope: "local"}}}},
		{Version: "0.2.0", Released: "2026-02-01", Changes: []Change{{ID: "B", Kind: "feat", Action: Action{Scope: "space"}}}},
	}
	cases := []struct {
		name   string
		clock  Clock
		binary string
		want   []string // substrings; nil means silent
	}{
		{"never recorded", Clock{}, "0.2.0", []string{"never recorded adoption: 2 step(s) since v0.1.0", "`a2a adapt`", "`a2a adapt --done`"}},
		{"behind", Clock{Through: "0.1.0"}, "0.2.0", []string{"1 adoption step(s) pending since v0.1.0 (this binary is v0.2.0)"}},
		{"current", Clock{Through: "0.2.0"}, "0.2.0", nil},
		{"adopted ahead by id", Clock{Through: "0.1.0", Ahead: []string{"B"}}, "0.2.0", nil},
		{"ahead of the binary", Clock{Through: "0.3.0"}, "0.2.0", []string{"adapted through v0.3.0, newer than this binary (v0.2.0 at /bin/a2a)", "which a2a comes first in PATH"}},
		{"dev build", Clock{}, "dev", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Notice(all, nil, tc.clock, tc.binary, "/bin/a2a", a2a, a2aVerbs)
			if tc.want == nil {
				if got != "" {
					t.Fatalf("Notice = %q, want silent", got)
				}
				return
			}
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("Notice = %q, missing %q", got, w)
				}
			}
		})
	}
}

// TestNoticeMatchesA2ahubWording pins the sentence a2ahub prints today
// (internal/cli/adoptionnotice.go at b5f12155), so switching a2ahub to this
// unit changes no byte a user sees.
func TestNoticeMatchesA2ahubWording(t *testing.T) {
	t.Parallel()
	all := []Release{{Version: "0.2.0", Changes: []Change{{ID: "B", Kind: "feat", Action: Action{Scope: "local"}}}}}
	got := Notice(all, nil, Clock{Through: "0.1.0"}, "0.2.0", "a2a", a2a, a2aVerbs)
	want := "this repository has 1 adoption step(s) pending since v0.1.0 (this binary is v0.2.0) — run `a2a adapt`, do them, then `a2a adapt --done`"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

// TestPolicyScopesArePositive: a scope the tool did not name obliges nothing.
func TestPolicyScopesArePositive(t *testing.T) {
	t.Parallel()
	all := []Release{{Version: "1.0.0", Changes: []Change{{ID: "S", Kind: "feat", Action: Action{Scope: "space"}}}}}
	proj, err := Pending(all, nil, Clock{}, "1.0.0", Policy{})
	if err != nil || len(proj.Items) != 0 {
		t.Fatalf("an unnamed party scope obliged: %+v, %v", proj.Items, err)
	}
}
