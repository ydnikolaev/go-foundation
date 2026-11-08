package adoption

import (
	"errors"
	"slices"
	"testing"
	"testing/fstest"
)

func corpus() fstest.MapFS {
	return fstest.MapFS{
		"0.10.0.yaml":               {Data: []byte("version: 0.10.0\nreleased: 2026-01-02\nchanges:\n  - id: TEN\n    kind: feat\n    action: {scope: local, why: w}\n")},
		"0.9.0.yaml":                {Data: []byte("version: 0.9.0\nreleased: 2026-01-01\nchanges: []\n")},
		"0.11.0.yaml":               {Data: []byte("version: 0.11.0\nreleased: unreleased\nchanges: []\n")},
		"current/known-issues.yaml": {Data: []byte("schema: x\nissues:\n  - id: KI-1\n    kind: known-issue\n    action: {scope: none, why: w}\n")},
		"README.md":                 {Data: []byte("not a note")},
	}
}

func versions(rs []Release) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.Version)
	}
	return out
}

func TestLoadSortsByVersionNotByName(t *testing.T) {
	t.Parallel()
	all, err := Load(corpus(), "*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := versions(all), []string{"0.9.0", "0.10.0", "0.11.0"}; !slices.Equal(got, want) {
		t.Fatalf("Load order = %v, want %v", got, want)
	}
	if len(all[1].Raw) == 0 {
		t.Fatal("Raw was not kept")
	}
}

func TestLoadRefusesWhatItCannotJudge(t *testing.T) {
	t.Parallel()
	cases := map[string]fstest.MapFS{
		"invalid yaml":       {"a.yaml": {Data: []byte("not: [valid")}},
		"malformed released": {"a.yaml": {Data: []byte("version: 1.0.0\nreleased: yesterday\n")}},
		"uncomparable":       {"a.yaml": {Data: []byte("version: next\n")}, "b.yaml": {Data: []byte("version: 1.0.0\n")}},
	}
	for name, fsys := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := Load(fsys, "*.yaml"); err == nil {
				t.Fatal("Load returned no error")
			}
		})
	}
	if _, err := Load(fstest.MapFS{}, "["); !errors.Is(err, ErrCorpusLoad) {
		t.Fatalf("bad pattern: %v, want ErrCorpusLoad", err)
	}
	if all, err := Load(fstest.MapFS{}, "*.yaml"); err != nil || len(all) != 0 {
		t.Fatalf("empty corpus: %v, %v", all, err)
	}
}

func TestSinceExactlyLastReleased(t *testing.T) {
	t.Parallel()
	all, err := Load(corpus(), "*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if got := versions(Since(all, "0.9.0", "0.10.0")); !slices.Equal(got, []string{"0.10.0"}) {
		t.Errorf("Since(0.9.0, 0.10.0) = %v", got)
	}
	if got := versions(Since(all, "", "")); len(got) != 3 {
		t.Errorf("Since open = %v", got)
	}
	if r, ok := Exactly(all, "0.10.0"); !ok || r.Changes[0].ID != "TEN" {
		t.Errorf("Exactly = %+v, %v", r, ok)
	}
	if _, ok := Exactly(all, "9.9.9"); ok {
		t.Error("Exactly found a missing version")
	}
	if v, err := LastReleased(all); err != nil || v != "0.10.0" {
		t.Errorf("LastReleased = %q, %v; want 0.10.0 (0.11.0 is unreleased)", v, err)
	}
	if _, err := LastReleased(all[2:]); !errors.Is(err, ErrNoReleased) {
		t.Errorf("LastReleased over only unreleased = %v", err)
	}
}

func TestStanding(t *testing.T) {
	t.Parallel()
	issues, err := LoadStanding(corpus(), "current/known-issues.yaml")
	if err != nil || len(issues) != 1 || issues[0].ID != "KI-1" {
		t.Fatalf("LoadStanding = %+v, %v", issues, err)
	}
	if _, err := LoadStanding(corpus(), "missing.yaml"); !errors.Is(err, ErrCorpusLoad) {
		t.Errorf("missing file: %v", err)
	}
	if _, err := LoadStanding(fstest.MapFS{"k.yaml": {Data: []byte("issues: [")}}, "k.yaml"); !errors.Is(err, ErrStandingInvalid) {
		t.Errorf("bad yaml: %v", err)
	}

	all, _ := Load(corpus(), "*.yaml")
	// An empty selection borrows the newest release as a carrier.
	got := AttachStanding(nil, all, issues)
	if len(got) != 1 || got[0].Version != "0.11.0" || len(got[0].Changes) != 1 {
		t.Fatalf("carrier = %+v", got)
	}
	// Attaching twice never duplicates, and never mutates the input.
	twice := AttachStanding(got, all, issues)
	if len(twice[0].Changes) != 1 || len(all[2].Changes) != 0 {
		t.Fatalf("duplicate or mutation: %+v / %+v", twice, all[2])
	}
	if out := AttachStanding(nil, nil, issues); len(out) != 0 {
		t.Fatalf("no corpus: %+v", out)
	}
}

func TestOlderThanAndImpact(t *testing.T) {
	t.Parallel()
	cases := []struct {
		a, b string
		want bool
	}{
		{"0.9.0", "0.10.0", true}, {"v0.10.0", "0.9.0", false}, {"0.27", "0.27.0", false},
		{"0.28.0-rc.1", "0.28.0", true}, {"1.0.0", "1.0.0", false},
	}
	for _, c := range cases {
		if got, err := OlderThan(c.a, c.b); err != nil || got != c.want {
			t.Errorf("OlderThan(%q, %q) = %v, %v; want %v", c.a, c.b, got, err, c.want)
		}
	}
	for _, bad := range []string{"", "dev", "1.x"} {
		if _, err := OlderThan(bad, "1.0.0"); !errors.Is(err, ErrVersionInvalid) {
			t.Errorf("OlderThan(%q) = %v", bad, err)
		}
	}
	if ImpactOrder("high") >= ImpactOrder("normal") || ImpactOrder("low") >= ImpactOrder("weird") {
		t.Error("ImpactOrder is not high < normal < low < unknown")
	}
}
