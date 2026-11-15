# testdb

A Postgres database of its own for every Go test. `New` returns the DSN of a database copied from a template, so a test
pays for one `CREATE DATABASE ... TEMPLATE ... STRATEGY FILE_COPY`, not for the migrations: the schema is built once per
package and migration set, then every test clones it. Parallel tests never share a table, and checkouts of one repo
share one server without touching each other's databases. Standard library only: you register your own `database/sql`
driver and supply your own `Migrate`.

## Install

```sh
go get github.com/ydnikolaev/go-foundation/units/testdb@v0.2.0
```

go-foundation is public: no GOPRIVATE, no token, no vendoring. The test server is Postgres 15 or newer (the copy needs
`STRATEGY FILE_COPY`) and the role in `TESTDB_URL` needs `CREATEDB`.

## Wire

1. Run a Postgres for tests and export `TESTDB_URL`, an admin DSN of it: `postgres://user:pass@127.0.0.1:5432/postgres`
   (the URL form and the `key=value` form both work).
2. Import a `database/sql` driver in the test binary (for pgx, `_ "github.com/jackc/pgx/v5/stdlib"`, whose name is
   `"pgx"`, the default) and describe the template in a `testdb.Config`: `Migrations`, the files that make it up, and
   `Migrate`, the function that applies them to the empty template database it is handed.
3. In each test, call `t.Parallel()`, then `dsn := testdb.New(t, cfg)`, and open your own pool on `dsn`.
4. Run the fast lane with `go test -short`: `New` skips there, before it reads the environment. Any other run needs
   `TESTDB_URL`, and an unset or unreachable one fails the test, naming the DSN with its password masked: a database
   test that silently skips is a green lie.

## Configure

| setting | meaning | default |
|---|---|---|
| `TESTDB_URL` | admin DSN of a Postgres 15+; the template and every test database are created beside its database | none, required outside `-short` |
| `Config.Driver` | the `database/sql` driver name you registered | `pgx` |
| `Config.Migrations` | files read only for the template's digest: a changed name or byte builds a new template | empty |
| `Config.Migrate` | applies the schema to the template database given by its DSN, once per template | none, an empty template |
| `Config.Namespace` | tells checkouts apart when their module roots do not (see below) | empty: the module root decides |

Names are fixed: a template is `tmpl_<8 hex of the package directory>_<12 hex of the migrations>`, a test's database is
`t_<8 hex of the module root>_<16 hex of the package and the test>`, each at most 63 bytes. The module root is the
nearest `go.mod` above the working directory, found in process, so a worktree needs no environment variable (an
environment variable would put the worktree into `go test`'s cache key).

Checkouts whose `go.mod` sits at the same path share those hashes: every checkout's container builds at one `WORKDIR`
(say `/src`), so two of them on one Postgres would drop each other's `t_` databases and idle templates. Set
`Config.Namespace` to a string that is unique per checkout (its branch, a run id, a container name):

```go
var cfg = testdb.Config{
	Migrations: migrations,
	Migrate:    migrate,
	Namespace:  os.Getenv("CHECKOUT_ID"), // read your own variable here; the unit reads none
}
```

Empty keeps the names above, byte for byte. Set, the namespace replaces the module-root hash in a test database's name
(`t_<8 hex of the namespace>_<16 hex of the package and the test>`) and is mixed into the template's
(`tmpl_<8 hex of the namespace and the package directory>_<12 hex of the migrations>`). It is hashed, never embedded:
any string is valid and a name keeps its length. The unit reads no environment variable for it; read yours where you
build the `Config`, so the value joins your own cache key by your choice. The cleanup that drops stale templates and a
test's leftover database only touches names of its own namespace. Two checkouts that share a namespace share
databases: give each its own.

## API

<!-- api: devkit foundation api; do not edit -->
```go
FUNCTIONS
func New(t testing.TB, cfg Config) string
TYPES
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
	// Namespace tells checkouts apart when their module roots do not: two checkouts that build at one path (every
	// container at /src) share the hash of the module root, so one would drop the other's databases and templates.
	// Give each checkout its own string (its branch, a run id, a container name). Empty keeps the module-root names
	// of v0.1.0. Set, it replaces the module-root hash in a test database's name and is mixed into the template's;
	// it is hashed, so any string is valid. The package reads no environment variable for it: read yours where you
	// build the Config, so the value is in your own cache key by your choice.
	Namespace string
}
```

## Example

```go
package store_test

import (
	"context"
	"database/sql"
	"embed"
	"io/fs"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/ydnikolaev/go-foundation/units/testdb"
)

//go:embed migrations/*.sql
var migrations embed.FS

var cfg = testdb.Config{
	Migrations: migrations,
	Migrate: func(ctx context.Context, dsn string) error {
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			return err
		}
		defer db.Close() // the template must have no session left when Migrate returns
		files, _ := fs.Glob(migrations, "migrations/*.sql")
		for _, f := range files {
			sqlText, _ := migrations.ReadFile(f)
			if _, err := db.ExecContext(ctx, string(sqlText)); err != nil {
				return err
			}
		}
		return nil
	},
}

func TestOrders(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("pgx", testdb.New(t, cfg))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	// the test owns this database: the schema is there, no other test sees its rows
}
```

## Extension points

- `Config.Driver` takes any `database/sql` driver that speaks Postgres and runs a statement with no arguments.
- `Config.Migrate` is the whole migration story: use goose, tern, plain SQL or a seed; the unit only digests
  `Config.Migrations` to know when the template is stale.
- The test's DSN is plain text: open it with a pool, an ORM or `psql`.

## Do not

- Do not leave a connection open in `Migrate`: Postgres refuses to copy a database that has a session. `New` retries
  the copy for five seconds, then fails.
- Do not call `New` outside a test, and do not call it twice for one database: a second call in the same test gets a
  second database.
- Do not run it against a server you care about: it creates and drops databases named `tmpl_*` and `t_*`.
- Do not expect a sweep: a killed run leaves its databases behind, the next run of the same test drops its own, and the
  rest go with the server's volume. A package's templates of older migration digests are dropped once no session holds
  them.
- Do not copy this package. Extend it here; a copy carries `go-foundation-copy: testdb@<sha>` and a removal row.
