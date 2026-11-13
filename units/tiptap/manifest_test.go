package tiptap

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestEmbeddedProfiles(t *testing.T) {
	m, err := LoadManifest(manifestJSON)
	if err != nil {
		t.Fatalf("LoadManifest(embedded): %v", err)
	}
	if got := sortedKeys(m.Profiles); !slices.Equal(got, []string{"article", "email", "post"}) {
		t.Errorf("profiles = %v", got)
	}
	cases := []struct {
		name               string
		p                  Profile
		maxNodes, maxDepth int
		video              bool
	}{
		{"post", Post, 1000, 4, false},
		{"article", Article, 5000, 10, false},
		{"email", Email, 5000, 10, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.p.MaxNodes != c.maxNodes || c.p.MaxDepth != c.maxDepth {
				t.Errorf("caps = %d/%d, want %d/%d", c.p.MaxNodes, c.p.MaxDepth, c.maxNodes, c.maxDepth)
			}
			if _, ok := c.p.Nodes["video"]; ok != c.video {
				t.Errorf("video listed = %v, want %v", ok, c.video)
			}
			if !c.p.Nodes["text"].Leaf || c.p.Nodes["paragraph"].Leaf || c.p.Nodes["doc"].Leaf {
				t.Error("leaf flags not read: text must be a leaf, paragraph and doc must not")
			}
		})
	}
}

// The embedded manifest lists no video provider; the example manifest adds a video node and nothing else.
func TestEmbeddedMatchesTheExampleManifest(t *testing.T) {
	ex := exampleManifest.Profiles
	if !reflect.DeepEqual(Post, ex["post"]) || !reflect.DeepEqual(Email, ex["email"]) {
		t.Error("embedded Post or Email differs from testdata/manifest.example.json")
	}
	video, ok := ex["article"].Nodes["video"]
	if !ok {
		t.Fatal("the example manifest has no video node")
	}
	if len(video.Attrs["provider"].Enum) == 0 {
		t.Error("the example video node lists no provider")
	}
	withoutVideo := ex["article"]
	withoutVideo.Nodes = maps.Clone(withoutVideo.Nodes)
	delete(withoutVideo.Nodes, "video")
	if !reflect.DeepEqual(Article, withoutVideo) {
		t.Error("embedded Article differs from the example article minus its video node")
	}
}

// profileJSON is a one-profile manifest; attrs is the body of node n's attrs.
func profileJSON(caps, attrs string) []byte {
	return []byte(`{"version":1,"profiles":{"p":{` + caps +
		`"nodes":{"doc":{"attrs":{}},"n":{"attrs":{` + attrs + `}}},"marks":{}}}}`)
}

const caps = `"maxNodes":10,"maxDepth":3,`

func TestLoadManifestAccepts(t *testing.T) {
	cases := []struct{ name, attrs string }{
		{"every rule shape", `"s":{"type":"string","required":true,"min":1,"max":5,"enum":["a"],"pattern":"^a"},` +
			`"i":{"type":"integer","required":false,"min":1,"max":9,"enum":[1,2]},` +
			`"k":{"type":"string","required":true,"patternBy":{"attr":"s","patterns":{"a":"^x$"}}},` +
			`"l":{"type":"array","required":true,"min":2,"max":3,"items":{"attrs":{"m":{"type":"string","required":true}},"transient":["u"]}}`},
		{"no attributes", ``},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := LoadManifest(profileJSON(caps, c.attrs)); err != nil {
				t.Fatal(err)
			}
		})
	}
	t.Run("leaf and transient on an entry", func(t *testing.T) {
		b := []byte(`{"version":1,"profiles":{"p":{` + caps + `"nodes":{"doc":{"attrs":{}},` +
			`"img":{"attrs":{},"leaf":true,"transient":["uploadId"]}},"marks":{}}}}`)
		m, err := LoadManifest(b)
		if err != nil {
			t.Fatal(err)
		}
		img := m.Profiles["p"].Nodes["img"]
		if !img.Leaf || !slices.Equal(img.Transient, []string{"uploadId"}) {
			t.Errorf("img = %+v", img)
		}
	})
}

func TestLoadManifestRefuses(t *testing.T) {
	cases := []struct {
		name string
		body []byte
		want string // substring of the error
	}{
		{"unknown rule type", profileJSON(caps, `"a":{"type":"boolean","required":true}`), `unknown rule type "boolean"`},
		{"patternBy to an absent attribute", profileJSON(caps, `"a":{"type":"string","required":true,"patternBy":{"attr":"kind","patterns":{"x":"^x$"}}}`), `"kind"`},
		{"non-RE2 pattern", profileJSON(caps, `"a":{"type":"string","required":true,"pattern":"(?<=a)b"}`), "pattern"},
		{"non-RE2 patternBy pattern", profileJSON(caps, `"k":{"type":"string","required":true},"a":{"type":"string","required":true,"patternBy":{"attr":"k","patterns":{"x":"(?<=a)b"}}}`), "patternBy x"},
		{"maxNodes 0", profileJSON(`"maxNodes":0,"maxDepth":3,`, ``), "maxNodes"},
		{"maxNodes absent", profileJSON(`"maxDepth":3,`, ``), "maxNodes"},
		{"maxNodes null", profileJSON(`"maxNodes":null,"maxDepth":3,`, ``), "maxNodes"},
		{"maxDepth absent", profileJSON(`"maxNodes":10,`, ``), "maxDepth"},
		{"maxDepth negative", profileJSON(`"maxNodes":10,"maxDepth":-1,`, ``), "maxDepth"},
		{"attribute max 0", profileJSON(caps, `"a":{"type":"string","required":true,"max":0}`), "max must be"},
		{"attribute min 0", profileJSON(caps, `"a":{"type":"integer","required":true,"min":0}`), "min must be"},
		{"array item max 0", profileJSON(caps, `"l":{"type":"array","required":true,"items":{"attrs":{"m":{"type":"string","required":true,"max":0}}}}`), "items.attrs.m: max must be"},
		{"unknown rule key", profileJSON(caps, `"a":{"type":"string","required":true,"maxLength":3}`), `"maxLength"`},
		{"unknown entry key", []byte(`{"version":1,"profiles":{"p":{` + caps + `"nodes":{"doc":{"attrs":{},"content":"block+"}},"marks":{}}}}`), `"content"`},
		{"unknown profile key", profileJSON(caps+`"maxMarks":3,`, ``), `"maxMarks"`},
		{"unknown top-level key", []byte(`{"version":1,"profiles":{},"extra":1}`), `"extra"`},
		{"array without items", profileJSON(caps, `"l":{"type":"array","required":true}`), "items"},
		{"items on a string", profileJSON(caps, `"s":{"type":"string","required":true,"items":{"attrs":{}}}`), "items"},
		{"pattern on an integer", profileJSON(caps, `"i":{"type":"integer","required":true,"pattern":"^1$"}`), "on a rule of type integer"},
		{"bad JSON", []byte(`{"version":1`), "decode manifest"},
		{"data after the manifest", []byte(`{"version":1,"profiles":{}} {}`), "data after"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, err := LoadManifest(c.body)
			if err == nil {
				t.Fatalf("LoadManifest = %+v, want an error", m)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %q, want it to contain %q", err, c.want)
			}
		})
	}
}

func ExampleLoadManifest() {
	m, err := LoadManifest([]byte(`{"version":1,"profiles":{"note":{"maxNodes":50,"maxDepth":3,` +
		`"nodes":{"doc":{"attrs":{}},"paragraph":{"attrs":{}},"text":{"attrs":{},"leaf":true}},"marks":{}}}}`))
	if err != nil {
		fmt.Println(err)
		return
	}
	_, err = Validate([]byte(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"hi"}]}]}`),
		m.Profiles["note"], NoRuneCap)
	fmt.Println(err)
	// Output: <nil>
}
