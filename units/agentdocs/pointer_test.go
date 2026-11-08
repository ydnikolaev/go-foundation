package agentdocs_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/go-foundation/units/agentdocs"
)

const (
	pointerStart = "<!-- mytool:pointer:start -->"
	pointerEnd   = "<!-- mytool:pointer:end -->"
	pointerBody  = "## mytool\n\n- **Docs:** `.mytool/skill/SKILL.md` (refresh with `mytool skill install`).\n"
)

func upsert(t *testing.T, path, body string) bool {
	t.Helper()
	changed, err := upsertAt(path, "mytool", body)
	if err != nil {
		t.Fatalf("UpsertPointer: %v", err)
	}
	return changed
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Ported: cmd_init_pointer_test.go TestInitAgentsPointer_CreatesWhenAbsent.
func TestUpsertPointerCreatesWhenAbsent(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	if !upsert(t, path, pointerBody) {
		t.Fatal("changed = false on a fresh file")
	}
	want := pointerStart + "\n" + pointerBody + pointerEnd + "\n"
	if got := read(t, path); got != want {
		t.Fatalf("AGENTS.md = %q, want %q", got, want)
	}
}

// Ported: TestInitAgentsPointer_AppendsPreservingExisting and
// TestInitClaudeMd_AppendsPointerWhenExisting: every existing byte survives at
// the head, separated by one blank line.
func TestUpsertPointerAppendsPreservingExisting(t *testing.T) {
	t.Parallel()
	for name, existing := range map[string]string{
		"trailing newline":    "# Consumer AGENTS\n\nMy own rules that must survive.\n",
		"no trailing newline": "# Consumer AGENTS\n\nMy own rules that must survive.",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "AGENTS.md")
			mustWrite(t, path, existing)
			if !upsert(t, path, pointerBody) {
				t.Fatal("changed = false")
			}
			got := read(t, path)
			if !strings.HasPrefix(got, existing) {
				t.Fatalf("existing content not preserved verbatim at the head:\n%s", got)
			}
			if !strings.HasSuffix(got, "survive.\n\n"+pointerStart+"\n"+pointerBody+pointerEnd+"\n") {
				t.Fatalf("block not appended after one blank line:\n%q", got)
			}
		})
	}
}

// Ported: TestInitAgentsPointer_Idempotent. A second run with the same body
// reports no change, adds no second block, and does not write the file.
func TestUpsertPointerIsIdempotent(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	mustWrite(t, path, "# Mine\n")
	upsert(t, path, pointerBody)
	first := read(t, path)
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if upsert(t, path, pointerBody) {
		t.Fatal("changed = true on an identical re-run")
	}
	if got := read(t, path); got != first {
		t.Fatalf("re-run changed the file:\n%q\nwant\n%q", got, first)
	}
	if n := strings.Count(read(t, path), pointerStart); n != 1 {
		t.Fatalf("block appears %d times, want 1", n)
	}
	if info, _ := os.Stat(path); !info.ModTime().Equal(old) {
		t.Errorf("an unchanged upsert wrote the file (mtime %v, want %v)", info.ModTime(), old)
	}
}

// UpsertPointer replaces only its own block: every byte before the start
// marker and after the end marker is identical, including a block that sits
// mid-file and a file that ends without a newline.
func TestUpsertPointerNeverTouchesBytesOutsideItsBlock(t *testing.T) {
	t.Parallel()
	head := "# Mine\r\n\nwindows line endings stay\r\n<!-- other:pointer:start -->\nsomeone else's block\n<!-- other:pointer:end -->\n\n"
	tail := "\n\n## After\ntrailing text with no newline"
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	mustWrite(t, path, head+pointerStart+"\nold body\n"+pointerEnd+tail)

	if !upsert(t, path, pointerBody) {
		t.Fatal("changed = false on a new body")
	}
	want := head + pointerStart + "\n" + pointerBody + pointerEnd + tail
	if got := read(t, path); got != want {
		t.Fatalf("file = %q\nwant  %q", got, want)
	}
	if upsert(t, path, pointerBody) {
		t.Fatal("second upsert changed = true")
	}
}

// A body without a trailing newline still closes the block on its own line.
func TestUpsertPointerBodyWithoutTrailingNewline(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	upsert(t, path, "one line")
	if got, want := read(t, path), pointerStart+"\none line\n"+pointerEnd+"\n"; got != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
	if upsert(t, path, "one line") {
		t.Fatal("re-run changed = true")
	}
}

// a2a's block carries an annotation in its start marker ("(managed by ...)").
// It is recognized as this tool's block and rewritten to the spec form, so
// moving a2a to this unit never appends a second block to a consumer's file.
func TestUpsertPointerRecognizesAnAnnotatedStartMarker(t *testing.T) {
	t.Parallel()
	legacy := "# Consumer\n\n<!-- a2ahub:pointer:start (managed by `a2a init --agents-pointer`) -->\n## a2ahub\nold\n<!-- a2ahub:pointer:end -->\n"
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	mustWrite(t, path, legacy)
	changed, err := upsertAt(path, "a2ahub", "## a2ahub\nnew\n")
	if err != nil || !changed {
		t.Fatalf("UpsertPointer = %v, %v", changed, err)
	}
	want := "# Consumer\n\n<!-- a2ahub:pointer:start -->\n## a2ahub\nnew\n<!-- a2ahub:pointer:end -->\n"
	if got := read(t, path); got != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
}

// A name that is a prefix of another name is not that name's block.
func TestUpsertPointerIgnoresALongerNamesBlock(t *testing.T) {
	t.Parallel()
	other := "<!-- mytoolx:pointer:start -->\nx\n<!-- mytoolx:pointer:end -->\n"
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	mustWrite(t, path, other)
	upsert(t, path, pointerBody)
	if got := read(t, path); !strings.HasPrefix(got, other) || strings.Count(got, pointerStart) != 1 {
		t.Fatalf("file = %q", got)
	}
}

// A file whose markers do not delimit exactly one block is refused and left
// byte-identical: replacing "to the end of the file" would eat the owner's
// bytes, and appending would leave two blocks.
func TestUpsertPointerRefusesMalformedMarkers(t *testing.T) {
	t.Parallel()
	for name, content := range map[string]string{
		"start without end":  "# Mine\n" + pointerStart + "\nbody\n\nthe owner's text\n",
		"end without start":  "# Mine\n" + pointerEnd + "\n",
		"end before start":   pointerEnd + "\n" + pointerStart + "\n",
		"two blocks":         pointerStart + "\na\n" + pointerEnd + "\n" + pointerStart + "\nb\n" + pointerEnd + "\n",
		"start inside block": pointerStart + "\n" + pointerStart + "\n" + pointerEnd + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "AGENTS.md")
			mustWrite(t, path, content)
			changed, err := upsertAt(path, "mytool", pointerBody)
			if !errors.Is(err, agentdocs.ErrPointerMalformed) || changed {
				t.Fatalf("UpsertPointer = %v, %v; want ErrPointerMalformed, unchanged", changed, err)
			}
			if got := read(t, path); got != content {
				t.Fatalf("a refused upsert changed the file: %q", got)
			}
		})
	}
}

// A body that carries the tool's own markers would corrupt the next run.
func TestUpsertPointerRefusesABodyCarryingTheMarkers(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	for _, body := range []string{"x\n" + pointerEnd + "\n", pointerStart + "\n"} {
		if _, err := upsertAt(path, "mytool", body); !errors.Is(err, agentdocs.ErrPointerMalformed) {
			t.Errorf("UpsertPointer(body %q) = %v, want ErrPointerMalformed", body, err)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("a refused upsert created the file")
	}
}
