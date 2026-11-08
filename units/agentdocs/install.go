package agentdocs

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/mod/semver"
)

// ProvenanceFile is the marker file every Install writes at the target root.
// Its first line identifies the target as this tool's (so a refresh may wipe
// it), and it tells a reader what the tree is and how to refresh it.
const ProvenanceFile = "PROVENANCE.md"

// comparablePrefix and commentSuffix frame the machine line that records the
// comparable version: "<!-- comparable: 1.4.0 -->". It sits in the leading
// comment block, right under the marker, and is the only thing Installed
// compares; the prose below it carries the display version.
const (
	comparablePrefix = "<!-- comparable:"
	commentSuffix    = "-->"
)

// installMarker is the first line of PROVENANCE.md for a tool:
// "<!-- <id>-skill-install -->".
func installMarker(id string) string { return "<!-- " + id + "-skill-install -->" }

// Install mirrors tree's dir subtree into target, a path inside root, and
// writes PROVENANCE.md beside it, returning the number of tree files written
// (PROVENANCE.md is not counted). dir is stripped, so tree's "<dir>/SKILL.md"
// lands at "<target>/SKILL.md"; dir "." installs the whole tree. root must
// exist; target "." installs into root itself.
//
// Nothing Install touches leaves root, symlinks included: a target that
// climbs out, or reaches out through a symlink, is refused with an *Error
// naming it, and a symlink inside the target is removed as a link, never
// followed.
//
// A target that is absent or empty is installed directly. A non-empty target
// carrying this tool's marker is wiped first, so no stale file survives. A
// non-empty target without it is someone else's: Install wraps
// ErrForeignTarget and writes nothing, unless force is set. A Tool with no ID
// wraps ErrToolID, and a dir the tree does not hold wraps ErrTreeMissing, both
// before anything is removed. Install writes only under target.
func Install(tree fs.FS, dir, root, target string, t Tool, force bool) (written int, err error) {
	if t.ID == "" {
		return 0, &Error{Op: "Install", Err: ErrToolID}
	}
	if info, statErr := fs.Stat(tree, dir); statErr != nil || !info.IsDir() {
		return 0, &Error{Op: "Install", Input: dir, Err: ErrTreeMissing}
	}
	if err := local("Install", target); err != nil {
		return 0, err
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return 0, &Error{Op: "Install", Input: root, Err: err}
	}
	defer func() { _ = r.Close() }()
	nonEmpty, owned, err := targetState(r, target, t.ID)
	if err != nil {
		return 0, &Error{Op: "Install", Input: target, Err: err}
	}
	if nonEmpty && !owned && !force {
		return 0, &Error{Op: "Install", Input: target, Err: ErrForeignTarget}
	}
	if nonEmpty {
		if err := wipe(r, target); err != nil {
			return 0, &Error{Op: "Install", Input: target, Err: err}
		}
	}
	written, err = writeTree(tree, dir, r, target)
	if err != nil {
		return written, &Error{Op: "Install", Input: target, Err: err}
	}
	if err := r.WriteFile(filepath.Join(target, ProvenanceFile), []byte(provenance(t)), 0o644); err != nil {
		return written, &Error{Op: "Install", Input: target, Err: err}
	}
	return written, nil
}

// wipe removes target for a refresh. A target that is a symlink is removed as
// the link, so the directory it points at keeps its files. The root itself
// cannot be removed, so target "." loses its entries instead.
func wipe(r *os.Root, target string) error {
	if filepath.Clean(target) != "." {
		return r.RemoveAll(target)
	}
	entries, err := readDir(r, ".")
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := r.RemoveAll(e.Name()); err != nil {
			return err
		}
	}
	return nil
}

func readDir(r *os.Root, name string) ([]fs.DirEntry, error) {
	f, err := r.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return f.ReadDir(-1)
}

// writeTree materializes tree's dir subtree under target, dir stripped.
func writeTree(tree fs.FS, dir string, r *os.Root, target string) (int, error) {
	var count int
	err := fs.WalkDir(tree, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := p
		if dir != "." {
			rel = strings.TrimPrefix(strings.TrimPrefix(p, dir), "/")
		}
		dest := filepath.Join(target, filepath.FromSlash(rel)) // fs paths never hold ".."
		if d.IsDir() {
			return r.MkdirAll(dest, 0o755)
		}
		data, err := fs.ReadFile(tree, p)
		if err != nil {
			return err
		}
		if err := r.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		if err := r.WriteFile(dest, data, 0o644); err != nil {
			return err
		}
		count++
		return nil
	})
	return count, err
}

// provenance renders PROVENANCE.md: the marker, the comparable line, then the
// human note naming the tool, its display version, the source and the
// refresh command.
func provenance(t Tool) string {
	return installMarker(t.ID) + "\n" +
		comparablePrefix + " " + t.Comparable + " " + commentSuffix + "\n" +
		"# " + t.Name + " docs — installed artifact\n\n" +
		"Written by " + t.Name + " " + t.Version + ".\n" +
		"Source: " + t.Source + ".\n\n" +
		"**Do not hand-edit** — refresh with `" + t.Refresh + "`.\n"
}

// targetState inspects target: nonEmpty when it exists and holds an entry,
// owned when its PROVENANCE.md opens with id's marker. An absent or empty
// target is (false, false); an absent or unreadable marker file (one that
// links out of the root is unreadable), or an empty id, is unowned, not an
// error.
func targetState(r *os.Root, target, id string) (nonEmpty, owned bool, err error) {
	entries, err := readDir(r, target)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, false, nil
		}
		return false, false, err
	}
	if len(entries) == 0 {
		return false, false, nil
	}
	data, readErr := r.ReadFile(filepath.Join(target, ProvenanceFile))
	if readErr != nil || id == "" {
		return true, false, nil
	}
	return true, bytes.HasPrefix(data, []byte(installMarker(id))), nil
}

// State is what Installed finds at a target.
type State int

const (
	// StateNone is no install: the target is absent, empty or unreadable.
	StateNone State = iota
	// StateForeign is a non-empty target without this tool's marker. It is
	// someone else's and is not judged.
	StateForeign
	// StateDev is this tool's install where either side has no comparable
	// version (a dev build on either side, a stamp that records none, or a
	// value that is not a version), so nothing can be compared.
	StateDev
	// StateBehind is an install older than the running binary.
	StateBehind
	// StateCurrent is an install of the running binary's version.
	StateCurrent
	// StateAhead is an install newer than the running binary: the binary is
	// older than the copy, and a reinstall would write an older tree.
	StateAhead
)

var stateNames = [...]string{"none", "foreign", "dev", "behind", "current", "ahead"}

func (s State) String() string {
	if s >= 0 && int(s) < len(stateNames) {
		return stateNames[s]
	}
	return fmt.Sprintf("State(%d)", int(s))
}

// Severity is a doctor's verdict, in the shape of adoption.ClockFileStatus's.
type Severity int

const (
	// SeverityOK needs nothing.
	SeverityOK Severity = iota
	// SeverityWarn is advisory: worth a line, never a failing row.
	SeverityWarn
	// SeverityFail is a failing row. No InstallStatus maps to it in this
	// version; it is defined so the scale matches adoption's.
	SeverityFail
)

// InstallStatus is Installed's answer. Version is the comparable version the
// install's stamp records ("" when it records none, or for None and Foreign).
type InstallStatus struct {
	State   State
	Version string
}

// Severity says how a doctor should report s: Dev, Behind and Ahead are Warn;
// None, Foreign and Current are OK. None is OK on purpose: a tool whose
// install is an explicit verb must not warn on a repository that never ran it.
func (s InstallStatus) Severity() Severity {
	switch s.State {
	case StateDev, StateBehind, StateAhead:
		return SeverityWarn
	default:
		return SeverityOK
	}
}

// Installed reports the install at target against the running tool t. It
// compares only t.Comparable with the comparable version the stamp records;
// t.Version is display only. An empty or non-version Comparable on either
// side is Dev, and so is a stamp of this tool's that records no comparable
// line at all (a legacy install): the next Install heals it. Ownership is
// t.ID's marker; an empty ID owns nothing. Installed only reads, and only
// inside root: a target outside it, lexically or through a symlink, is None.
func Installed(root, target string, t Tool) InstallStatus {
	if local("Installed", target) != nil {
		return InstallStatus{State: StateNone}
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return InstallStatus{State: StateNone}
	}
	defer func() { _ = r.Close() }()
	nonEmpty, owned, err := targetState(r, target, t.ID)
	if err != nil || !nonEmpty {
		return InstallStatus{State: StateNone}
	}
	if !owned {
		return InstallStatus{State: StateForeign}
	}
	data, err := r.ReadFile(filepath.Join(target, ProvenanceFile))
	if err != nil {
		return InstallStatus{State: StateNone}
	}
	stamp := stampedComparable(data)
	st := InstallStatus{Version: stamp}
	have, okHave := canonical(stamp)
	want, okWant := canonical(t.Comparable)
	if !okHave || !okWant {
		st.State = StateDev
		return st
	}
	switch c := semver.Compare(have, want); {
	case c < 0:
		st.State = StateBehind
	case c > 0:
		st.State = StateAhead
	default:
		st.State = StateCurrent
	}
	return st
}

// stampedComparable reads the comparable line from PROVENANCE.md's leading
// comment block (every line up to the first that is not an HTML comment).
func stampedComparable(data []byte) string {
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "<!--") {
			return ""
		}
		if strings.HasPrefix(line, comparablePrefix) && strings.HasSuffix(line, commentSuffix) {
			return strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, comparablePrefix), commentSuffix))
		}
	}
	return ""
}

// canonical turns "1.4", "v1.4.0" or "1.5.0-rc.1" into the form semver
// compares, or reports that it is not a version.
func canonical(v string) (string, bool) {
	s := strings.TrimSpace(v)
	if s == "" {
		return "", false
	}
	if !strings.HasPrefix(s, "v") {
		s = "v" + s
	}
	return s, semver.IsValid(s)
}
