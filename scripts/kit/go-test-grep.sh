#!/usr/bin/env bash
# managed by devkit — do not edit; fix upstream in ~/Developer/projects/devkit
# Purpose: the go pack's grep gates (FB-725, R-214): no `//nolint` and no `time.Sleep(` in a _test.go file.
#          A suppression in a test hides a finding instead of fixing it; a sleep is a race waiting for a slow host.
#          Code keeps the explained `//nolint:<linter> // <why>`: .golangci.yml (nolintlint) polices it, not this gate.
# Usage:   scripts/kit/go-test-grep.sh     (whole tree: tracked and untracked-not-ignored *.go, hits only in *_test.go; vendor/ and testdata/ skipped)
# Output:  one `file:line:text` line per hit on stderr, one summary line. Exit 0 clean, 1 a hit.
# The one hatch is the adoption ceiling (FB-932, R-259), which only goes down: with config/go-grep.ceiling (project-owned, written once by
#          `make kit-go-baseline`) the hits are counted per package and pattern (`nolint`, `sleep`) and judged by go-ceiling.sh: a key over its
#          ceiling is red, a ceiling above its count says `lower it to <n>`. KIT_GO_CEILING=write writes that file (refuses one that exists).
#          Without the file every hit is red, and a red run ends with a pointer to standards/adopt.md#go-baseline.
#          Fix the finding, or poll with a deadline (a channel, a ticker, `eventually`) not a sleep.
set -uo pipefail
set -f

top="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$top" || exit 2
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

CEILING="config/go-grep.ceiling"
write=0
[ "${KIT_GO_CEILING:-}" = "write" ] && write=1
mode=plain
{ [ -f "$CEILING" ] || [ "$write" -eq 1 ]; } && mode=ceiling

hits=0
files=0
rows=""
tab="$(printf '\t')"
# hit PATH CHECK LINE MESSAGE: a plain run prints the hit; a ceiling run keeps it as a row for go-ceiling.sh.
hit() {
  if [ "$mode" = ceiling ]; then
    rows+="$1$tab$2$tab$1:$3"$'\n'
  else
    printf 'go-test-grep: %s:%s  [%s]\n' "$1" "$3" "$4" >&2
  fi
  hits=$((hits + 1))
}
while IFS= read -r -d '' f; do
  [ -f "$f" ] || continue  # tracked but deleted from the work tree
  case "/$f" in */vendor/*|*/testdata/*) continue ;; esac
  files=$((files + 1))
  case "$f" in
    *_test.go)
      out="$(grep -nE '//[[:space:]]*nolint' -- "$f" 2>/dev/null || true)"
      if [ -n "$out" ]; then
        while IFS= read -r line; do
          hit "$f" nolint "$line" "//nolint in a test: fix the finding, do not suppress it"
        done <<<"$out"
      fi
      out="$(grep -nE 'time\.Sleep\(' -- "$f" 2>/dev/null || true)"
      if [ -n "$out" ]; then
        while IFS= read -r line; do
          hit "$f" sleep "$line" "time.Sleep in a test: wait on a signal or poll with a deadline"
        done <<<"$out"
      fi ;;
  esac
done < <(git ls-files -co --exclude-standard -z -- '*.go')

if [ "$mode" = ceiling ]; then
  if [ "$write" -eq 1 ]; then
    printf '%s' "$rows" | KIT_CEILING_TOOL="go-test-grep.sh" bash "$here/go-ceiling.sh" kit-go-grep "$CEILING" --write
  else
    printf '%s' "$rows" | KIT_CEILING_TOOL="go-test-grep.sh" bash "$here/go-ceiling.sh" kit-go-grep "$CEILING"
  fi
  exit $?
fi

if [ "$hits" -gt 0 ]; then
  echo "go-test-grep: $hits hit(s) in $files Go file(s): red" >&2
  echo "kit-go-grep: adopting with old findings? standards/adopt.md#go-baseline" >&2
  exit 1
fi
echo "go-test-grep: $files Go file(s) clean"
exit 0
