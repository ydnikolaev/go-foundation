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
#   tags      FB-685: a file under `//go:build <tag>` is invisible to a plain `go list`, so a second listing runs with the
#             tags those files name: per module, `go list` gives each package's IgnoredGoFiles, the first `//go:build` line
#             of each is read, and its plain identifiers become `-tags` (a term under `!`, a GOOS or GOARCH name, `ignore`,
#             cgo, unix, the compiler names and go1.N are skipped); a tag already in GOFLAGS stays in the list. The two
#             listings are merged, so a file selected by the absence of a tag is still read
# Known gaps: the GOFLAGS tags and the tags found in ignored files are the only ones read (run by hand, go's defaults apply,
#             as do its GOCACHE and GOMODCACHE); a `// +build` line without a `//go:build` one is not read; a tag under a
#             nested `!( )` is turned on like a plain one (harmless: the plain listing is merged); all found tags are on at
#             once, so two files that exclude each other by tag are not both read in one listing (the plain listing keeps
#             the default side); `go list` reads every .go file of a package
#             dir (unlike the Python and TS listers, which read listed files); a package named only by a //go:embed or cgo
#             include is not an import; a go.mod under testdata/ or vendor/ is not a module here.
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

# FB-685: the tags a file's first //go:build line names; the skip set holds what `go build` fills in itself
# shellcheck disable=SC2016  # an awk program: its $0 is awk's, not the shell's
tagawk='
FNR == 1 { done = 0 }
done { next }
/^[[:space:]]*package[[:space:]]/ { done = 1; next }
/^\/\/go:build[[:space:]]/ {
  done = 1
  line = substr($0, 11)
  gsub(/![[:space:]]*\([^()]*\)/, " ", line)
  gsub(/![[:space:]]*[A-Za-z0-9_.]+/, " ", line)
  n = split(line, tok, /[^A-Za-z0-9_.]+/)
  for (i = 1; i <= n; i++) {
    t = tok[i]
    if (t == "" || (t in skip) || t ~ /^go1\./ || t ~ /^goexperiment\./) continue
    print t
  }
}'
skiptags="ignore cgo unix gc gccgo race msan asan boringcrypto aix android darwin dragonfly freebsd hurd illumos ios js linux nacl netbsd openbsd plan9 solaris wasip1 windows zos 386 amd64 amd64p32 arm armbe arm64 arm64be loong64 mips mipsle mips64 mips64le mips64p32 mips64p32le ppc ppc64 ppc64le riscv riscv64 s390 s390x sparc sparc64 wasm"

# the tags GOFLAGS already carries (-tags=a,b): a -tags on the command line replaces them, so they are passed on again
goflags_tags=""
for flag in ${GOFLAGS:-}; do
  case "$flag" in
    -tags=*|--tags=*) goflags_tags="${goflags_tags:+$goflags_tags,}${flag#*=}" ;;
  esac
done

# pass 2: the packages of every module and what they import: listed plain, then again under the tags of the ignored files
edges="$tmp/edges"
: > "$edges"
fmt='{{.Dir}}	{{join .Imports " "}} {{join .TestImports " "}} {{join .XTestImports " "}}'
for dir in ${dirs[@]+"${dirs[@]}"}; do
  if ! (cd "$dir" && GOWORK=off go list -e -f "$fmt" ./... > "$tmp/lists" 2>"$tmp/err"); then
    echo "modules-go: go list failed in $dir:" >&2
    head -n 5 "$tmp/err" >&2
    exit 1
  fi
  if ! (cd "$dir" && GOWORK=off go list -e -f '{{.Dir}}	{{join .IgnoredGoFiles " "}}' ./... > "$tmp/ignored" 2>"$tmp/err"); then
    echo "modules-go: go list failed in $dir:" >&2
    head -n 5 "$tmp/err" >&2
    exit 1
  fi
  files=()
  while IFS=$'\t' read -r pkgdir names; do
    for name in $names; do
      files+=("$pkgdir/$name")
    done
  done < "$tmp/ignored"
  tags=""
  if [ "${#files[@]}" -gt 0 ]; then
    awk -v skiplist="$skiptags" 'BEGIN { n = split(skiplist, s, " "); for (i = 1; i <= n; i++) skip[s[i]] = 1 }'"$tagawk" \
      "${files[@]}" 2>/dev/null | sort -u > "$tmp/tags"
    tags="$(paste -sd, - < "$tmp/tags")"
  fi
  if [ -n "$tags" ]; then
    if ! (cd "$dir" && GOWORK=off go list -e -tags "${goflags_tags:+$goflags_tags,}$tags" -f "$fmt" ./... \
        > "$tmp/list" 2>"$tmp/err"); then
      echo "modules-go: go list failed in $dir:" >&2
      head -n 5 "$tmp/err" >&2
      exit 1
    fi
    cat "$tmp/list" >> "$tmp/lists"
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
    }' "$map" "$tmp/lists" >> "$edges"
done

sort -u "$edges"
