package testdb

// The round trip runs against a real Postgres 15 or newer named by TESTDB_URL, through the in-test driver of
// pgwire_test.go. Without TESTDB_URL each test here is an announced skip, never a silent pass.

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"
)

// adminURL is TESTDB_URL, or the announced skip.
func adminURL(t *testing.T) string {
	t.Helper()
	u := os.Getenv(envURL)
	if u == "" {
		t.Skip("testdb: round trip skipped: TESTDB_URL is unset; point it at an admin DSN of a Postgres 15+ and run with -v")
	}
	return u
}

func openDB(t testing.TB, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open(testDriver, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// scalar runs a query that returns one text value.
func scalar(t testing.TB, db *sql.DB, q string) string {
	t.Helper()
	var s sql.NullString
	if err := db.QueryRow(q).Scan(&s); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return s.String
}

func nonce(t testing.TB) string {
	t.Helper()
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

// seedConfig is a template with one table and one row; nonce makes its digest new, so a run builds its own template.
func seedConfig(nonce string, built *atomic.Int64) Config {
	return Config{
		Driver:     testDriver,
		Migrations: fstest.MapFS{"001_seed.sql": {Data: []byte("-- " + nonce + "\nCREATE TABLE seed(id int); INSERT INTO seed VALUES (1);")}},
		Migrate: func(ctx context.Context, dsn string) error {
			built.Add(1)
			db, err := sql.Open(testDriver, dsn)
			if err != nil {
				return fmt.Errorf("open template: %w", err)
			}
			defer func() { _ = db.Close() }()
			if _, err = db.ExecContext(ctx, "CREATE TABLE seed(id int); INSERT INTO seed VALUES (1)"); err != nil {
				return fmt.Errorf("seed template: %w", err)
			}
			return nil
		},
	}
}

// checkout is a temporary module root with one package directory in it, and that directory.
func checkout(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	pkg := filepath.Join(root, "pkg")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module m\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return pkg
}

func inDir(dir string) env {
	return env{short: testing.Short, getenv: os.Getenv, getwd: func() (string, error) { return dir, nil }}
}

func dbName(t testing.TB, dsn string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	return path.Base(u.Path)
}

func exists(t testing.TB, admin *sql.DB, name string) bool {
	t.Helper()
	return scalar(t, admin, "SELECT count(*) FROM pg_database WHERE datname = "+literal(name)) == "1"
}

// dropAtEnd removes databases a test made by hand or that outlive their test (templates).
func dropAtEnd(t *testing.T, admin *sql.DB, names ...string) {
	t.Helper()
	t.Cleanup(func() {
		for _, n := range names {
			_, _ = admin.Exec("DROP DATABASE IF EXISTS " + quote(n) + " WITH (FORCE)")
		}
	})
}

func TestRoundTrip(t *testing.T) {
	raw := adminURL(t)
	admin := openDB(t, raw)
	var built atomic.Int64
	cfg := seedConfig(nonce(t), &built)
	pkg, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	d, err := digest(cfg.Migrations)
	if err != nil {
		t.Fatal(err)
	}
	dropAtEnd(t, admin, templateName(pkg, d))

	// Eight tests at once, one template: it is built once and each test sees only its own rows.
	var mu sync.Mutex
	var names []string
	t.Run("parallel", func(t *testing.T) {
		for i := range 8 {
			t.Run(fmt.Sprintf("db%d", i), func(t *testing.T) {
				t.Parallel()
				dsn := New(t, cfg)
				db := openDB(t, dsn)
				if _, err := db.Exec(fmt.Sprintf("INSERT INTO seed VALUES (%d)", 100+i)); err != nil {
					t.Fatal(err)
				}
				if got := scalar(t, db, "SELECT count(*) FROM seed"); got != "2" {
					t.Errorf("rows = %s, want 2: the seed and this test's own", got)
				}
				if got := scalar(t, db, "SELECT max(id) FROM seed"); got != fmt.Sprint(100+i) {
					t.Errorf("max id = %s, want %d: another test's row is visible", got, 100+i)
				}
				mu.Lock()
				names = append(names, dbName(t, dsn))
				mu.Unlock()
			})
		}
	})
	if got := built.Load(); got != 1 {
		t.Errorf("Migrate ran %d times for 8 tests, want 1", got)
	}
	if len(names) != 8 {
		t.Fatalf("%d databases recorded, want 8", len(names))
	}
	for _, n := range names {
		if exists(t, admin, n) {
			t.Errorf("database %s outlived its test", n)
		}
	}

	// Warm: the template is ready, so New is one copy.
	var durs []time.Duration
	t.Run("warm", func(t *testing.T) {
		for i := range 20 {
			t.Run(fmt.Sprintf("n%d", i), func(t *testing.T) {
				start := time.Now()
				New(t, cfg)
				durs = append(durs, time.Since(start))
			})
		}
	})
	slices.Sort(durs)
	t.Logf("testdb: warm New p50 = %.1f ms, p90 = %.1f ms, max = %.1f ms over %d calls",
		ms(durs[len(durs)/2]), ms(durs[len(durs)*9/10]), ms(durs[len(durs)-1]), len(durs))
	if got := built.Load(); got != 1 {
		t.Errorf("Migrate ran %d times after the warm calls, want 1", got)
	}
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

// Two checkouts on one server, at once: each gets its own namespace and template and sees only its own databases.
func TestRoundTrip_TwoNamespacesAtOnce(t *testing.T) {
	raw := adminURL(t)
	admin := openDB(t, raw)
	n := nonce(t)
	var built atomic.Int64
	cfg := seedConfig(n, &built)
	d, err := digest(cfg.Migrations)
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	prefixes := map[string]string{}
	t.Run("checkouts", func(t *testing.T) {
		for _, who := range []string{"a", "b"} {
			dir := checkout(t)
			dropAtEnd(t, admin, templateName(dir, d))
			t.Run(who, func(t *testing.T) {
				t.Parallel()
				dsn := create(t, cfg, inDir(dir))
				name := dbName(t, dsn)
				db := openDB(t, dsn)
				if got := scalar(t, db, "SELECT count(*) FROM seed"); got != "1" {
					t.Errorf("checkout %s: rows = %s, want the seed", who, got)
				}
				mu.Lock()
				prefixes[who] = strings.Join(strings.SplitN(name, "_", 3)[:2], "_")
				mu.Unlock()
			})
		}
	})
	if prefixes["a"] == "" || prefixes["a"] == prefixes["b"] {
		t.Fatalf("namespaces = %v, want two different ones", prefixes)
	}
	if got := built.Load(); got != 2 {
		t.Errorf("Migrate ran %d times for two checkouts, want 2: one template each", got)
	}
}

// Two checkouts that build at one path (every container at /src) share the module root and the package directory;
// their Namespaces alone keep them apart: two databases at once for one test name, two templates, and a prune of one
// never drops the other's template, whatever its migration set.
func TestRoundTrip_TwoConfigNamespacesOnOneDir(t *testing.T) {
	raw := adminURL(t)
	admin := openDB(t, raw)
	dir := checkout(t)
	var built atomic.Int64
	cfgA, cfgB := seedConfig(nonce(t), &built), seedConfig(nonce(t), &built)
	cfgA.Namespace, cfgB.Namespace = "checkout-a", "checkout-b"
	dA, _ := digest(cfgA.Migrations)
	dB, _ := digest(cfgB.Migrations)
	tmplA, tmplB := templateName(templateKey(cfgA.Namespace, dir), dA), templateName(templateKey(cfgB.Namespace, dir), dB)
	dropAtEnd(t, admin, tmplA, tmplB)

	dsnA := create(t, cfgA, inDir(dir))
	dsnB := create(t, cfgB, inDir(dir)) // same dir: only the namespace differs
	if a, b := dbName(t, dsnA), dbName(t, dsnB); a == b {
		t.Fatalf("both checkouts got database %s", a)
	}
	for _, dsn := range []string{dsnA, dsnB} {
		if got := scalar(t, openDB(t, dsn), "SELECT count(*) FROM seed"); got != "1" {
			t.Errorf("%s: rows = %s, want the seed", dbName(t, dsn), got)
		}
	}
	if !exists(t, admin, tmplA) || !exists(t, admin, tmplB) {
		t.Errorf("a template is missing: A %v, B %v", exists(t, admin, tmplA), exists(t, admin, tmplB))
	}
	if built.Load() != 2 {
		t.Errorf("Migrate ran %d times for two namespaces, want 2: one template each", built.Load())
	}

	// B's new migration set prunes B's stale template, and A's template stays.
	orphanB := templateFamily(templateKey(cfgB.Namespace, dir)) + "orphan000001"
	if _, err := admin.Exec("CREATE DATABASE " + quote(orphanB)); err != nil {
		t.Fatal(err)
	}
	cfgB2 := seedConfig(nonce(t), &built)
	cfgB2.Namespace = cfgB.Namespace
	dB2, _ := digest(cfgB2.Migrations)
	dropAtEnd(t, admin, orphanB, templateName(templateKey(cfgB2.Namespace, dir), dB2))
	create(t, cfgB2, inDir(dir))
	if exists(t, admin, orphanB) {
		t.Error("a stale template of namespace B was not dropped")
	}
	if !exists(t, admin, tmplA) {
		t.Error("a prune in namespace B dropped namespace A's template")
	}
}

// A stale template of the package is dropped, one a session holds is not, and neither is the one this process uses.
// A leftover database of a test's own name is replaced by a fresh copy.
func TestRoundTrip_PruneAndLeftover(t *testing.T) {
	raw := adminURL(t)
	admin := openDB(t, raw)
	dir := checkout(t)
	family := templateFamily(dir)
	orphan, held := family+"orphan000001", family+"held00000001"
	leftover := testName(namespace(dir), dir, t.Name(), 0)
	for _, n := range []string{orphan, held, leftover} {
		if _, err := admin.Exec("CREATE DATABASE " + quote(n)); err != nil {
			t.Fatal(err)
		}
	}
	junk, err := sql.Open(testDriver, mustWith(t, raw, leftover))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := junk.Exec("CREATE TABLE junk(id int)"); err != nil {
		t.Fatal(err)
	}
	_ = junk.Close()

	holder, err := sql.Open(testDriver, mustWith(t, raw, held))
	if err != nil {
		t.Fatal(err)
	}
	holdConn, err := holder.Conn(context.Background()) // an open session on `held`
	if err != nil {
		t.Fatal(err)
	}

	var built atomic.Int64
	first, second := seedConfig(nonce(t), &built), seedConfig(nonce(t), &built)
	d1, _ := digest(first.Migrations)
	d2, _ := digest(second.Migrations)
	dropAtEnd(t, admin, templateName(dir, d1), templateName(dir, d2), orphan, held, leftover)
	t.Cleanup(func() { _ = holdConn.Close(); _ = holder.Close() })

	dsn := create(t, first, inDir(dir))
	if got := scalar(t, openDB(t, dsn), "SELECT count(*) FROM pg_tables WHERE tablename = 'junk'"); got != "0" {
		t.Errorf("the leftover database was reused: its junk table is still there")
	}
	if exists(t, admin, orphan) {
		t.Error("a stale template of the package was not dropped")
	}
	if !exists(t, admin, held) {
		t.Error("a template with a live session was dropped")
	}

	create(t, second, inDir(dir))
	if !exists(t, admin, templateName(dir, d1)) {
		t.Error("a template this process built was dropped by a later one")
	}
	if !exists(t, admin, templateName(dir, d2)) {
		t.Error("the second template is missing")
	}
}

func mustWith(t testing.TB, raw, name string) string {
	t.Helper()
	dsn, err := withDatabase(raw, name)
	if err != nil {
		t.Fatal(err)
	}
	return dsn
}

// A migration that fails leaves a template that is not marked ready; the next call rebuilds it.
func TestRoundTrip_HalfBuiltTemplateIsRebuilt(t *testing.T) {
	raw := adminURL(t)
	admin := openDB(t, raw)
	dir := checkout(t)
	var built atomic.Int64
	cfg := seedConfig(nonce(t), &built)
	d, _ := digest(cfg.Migrations)
	dropAtEnd(t, admin, templateName(dir, d))

	failing := cfg
	failing.Migrate = func(context.Context, string) error { return fmt.Errorf("boom") }
	r := &recorder{name: "TestHalfBuilt"}
	if !run(func() { create(r, failing, inDir(dir)) }) || !strings.Contains(r.fatal, "boom") {
		t.Fatalf("a failing migration did not fail the test: %q", r.fatal)
	}
	if !exists(t, admin, templateName(dir, d)) {
		t.Fatal("the half-built template is gone; the test cannot tell a rebuild from a first build")
	}

	dsn := create(t, cfg, inDir(dir))
	if got := scalar(t, openDB(t, dsn), "SELECT count(*) FROM seed"); got != "1" {
		t.Errorf("rows = %s after the rebuild, want the seed", got)
	}
	if built.Load() != 1 {
		t.Errorf("Migrate ran %d times, want 1 (the failing one is not counted)", built.Load())
	}
}
