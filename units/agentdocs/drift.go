package agentdocs

import (
	"bytes"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// TreeDrift is Drift's answer: how an installed target differs from the tree
// that would be installed. Paths are slash-separated, relative to the target,
// and sorted.
//
// Undecidable names the first disk path that exists but cannot be read (a
// dangling symlink, a permission denial), or "." for a target that cannot be
// walked at all. When it is set, nothing is classified: Missing, Modified and
// Unexpected are empty and Total counts only the tree files reached before it.
type TreeDrift struct {
	// Total is the number of tree files compared.
	Total int
	// Missing are tree files absent from the target.
	Missing []string
	// Modified are tree files whose bytes on disk differ.
	Modified []string
	// Unexpected are disk files the tree does not hold. The target's own
	// root PROVENANCE.md is the one exemption; a PROVENANCE.md anywhere
	// deeper is unexpected like any other file.
	Unexpected []string
	// Undecidable is the path the comparison could not judge, or "".
	Undecidable string
}

// HasDiff reports whether any file was classified as missing, modified or
// unexpected. Check Undecidable first: an undecidable result has no diff
// because nothing was judged, not because the tree matches.
func (d TreeDrift) HasDiff() bool { return d.Count() > 0 }

// Count is the number of classified files across the three classes.
func (d TreeDrift) Count() int { return len(d.Missing) + len(d.Modified) + len(d.Unexpected) }

// Drift compares the target an Install wrote, a path inside root, against
// tree's dir subtree, stripping dir the same way Install does. It only reads,
// and only inside root. A disk-side failure is reported through Undecidable,
// never as an error: a root that cannot be opened, a target reached through a
// symlink out of root, or a file in it that links out. The error is reserved
// for the arguments: a dir the tree does not hold wraps ErrTreeMissing, and a
// target that climbs out of root is refused naming it.
//
// How severe drift is stays the caller's call. a2a's doctor fails a Current
// install that drifts (the stamp names this binary, so the tree contradicts
// it) and only notes drift on a Dev install.
func Drift(tree fs.FS, dir, root, target string) (TreeDrift, error) {
	if info, statErr := fs.Stat(tree, dir); statErr != nil || !info.IsDir() {
		return TreeDrift{}, &Error{Op: "Drift", Input: dir, Err: ErrTreeMissing}
	}
	if err := local("Drift", target); err != nil {
		return TreeDrift{}, err
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return TreeDrift{Undecidable: "."}, nil
	}
	defer func() { _ = r.Close() }()
	disk := r.FS()
	base := path.Clean(filepath.ToSlash(target))
	var res TreeDrift

	onDisk := map[string]bool{}
	walkErr := fs.WalkDir(disk, base, func(p string, d fs.DirEntry, err error) error {
		rel := under(base, p)
		if err != nil {
			res.Undecidable = rel
			return fs.SkipAll
		}
		if d.IsDir() || rel == ProvenanceFile {
			return nil
		}
		onDisk[rel] = true
		return nil
	})
	if res.Undecidable != "" {
		return res, nil
	}
	if walkErr != nil {
		return res, &Error{Op: "Drift", Input: target, Err: walkErr}
	}

	walkErr = fs.WalkDir(tree, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel := p
		if dir != "." {
			rel = strings.TrimPrefix(strings.TrimPrefix(p, dir), "/")
		}
		res.Total++
		want, err := fs.ReadFile(tree, p)
		if err != nil {
			return err
		}
		if !onDisk[rel] {
			res.Missing = append(res.Missing, rel)
			return nil
		}
		delete(onDisk, rel)
		got, undecidable := readDisk(r, filepath.Join(target, filepath.FromSlash(rel)))
		if undecidable {
			res.Undecidable = rel
			return fs.SkipAll
		}
		if !bytes.Equal(want, got) {
			res.Modified = append(res.Modified, rel)
		}
		return nil
	})
	if res.Undecidable != "" {
		return TreeDrift{Total: res.Total, Undecidable: res.Undecidable}, nil
	}
	if walkErr != nil {
		return res, &Error{Op: "Drift", Input: dir, Err: walkErr}
	}

	for rel := range onDisk {
		res.Unexpected = append(res.Unexpected, rel)
	}
	sort.Strings(res.Missing)
	sort.Strings(res.Modified)
	sort.Strings(res.Unexpected)
	return res, nil
}

// under is p relative to base, both slash paths from one fs.WalkDir.
func under(base, p string) string {
	switch {
	case p == base:
		return "."
	case base == ".":
		return p
	default:
		return strings.TrimPrefix(p, base+"/")
	}
}

// readDisk reads name inside r, telling a file that is present but
// unreadable (a dangling symlink, one that links out of the root, a
// permission denial) apart from one Lstat cannot find. The first is
// undecidable, never reclassified as missing.
func readDisk(r *os.Root, name string) (data []byte, undecidable bool) {
	if _, err := r.Lstat(name); err != nil {
		return nil, false
	}
	data, err := r.ReadFile(name)
	if err != nil {
		return nil, true
	}
	return data, false
}
