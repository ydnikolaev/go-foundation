package testdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
)

const readyMarker = "ready"

// tmplSpec is what one template is built from.
type tmplSpec struct {
	driver string
	raw    string // the admin DSN
	pkg    string // the package directory
	cfg    Config
}

var (
	templateMu    sync.Mutex
	templateReady = map[string]bool{} // admin DSN + template name -> built or found ready by this process
)

// ensureTemplate returns the name of the package's template for cfg, building it when it is missing or half built.
// Callers in one process queue on a mutex; callers in several processes queue on a Postgres advisory lock.
func ensureTemplate(ctx context.Context, admin *sql.DB, s tmplSpec) (string, error) {
	d, err := digest(s.cfg.Migrations)
	if err != nil {
		return "", fmt.Errorf("read migrations: %w", err)
	}
	name := templateName(s.pkg, d)

	templateMu.Lock()
	defer templateMu.Unlock()
	if templateReady[s.raw+"\x00"+name] {
		return name, nil
	}
	if err := buildTemplate(ctx, admin, s, name); err != nil {
		return "", err
	}
	templateReady[s.raw+"\x00"+name] = true
	pruneStale(ctx, admin, s, name)
	return name, nil
}

// buildTemplate creates, migrates and marks the template under a session advisory lock, so two test processes of one
// package never build it together; the one that waits finds the ready marker and returns.
func buildTemplate(ctx context.Context, admin *sql.DB, s tmplSpec, name string) (err error) {
	conn, err := admin.Conn(ctx) // one session holds the lock, so the lock and its release share it
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = conn.Close() }()

	lock := "hashtextextended(" + literal("testdb:"+name) + ", 0)"
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock("+lock+")"); err != nil {
		return fmt.Errorf("lock %s: %w", name, err)
	}
	defer func() { _, _ = conn.ExecContext(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock("+lock+")") }()

	exists, comment, err := inspect(ctx, conn, name)
	if err != nil {
		return err
	}
	if exists && comment == readyMarker {
		return nil
	}
	// Missing, or left half built by a killed run or a failed migration: build from scratch.
	if _, err := conn.ExecContext(ctx, "DROP DATABASE IF EXISTS "+quote(name)+" WITH (FORCE)"); err != nil {
		return fmt.Errorf("drop half-built %s: %w", name, err)
	}
	if _, err := conn.ExecContext(ctx, "CREATE DATABASE "+quote(name)); err != nil {
		return fmt.Errorf("create %s: %w", name, err)
	}
	if s.cfg.Migrate != nil {
		dsn, err := withDatabase(s.raw, name)
		if err != nil {
			return err
		}
		if err := s.cfg.Migrate(ctx, dsn); err != nil {
			return fmt.Errorf("migrate %s: %w", name, err)
		}
	}
	if _, err := conn.ExecContext(ctx, "COMMENT ON DATABASE "+quote(name)+" IS "+literal(readyMarker)); err != nil {
		return fmt.Errorf("mark %s ready: %w", name, err)
	}
	return nil
}

// inspect reads whether database name exists and its comment.
func inspect(ctx context.Context, conn *sql.Conn, name string) (exists bool, comment string, err error) {
	var c sql.NullString
	err = conn.QueryRowContext(ctx,
		"SELECT shobj_description(oid, 'pg_database') FROM pg_database WHERE datname = "+literal(name)).Scan(&c)
	if errors.Is(err, sql.ErrNoRows) {
		return false, "", nil
	}
	if err != nil {
		return false, "", fmt.Errorf("inspect %s: %w", name, err)
	}
	return true, c.String, nil
}

// pruneStale drops the other templates of this package: those of older migration digests are dead weight. A template
// this process has built or used stays (one package may use several migration sets), and one a session still holds
// stays too, because the drop is refused; neither is an error. The caller holds templateMu.
func pruneStale(ctx context.Context, admin *sql.DB, s tmplSpec, keep string) {
	family := templateFamily(s.pkg)
	rows, err := admin.QueryContext(ctx,
		"SELECT datname FROM pg_database WHERE left(datname, "+fmt.Sprint(len(family))+") = "+literal(family))
	if err != nil {
		return
	}
	var stale []string
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil && n != keep && !templateReady[s.raw+"\x00"+n] {
			stale = append(stale, n)
		}
	}
	_ = rows.Close()
	for _, n := range stale {
		_, _ = admin.ExecContext(ctx, "DROP DATABASE IF EXISTS "+quote(n))
	}
}
