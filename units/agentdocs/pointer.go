package agentdocs

import (
	"bytes"
	"os"
	"strings"
)

// UpsertPointer writes body into path as one block delimited by
// "<!-- <name>:pointer:start -->" and "<!-- <name>:pointer:end -->".
//
// An absent file is created holding the block. A file without the block gets
// it appended after one blank line. A file with the block gets that block
// replaced, and every byte outside it is left as it was. changed is false,
// and nothing is written, when the file already holds exactly this block.
//
// A start marker may carry an annotation before its "-->" (a2a's
// "<!-- a2ahub:pointer:start (managed by ...) -->"); it is recognized as the
// block and rewritten in the plain form. Markers that do not delimit exactly
// one block, or a body that carries a marker itself, wrap ErrPointerMalformed
// and nothing is written. Whether to touch a file at all (a CLAUDE.md that
// already imports @AGENTS.md, a CLAUDE.md that does not exist) is the
// caller's policy.
func UpsertPointer(path, name, body string) (changed bool, err error) {
	start := "<!-- " + name + ":pointer:start -->"
	end := "<!-- " + name + ":pointer:end -->"
	startPrefix := "<!-- " + name + ":pointer:start"
	if strings.Contains(body, startPrefix) || strings.Contains(body, end) {
		return false, &Error{Op: "UpsertPointer", Input: path, Err: ErrPointerMalformed}
	}
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	block := []byte(start + "\n" + body + end)

	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, &Error{Op: "UpsertPointer", Input: path, Err: err}
	}

	starts := findStarts(existing, startPrefix)
	ends := allIndex(existing, []byte(end))
	var out []byte
	switch {
	case len(starts) == 0 && len(ends) == 0:
		out = append(out, existing...)
		if len(existing) > 0 {
			if !bytes.HasSuffix(out, []byte("\n")) {
				out = append(out, '\n')
			}
			out = append(out, '\n')
		}
		out = append(out, block...)
		out = append(out, '\n')
	case len(starts) == 1 && len(ends) == 1 && ends[0] >= starts[0][1]:
		out = append(out, existing[:starts[0][0]]...)
		out = append(out, block...)
		out = append(out, existing[ends[0]+len(end):]...)
	default:
		return false, &Error{Op: "UpsertPointer", Input: path, Err: ErrPointerMalformed}
	}
	if bytes.Equal(out, existing) {
		return false, nil
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return false, &Error{Op: "UpsertPointer", Input: path, Err: err}
	}
	return true, nil
}

// findStarts returns the [begin, end) span of every start marker: prefix
// followed by " -->", or by a space, an annotation and "-->" on the same line.
// A prefix followed by anything else (a longer name) is not a start marker.
func findStarts(data []byte, prefix string) [][2]int {
	var spans [][2]int
	for _, i := range allIndex(data, []byte(prefix)) {
		rest := data[i+len(prefix):]
		if len(rest) == 0 || rest[0] != ' ' {
			continue
		}
		line := rest
		if nl := bytes.IndexByte(rest, '\n'); nl >= 0 {
			line = rest[:nl]
		}
		if c := bytes.Index(line, []byte("-->")); c >= 0 {
			spans = append(spans, [2]int{i, i + len(prefix) + c + len("-->")})
		}
	}
	return spans
}

func allIndex(data, sub []byte) []int {
	var at []int
	for off := 0; ; {
		i := bytes.Index(data[off:], sub)
		if i < 0 {
			return at
		}
		at = append(at, off+i)
		off += i + len(sub)
	}
}
