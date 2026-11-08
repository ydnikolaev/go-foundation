package agentdocs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Surface is one verified agent-runtime fact row: where that runtime
// discovers a skill (SkillsHome) and where it reads always-on context
// (ContextFile), plus the row's own provenance (SourceURL, VerifiedOn) so it
// is re-verified against the source instead of recalled.
type Surface struct {
	// ID is the stable identifier ("claude", "codex", "dsh").
	ID string
	// SkillsHome is the repo-relative directory the runtime scans for skills.
	SkillsHome string
	// ContextFile is the repo-relative file the runtime reads as always-on
	// context.
	ContextFile string
	// ReadsAgentsMD is true when the runtime reads AGENTS.md as always-on
	// context. Claude Code does not; it reads CLAUDE.md.
	ReadsAgentsMD bool
	// SourceURL is the page the row was read from.
	SourceURL string
	// VerifiedOn is the date (YYYY-MM-DD) the row was last verified there.
	VerifiedOn string
}

// Surfaces returns every known row in a fixed order: claude, codex, dsh.
// These are a2ahub's verified rows, carried with their SourceURL and
// VerifiedOn unchanged. Adding a runtime means adding a row here, verified
// against that runtime's own current docs. The slice is the caller's.
func Surfaces() []Surface {
	return []Surface{
		{
			ID:            "claude",
			SkillsHome:    ".claude/skills",
			ContextFile:   "CLAUDE.md",
			ReadsAgentsMD: false,
			SourceURL:     "https://code.claude.com/docs/en/skills",
			VerifiedOn:    "2026-07-23",
		},
		{
			ID:            "codex",
			SkillsHome:    ".codex/skills",
			ContextFile:   "AGENTS.md",
			ReadsAgentsMD: true,
			SourceURL:     "https://developers.openai.com/codex/skills",
			VerifiedOn:    "2026-07-23",
		},
		{
			ID:            "dsh",
			SkillsHome:    ".dsh/skills",
			ContextFile:   "AGENTS.md",
			ReadsAgentsMD: true,
			SourceURL:     "https://github.com/deepseek-ai/deepseek-harness/blob/0a53fb55bea101816fa226bb964ae2bed71c343b/packages/skill/skill-filesystem/src/index.ts",
			VerifiedOn:    "2026-08-30",
		},
	}
}

// ByID returns the row for id.
func ByID(id string) (Surface, bool) {
	for _, s := range Surfaces() {
		if s.ID == id {
			return s, true
		}
	}
	return Surface{}, false
}

// MarkerDir is the directory whose presence under a repository root means the
// surface is in use: the parent of SkillsHome (".claude").
func (s Surface) MarkerDir() string { return filepath.Dir(s.SkillsHome) }

// Detect returns every row whose MarkerDir exists as a directory under root,
// in Surfaces' order. It is best-effort and never fails: a missing root, an
// unreadable entry, or a marker directory that links out of root is simply
// not detected. It never invents a harness directory a repository has not
// opted into.
func Detect(root string) []Surface {
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil
	}
	defer func() { _ = r.Close() }()
	var found []Surface
	for _, s := range Surfaces() {
		info, err := r.Stat(s.MarkerDir())
		if err != nil || !info.IsDir() {
			continue
		}
		found = append(found, s)
	}
	return found
}

// LinkMode records how Link installed an entry.
type LinkMode string

const (
	// LinkSymlink is a relative symlink to the installed tree.
	LinkSymlink LinkMode = "symlink"
	// LinkStub is a stub SKILL.md pointing at the installed tree, written
	// where a symlink cannot be created (Windows without Developer Mode). The
	// runtime fallback is the cross-platform mechanism; there is no build tag.
	LinkStub LinkMode = "stub"
)

// LinkResult describes one installed entry.
type LinkResult struct {
	// Surface is the row the entry was installed for.
	Surface Surface
	// Path is the repo-relative entry, "<SkillsHome>/<name>".
	Path string
	// Mode is how it was installed.
	Mode LinkMode
}

// symlink is a seam so a test can force the stub fallback.
var symlink = func(r *os.Root, oldname, newname string) error { return r.Symlink(oldname, newname) }

// linkMarker is the line a stub SKILL.md carries: "<!-- <name>-skill-link -->".
func linkMarker(name string) string { return "<!-- " + name + "-skill-link -->" }

// Link installs <root>/<s.SkillsHome>/<name>, pointing at the installed tree
// at <root>/<target>. It tries a relative symlink first and falls back to a
// stub SKILL.md carrying name's link marker.
//
// An existing entry is this tool's when it is a symlink resolving exactly to
// target, or carries the link marker; it is then replaced. Any other existing
// entry wraps ErrForeignTarget and nothing is written, unless force is set.
// A name that is not one path element wraps ErrInvalidName.
//
// Nothing Link touches leaves root, symlinks included: a skills home that
// links out of root, or a target outside it (lexically, or through a
// symlink), is refused with an *Error naming it. A skills home that is a
// symlink inside root works, and the relative link is computed from where the
// entry really lands. An existing entry that links out of root is foreign,
// and force replaces the link without following it.
func Link(root string, s Surface, name, target string, force bool) (LinkResult, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return LinkResult{}, &Error{Op: "Link", Input: name, Err: ErrInvalidName}
	}
	if err := local("Link", target); err != nil {
		return LinkResult{}, err
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return LinkResult{}, &Error{Op: "Link", Input: root, Err: err}
	}
	defer func() { _ = r.Close() }()
	// The target may not exist yet; it may not resolve out of root.
	if _, err := r.Stat(target); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return LinkResult{}, &Error{Op: "Link", Input: target, Err: err}
	}
	home, err := resolve(r, s.SkillsHome)
	if err != nil {
		return LinkResult{}, &Error{Op: "Link", Input: s.SkillsHome, Err: err}
	}
	entry := filepath.Join(home, name)
	rel := filepath.Join(s.SkillsHome, name)

	exists, owned, err := linkState(r, entry, name, target)
	if err != nil {
		return LinkResult{}, &Error{Op: "Link", Input: entry, Err: err}
	}
	if exists && !owned && !force {
		return LinkResult{}, &Error{Op: "Link", Input: entry, Err: ErrForeignTarget}
	}
	if exists {
		if err := r.RemoveAll(entry); err != nil {
			return LinkResult{}, &Error{Op: "Link", Input: entry, Err: err}
		}
	}
	if err := r.MkdirAll(home, 0o755); err != nil {
		return LinkResult{}, &Error{Op: "Link", Input: home, Err: err}
	}

	if dest, relErr := filepath.Rel(home, target); relErr == nil {
		if symlink(r, dest, entry) == nil {
			return LinkResult{Surface: s, Path: rel, Mode: LinkSymlink}, nil
		}
	}

	// Stub fallback. Only this branch creates the entry as a directory: a
	// directory made before the symlink would make the symlink fail.
	if err := r.MkdirAll(entry, 0o755); err != nil {
		return LinkResult{}, &Error{Op: "Link", Input: entry, Err: err}
	}
	if err := r.WriteFile(filepath.Join(entry, "SKILL.md"), []byte(linkStub(s, name, target)), 0o644); err != nil {
		return LinkResult{}, &Error{Op: "Link", Input: entry, Err: err}
	}
	return LinkResult{Surface: s, Path: rel, Mode: LinkStub}, nil
}

// linkStub renders the fallback SKILL.md: valid Agent-Skill frontmatter
// first (the format requires "---" to lead), then the marker and a
// repo-root-relative pointer to the real tree.
func linkStub(s Surface, name, target string) string {
	skillMD := filepath.ToSlash(filepath.Join(target, "SKILL.md"))
	return "---\n" +
		"name: " + name + "\n" +
		"description: The " + name + " skill — pointer only; the real content lives at " + skillMD + ".\n" +
		"---\n\n" +
		linkMarker(name) + "\n" +
		"# " + name + " (link)\n\n" +
		"This is a pointer, not the skill content: a symlink from " + s.SkillsHome + "/" + name +
		" could not be created on this filesystem. Read `" + skillMD + "` (repo root).\n"
}

// linkState inspects an existing or absent entry, a resolved path inside r.
// An unreadable or unresolvable symlink, one that links out of the root, or a
// missing marker, is foreign, not an error; err is only a genuine filesystem
// failure.
func linkState(r *os.Root, entry, name, target string) (exists, owned bool, err error) {
	info, err := r.Lstat(entry)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, false, nil
		}
		return false, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		dest, err := r.Readlink(entry)
		if err != nil || filepath.IsAbs(dest) {
			return true, false, nil
		}
		// The same file when both resolve inside the root; otherwise the
		// lexical destination, for a target that is temporarily missing.
		if d, err := r.Stat(entry); err == nil {
			if w, err := r.Stat(target); err == nil {
				return true, os.SameFile(d, w), nil
			}
		}
		return true, filepath.Join(filepath.Dir(entry), dest) == filepath.Clean(target), nil
	}
	stub := entry
	if info.IsDir() {
		stub = filepath.Join(entry, "SKILL.md")
	}
	data, err := r.ReadFile(stub)
	if err != nil {
		return true, false, nil
	}
	// Presence, not prefix: the frontmatter must lead the stub.
	return true, strings.Contains(string(data), linkMarker(name)), nil
}
