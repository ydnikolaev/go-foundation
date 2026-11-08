# Units — add, release, consume

Rulings in `docs/rulings/` decide all of this; this is the working checklist.

## Add a unit
1. Pick the capability id (lowercase, `-`); it is permanent. No category dir: `retry/`, never `prim/retry/`.
2. `mkdir <id> && cd <id> && go mod init github.com/ydnikolaev/go-foundation/<id>`; set `go` to at most
   `config/go-floor`. Then `go work use ./<id>` at the root.
3. `unit.json`: id, kind (`primitive` | `module`), stack `go`, summary, stability `experimental`, pairs, provenance
   (the repo, path and sha it came from, when it came from one).
4. `README.md` with exactly: `## Install`, `## Wire`, `## Configure`, `## Extension points`, `## Do not`.
5. `releasenotes/0.1.0.yaml` (schema `go-foundation/releasenotes/v1`; see `adoption/releasenotes/`).
6. Code takes its dependencies as options or interfaces: a consumer extends, never forks. No project names, paths
   or ids inside a unit.
7. A unit that requires another: a plain `require`, plus `deps.<id> = unit-<other>` in `config/modules.ini`.
8. `make units-catalogue && make check`.

## Release a unit
`scripts/unit-release.sh <id> <X.Y.Z>`: refuses without `releasenotes/<X.Y.Z>.yaml` or on a red `make check`,
then tags `<id>/vX.Y.Z`. Push the tag (owner-gated), then `make consumers` and commit `CONSUMERS.md`.
Breaking after v1: the module path becomes `<id>/vN`.

## Consume a unit
`GOPRIVATE=github.com/ydnikolaev/*` (while private), `go get github.com/ydnikolaev/go-foundation/<id>@v<X.Y.Z>`.
Add the repo to `config/consumers.txt`. A copy instead of an import carries
`// go-foundation-copy: <id>@<sha>` and a backlog row to replace it; `CONSUMERS.md` lists it until it is gone.
A change one consumer needs is made here, upstream, and released.
