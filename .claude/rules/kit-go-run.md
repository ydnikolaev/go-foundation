<!-- managed by devkit — do not edit; fix upstream in ~/Developer/projects/devkit -->
- Verify by package: `make test TESTS=<pkg dir>` (`RUN=TestX`), never a bare `-run`; no repo-wide `go build ./...`, `go test ./...`. ~
- `make check`: `-short`, `KIT_GO_TEST_TAGS_LIGHT`; `make check-full`: all, `KIT_GO_TEST_TAGS_FULL`; `// tier:`/`//go:build slow` prices a package. ~
- `go test` runs `-trimpath`: find test files with `os.Getwd`, never `runtime.Caller`. ~
- Wrap: `KIT_GO_TEST_RUNNER=./wrap.sh make kit-go-test`, `KIT_GO_TEST_FLAGS`; repeat: `make kit-go-test-flaky TESTS=<pkg dir> COUNT=<n>`, `config/go-{skip,flaky}.ledger`. ~
- Build the e2e binary with `GOWORK=off`. ~
- Coverage floors, the lead's at hand-off: `make coverage-raise`, `make check-full`. ~
- `kit-go-lint` refuses unformatted `.go`, untidy `go.mod`; `make kit-go-baseline` once for old findings, a fixed finding lowers its line. ~
- Go caches: main checkout's `.cache/`; `make cache-trim`, never `go clean -modcache`. ~
