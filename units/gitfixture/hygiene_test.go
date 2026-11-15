package gitfixture

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// recorder is a testing.TB that records Errorf and Fatalf instead of failing the real test. Any other method is
// the embedded nil TB's: a call to one the code under test should not make panics.
type recorder struct {
	testing.TB
	errors []string
	fatal  string
}

func (r *recorder) Helper() {}

func (r *recorder) Errorf(format string, args ...any) {
	r.errors = append(r.errors, fmt.Sprintf(format, args...))
}

func (r *recorder) Fatalf(format string, args ...any) { r.fatal = fmt.Sprintf(format, args...) }

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, src := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestRawSpawns_Table(t *testing.T) {
	t.Parallel()
	const fixtureImport = `"github.com/ydnikolaev/go-foundation/units/gitfixture"`
	tests := []struct {
		name string
		src  string
		want []int
	}{
		{"raw Command is reported", "package p\nimport \"os/exec\"\nfunc f() { exec.Command(\"git\", \"status\") }\n", []int{3}},
		{"Command through Args is not", "package p\nimport (\n\t\"os/exec\"\n\t" + fixtureImport + "\n)\nfunc f() { exec.Command(\"git\", gitfixture.Args(\"status\")...) }\n", nil},
		{"a non-git command is not seen", "package p\nimport \"os/exec\"\nfunc f() { exec.Command(\"ls\", \"-l\") }\n", nil},
		{"raw CommandContext is reported", "package p\nimport (\n\t\"context\"\n\t\"os/exec\"\n)\nfunc f(c context.Context) { exec.CommandContext(c, \"git\", \"log\") }\n", []int{6}},
		{"CommandContext through Args is not", "package p\nimport (\n\t\"context\"\n\t\"os/exec\"\n\t" + fixtureImport + "\n)\nfunc f(c context.Context) { exec.CommandContext(c, \"git\", gitfixture.Args(\"log\")...) }\n", nil},
		{"two sites are judged one by one", "package p\nimport (\n\t\"os/exec\"\n\t" + fixtureImport + "\n)\nfunc a() { exec.Command(\"git\", gitfixture.Args(\"x\")...) }\nfunc b() { exec.Command(\"git\", \"y\") }\n", []int{7}},
		{"an aliased os/exec is followed", "package p\nimport sh \"os/exec\"\nfunc f() { sh.Command(\"git\", \"status\") }\n", []int{3}},
		{"an aliased gitfixture is followed", "package p\nimport (\n\t\"os/exec\"\n\tgf " + fixtureImport + "\n)\nfunc f() { exec.Command(\"git\", gf.Args(\"status\")...) }\n", nil},
		{"Args of another package is no cover", "package p\nimport (\n\t\"os/exec\"\n\t\"other\"\n)\nfunc f() { exec.Command(\"git\", other.Args(\"status\")...) }\n", []int{6}},
		{"a git word in a string is not a spawn", "package p\nvar s = `exec.Command(\"git\", \"status\")`\n", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := rawSpawns("x.go", []byte(tt.src))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("lines = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHygiene_RedOnARawSpawnAndGreenOnAnAllowedOne(t *testing.T) {
	t.Parallel()
	root := writeTree(t, map[string]string{
		"app/bad_test.go":      "package app\nimport \"os/exec\"\nfunc f() { exec.Command(\"git\", \"init\") }\n",
		"helpers/git.go":       "package helpers\nimport \"os/exec\"\nfunc f() { exec.Command(\"git\", \"init\") }\n",
		"app/good_test.go":     "package app\nimport (\n\t\"os/exec\"\n\t\"x/gitfixture\"\n)\nfunc g() { exec.Command(\"git\", gitfixture.Args(\"init\")...) }\n",
		"app/testdata/raw.go":  "package raw\nimport \"os/exec\"\nfunc f() { exec.Command(\"git\", \"init\") }\n",
		"vendor/v/v.go":        "package v\nimport \"os/exec\"\nfunc f() { exec.Command(\"git\", \"init\") }\n",
		".hidden/h.go":         "package h\nimport \"os/exec\"\nfunc f() { exec.Command(\"git\", \"init\") }\n",
		"app/notes.txt":        "exec.Command(\"git\")",
		"helpers/sub/deep.go":  "package sub\nimport \"os/exec\"\nfunc f() { exec.Command(\"git\", \"init\") }\n",
		"helpers/other.go.bak": "not go",
	})

	rec := &recorder{}
	Hygiene(rec, root, "helpers")

	if rec.fatal != "" {
		t.Fatalf("Fatalf: %s", rec.fatal)
	}
	if len(rec.errors) != 1 || !strings.Contains(rec.errors[0], "app/bad_test.go:3: raw git spawn") {
		t.Fatalf("errors = %q, want exactly the raw spawn at app/bad_test.go:3", rec.errors)
	}

	rec = &recorder{}
	Hygiene(rec, root, "helpers", "app/bad_test.go")
	if rec.fatal != "" || len(rec.errors) != 0 {
		t.Fatalf("with both allowed: fatal %q errors %q, want none", rec.fatal, rec.errors)
	}

	rec = &recorder{}
	Hygiene(rec, root)
	if len(rec.errors) != 3 {
		t.Fatalf("with nothing allowed: errors = %q, want 3 raw spawns", rec.errors)
	}
}

func TestHygiene_EmptyTreeAndBadSource(t *testing.T) {
	t.Parallel()

	rec := &recorder{}
	Hygiene(rec, t.TempDir())
	if len(rec.errors) != 1 || !strings.Contains(rec.errors[0], "vacuously") {
		t.Fatalf("an empty tree: errors = %q, want the vacuous-pass failure", rec.errors)
	}

	rec = &recorder{}
	Hygiene(rec, writeTree(t, map[string]string{"a.go": "package a\nfunc {"}))
	if len(rec.errors) != 1 || !strings.Contains(rec.errors[0], "cannot parse") {
		t.Fatalf("a broken file: errors = %q, want a parse failure", rec.errors)
	}

	rec = &recorder{}
	Hygiene(rec, filepath.Join(t.TempDir(), "missing"))
	if rec.fatal == "" {
		t.Fatal("a missing root: want Fatalf")
	}
}
