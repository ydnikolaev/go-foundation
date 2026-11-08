package agentdocs_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ydnikolaev/go-foundation/units/agentdocs"
)

// manifestPath is where these fixtures keep the manifest. LoadManifest takes
// the path as an argument, so a tool chooses its own.
const manifestPath = "mytool/docs-manifest.json"

// fixtureTree builds a minimal tree carrying a manifest and the given pages,
// so a test can state a manifest and read back what the decode made of it.
// This is the reason LoadManifest takes an fs.FS: "a section added to the
// manifest is reachable with no code change" is a property OF the decode.
func fixtureTree(t *testing.T, manifest string, pages map[string]string) fstest.MapFS {
	t.Helper()
	tree := fstest.MapFS{manifestPath: {Data: []byte(manifest)}}
	for p, body := range pages {
		tree[p] = &fstest.MapFile{Data: []byte(body)}
	}
	return tree
}

const oneSection = `{
  "schema": "agentdocs-manifest/v1",
  "groups": ["Start"],
  "sections": [{"id": "getting-started", "group": "Start", "title": "Getting started", "file": "mytool/onboarding.md", "verbs": ["init"]}]
}`

// verbsManifest carries a parent ("env") and one of its subcommands
// ("env push") on separate topics, plus an unrelated verb.
const verbsManifest = `{
  "schema": "agentdocs-manifest/v1",
  "groups": ["Start", "Reference"],
  "sections": [
    {"id": "environments", "group": "Reference", "title": "Environments", "file": "mytool/env.md", "verbs": ["env"]},
    {"id": "pushing", "group": "Reference", "title": "Pushing", "file": "mytool/push.md", "verbs": ["env push"]},
    {"id": "getting-started", "group": "Start", "title": "Getting started", "file": "mytool/onboarding.md", "verbs": ["init", "skill install"]}
  ]
}`

func mustLoad(t *testing.T, tree fstest.MapFS) agentdocs.Manifest {
	t.Helper()
	m, err := agentdocs.LoadManifest(tree, manifestPath)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	return m
}

// Ported: skill/manifest_test.go TestLoadDocsManifestDecodesEveryKeyTheFileCarries
// (LoopCorpus is a2a-only and not carried; Verbs is new).
func TestLoadManifestDecodesEveryKeyTheFileCarries(t *testing.T) {
	t.Parallel()

	got := mustLoad(t, fixtureTree(t, oneSection, nil))
	if got.Schema != agentdocs.ManifestSchema {
		t.Errorf("Schema = %q, want %q", got.Schema, agentdocs.ManifestSchema)
	}
	if !reflect.DeepEqual(got.Groups, []string{"Start"}) {
		t.Errorf("Groups = %v, want [Start]", got.Groups)
	}
	want := agentdocs.Section{ID: "getting-started", Group: "Start", Title: "Getting started", File: "mytool/onboarding.md", Verbs: []string{"init"}}
	if len(got.Sections) != 1 || !reflect.DeepEqual(got.Sections[0], want) {
		t.Errorf("Sections = %+v, want [%+v]", got.Sections, want)
	}
}

// Ported: TestLoadDocsManifestSectionListFollowsTheManifest and cmd_docs_test.go
// TestDocsAddingManifestSectionAddsTopicWithNoCodeChange.
func TestLoadManifestSectionListFollowsTheManifest(t *testing.T) {
	t.Parallel()

	anchor := `{"id": "getting-started", "group": "Start", "title": "Getting started", "file": "mytool/onboarding.md", "verbs": ["init"]}`
	extended := strings.Replace(oneSection, anchor,
		anchor+`, {"id": "invented-topic", "group": "Start", "title": "Invented", "file": "mytool/invented.md"}`, 1)
	if extended == oneSection {
		t.Fatal("could not seed the extra section: the anchor string did not match")
	}

	m := mustLoad(t, fixtureTree(t, extended, map[string]string{"mytool/invented.md": "invented page\n"}))
	if len(m.Sections) != 2 || m.Sections[1].ID != "invented-topic" {
		t.Fatalf("Sections = %+v, want the manifest's two entries in order", m.Sections)
	}
	body, err := m.Read(fixtureTree(t, extended, map[string]string{"mytool/invented.md": "invented page\n"}), "invented-topic")
	if err != nil || string(body) != "invented page\n" {
		t.Fatalf("Read(invented-topic) = %q, %v; want the page, reachable with no code change", body, err)
	}
}

// Ported: TestLoadDocsManifestRefuses. New: a verb listed twice is refused.
func TestLoadManifestRefuses(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		tree     fstest.MapFS
		wantWord string
	}{
		{"absent", fstest.MapFS{}, "read"},
		{"undecodable", fixtureTree(t, "{not json", nil), "decode"},
		{"unknown schema", fixtureTree(t, strings.Replace(oneSection, "agentdocs-manifest/v1", "agentdocs-manifest/v99", 1), nil), "schema"},
		{"a2a's schema", fixtureTree(t, strings.Replace(oneSection, "agentdocs-manifest/v1", "a2a-docs-manifest/v1", 1), nil), "schema"},
		{"no sections", fixtureTree(t, `{"schema":"agentdocs-manifest/v1","groups":["Start"],"sections":[]}`, nil), "0 sections"},
		{"no groups", fixtureTree(t, `{"schema":"agentdocs-manifest/v1","groups":[],"sections":[{"id":"a","group":"x","title":"A","file":"a.md"}]}`, nil), "0 groups"},
		{
			"a verb on two sections",
			fixtureTree(t, `{"schema":"agentdocs-manifest/v1","groups":["S"],"sections":[
				{"id":"a","group":"S","title":"A","file":"a.md","verbs":["env push"]},
				{"id":"b","group":"S","title":"B","file":"b.md","verbs":["env  push"]}]}`, nil),
			`"env push"`,
		},
		{
			"a verb twice on one section",
			fixtureTree(t, `{"schema":"agentdocs-manifest/v1","groups":["S"],"sections":[
				{"id":"a","group":"S","title":"A","file":"a.md","verbs":["env","env"]}]}`, nil),
			`"env"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := agentdocs.LoadManifest(tc.tree, manifestPath)
			if err == nil {
				t.Fatalf("LoadManifest(%s) = nil error, want a refusal", tc.name)
			}
			if !errors.Is(err, agentdocs.ErrManifestInvalid) {
				t.Errorf("refusal %v does not wrap ErrManifestInvalid", err)
			}
			if !strings.Contains(err.Error(), tc.wantWord) {
				t.Errorf("refusal %q does not name %q: a caller cannot act on it", err, tc.wantWord)
			}
		})
	}
}

// The verb-collision refusal names both sections, so the author knows which
// two rows to reconcile.
func TestLoadManifestDuplicateVerbNamesBothSections(t *testing.T) {
	t.Parallel()
	_, err := agentdocs.LoadManifest(fixtureTree(t, `{"schema":"agentdocs-manifest/v1","groups":["S"],"sections":[
		{"id":"first","group":"S","title":"A","file":"a.md","verbs":["env push"]},
		{"id":"second","group":"S","title":"B","file":"b.md","verbs":["env push"]}]}`, nil), manifestPath)
	if err == nil {
		t.Fatal("want a refusal")
	}
	for _, id := range []string{"first", "second"} {
		if !strings.Contains(err.Error(), id) {
			t.Errorf("refusal %q does not name section %q", err, id)
		}
	}
}

// Topics is the manifest's own order, never a sort: the order is the
// author's, and a listing renders it.
func TestTopicsAreInManifestOrder(t *testing.T) {
	t.Parallel()
	m := mustLoad(t, fixtureTree(t, verbsManifest, nil))
	want := []string{"environments", "pushing", "getting-started"}
	if got := m.Topics(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Topics() = %v, want %v (manifest order)", got, want)
	}
}

// Ported: cmd_docs_test.go TestDocsResolveTopic.
func TestResolve(t *testing.T) {
	t.Parallel()
	m := mustLoad(t, fixtureTree(t, verbsManifest, nil))
	for _, tc := range []struct {
		topic, wantFile string
		wantOK          bool
	}{
		{"pushing", "mytool/push.md", true},
		{"getting-started", "mytool/onboarding.md", true},
		{"Pushing", "", false},
		{"push", "", false},
		{"", "", false},
	} {
		s, ok := m.Resolve(tc.topic)
		if ok != tc.wantOK || s.File != tc.wantFile {
			t.Errorf("Resolve(%q) = (%q, %v), want (%q, %v)", tc.topic, s.File, ok, tc.wantFile, tc.wantOK)
		}
	}
}

// TopicFor takes a command path without the root and matches the longest
// listed WORD prefix: "env push x" tries "env push x", "env push", "env".
func TestTopicForMatchesTheLongestWordPrefix(t *testing.T) {
	t.Parallel()
	m := mustLoad(t, fixtureTree(t, verbsManifest, nil))
	for _, tc := range []struct {
		path, want string
		wantOK     bool
	}{
		{"env push", "pushing", true},              // the subcommand listed on its own topic wins
		{"env push x", "pushing", true},            // a deeper path falls back to its longest listed prefix
		{"env push x y", "pushing", true},          // ...at any depth
		{"env", "environments", true},              // the parent itself
		{"env list", "environments", true},         // a parent covers its unlisted subcommands
		{"env pushx", "environments", true},        // a word prefix, never a byte prefix
		{"  env   push  ", "pushing", true},        // whitespace is not a word
		{"skill install", "getting-started", true}, // a second verb on one section
		{"skill", "", false},                       // a child listed does not cover its parent
		{"envx", "", false},
		{"", "", false},
	} {
		got, ok := m.TopicFor(tc.path)
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("TopicFor(%q) = (%q, %v), want (%q, %v)", tc.path, got, ok, tc.want, tc.wantOK)
		}
	}
}

// Ported: cmd_docs_test.go TestDocsPrintsKnownTopicPageVerbatim.
func TestReadReturnsThePageVerbatim(t *testing.T) {
	t.Parallel()
	page := "# Pushing\n\nbytes\twith tabs and no trailing newline"
	tree := fixtureTree(t, verbsManifest, map[string]string{"mytool/push.md": page})
	m := mustLoad(t, tree)
	got, err := m.Read(tree, "pushing")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(got) != page {
		t.Fatalf("Read = %q, want the page verbatim %q", got, page)
	}
}

// Ported: cmd_docs_test.go TestDocsUnknownTopicRefusesNamingValidIDs. The
// refusal names EVERY valid topic, never "see the docs".
func TestReadUnknownTopicNamesEveryValidTopic(t *testing.T) {
	t.Parallel()
	tree := fixtureTree(t, verbsManifest, nil)
	m := mustLoad(t, tree)
	_, err := m.Read(tree, "no-such-topic")
	if !errors.Is(err, agentdocs.ErrUnknownTopic) {
		t.Fatalf("Read(unknown) = %v, want ErrUnknownTopic", err)
	}
	if !strings.Contains(err.Error(), `"no-such-topic"`) {
		t.Errorf("refusal %q does not name the attempted topic", err)
	}
	for _, id := range m.Topics() {
		if !strings.Contains(err.Error(), id) {
			t.Errorf("refusal %q does not name valid topic %q", err, id)
		}
	}
}

// Ported: cmd_docs_test.go TestDocsMissingPageRefusesNamingSectionAndAbsentPage.
func TestReadMissingPageNamesSectionAndFile(t *testing.T) {
	t.Parallel()
	tree := fixtureTree(t, verbsManifest, nil)
	m := mustLoad(t, tree)
	_, err := m.Read(tree, "pushing")
	if !errors.Is(err, agentdocs.ErrPageMissing) {
		t.Fatalf("Read(missing page) = %v, want ErrPageMissing", err)
	}
	if !strings.Contains(err.Error(), "pushing") || !strings.Contains(err.Error(), "mytool/push.md") {
		t.Errorf("refusal %q must name the section and the absent page", err)
	}
}

// Ported: cli.go workflowLine, through every verb that printed it.
func TestWorkflowLine(t *testing.T) {
	t.Parallel()
	got := agentdocs.WorkflowLine(agentdocs.Tool{Name: "mytool"}, "pushing")
	want := "workflow: run `mytool docs pushing` for the walkthrough"
	if got != want {
		t.Fatalf("WorkflowLine = %q, want %q", got, want)
	}
	if a2a := agentdocs.WorkflowLine(agentdocs.Tool{Name: "a2a"}, "feedback"); a2a != "workflow: run `a2a docs feedback` for the walkthrough" {
		t.Fatalf("WorkflowLine(a2a) = %q, want a2a's own line byte for byte", a2a)
	}
}
