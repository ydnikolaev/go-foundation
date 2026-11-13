<!-- managed by devkit — do not edit; fix upstream in ~/Developer/projects/devkit -->
- Verify by package: `go test ./pkg/x`, never `-run`. A coder runs no repo-wide `go build ./...` or `go test ./...`. ~
- `make check` tests the changed packages, `make check-full` all. ~
- Build the e2e binary with `GOWORK=off`. ~
- Coverage floors: the lead raises them at hand-off: bare `make check-full`, `make coverage-raise`, commit `config/coverage-go.yaml` (or its `coverage-go.d/*.yaml` fragments), then `devkit gate`. ~
- Old lint and grep findings: `make kit-go-baseline` writes `config/go-lint.ceiling` and `config/go-grep.ceiling` once (standards/adopt.md#go-baseline); a fixed finding lowers its line. ~
- Run Go through `make`: `mk/kit-go.mk` keeps the caches in the main checkout's `.cache/`; trim with `make cache-trim`, never `go clean -modcache`. ~
