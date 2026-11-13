package tiptap

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the email golden files")

// emailPort is a test ImageURL: the address spells out the id and the width asked for.
func emailPort(id string, width int) (string, bool) {
	return fmt.Sprintf("https://example.com/m/%s?w=%d", id, width), true
}

func emailOpts(port func(string, int) (string, bool)) EmailOptions {
	return EmailOptions{
		ImageURL:    port,
		ArticleURL:  "https://example.com/articles/1",
		Labels:      EmailLabels{Photo: "Photo", Gallery: "Gallery"},
		ColumnWidth: 592,
		ClassPrefix: "x-",
	}
}

func renderEmail(t *testing.T, body []byte, opts EmailOptions) Letter {
	t.Helper()
	l, err := RenderEmail(body, opts)
	if err != nil {
		t.Fatalf("RenderEmail: %v", err)
	}
	return l
}

func emailPara(inlines ...string) string {
	return `{"type":"paragraph","content":[` + strings.Join(inlines, ",") + `]}`
}

func emailText(text string, marks ...string) string {
	b, _ := json.Marshal(text)
	s := `{"type":"text","text":` + string(b)
	if len(marks) > 0 {
		s += `,"marks":[` + strings.Join(marks, ",") + `]`
	}
	return s + "}"
}

func emailLink(href string) string {
	b, _ := json.Marshal(href)
	return `{"type":"link","attrs":{"href":` + string(b) + `}}`
}

func emailPic(id string, w, h int, alt string) string {
	a, _ := json.Marshal(alt)
	return fmt.Sprintf(`{"type":"image","attrs":{"mediaId":%q,"width":%d,"height":%d,"alt":%s}}`, id, w, h, a)
}

func emailGallery(items ...string) string {
	return `{"type":"gallery","attrs":{"items":[` + strings.Join(items, ",") + `]}}`
}

func emailItem(id string, w, h int) string {
	return fmt.Sprintf(`{"mediaId":%q,"width":%d,"height":%d}`, id, w, h)
}

func TestRenderEmailGolden(t *testing.T) {
	body := goldenFixture(t, "email.json")
	for _, c := range []struct {
		name string
		port func(string, int) (string, bool)
		html string
		txt  string
	}{
		{"port", emailPort, "testdata/email.golden.html", "testdata/email.golden.txt"},
		{"no port", nil, "testdata/email-noport.golden.html", "testdata/email-noport.golden.txt"},
	} {
		t.Run(c.name, func(t *testing.T) {
			l := renderEmail(t, body, emailOpts(c.port))
			if *update {
				for path, got := range map[string]string{c.html: string(l.HTML) + "\n", c.txt: l.Text + "\n"} {
					if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}
			for path, got := range map[string]string{c.html: string(l.HTML) + "\n", c.txt: l.Text + "\n"} {
				want, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if got != string(want) {
					t.Errorf("%s differs (run with -update to rewrite it):\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
				}
			}
			again := renderEmail(t, body, emailOpts(c.port))
			if again != l {
				t.Error("two renders of the same body differ")
			}
		})
	}
}

func TestRenderEmailRefused(t *testing.T) {
	for _, c := range []struct {
		name string
		body []byte
	}{
		{"video", goldenFixture(t, "article.json")},
		{"bad json", []byte(`{"type":`)},
		{"not a doc", []byte(`{"type":"paragraph"}`)},
		{"unknown attribute", doc(`{"type":"paragraph","attrs":{"align":"left"}}`)},
	} {
		t.Run(c.name, func(t *testing.T) {
			l, err := RenderEmail(c.body, emailOpts(emailPort))
			if !errors.Is(err, ErrNotAllowed) {
				t.Fatalf("err = %v, want ErrNotAllowed", err)
			}
			if l != (Letter{}) {
				t.Errorf("letter = %+v, want the zero Letter", l)
			}
		})
	}
}

var classAttr = regexp.MustCompile(`class="([^"]*)"`)

// emailClassNames are the class suffixes the renderer may emit.
var emailClassNames = map[string]bool{
	"p": true, "h2": true, "h3": true, "quote": true, "ul": true, "ol": true, "hr": true,
	"callout": true, "callout-info": true, "callout-warning": true,
	"figure": true, "img": true, "photo-placeholder": true, "a": true, "code": true,
}

func TestRenderEmailEscaping(t *testing.T) {
	const hostile = `<script>&"'`
	body := doc(
		emailPara(emailText(hostile)) + "," +
			emailPic("m-1", 100, 50, `a"b`) + "," +
			emailPara(emailText(hostile, emailLink(`https://example.com/?a=1&b="2"`))),
	)
	opts := emailOpts(func(id string, w int) (string, bool) { return `https://example.com/m?id=` + id + `&q="x"`, true })
	opts.ClassPrefix = `x"-`
	l := renderEmail(t, body, opts)
	out := string(l.HTML)

	for _, want := range []string{
		`&lt;script&gt;&amp;&#34;&#39;`,
		`alt="a&#34;b"`,
		`src="https://example.com/m?id=m-1&amp;q=&#34;x&#34;"`,
		`href="https://example.com/?a=1&amp;b=&#34;2&#34;"`,
		`class="x&#34;-p"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %s:\n%s", want, out)
		}
	}
	if strings.Contains(out, "<script") || strings.Contains(out, "style=") {
		t.Errorf("output holds a script or a style:\n%s", out)
	}
	if !strings.Contains(l.Text, hostile) {
		t.Errorf("text part = %q, want the raw text %q", l.Text, hostile)
	}
}

func TestRenderEmailClasses(t *testing.T) {
	body := goldenFixture(t, "email.json")
	for _, port := range []func(string, int) (string, bool){emailPort, nil} {
		out := string(renderEmail(t, body, emailOpts(port)).HTML)
		seen := 0
		for _, m := range classAttr.FindAllStringSubmatch(out, -1) {
			for _, tok := range strings.Fields(m[1]) {
				seen++
				name, ok := strings.CutPrefix(tok, "x-")
				if !ok || !emailClassNames[name] {
					t.Errorf("class token %q is not a prefixed known name", tok)
				}
			}
		}
		if seen == 0 {
			t.Error("no class emitted with a prefix")
		}
	}

	t.Run("empty prefix", func(t *testing.T) {
		for _, port := range []func(string, int) (string, bool){emailPort, nil} {
			opts := emailOpts(port)
			opts.ClassPrefix = ""
			out := string(renderEmail(t, body, opts).HTML)
			if strings.Contains(out, "class") || strings.Contains(out, "style=") {
				t.Errorf("an empty prefix still emits a class or a style:\n%s", out)
			}
		}
	})

	t.Run("callout tone", func(t *testing.T) {
		out := string(renderEmail(t, doc(
			`{"type":"callout","content":[`+emailPara(emailText("a"))+`]},`+
				`{"type":"callout","attrs":{"tone":"warning"},"content":[`+emailPara(emailText("b"))+`]}`),
			emailOpts(nil)).HTML)
		for _, want := range []string{
			`<table role="presentation" class="x-callout x-callout-info">`,
			`<table role="presentation" class="x-callout x-callout-warning">`,
		} {
			if !strings.Contains(out, want) {
				t.Errorf("output lacks %s:\n%s", want, out)
			}
		}
	})
}

func TestRenderEmailLinks(t *testing.T) {
	const external = ` href="https://example.com/a?x=1" target="_blank" rel="ugc nofollow noopener noreferrer">`
	hostile := `{"type":"link","attrs":{"href":"https://example.com/a?x=1","target":"_self","rel":"noopener","class":"evil","title":"Hover"}}`
	for _, c := range []struct {
		name     string
		mark     string
		wantHTML string
		wantText string
	}{
		{"https", emailLink("https://example.com/a?x=1"), `<a class="x-a"` + external + "label</a>", "label (https://example.com/a?x=1)"},
		{"http", emailLink("http://example.com/"), `<a class="x-a" href="http://example.com/" target="_blank" rel="ugc nofollow noopener noreferrer">label</a>`, "label (http://example.com/)"},
		{"mailto", emailLink("mailto:a"), `<a class="x-a" href="mailto:a">label</a>`, "label (mailto:a)"},
		{"document attributes ignored", hostile, `<a class="x-a"` + external + "label</a>", "label (https://example.com/a?x=1)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			l := renderEmail(t, doc(emailPara(emailText("label", c.mark))), emailOpts(nil))
			if !strings.Contains(string(l.HTML), c.wantHTML) {
				t.Errorf("html = %s, want it to hold %s", l.HTML, c.wantHTML)
			}
			if l.Text != c.wantText {
				t.Errorf("text = %q, want %q", l.Text, c.wantText)
			}
			for _, banned := range []string{"_self", "evil", "title=", `rel="noopener"`, "Hover"} {
				if strings.Contains(string(l.HTML), banned) {
					t.Errorf("html holds the document's %q: %s", banned, l.HTML)
				}
			}
		})
	}

	t.Run("href alone when equal to the text", func(t *testing.T) {
		l := renderEmail(t, doc(emailPara(emailText("https://example.com/", emailLink("https://example.com/")))), emailOpts(nil))
		if l.Text != "https://example.com/" {
			t.Errorf("text = %q", l.Text)
		}
	})

	t.Run("a link split by marks is written once", func(t *testing.T) {
		const href = "https://example.com/p"
		l := renderEmail(t, doc(emailPara(
			emailText("ab", emailLink(href)),
			emailText("cd", `{"type":"bold"}`, emailLink(href)),
			emailText(" tail"),
		)), emailOpts(nil))
		if want := "abcd (" + href + ") tail"; l.Text != want {
			t.Errorf("text = %q, want %q", l.Text, want)
		}
	})

	t.Run("marks nest with the link outermost", func(t *testing.T) {
		l := renderEmail(t, doc(emailPara(emailText("m", `{"type":"bold"}`, `{"type":"italic"}`, `{"type":"code"}`, emailLink("mailto:a")))), emailOpts(nil))
		want := `<a class="x-a" href="mailto:a"><code class="x-code"><em><strong>m</strong></em></code></a>`
		if !strings.Contains(string(l.HTML), want) {
			t.Errorf("html = %s, want it to hold %s", l.HTML, want)
		}
	})
}

func TestRenderEmailPhotos(t *testing.T) {
	refuse := func(string, int) (string, bool) { return "", false }
	empty := func(string, int) (string, bool) { return "", true }
	placeholder := `<p class="x-photo-placeholder"><a class="x-a" href="https://example.com/articles/1">Photo</a></p>`
	for _, c := range []struct {
		name string
		port func(string, int) (string, bool)
	}{{"nil port", nil}, {"ok false", refuse}, {"empty address", empty}} {
		t.Run(c.name, func(t *testing.T) {
			l := renderEmail(t, doc(emailPic("m-1", 2000, 1000, "alt")), emailOpts(c.port))
			if string(l.HTML) != placeholder {
				t.Errorf("html = %s, want %s", l.HTML, placeholder)
			}
			if want := "Photo: https://example.com/articles/1"; l.Text != want {
				t.Errorf("text = %q, want %q", l.Text, want)
			}
		})
	}

	for _, c := range []struct {
		name         string
		w, h, column int
		wantW, wantH int
	}{
		{"clamped", 2000, 1000, 592, 592, 296},
		{"rounds half up", 1200, 800, 592, 592, 395},
		{"rounds down below half", 10, 3, 4, 4, 1},
		{"rounds up at exactly half", 10, 5, 3, 3, 2},
		{"height never below one", 20000, 1, 592, 592, 1},
		{"narrow image is not scaled up", 300, 200, 592, 300, 200},
		{"exactly the column", 592, 400, 592, 592, 400},
		{"no clamp at zero", 2000, 1000, 0, 2000, 1000},
		{"large without overflow", 20000, 20000, 592, 592, 592},
	} {
		t.Run(c.name, func(t *testing.T) {
			var asked int
			opts := emailOpts(func(id string, w int) (string, bool) { asked = w; return emailPort(id, w) })
			opts.ColumnWidth = c.column
			l := renderEmail(t, doc(emailPic("m-1", c.w, c.h, "")), opts)
			want := fmt.Sprintf(`<p class="x-figure"><img class="x-img" src="https://example.com/m/m-1?w=%d" alt="" width="%d" height="%d"></p>`, c.wantW, c.wantW, c.wantH)
			if string(l.HTML) != want {
				t.Errorf("html = %s, want %s", l.HTML, want)
			}
			if asked != c.wantW {
				t.Errorf("port asked for width %d, want %d", asked, c.wantW)
			}
		})
	}

	t.Run("no article address", func(t *testing.T) {
		opts := emailOpts(nil)
		opts.ArticleURL = ""
		l := renderEmail(t, doc(emailPic("m-1", 10, 10, "")), opts)
		if want := `<p class="x-photo-placeholder">Photo</p>`; string(l.HTML) != want {
			t.Errorf("html = %s, want %s", l.HTML, want)
		}
		if l.Text != "Photo" {
			t.Errorf("text = %q", l.Text)
		}
	})
}

func TestRenderEmailGallery(t *testing.T) {
	body := doc(emailGallery(emailItem("g-1", 800, 1200), emailItem("g-2", 1200, 800), emailItem("g-3", 100, 100)))

	t.Run("one image per item in order", func(t *testing.T) {
		l := renderEmail(t, body, emailOpts(emailPort))
		want := strings.Join([]string{
			`<p class="x-figure"><img class="x-img" src="https://example.com/m/g-1?w=592" alt="" width="592" height="888"></p>`,
			`<p class="x-figure"><img class="x-img" src="https://example.com/m/g-2?w=592" alt="" width="592" height="395"></p>`,
			`<p class="x-figure"><img class="x-img" src="https://example.com/m/g-3?w=100" alt="" width="100" height="100"></p>`,
		}, "\n")
		if string(l.HTML) != want {
			t.Errorf("html = %s\nwant %s", l.HTML, want)
		}
		if want := "Gallery: https://example.com/articles/1"; l.Text != want {
			t.Errorf("text = %q, want %q", l.Text, want)
		}
	})

	t.Run("one placeholder line without a port", func(t *testing.T) {
		l := renderEmail(t, body, emailOpts(nil))
		want := `<p class="x-photo-placeholder"><a class="x-a" href="https://example.com/articles/1">Gallery</a></p>`
		if string(l.HTML) != want {
			t.Errorf("html = %s, want %s", l.HTML, want)
		}
	})

	t.Run("one placeholder line when an item has no address", func(t *testing.T) {
		port := func(id string, w int) (string, bool) {
			if id == "g-2" {
				return "", false
			}
			return emailPort(id, w)
		}
		l := renderEmail(t, body, emailOpts(port))
		if strings.Contains(string(l.HTML), "<img") || strings.Count(string(l.HTML), "<p") != 1 {
			t.Errorf("html = %s, want a single placeholder", l.HTML)
		}
	})
}

func TestRenderEmailLists(t *testing.T) {
	li := func(s string) string { return `{"type":"listItem","content":[` + emailPara(emailText(s)) + `]}` }
	body := doc(
		`{"type":"orderedList","attrs":{"start":3,"type":"a"},"content":[` + li("three") + "," + li("four") + `]},` +
			`{"type":"orderedList","content":[` + li("one") + `]},` +
			`{"type":"bulletList","content":[` + li("dot") + "," +
			`{"type":"listItem","content":[` + emailPara(emailText("outer")) + `,{"type":"orderedList","content":[` + li("inner") + `]}]}]}`)
	l := renderEmail(t, body, emailOpts(nil))
	out := string(l.HTML)
	for _, want := range []string{`<ol class="x-ol" start="3" type="a">`, `<ol class="x-ol">`, `<ul class="x-ul">`, "<li>\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("html lacks %s:\n%s", want, out)
		}
	}
	wantText := strings.Join([]string{
		"3. three",
		"4. four",
		"",
		"1. one",
		"",
		"- dot",
		"- outer",
		"  1. inner",
	}, "\n")
	if l.Text != wantText {
		t.Errorf("text =\n%s\nwant\n%s", l.Text, wantText)
	}
}

func TestRenderEmailTextBlocks(t *testing.T) {
	body := doc(
		`{"type":"heading","attrs":{"level":2},"content":[` + emailText("Head") + `]},` +
			`{"type":"paragraph"},` +
			emailPara(emailText("one"), `{"type":"hardBreak"}`, emailText("two")) + "," +
			`{"type":"blockquote","content":[` + emailPara(emailText("q1")) + "," + emailPara(emailText("q2")) + `]},` +
			`{"type":"horizontalRule"},` +
			`{"type":"callout","content":[` + emailPara(emailText("c1")) + "," + emailPara(emailText("c2")) + `]}`)
	l := renderEmail(t, body, emailOpts(nil))
	want := "Head\n\none\ntwo\n\n> q1\n>\n> q2\n\n---\n\nc1\n\nc2"
	if l.Text != want {
		t.Errorf("text = %q, want %q", l.Text, want)
	}

	// A quote whose text is only ">" keeps it; an empty quote is left out.
	body = doc(
		`{"type":"blockquote","content":[` + emailPara(emailText(">")) + `]},` +
			`{"type":"blockquote","content":[{"type":"paragraph"}]},` +
			emailPara(emailText("end")))
	l = renderEmail(t, body, emailOpts(nil))
	if want := "> >\n\nend"; l.Text != want {
		t.Errorf("text = %q, want %q", l.Text, want)
	}
}

// The unit is public: its sources hold no non-ASCII text (no natural-language copy beyond English).
func TestRenderEmailSourceIsPlainASCII(t *testing.T) {
	for _, name := range []string{"email.go", "email_test.go"} {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for i, c := range b {
			if c >= 0x80 {
				t.Errorf("%s: non-ASCII byte at offset %d", name, i)
				break
			}
		}
	}
}
