package testdb

import (
	"fmt"
	"net"
	"strings"
	"testing"
)

// stop is what the recorder panics with after Skip or Fatalf, standing in for the runtime.Goexit the real testing.T
// does: the code under test must not run on.
type stop struct{}

// recorder is a testing.TB that records the calls the rules are about. Any other method is the embedded nil TB's: a
// call the code under test should not make panics.
type recorder struct {
	testing.TB
	name     string
	skipped  string
	fatal    string
	cleanups []func()
}

func (r *recorder) Helper()      {}
func (r *recorder) Name() string { return r.name }
func (r *recorder) Skip(args ...any) {
	r.skipped = fmt.Sprint(args...)
	panic(stop{})
}
func (r *recorder) Fatalf(format string, args ...any) {
	r.fatal = fmt.Sprintf(format, args...)
	panic(stop{})
}
func (r *recorder) Logf(string, ...any) {}
func (r *recorder) Cleanup(fn func())   { r.cleanups = append(r.cleanups, fn) }

// run calls fn and reports whether it ended in Skip or Fatalf.
func run(fn func()) (stopped bool) {
	defer func() {
		if p := recover(); p != nil {
			if _, ok := p.(stop); !ok {
				panic(p)
			}
			stopped = true
		}
	}()
	fn()
	return false
}

// envOf is an env that serves vars and records every key read.
func envOf(short bool, vars map[string]string, reads *[]string) env {
	return env{
		short: func() bool { return short },
		getenv: func(k string) string {
			*reads = append(*reads, k)
			return vars[k]
		},
		getwd: func() (string, error) { return "/some/pkg", nil },
	}
}

func TestRule_ShortSkipsBeforeAnyEnvRead(t *testing.T) {
	t.Parallel()
	var reads []string
	r := &recorder{name: "TestX"}
	e := envOf(true, map[string]string{envURL: "postgres://u@h/d"}, &reads)

	if !run(func() { create(r, Config{}, e) }) {
		t.Fatal("create ran on under -short")
	}
	if want := "testdb: -short: database tests run without -short"; r.skipped != want {
		t.Fatalf("skip message = %q, want %q", r.skipped, want)
	}
	if len(reads) != 0 {
		t.Fatalf("-short read the environment first: %v", reads)
	}
	if r.fatal != "" {
		t.Fatalf("-short also failed: %s", r.fatal)
	}
}

func TestRule_UnsetFails(t *testing.T) {
	t.Parallel()
	var reads []string
	r := &recorder{name: "TestX"}

	if !run(func() { create(r, Config{}, envOf(false, nil, &reads)) }) {
		t.Fatal("create ran on with TESTDB_URL unset")
	}
	if !strings.Contains(r.fatal, "TESTDB_URL is not set") || r.skipped != "" {
		t.Fatalf("fatal = %q, skipped = %q: want a failure naming TESTDB_URL, no skip", r.fatal, r.skipped)
	}
}

func TestRule_UnreachableFailsAndMasksThePassword(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("testdb: no loopback to find a closed port: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close() // the port is now closed

	var reads []string
	r := &recorder{name: "TestX"}
	dsn := "postgres://admin:s3cret@" + addr + "/postgres?sslmode=disable"
	e := envOf(false, map[string]string{envURL: dsn}, &reads)

	if !run(func() { create(r, Config{Driver: testDriver}, e) }) {
		t.Fatal("create ran on with a closed port")
	}
	if !strings.Contains(r.fatal, addr) || !strings.Contains(r.fatal, "admin:xxxxx@") {
		t.Fatalf("fatal = %q, want the DSN with its password masked", r.fatal)
	}
	if strings.Contains(r.fatal, "s3cret") {
		t.Fatalf("the password leaked: %q", r.fatal)
	}
	if r.skipped != "" || len(r.cleanups) != 0 {
		t.Fatalf("unreachable skipped (%q) or left cleanups (%d)", r.skipped, len(r.cleanups))
	}
}

func TestRule_UnknownDriverFailsNamingIt(t *testing.T) {
	t.Parallel()
	var reads []string
	r := &recorder{name: "TestX"}
	e := envOf(false, map[string]string{envURL: "postgres://u:s3cret@127.0.0.1:1/d"}, &reads)

	if !run(func() { create(r, Config{Driver: "no-such-driver"}, e) }) {
		t.Fatal("create ran on with an unregistered driver")
	}
	if !strings.Contains(r.fatal, "no-such-driver") || strings.Contains(r.fatal, "s3cret") {
		t.Fatalf("fatal = %q, want the driver named and the password hidden", r.fatal)
	}
}
