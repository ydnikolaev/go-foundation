package agentdocs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Containment. Every function that touches the disk takes the root the caller
// names and a path inside it, and reaches the disk only through an os.Root
// opened on that root: os.Root follows a symlink that stays inside and
// refuses one that leaves (or is absolute), on every call, so a repository
// that links CLAUDE.md to ~/.bashrc cannot route a write there.
//
// os.Root does not export its refusal ("path escapes from parent"), so a
// caller recognizes an escape by the *Error naming the path, not by
// errors.Is. The lexical half below says the same thing in its own words.

// errEscapesRoot is a path argument, or a symlink destination this package
// computes, that leaves the root lexically.
var errEscapesRoot = errors.New("path escapes the root")

// local refuses a path argument that is empty, absolute or climbs out with
// "..".
func local(op, name string) error {
	if !filepath.IsLocal(name) {
		return &Error{Op: op, Input: name, Err: errEscapesRoot}
	}
	return nil
}

// maxLinkHops bounds resolve, as the kernel bounds a lookup.
const maxLinkHops = 40

// resolve returns name with every symlink in it replaced by what it points
// at, inside the root, so a caller can name the real file: the rename that
// replaces a file must not replace the link to it, and a relative link must
// be computed from the directory it really lands in. A link that is absolute
// or leads out of the root fails. A component that does not exist ends the
// walk; the rest is returned as spelled.
//
// It names; it does not guard. Every access still goes through r, which
// enforces containment on its own when the tree changes underneath.
func resolve(r *os.Root, name string) (string, error) {
	for hops := 0; ; {
		parts := strings.Split(filepath.Clean(name), string(filepath.Separator))
		done, next := "", ""
		for i, c := range parts {
			if c == "." {
				continue
			}
			p := filepath.Join(done, c)
			info, err := r.Lstat(p)
			if errors.Is(err, fs.ErrNotExist) {
				return filepath.Join(append([]string{p}, parts[i+1:]...)...), nil
			}
			if err != nil {
				return "", err
			}
			if info.Mode()&fs.ModeSymlink == 0 {
				done = p
				continue
			}
			if hops++; hops > maxLinkHops {
				return "", fmt.Errorf("%s: too many links", p)
			}
			dest, err := r.Readlink(p)
			if err != nil {
				return "", err
			}
			next = filepath.Join(append([]string{done, dest}, parts[i+1:]...)...)
			if filepath.IsAbs(dest) || !filepath.IsLocal(next) {
				return "", fmt.Errorf("%s: %w", p, errEscapesRoot)
			}
			break
		}
		if next == "" {
			if done == "" {
				return ".", nil
			}
			return done, nil
		}
		name = next
	}
}
