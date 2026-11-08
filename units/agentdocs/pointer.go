package agentdocs

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// UpsertPointer writes body into path, a file inside root, as one block
// delimited by "<!-- <name>:pointer:start -->" and
// "<!-- <name>:pointer:end -->".
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
//
// Nothing UpsertPointer touches leaves root, symlinks included. A path that
// is a symlink inside root (CLAUDE.md -> AGENTS.md) writes the file it points
// at and stays a symlink; one that leads out of root, or a path that climbs
// out, is refused with an *Error naming it. The file is replaced through a
// temporary file and a rename in its own directory, so a crash never leaves
// half a file, and it keeps its permission bits.
func UpsertPointer(root, path, name, body string) (changed bool, err error) {
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

	if err := local("UpsertPointer", path); err != nil {
		return false, err
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return false, &Error{Op: "UpsertPointer", Input: root, Err: err}
	}
	defer func() { _ = r.Close() }()
	file, err := resolve(r, path)
	if err != nil {
		return false, &Error{Op: "UpsertPointer", Input: path, Err: err}
	}
	existing, err := r.ReadFile(file)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
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
	if err := replaceFile(r, file, out); err != nil {
		return false, &Error{Op: "UpsertPointer", Input: path, Err: err}
	}
	return true, nil
}

// replaceFile writes data to name inside r through a temporary file in the
// same directory and a rename, keeping name's permission bits (0644 for a new
// file). The rename replaces name itself, so name must already be resolved
// past any symlink. The temporary file is removed on any failure.
func replaceFile(r *os.Root, name string, data []byte) (err error) {
	perm := fs.FileMode(0o644)
	if info, statErr := r.Stat(name); statErr == nil {
		perm = info.Mode().Perm()
	}
	tmp := filepath.Join(filepath.Dir(name), "."+filepath.Base(name)+"."+rand.Text()+".tmp")
	f, err := r.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = f.Close()
			_ = r.Remove(tmp)
		}
	}()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Chmod(perm); err != nil { // the umask applied at create
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return r.Rename(tmp, name)
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
