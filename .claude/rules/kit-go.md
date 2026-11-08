---
paths:
  - "**/*.go"
  - "**/go.mod"
  - "**/go.sum"
---
<!-- managed by devkit — do not edit; fix upstream in ~/Developer/projects/devkit -->
# Go idiom
- Errors: wrap with `%w`; log or return, never both; map typed errors at the boundary; no swallowed error.
- `context.Context` is the first parameter; no goroutine without an owner that stops it.
- Stdlib first; `log/slog` for logs.
- Tests: table-driven per package; no `//nolint`.
- Tests hermetic (`~/.devkit/current/standards/testing.md`): `t.Setenv`, `t.TempDir()`, an injected clock; wait on a condition, never `time.Sleep`.
- CLI e2e: run the built binary through `os/exec`, one testscript per behaviour, hermetic.
- Interactive prompts: `charm.land/huh`, gated on `term.IsTerminal`, with a headless path; none in a flags-only CLI.
