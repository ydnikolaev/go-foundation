#!/usr/bin/env bash
# managed by devkit — do not edit; fix upstream in ~/Developer/projects/devkit
# Purpose: the Go import-edge lister of the module boundary gate (scripts/kit/modules.py check); renders with the go module.
# Usage:   scripts/kit/modules-go.sh        (run at the repo root; modules.py runs it so)
# Output:  one line `<dir>/ <dir>/` per package-to-package import that stays inside the repo (repo-relative, the root is
#          `./`), deduped and sorted; `go list` reports packages, not files or lines. Exit 0 ok, 1 a `go` call failed or
#          git failed, 2 no go.
# Rules:
#   modules   each listed go.mod (git ls-files --cached --others --exclude-standard) not under a testdata/ or vendor/ path
#             segment; for each, in its dir with GOWORK=off: `go list -m` names the module path, then `go list -e` over
#             ./... lists the packages with their Imports, TestImports and XTestImports
#   edges     an import whose prefix is one of the repo's module paths (the longest wins) becomes that module's dir plus
#             the rest of the path, so a nested module (a go.mod in a subdir) resolves; every other import (the standard
#             library, an external module) prints nothing; a package importing itself prints nothing
#   -e        a package that does not load (a missing go.sum entry, an import nobody provides) is still listed with the
#             imports its files name, so the lister fails only for a failed `go` call, never for a build problem
# Known gaps: build-tagged files are read only under the tags in GOFLAGS (make exports them; run by hand, go's defaults
#             apply, as do its GOCACHE and GOMODCACHE); `go list` reads every .go file of a package dir, an ignored one
#             included (unlike the Python and TS listers, which read listed files); a package named only by a //go:embed or
#             cgo include is not an import; a go.mod under testdata/ or vendor/ is not a module here.
set -uo pipefail
set -f

if ! command -v go >/dev/null 2>&1; then
  echo "modules-go: go is not on PATH: install Go, or run through a shell that has it" >&2
  exit 2
fi

root_logical="$PWD"
root_physical="$(pwd -P)"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/modules-go.XXXXXX")" || exit 1
trap 'rm -rf "$tmp"' EXIT

gomods="$tmp/gomods"
if ! git ls-files --cached --others --exclude-standard -z | tr '\0' '\n' \
  | awk '/(^|\/)go\.mod$/ && $0 !~ /(^|\/)(testdata|vendor)\//' | sort > "$gomods"; then
  echo "modules-go: git ls-files failed" >&2
  exit 1
fi

# pass 1: the module path of every go.mod -> its dir (repo-relative, `.` for the root)
map="$tmp/map"
: > "$map"
dirs=()
while IFS= read -r file; do
  [ -f "$file" ] || continue
  dir="$(dirname "$file")"
  if ! path="$(cd "$dir" && GOWORK=off go list -m -f '{{.Path}}' 2>"$tmp/err")"; then
    echo "modules-go: go list -m failed in $dir:" >&2
    head -n 5 "$tmp/err" >&2
    exit 1
  fi
  path="${path%%$'\n'*}"  # the first line only, without an early-reading pipe (SIGPIPE under pipefail)
  [ -n "$path" ] || continue
  printf '%s\t%s\n' "$path" "$dir" >> "$map"
  dirs+=("$dir")
done < "$gomods"

# pass 2: the packages of every module and what they import
edges="$tmp/edges"
: > "$edges"
for dir in ${dirs[@]+"${dirs[@]}"}; do
  if ! (cd "$dir" && GOWORK=off go list -e \
      -f '{{.Dir}}	{{join .Imports " "}} {{join .TestImports " "}} {{join .XTestImports " "}}' ./... \
      > "$tmp/list" 2>"$tmp/err"); then
    echo "modules-go: go list failed in $dir:" >&2
    head -n 5 "$tmp/err" >&2
    exit 1
  fi
  awk -F'\t' -v p1="$root_logical" -v p2="$root_physical" '
    NR == FNR { mp[$1] = $2; next }
    {
      dir = $1
      rel = ""
      if (dir == p1 || dir == p2) rel = "."
      else if (index(dir, p1 "/") == 1) rel = substr(dir, length(p1) + 2)
      else if (index(dir, p2 "/") == 1) rel = substr(dir, length(p2) + 2)
      else next
      n = split($2, imp, " ")
      for (i = 1; i <= n; i++) {
        best = ""
        for (k in mp) {
          if ((imp[i] == k || index(imp[i], k "/") == 1) && length(k) > length(best)) best = k
        }
        if (best == "") continue
        rest = substr(imp[i], length(best) + 2)
        base = mp[best]
        if (base == ".") tgt = rest
        else if (rest == "") tgt = base
        else tgt = base "/" rest
        if (tgt == "") tgt = "."
        if (tgt != rel) print rel "/ " tgt "/"
      }
    }' "$map" "$tmp/list" >> "$edges"
done

sort -u "$edges"
