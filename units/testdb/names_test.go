package testdb

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestHash_PartsAreSeparated(t *testing.T) {
	t.Parallel()
	if hash("a", "bc") == hash("ab", "c") {
		t.Fatal("hash(a, bc) equals hash(ab, c): parts run together")
	}
	if hash("x")[:8] != hash("x")[:8] || len(hash("x")) != 64 {
		t.Fatal("hash is not a stable 64-hex digest")
	}
}

func TestModuleRootAndNamespace(t *testing.T) {
	t.Parallel()
	mk := func(root string) string {
		deep := filepath.Join(root, "pkg", "sub")
		if err := os.MkdirAll(deep, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module m\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return deep
	}
	rootA, rootB := t.TempDir(), t.TempDir()
	deepA, deepB := mk(rootA), mk(rootB)

	if got := moduleRoot(deepA); got != rootA {
		t.Fatalf("moduleRoot(%s) = %s, want %s", deepA, got, rootA)
	}
	if namespace(deepA) != namespace(rootA) {
		t.Fatal("two directories of one module have two namespaces")
	}
	if namespace(deepA) == namespace(deepB) {
		t.Fatal("two checkouts share a namespace")
	}
	if ns := namespace(deepA); len(ns) != 8 || strings.Trim(ns, "0123456789abcdef") != "" {
		t.Fatalf("namespace %q is not 8 hex", ns)
	}
	// No go.mod above: the directory itself is the root.
	bare := t.TempDir()
	if got := moduleRoot(bare); got != bare {
		t.Fatalf("moduleRoot with no go.mod = %s", got)
	}
}

func TestDigest(t *testing.T) {
	t.Parallel()
	base := fstest.MapFS{"001.sql": {Data: []byte("create table a();")}, "002.sql": {Data: []byte("create table b();")}}
	d1, err := digest(base)
	if err != nil {
		t.Fatal(err)
	}
	if len(d1) != 12 {
		t.Fatalf("digest %q is not 12 hex", d1)
	}
	if d2, _ := digest(fstest.MapFS{"002.sql": base["002.sql"], "001.sql": base["001.sql"]}); d2 != d1 {
		t.Fatal("the digest depends on map order")
	}
	changed := fstest.MapFS{"001.sql": {Data: []byte("create table a(x int);")}, "002.sql": base["002.sql"]}
	if d, _ := digest(changed); d == d1 {
		t.Fatal("a changed byte keeps the digest")
	}
	renamed := fstest.MapFS{"001.sql": base["001.sql"], "003.sql": base["002.sql"]}
	if d, _ := digest(renamed); d == d1 {
		t.Fatal("a renamed file keeps the digest")
	}
	moved := fstest.MapFS{"001.sql": {Data: []byte("create table a();c")}, "002.sql": {Data: []byte("reate table b();")}}
	if d, _ := digest(moved); d == d1 {
		t.Fatal("bytes moved across a file boundary keep the digest")
	}
	empty, _ := digest(nil)
	if e2, _ := digest(fstest.MapFS{}); empty != e2 {
		t.Fatal("a nil FS and an empty FS differ")
	}
}

func TestNames(t *testing.T) {
	t.Parallel()
	pkg := "/work/" + strings.Repeat("deeply/", 40) + "pkg"
	test := "TestSomething/" + strings.Repeat("a very long subtest name ", 20)
	tn := testName("abcd1234", pkg, test, 0)
	tmpl := templateName(pkg, "0123456789ab")
	for _, n := range []string{tn, tmpl} {
		if len(n) > maxName {
			t.Fatalf("%q is %d bytes, over %d", n, len(n), maxName)
		}
	}
	if !strings.HasPrefix(tn, "t_abcd1234_") || !strings.HasPrefix(tmpl, "tmpl_") || !strings.HasSuffix(tmpl, "_0123456789ab") {
		t.Fatalf("shapes: %q %q", tn, tmpl)
	}
	if testName("abcd1234", pkg, test, 1) == tn || testName("abcd1234", pkg, test+"x", 0) == tn {
		t.Fatal("a second call or another test shares a database name")
	}
	if testName("abcd1234", pkg, test, 0) != tn {
		t.Fatal("a test's name is not stable")
	}
	if !strings.HasPrefix(tmpl, templateFamily(pkg)) {
		t.Fatal("a template is outside its package's family")
	}
	if templateFamily("/a") == templateFamily("/b") {
		t.Fatal("two packages share a template family")
	}
}

func TestMask(t *testing.T) {
	t.Parallel()
	tests := []struct{ in, wantHas, wantNot string }{
		{"postgres://admin:s3cret@127.0.0.1:5439/postgres?sslmode=disable", "admin:xxxxx@127.0.0.1:5439", "s3cret"},
		{"postgresql://admin@host/db", "admin@host", ""},
		{"postgres://admin@host/db?password=s3cret&sslmode=disable", "password=xxxxx", "s3cret"},
		{"host=h port=1 user=u password=s3cret dbname=d", "password=xxxxx", "s3cret"},
		{"host=h password='s 3cret' dbname=d", "password=xxxxx", "3cret"},
		{"postgres://a:b@%zz/x", "unparsable", "b@"},
	}
	for _, tt := range tests {
		got := mask(tt.in)
		if !strings.Contains(got, tt.wantHas) || (tt.wantNot != "" && strings.Contains(got, tt.wantNot)) {
			t.Errorf("mask(%q) = %q, want it to hold %q and not %q", tt.in, got, tt.wantHas, tt.wantNot)
		}
	}
	if got := redact("dial s3cret failed", "postgres://u:s3cret@h/d"); strings.Contains(got, "s3cret") {
		t.Fatalf("redact left the password: %q", got)
	}
}

func TestWithDatabase(t *testing.T) {
	t.Parallel()
	got, err := withDatabase("postgres://u:p@h:5/admin?sslmode=disable", "t_1")
	if err != nil || got != "postgres://u:p@h:5/t_1?sslmode=disable" {
		t.Fatalf("URL form: %q, %v", got, err)
	}
	got, err = withDatabase("host=h user=u dbname=admin port=5", "t_1")
	if err != nil || got != "host=h user=u port=5 dbname=t_1" {
		t.Fatalf("key=value form: %q, %v", got, err)
	}
	if _, err := withDatabase("postgres://a:b@%zz/x", "t_1"); err == nil {
		t.Fatal("an unparsable URL is accepted")
	}
}

func TestQuoting(t *testing.T) {
	t.Parallel()
	if got := quote(`a"b`); got != `"a""b"` {
		t.Fatalf("quote = %s", got)
	}
	if got := literal(`it's`); got != `'it''s'` {
		t.Fatalf("literal = %s", got)
	}
}
