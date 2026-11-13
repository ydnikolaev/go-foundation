package tiptap

import (
	"html"
	"html/template"
	"strconv"
	"strings"
)

// EmailOptions carries every value of a letter that belongs to the caller: the
// unit holds no class name, word, link or image address of its own.
type EmailOptions struct {
	// ImageURL returns an absolute address of a rendition of the media item
	// about width pixels wide. A nil func, or ok == false (or an empty address),
	// means the photo has no address and renders as a placeholder.
	ImageURL func(mediaID string, width int) (src string, ok bool)
	// ArticleURL is the absolute address a placeholder links to; the text part
	// prints it after the photo label.
	ArticleURL string
	// Labels are the words of the placeholder and of the text part's photo line.
	Labels EmailLabels
	// ColumnWidth is the letter's column in pixels: an image is never rendered
	// wider than it, and its height keeps the ratio. 0 means no clamp.
	ColumnWidth int
	// ClassPrefix is put before every class name. Empty: no class attribute.
	ClassPrefix string
}

// EmailLabels are the caller's words for a photo and a gallery that have no
// image address.
type EmailLabels struct {
	Photo, Gallery string
}

// Letter is the two parts of a rendered body. HTML is a fragment of semantic
// elements for the caller's layout to wrap; it carries no style attribute.
type Letter struct {
	HTML template.HTML
	Text string
}

// RenderEmail renders a body of the Email profile as a letter. A body the
// profile refuses is returned as Validate would refuse it (ErrNotAllowed).
// The same body and options give byte-identical output.
func RenderEmail(body []byte, opts EmailOptions) (Letter, error) {
	root, _, err := validate(body, Email)
	if err != nil {
		return Letter{}, err
	}
	r := &emailRenderer{opts: opts}
	var b strings.Builder
	r.blocks(&b, root.Content)
	return Letter{
		// Every text and attribute value above went through html.EscapeString.
		HTML: template.HTML(strings.TrimRight(b.String(), "\n")),
		Text: strings.Join(r.textBlocks(root.Content), "\n\n"),
	}, nil
}

type emailRenderer struct {
	opts EmailOptions
}

// externalRel is what an http(s) link in user content carries.
const externalRel = "ugc nofollow noopener noreferrer"

func esc(s string) string { return html.EscapeString(s) }

// cls is the class attribute for the given names, or nothing without a prefix.
func (r *emailRenderer) cls(names ...string) string {
	if r.opts.ClassPrefix == "" || len(names) == 0 {
		return ""
	}
	prefixed := make([]string, len(names))
	for i, n := range names {
		prefixed[i] = r.opts.ClassPrefix + n
	}
	return ` class="` + esc(strings.Join(prefixed, " ")) + `"`
}

// intAttr reads a validated integer attribute.
func intAttr(v any) (int, bool) {
	f, ok := number(v)
	if !ok {
		return 0, false
	}
	return int(f), true
}

func strAttr(n Node, name string) string {
	s, _ := n.Attrs[name].(string)
	return s
}

// linkHref is the href of a link mark the letter may emit: http(s) or mailto
// only, whatever the document says.
func linkHref(marks []Mark) (string, bool) {
	for _, m := range marks {
		if m.Type != "link" {
			continue
		}
		href, _ := m.Attrs["href"].(string)
		if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") || strings.HasPrefix(href, "mailto:") {
			return href, true
		}
		return "", false
	}
	return "", false
}

func hasMark(marks []Mark, typ string) bool {
	for _, m := range marks {
		if m.Type == typ {
			return true
		}
	}
	return false
}

// ---- HTML ----

func (r *emailRenderer) blocks(b *strings.Builder, nodes []Node) {
	for _, n := range nodes {
		r.block(b, n)
	}
}

func (r *emailRenderer) wrap(b *strings.Builder, tag, class string, n Node, inline bool) {
	b.WriteString("<" + tag + r.cls(class) + ">")
	if inline {
		r.inlines(b, n.Content)
	} else {
		b.WriteString("\n")
		r.blocks(b, n.Content)
	}
	b.WriteString("</" + tag + ">\n")
}

func (r *emailRenderer) block(b *strings.Builder, n Node) {
	switch n.Type {
	case "paragraph":
		r.wrap(b, "p", "p", n, true)
	case "heading":
		if lvl, _ := intAttr(n.Attrs["level"]); lvl == 2 {
			r.wrap(b, "h2", "h2", n, true)
		} else {
			r.wrap(b, "h3", "h3", n, true)
		}
	case "blockquote":
		r.wrap(b, "blockquote", "quote", n, false)
	case "bulletList":
		r.wrap(b, "ul", "ul", n, false)
	case "orderedList":
		b.WriteString("<ol" + r.cls("ol"))
		if start, ok := intAttr(n.Attrs["start"]); ok {
			b.WriteString(` start="` + strconv.Itoa(start) + `"`)
		}
		if typ := strAttr(n, "type"); typ != "" {
			b.WriteString(` type="` + esc(typ) + `"`)
		}
		b.WriteString(">\n")
		r.blocks(b, n.Content)
		b.WriteString("</ol>\n")
	case "listItem":
		b.WriteString("<li>\n")
		r.blocks(b, n.Content)
		b.WriteString("</li>\n")
	case "horizontalRule":
		b.WriteString("<hr" + r.cls("hr") + ">\n")
	case "callout":
		tone := strAttr(n, "tone")
		if tone == "" {
			tone = "info"
		}
		b.WriteString(`<table role="presentation"` + r.cls("callout", "callout-"+tone) + ">\n<tr><td>\n")
		r.blocks(b, n.Content)
		b.WriteString("</td></tr>\n</table>\n")
	case "image":
		r.photo(b, strAttr(n, "mediaId"), n.Attrs["width"], n.Attrs["height"], strAttr(n, "alt"))
	case "gallery":
		r.gallery(b, n)
	default:
		r.blocks(b, n.Content)
	}
}

func (r *emailRenderer) inlines(b *strings.Builder, nodes []Node) {
	for _, n := range nodes {
		switch n.Type {
		case "text":
			r.text(b, n)
		case "hardBreak":
			b.WriteString("<br>")
		}
	}
}

// text writes a text node with its marks: bold innermost, then italic, code,
// and the link outermost (the order of the web renderer).
func (r *emailRenderer) text(b *strings.Builder, n Node) {
	var closes []string
	if href, ok := linkHref(n.Marks); ok {
		b.WriteString(`<a` + r.cls("a") + ` href="` + esc(href) + `"`)
		if !strings.HasPrefix(href, "mailto:") {
			b.WriteString(` target="_blank" rel="` + externalRel + `"`)
		}
		b.WriteString(">")
		closes = append(closes, "</a>")
	}
	if hasMark(n.Marks, "code") {
		b.WriteString("<code" + r.cls("code") + ">")
		closes = append(closes, "</code>")
	}
	if hasMark(n.Marks, "italic") {
		b.WriteString("<em>")
		closes = append(closes, "</em>")
	}
	if hasMark(n.Marks, "bold") {
		b.WriteString("<strong>")
		closes = append(closes, "</strong>")
	}
	b.WriteString(esc(n.Text))
	for i := len(closes) - 1; i >= 0; i-- {
		b.WriteString(closes[i])
	}
}

// size is the rendered width and height of an image: clamped to the column,
// the height scaled by integer math, rounded half up.
func (r *emailRenderer) size(width, height any) (w, h int) {
	w, _ = intAttr(width)
	h, _ = intAttr(height)
	if c := r.opts.ColumnWidth; c > 0 && w > c {
		h = int((2*int64(h)*int64(c) + int64(w)) / (2 * int64(w)))
		w, h = c, max(h, 1)
	}
	return w, h
}

func (r *emailRenderer) src(mediaID string, w int) (string, bool) {
	if r.opts.ImageURL == nil {
		return "", false
	}
	src, ok := r.opts.ImageURL(mediaID, w)
	return src, ok && src != ""
}

func (r *emailRenderer) img(b *strings.Builder, src string, w, h int, alt string) {
	b.WriteString("<p" + r.cls("figure") + "><img" + r.cls("img") + ` src="` + esc(src) + `" alt="` + esc(alt) +
		`" width="` + strconv.Itoa(w) + `" height="` + strconv.Itoa(h) + `"></p>` + "\n")
}

func (r *emailRenderer) placeholder(b *strings.Builder, label string) {
	b.WriteString("<p" + r.cls("photo-placeholder") + ">")
	if r.opts.ArticleURL != "" {
		b.WriteString("<a" + r.cls("a") + ` href="` + esc(r.opts.ArticleURL) + `">` + esc(label) + "</a>")
	} else {
		b.WriteString(esc(label))
	}
	b.WriteString("</p>\n")
}

func (r *emailRenderer) photo(b *strings.Builder, mediaID string, width, height any, alt string) {
	w, h := r.size(width, height)
	if src, ok := r.src(mediaID, w); ok {
		r.img(b, src, w, h, alt)
		return
	}
	r.placeholder(b, r.opts.Labels.Photo)
}

// gallery renders one image per item, one per row, when every item has an
// address; otherwise one placeholder line stands for the whole gallery.
func (r *emailRenderer) gallery(b *strings.Builder, n Node) {
	type shot struct {
		src  string
		w, h int
		alt  string
	}
	items, _ := n.Attrs["items"].([]any)
	shots := make([]shot, 0, len(items))
	for _, item := range items {
		fields, _ := item.(map[string]any)
		w, h := r.size(fields["width"], fields["height"])
		id, _ := fields["mediaId"].(string)
		src, ok := r.src(id, w)
		if !ok {
			r.placeholder(b, r.opts.Labels.Gallery)
			return
		}
		alt, _ := fields["alt"].(string)
		shots = append(shots, shot{src, w, h, alt})
	}
	for _, s := range shots {
		r.img(b, s.src, s.w, s.h, s.alt)
	}
}

// ---- text/plain ----

// textBlocks returns the text of each block-level node; the caller joins them.
func (r *emailRenderer) textBlocks(nodes []Node) []string {
	var out []string
	for _, n := range nodes {
		var s string
		switch n.Type {
		case "paragraph", "heading":
			var b strings.Builder
			r.textInlines(&b, n.Content)
			s = b.String()
		case "blockquote":
			inner := strings.Join(r.textBlocks(n.Content), "\n\n")
			if strings.TrimSpace(inner) == "" {
				break
			}
			lines := strings.Split(inner, "\n")
			for i, l := range lines {
				lines[i] = strings.TrimRight("> "+l, " ")
			}
			s = strings.Join(lines, "\n")
		case "bulletList":
			s = r.textList(n, func(int) string { return "- " })
		case "orderedList":
			start, ok := intAttr(n.Attrs["start"])
			if !ok {
				start = 1
			}
			s = r.textList(n, func(i int) string { return strconv.Itoa(start+i) + ". " })
		case "listItem":
			s = strings.Join(r.textBlocks(n.Content), "\n")
		case "horizontalRule":
			s = "---"
		case "callout":
			s = strings.Join(r.textBlocks(n.Content), "\n\n")
		case "image":
			s = labelLine(r.opts.Labels.Photo, r.opts.ArticleURL)
		case "gallery":
			s = labelLine(r.opts.Labels.Gallery, r.opts.ArticleURL)
		default:
			s = strings.Join(r.textBlocks(n.Content), "\n\n")
		}
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// textList prefixes the first line of each item with its marker and indents
// the rest by the marker's width.
func (r *emailRenderer) textList(n Node, marker func(i int) string) string {
	var items []string
	for i, item := range n.Content {
		m := marker(i)
		lines := strings.Split(strings.Join(r.textBlocks(item.Content), "\n"), "\n")
		for j, l := range lines {
			switch {
			case j == 0:
				lines[j] = strings.TrimRight(m+l, " ")
			case l != "":
				lines[j] = strings.Repeat(" ", len(m)) + l
			}
		}
		items = append(items, strings.Join(lines, "\n"))
	}
	return strings.Join(items, "\n")
}

func labelLine(label, url string) string {
	switch {
	case label != "" && url != "":
		return label + ": " + url
	case label != "":
		return label
	default:
		return url
	}
}

// textInlines writes the text of inline nodes, marks dropped. A run of text
// nodes under the same link is written once as `text (href)`, or as the href
// alone when it equals the text.
func (r *emailRenderer) textInlines(b *strings.Builder, nodes []Node) {
	for i := 0; i < len(nodes); i++ {
		n := nodes[i]
		switch n.Type {
		case "hardBreak":
			b.WriteString("\n")
		case "text":
			href, linked := linkHref(n.Marks)
			if !linked {
				b.WriteString(n.Text)
				continue
			}
			text := n.Text
			for i+1 < len(nodes) && nodes[i+1].Type == "text" {
				next, ok := linkHref(nodes[i+1].Marks)
				if !ok || next != href {
					break
				}
				text += nodes[i+1].Text
				i++
			}
			if text == href {
				b.WriteString(href)
			} else {
				b.WriteString(text + " (" + href + ")")
			}
		}
	}
}
