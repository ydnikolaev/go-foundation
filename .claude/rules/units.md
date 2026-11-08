# Units — add, release, consume

Rulings in `docs/rulings/` decide all of this; this is the working checklist.

## Add a unit
1. Pick the capability id (lowercase, `-`); it is permanent. Every unit sits under the one fixed prefix `units/`, as
   `units/<id>/`: never a category segment (prim, mod) and never at the root (`make units-check` refuses both).
   Inside a module-kind unit, Go's own rule applies: subpackages and `internal/`.
2. `mkdir units/<id> && cd units/<id> && go mod init github.com/ydnikolaev/go-foundation/units/<id>`; set `go` to at
   most `config/go-floor`. Then `go work use ./units/<id>` at the root.
3. `unit.json`: id, kind (`primitive` | `module`), stack `go`, summary, stability `experimental`, pairs, provenance
   (the repo, path and sha it came from, when it came from one).
4. `README.md` with exactly: `## Install`, `## Wire`, `## Configure`, `## Extension points`, `## Do not`.
5. `releasenotes/0.1.0.yaml` (schema `go-foundation/releasenotes/v1`; see `units/adoption/releasenotes/`).
6. Code takes its dependencies as options or interfaces: a consumer extends, never forks. No project names, paths
   or ids inside a unit.
7. A unit that requires another: a plain `require`, plus `deps.<id> = unit-<other>` in `config/modules.ini`.
8. `make units-catalogue && make check`.

## Release a unit
`scripts/unit-release.sh <id> <X.Y.Z>`: refuses without `releasenotes/<X.Y.Z>.yaml` or on a red `make check`,
then tags `units/<id>/vX.Y.Z`. `make public-scan`, then push that one tag yourself (R-7; main, force-push and tag deletion stay owner-gated), then `make consumers`; the table lands in the host registry,
whose owner commits it, never here (R-5).
Breaking after v1: the module path becomes `units/<id>/vN`.

## Consume a unit
`go get github.com/ydnikolaev/go-foundation/units/<id>@v<X.Y.Z>` (public: no GOPRIVATE, no token).
Add the repo to the host registry's `foundation-consumers.txt` (`$FOUNDATION_CONSUMERS`). A copy instead of an import carries
`// go-foundation-copy: <id>@<sha>` and a backlog row to replace it; the consumer table lists it until it is gone.
A change one consumer needs is made here, upstream, and released.
