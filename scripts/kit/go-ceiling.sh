#!/usr/bin/env bash
# managed by devkit — do not edit; fix upstream in ~/Developer/projects/devkit
# Purpose: the ceiling judge of the go pack's lint and grep gates (FB-932, R-259): an adopting repo records how many findings each package
#          has today, and the gate then refuses a new one and asks to lower a line when one is fixed. Same ratchet shape as go-shape.sh.
# Usage:   scripts/kit/go-ceiling.sh <tag> <file> [--write]     <tag> names the gate in every message (`kit-go-lint`, `kit-go-grep`).
# Input:   stdin, one finding per line: `<path>\t<check>\t<text>`; <path> is relative to the git top, <check> a linter or `nolint|sleep`.
# File:    <file> (project-owned, config/go-lint.ceiling or config/go-grep.ceiling): `<pkg> <check> <max>`, `#` comments, `.` the repo root.
#          <pkg> is the directory of the finding's file, one key space over every go.mod dir; an absent key has ceiling 0.
# Check:   a key over its max prints every finding line of the key, then `<n> > <max>: a new finding`; a max above the count prints
#          `lower it to <n>`; a malformed line is named. A `typecheck` finding is always red: a build error is never baselined.
#          Exit 0 every key at its ceiling, 1 a key off it, 2 usage or a missing file.
# --write: writes <file> from the counts (sorted, with a header: date, short HEAD, tool from KIT_CEILING_TOOL); refuses (exit 1) a file that
#          exists and any `typecheck` finding. Exit 0 written, 1 refused.
set -uo pipefail
set -f

tag="${1:-}"
file="${2:-}"
write=0
if [ "$#" -lt 2 ] || [ "$#" -gt 3 ] || [ -z "$tag" ] || [ -z "$file" ]; then
  echo "usage: go-ceiling.sh <tag> <file> [--write]" >&2
  exit 2
fi
if [ "$#" -eq 3 ]; then
  if [ "$3" != "--write" ]; then
    echo "usage: go-ceiling.sh <tag> <file> [--write]" >&2
    exit 2
  fi
  write=1
fi

tab="$(printf '\t')"

# stage 1: <path>\t<check>\t<text> becomes <pkg>\t<check>\t<text>, grouped by key (a stable sort keeps the input order inside a key).
rows="$(
  LC_ALL=C awk -F'\t' '
    $0 == "" { next }
    {
      p = $1; c = $2
      i = index($0, "\t"); rest = substr($0, i + 1); j = index(rest, "\t"); text = substr(rest, j + 1)
      sub(/^\.\//, "", p)
      d = p
      if (sub(/\/[^\/]*$/, "", d) == 0) d = "."
      printf "%s\t%s\t%s\n", d, c, text
    }
  ' | LC_ALL=C sort -s -t "$tab" -k1,1 -k2,2
)"

if [ "$write" -eq 1 ]; then
  if [ -e "$file" ]; then
    echo "$tag: $file exists: lower or raise a line by hand" >&2
    exit 1
  fi
  typecheck="$(printf '%s\n' "$rows" | LC_ALL=C awk -F'\t' '$2 == "typecheck" { print $3 }')"
  if [ -n "$typecheck" ]; then
    printf '%s\n' "$typecheck" >&2
    echo "$tag: typecheck finding(s): a build error is never baselined; fix it, then write $file" >&2
    exit 1
  fi
  counts="$(printf '%s\n' "$rows" | LC_ALL=C awk -F'\t' '
    $0 == "" { next }
    { k = $1 " " $2; if (k != last) { if (last != "") print last, n; last = k; n = 0 } n++ }
    END { if (last != "") print last, n }
  ')"
  rev="$(git rev-parse --short HEAD 2>/dev/null || echo none)"
  mkdir -p "$(dirname "$file")" || exit 1
  {
    echo "# $tag ceiling: written $(date +%F) at $rev by ${KIT_CEILING_TOOL:-$tag}; a line is \`<pkg> <check> <max>\`."
    echo "# A line only goes down: a fixed finding lowers its line; a raise is a hand edit. standards/adopt.md#go-baseline"
    if [ -n "$counts" ]; then printf '%s\n' "$counts"; fi
  } >"$file" || exit 1
  nkeys=0
  nfind=0
  if [ -n "$counts" ]; then
    nkeys="$(printf '%s\n' "$counts" | wc -l | tr -d ' ')"
    nfind="$(printf '%s\n' "$counts" | LC_ALL=C awk '{ s += $NF } END { print s + 0 }')"
  fi
  echo "$tag: wrote $file: $nfind finding(s) in $nkeys key(s)"
  exit 0
fi

if [ ! -f "$file" ]; then
  echo "$tag: $file is missing" >&2
  exit 2
fi

out="$(
  LC_ALL=C awk -F'\t' -v tag="$tag" -v file="$file" '
    function finding(msg) { print msg; bad++ }
    function flush(   m, n2) {
      if (key == "") return
      keys++; seen[key] = 1
      m = (key in cmax) ? cmax[key] : 0
      if (chk == "typecheck") {
        printf "%s", buf
        finding(tag ": " pkg " typecheck " n ": a build error is never baselined; fix it")
      } else if (n > m) {
        printf "%s", buf
        finding(tag ": " pkg " " chk " " n " > " m ": a new finding; fix it (a raise is a hand edit of " file ")")
      } else if (m > n) finding(tag ": " pkg " " chk " ceiling " m " > count " n ": lower it to " n)
    }
    BEGIN {
      ln = 0
      while ((getline l < file) > 0) {
        ln++; sub(/\r$/, "", l)
        if (l ~ /^[ \t]*(#|$)/) continue
        nf = split(l, w, /[ \t]+/); k = 1
        if (w[1] == "") k = 2
        p = w[k]; c = w[k + 1]; v = w[k + 2]; x = w[k + 3]
        if (p == "" || c == "" || v == "" || v ~ /[^0-9]/ || (x != "" && x !~ /^#/)) {
          finding(tag ": " file " line " ln ": want `<pkg> <check> <max>`")
          continue
        }
        sub(/^\.\//, "", p); sub(/\/$/, "", p); if (p == "") p = "."
        q = p " " c
        if (!(q in cmax)) { corder[++nc] = q; cpkg[q] = p; cchk[q] = c }
        cmax[q] = v + 0
      }
      close(file)
    }
    $0 == "" { next }
    {
      q = $1 " " $2
      if (q != key) { flush(); key = q; pkg = $1; chk = $2; n = 0; buf = "" }
      n++
      i = index($0, "\t"); rest = substr($0, i + 1); j = index(rest, "\t")
      buf = buf substr(rest, j + 1) "\n"
    }
    END {
      flush()
      for (j = 1; j <= nc; j++) {
        q = corder[j]
        if (q in seen) continue
        keys++
        if (cmax[q] > 0) finding(tag ": " cpkg[q] " " cchk[q] " ceiling " cmax[q] " > count 0: lower it to 0")
      }
      print "#sum " bad + 0 " " keys + 0
    }
  ' <<<"$rows"
)"

sum="$(printf '%s\n' "$out" | sed -n 's/^#sum //p')"
bad="${sum%% *}"
keys="${sum##* }"
: "${bad:=0}" "${keys:=0}"
findings="$(printf '%s\n' "$out" | grep -v '^#sum ' || true)"

if [ "$bad" -gt 0 ]; then
  printf '%s\n' "$findings" >&2
  echo "$tag: $bad key(s) off the ceiling: red" >&2
  exit 1
fi
echo "$tag: $keys key(s) at their ceiling"
exit 0
