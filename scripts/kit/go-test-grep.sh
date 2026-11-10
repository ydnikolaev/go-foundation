#!/usr/bin/env bash
# managed by devkit — do not edit; fix upstream in ~/Developer/projects/devkit
# Purpose: the go pack's grep gates (FB-725, R-214): no `//nolint` and no `time.Sleep(` in a _test.go file.
#          A suppression in a test hides a finding instead of fixing it; a sleep is a race waiting for a slow host.
#          Code keeps the explained `//nolint:<linter> // <why>`: .golangci.yml (nolintlint) polices it, not this gate.
# Usage:   scripts/kit/go-test-grep.sh     (whole tree: tracked and untracked-not-ignored *.go, hits only in *_test.go; vendor/ and testdata/ skipped)
# Output:  one `file:line:text` line per hit on stderr, one summary line. Exit 0 clean, 1 a hit.
# No escape hatch on purpose: fix the finding, or poll with a deadline (a channel, a ticker, `eventually`) not a sleep.
set -uo pipefail
set -f

top="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$top" || exit 2

hits=0
files=0
while IFS= read -r -d '' f; do
  [ -f "$f" ] || continue  # tracked but deleted from the work tree
  case "/$f" in */vendor/*|*/testdata/*) continue ;; esac
  files=$((files + 1))
  case "$f" in
    *_test.go)
      out="$(grep -nE '//[[:space:]]*nolint' -- "$f" 2>/dev/null || true)"
      if [ -n "$out" ]; then
        while IFS= read -r line; do
          printf 'go-test-grep: %s:%s  [//nolint in a test: fix the finding, do not suppress it]\n' "$f" "$line" >&2
          hits=$((hits + 1))
        done <<<"$out"
      fi
      out="$(grep -nE 'time\.Sleep\(' -- "$f" 2>/dev/null || true)"
      if [ -n "$out" ]; then
        while IFS= read -r line; do
          printf 'go-test-grep: %s:%s  [time.Sleep in a test: wait on a signal or poll with a deadline]\n' "$f" "$line" >&2
          hits=$((hits + 1))
        done <<<"$out"
      fi ;;
  esac
done < <(git ls-files -co --exclude-standard -z -- '*.go')

if [ "$hits" -gt 0 ]; then
  echo "go-test-grep: $hits hit(s) in $files Go file(s): red" >&2
  exit 1
fi
echo "go-test-grep: $files Go file(s) clean"
exit 0
