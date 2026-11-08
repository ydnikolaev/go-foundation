package agentdocs

import (
	"bytes"
	"io/fs"
	"os"
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

// Drift compares the target an Install wrote against tree's dir subtree,
// stripping dir the same way Install does. It only reads. A disk-side
// failure is reported through Undecidable, never as an error; the error is
// reserved for the tree side, and a dir the tree does not hold wraps
// ErrTreeMissing.
//
// How severe drift is stays the caller's call. a2a's doctor fails a Current
// install that drifts (the stamp names this binary, so the tree contradicts
// it) and only notes drift on a Dev install.
func Drift(tree fs.FS, dir, target string) (TreeDrift, error) {
	if info, statErr := fs.Stat(tree, dir); statErr != nil || !info.IsDir() {
		return TreeDrift{}, &Error{Op: "Drift", Input: dir, Err: ErrTreeMissing}
	}
	var res TreeDrift

	onDisk := map[string]bool{}
	walkErr := filepath.WalkDir(target, func(p string, d fs.DirEntry, err error) error {
		rel, relErr := filepath.Rel(target, p)
		if err != nil || relErr != nil {
			if relErr != nil {
				rel = p
			}
			res.Undecidable = filepath.ToSlash(rel)
			return filepath.SkipAll
		}
		if d.IsDir() {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if rel == ProvenanceFile {
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
		got, undecidable := readDisk(filepath.Join(target, filepath.FromSlash(rel)))
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

// readDisk reads path, telling a file that is present but unreadable (a
// dangling symlink, a permission denial) apart from one Lstat cannot find.
// The first is undecidable, never reclassified as missing.
func readDisk(path string) (data []byte, undecidable bool) {
	if _, err := os.Lstat(path); err != nil {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, true
	}
	return data, false
}
