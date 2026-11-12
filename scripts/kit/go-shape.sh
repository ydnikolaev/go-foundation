#!/usr/bin/env bash
# managed by devkit — do not edit; fix upstream in ~/Developer/projects/devkit
# Purpose: the go pack's code-shape gate (FB-878, FB-879): an entry package (`package main`, or a dir under cmd/) holds the wiring, not
#          the features, and no package outgrows its budget. standards/code-shape.md rules 1 and 5.
# Usage:   scripts/kit/go-shape.sh     (whole tree: tracked and untracked-not-ignored *.go; vendor/, testdata/ and _test.go skipped)
# Count:   code lines per package (a directory). A line counts when any character on it sits outside a comment: blank lines, `//` lines and
#          lines wholly inside a /* */ block do not; code with a trailing comment does. `//` or `/*` inside "..." or a `raw string` is text.
# Bounds:  KIT_GO_ENTRY_LINES (entry package, default 300), KIT_GO_PKG_LINES (any package, default 2000); the environment first, then
#          .devkit/config.env. config/go-shape.ceiling (project-owned) lists `<pkg> <max lines>`: a listed package may be at most that
#          (it replaces the default bound for it); a line above the package's size is a finding too (`lower it to <n>`), so the number only falls.
# Opt-in:  the gate fails (exit 1) only when config/go-shape.ceiling exists (even empty) or a KIT_GO_* bound is set. Until then it prints the
#          findings and an `advisory` line and exits 0, so the first update of a repo with a large package leaves `make check` green.
# Output:  one `kit-go-shape: <pkg> <n> lines > <bound> (standards/code-shape.md rule 1|5)` line per finding (stderr when it fails, stdout
#          in advisory mode), then one summary line. Exit 0 clean or advisory, 1 a finding, 2 a bad bound. No Go toolchain needed.
set -uo pipefail
set -f

top="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$top" || exit 2

CEILING="config/go-shape.ceiling"
optin=0
[ -f "$CEILING" ] && optin=1

# bound NAME DEFAULT: the environment first, then .devkit/config.env (as leak-gate.sh reads its keys); a set value opts the repo in.
bound() {
  local val="${!1-}"
  if [ -z "$val" ] && [ -f .devkit/config.env ]; then
    val="$(sed -n "s/^$1=//p" .devkit/config.env | sed -n 1p | tr -d "\"'")"
  fi
  if [ -z "$val" ]; then
    printf '%s' "$2"
    return 0
  fi
  case "$val" in
    *[!0-9]*)
      echo "kit-go-shape: $1=$val is not a number of lines" >&2
      return 1 ;;
  esac
  printf '%s' "$val"
  return 3
}

entry_bound="$(bound KIT_GO_ENTRY_LINES 300)"; rc=$?
[ "$rc" -eq 1 ] && exit 2
[ "$rc" -eq 3 ] && optin=1
pkg_bound="$(bound KIT_GO_PKG_LINES 2000)"; rc=$?
[ "$rc" -eq 1 ] && exit 2
[ "$rc" -eq 3 ] && optin=1

ceiling_file=""
[ -f "$CEILING" ] && ceiling_file="$CEILING"

out="$(
  while IFS= read -r -d '' f; do
    [ -f "$f" ] || continue  # tracked but deleted from the work tree
    case "/$f" in */vendor/*|*/testdata/*) continue ;; esac
    case "$f" in *_test.go) continue ;; esac
    printf '%s\n' "$f"
  done < <(git ls-files -co --exclude-standard -z -- '*.go') |
  LC_ALL=C awk -v entry="$entry_bound" -v pkgb="$pkg_bound" -v ceilfile="$ceiling_file" '
    # count(f): sets cnt (code lines) and ismain (first code line is `package main`).
    function count(f,   line, n, i, c, c2, d, has, code, seen) {
      cnt = 0; ismain = 0; seen = 0; inblock = 0; inraw = 0
      while ((getline line < f) > 0) {
        n = length(line); has = inraw ? 1 : 0; code = ""; i = 1
        while (i <= n) {
          c = substr(line, i, 1); c2 = substr(line, i, 2)
          if (inblock) { if (c2 == "*/") { inblock = 0; i += 2 } else i++; continue }
          if (inraw)   { if (c == "`") inraw = 0; i++; continue }
          if (c2 == "//") break
          if (c2 == "/*") { inblock = 1; i += 2; continue }
          if (c == " " || c == "\t" || c == "\r") { i++; continue }
          has = 1; code = code c
          if (c == "\"") {
            i++
            while (i <= n) {
              d = substr(line, i, 1)
              if (d == "\\") i += 2
              else if (d == "\"") { i++; break }
              else i++
            }
            continue
          }
          if (c == "`") { inraw = 1; i++; continue }
          if (c == "\047") {                     # a rune literal such as the quote in the single quotes
            i++
            if (substr(line, i, 1) == "\\") i += 2; else i++
            while (i <= n && substr(line, i, 1) != "\047") i++
            i++
            continue
          }
          i++
        }
        if (has) {
          cnt++
          if (!seen) { seen = 1; ismain = (code ~ /^package[ \t]*main([^A-Za-z0-9_]|$)/) }
        }
      }
      close(f)
    }
    function isentry(p) { return p == "cmd" || p ~ /^cmd\// || p ~ /\/cmd$/ || p ~ /\/cmd\// }
    function finding(msg) { print msg; bad++ }
    {
      f = $0; d = f; sub(/\/[^\/]*$/, "", d); if (d == f) d = "."
      count(f)
      if (!(d in size)) { order[++np] = d; size[d] = 0; main[d] = 0 }
      size[d] += cnt
      if (ismain) main[d] = 1
    }
    END {
      if (ceilfile != "") {
        ln = 0
        while ((getline l < ceilfile) > 0) {
          ln++; sub(/\r$/, "", l)
          if (l ~ /^[ \t]*(#|$)/) continue
          nf = split(l, w, /[ \t]+/); k = 1
          if (w[1] == "") k = 2
          p = w[k]; v = w[k + 1]
          if (p == "" || v == "" || v ~ /[^0-9]/ || (k + 2 <= nf && w[k + 2] != "" && w[k + 2] !~ /^#/)) {
            finding("kit-go-shape: " ceilfile " line " ln ": want `<pkg> <max lines>`")
            continue
          }
          sub(/^\.\//, "", p); sub(/\/$/, "", p); if (p == "") p = "."
          ceil[p] = v + 0
        }
        close(ceilfile)
      }
      for (j = 1; j <= np; j++) {
        p = order[j]; n = size[p]; e = main[p] || isentry(p)
        if (p in ceil) {
          if (n > ceil[p]) finding("kit-go-shape: " p " " n " lines > " ceil[p] " (standards/code-shape.md rule " (e ? 1 : 5) ")")
          else if (ceil[p] > n) finding("kit-go-shape: " p " ceiling " ceil[p] " > size " n ": lower it to " n)
        } else if (e && n > entry + 0) finding("kit-go-shape: " p " " n " lines > " entry " (standards/code-shape.md rule 1)")
        else if (n > pkgb + 0) finding("kit-go-shape: " p " " n " lines > " pkgb " (standards/code-shape.md rule 5)")
      }
      for (p in ceil) if (!(p in size) && ceil[p] > 0) finding("kit-go-shape: " p " ceiling " ceil[p] " > size 0: lower it to 0")
      print "#pkgs " np
    }
  '
)"

pkgs="$(printf '%s\n' "$out" | sed -n 's/^#pkgs //p')"
findings="$(printf '%s\n' "$out" | grep -v '^#pkgs ' || true)"
: "${pkgs:=0}"

if [ -z "$findings" ]; then
  echo "kit-go-shape: $pkgs package(s) within bounds"
  exit 0
fi

count="$(printf '%s\n' "$findings" | wc -l | tr -d ' ')"
if [ "$optin" -eq 1 ]; then
  printf '%s\n' "$findings" >&2
  echo "kit-go-shape: $count finding(s) in $pkgs package(s): red" >&2
  exit 1
fi
printf '%s\n' "$findings"
echo "kit-go-shape: advisory (no config/go-shape.ceiling); write it to enforce, standards/code-shape.md#adopt"
exit 0
