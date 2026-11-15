// Package testdb gives every database test its own Postgres database, copied from a template that is built once.
//
// New(t, cfg) returns the DSN of a database no other test shares. The database is created from a per-package template
// (CREATE DATABASE ... TEMPLATE ... STRATEGY FILE_COPY), so a test pays for a copy, not for the migrations: the schema
// is built once per package and migration set, then every test clones it. The test opens its own pool on the DSN with
// the driver it already uses; this package registers no driver and knows no migration format.
//
// The target is TESTDB_URL, an admin DSN of a Postgres 15 or newer (the role needs CREATEDB). The URL form
// (postgres://user:pass@host:port/db) and the key=value form both work. Under `go test -short` New skips, before it
// reads the environment: a database test that runs without a database is a lie, so the one place it may skip is the
// fast lane. Outside -short an unset or unreachable TESTDB_URL fails the test, naming the DSN with its password
// masked.
//
// Names are scoped so checkouts of one repo share one server without touching each other: a template is
// tmpl_<8 hex of the package directory>_<12 hex of the migrations>, a test's database is t_<8 hex of the module
// root>_<16 hex of the package and the test>, each at most 63 bytes. The module root comes from the working directory,
// in process, so a worktree needs no environment variable.
//
// A killed run can leave databases behind; the next run of the same test drops its own, and nothing sweeps the rest.
//
// Standard library only.
package testdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	envURL        = "TESTDB_URL"
	defaultDriver = "pgx"
	setupTimeout  = 3 * time.Minute
	adminConns    = 8
)

var errUnparsable = errors.New("TESTDB_URL is neither a postgres:// URL nor a key=value DSN")

// Config says how to build the template a test's database is copied from.
type Config struct {
	// Driver is the database/sql driver name the consumer registered, "pgx" when empty. The consumer imports the
	// driver (for pgx, github.com/jackc/pgx/v5/stdlib); this package only opens connections with it.
	Driver string
	// Migrations is the migration files, read only for the template's digest: a changed name or byte builds a new
	// template, an unchanged set reuses the old one. Nil is the empty set.
	Migrations fs.FS
	// Migrate builds the schema in the template, once per template. It gets the DSN of the empty template database
	// and must close every connection it opens before it returns: Postgres refuses to copy a database that has a
	// session. Nil leaves the template empty.
	Migrate func(ctx context.Context, dsn string) error
}

// New returns the DSN of a fresh database for t, copied from the template of t's package and cfg, and drops it when
// the test ends. Under -short it skips t; otherwise an unset or unreachable TESTDB_URL fails it.
//
// Call t.Parallel in every test that uses New: each call has its own database, so it is race-free, and the copies run
// side by side. Call New once per test; a second call in the same test gets a second database.
func New(t testing.TB, cfg Config) string {
	t.Helper()
	return create(t, cfg, hostEnv)
}

// env is what New reads from the process, so a test of the rules can stand in for it.
type env struct {
	short  func() bool
	getenv func(string) string
	getwd  func() (string, error)
}

var hostEnv = env{short: testing.Short, getenv: os.Getenv, getwd: os.Getwd}

func create(t testing.TB, cfg Config, e env) string {
	t.Helper()
	// The skip comes first, before any environment read: a read joins go test's cache key.
	if e.short() {
		t.Skip("testdb: -short: database tests run without -short")
		return ""
	}
	raw := e.getenv(envURL)
	if raw == "" {
		t.Fatalf("testdb: %s is not set: point it at an admin DSN of a Postgres 15 or newer (database tests fail without one, they never skip)", envURL)
		return ""
	}
	pkg, err := e.getwd()
	if err != nil {
		t.Fatalf("testdb: getwd: %v", err)
		return ""
	}
	driver := cfg.Driver
	if driver == "" {
		driver = defaultDriver
	}

	ctx, cancel := context.WithTimeout(context.Background(), setupTimeout)
	defer cancel()
	admin, err := adminDB(ctx, driver, raw)
	if err != nil {
		t.Fatalf("testdb: %s (%s) is unreachable: %s", envURL, mask(raw), redact(err.Error(), raw))
		return ""
	}

	tmpl, err := ensureTemplate(ctx, admin, tmplSpec{driver: driver, raw: raw, pkg: pkg, cfg: cfg})
	if err != nil {
		t.Fatalf("testdb: template: %s", redact(err.Error(), raw))
		return ""
	}
	name := testName(namespace(pkg), pkg, t.Name(), nextCall(pkg, t.Name()))
	if err := cloneInto(ctx, admin, tmpl, name); err != nil {
		t.Fatalf("testdb: create database %s from %s: %s", name, tmpl, redact(err.Error(), raw))
		return ""
	}
	t.Cleanup(func() {
		dctx, dcancel := context.WithTimeout(context.Background(), setupTimeout)
		defer dcancel()
		if err := dropDatabase(dctx, admin, name); err != nil {
			t.Logf("testdb: drop database %s: %s (non-fatal)", name, redact(err.Error(), raw))
		}
	})
	dsn, err := withDatabase(raw, name)
	if err != nil {
		t.Fatalf("testdb: %v", err)
		return ""
	}
	return dsn
}

var calls sync.Map // "pkg\x00test" -> *atomic.Int64

// nextCall counts the New calls of one test in this process, from 0.
func nextCall(pkg, test string) int {
	c, _ := calls.LoadOrStore(pkg+"\x00"+test, new(atomic.Int64))
	return int(c.(*atomic.Int64).Add(1) - 1)
}

var (
	adminMu  sync.Mutex
	adminDBs = map[string]*sql.DB{} // driver + DSN -> shared admin handle
)

// adminDB returns the process's admin handle on raw, opened and pinged once. It is never closed: its sessions end with
// the test binary, and a Cleanup may still need it after the last New returned.
func adminDB(ctx context.Context, driver, raw string) (*sql.DB, error) {
	adminMu.Lock()
	defer adminMu.Unlock()
	key := driver + "\x00" + raw
	if db, ok := adminDBs[key]; ok {
		return db, nil
	}
	db, err := sql.Open(driver, raw)
	if err != nil {
		return nil, fmt.Errorf("%w (does the test binary import the %q driver?)", err, driver)
	}
	db.SetMaxOpenConns(adminConns)
	db.SetConnMaxIdleTime(time.Minute)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	adminDBs[key] = db
	return db, nil
}

// cloneInto drops a leftover database of the same name, then copies tmpl into it.
func cloneInto(ctx context.Context, admin *sql.DB, tmpl, name string) error {
	if err := dropDatabase(ctx, admin, name); err != nil {
		return fmt.Errorf("drop leftover: %w", err)
	}
	stmt := "CREATE DATABASE " + quote(name) + " TEMPLATE " + quote(tmpl) + " STRATEGY FILE_COPY"
	return retryBusy(ctx, func() error {
		if _, err := admin.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("exec: %w", err)
		}
		return nil
	})
}

func dropDatabase(ctx context.Context, admin *sql.DB, name string) error {
	if _, err := admin.ExecContext(ctx, "DROP DATABASE IF EXISTS "+quote(name)+" WITH (FORCE)"); err != nil {
		return fmt.Errorf("exec: %w", err)
	}
	return nil
}

// retryBusy repeats op while Postgres says the template is in use (SQLSTATE 55006: a session of a migration that has
// not closed yet), for up to five seconds.
func retryBusy(ctx context.Context, op func() error) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := op()
		if err == nil || !strings.Contains(err.Error(), "being accessed by other users") || time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// quote is an identifier in double quotes.
func quote(ident string) string { return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"` }

// literal is a string literal in single quotes. The statements here carry no arguments, so they run on any driver.
func literal(s string) string { return `'` + strings.ReplaceAll(s, `'`, `''`) + `'` }
