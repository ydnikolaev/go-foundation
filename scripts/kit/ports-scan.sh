#!/usr/bin/env bash
# managed by devkit — do not edit; fix upstream in ~/Developer/projects/devkit
# Purpose: fail on a literal host port in a compose file. config/ports.env is the one place a host port is written
#          (standards/ports.md rule 3), so a `ports:` entry whose host side is a number, a range or `${VAR:-n}` is a hit.
#          Judges the host port only, never the bind address. Text scan (bash + awk, no PyYAML): it reads `ports:` blocks
#          (block lists, a compact list at the key's column, inline `[...]` lists, long form `published:`); it does NOT read
#          ports that arrive through YAML anchors, `extends` or an override file outside the globs.
# Usage:   scripts/kit/ports-scan.sh        (run from the repo root; no arguments)
#          Scans compose*.y*ml and docker-compose*.y*ml at the root and under deploy/.
# Exit:    0 no literal host port (or no compose file); 1 at least one hit (each printed as `file:line: literal host port ...`);
#          2 the scan itself failed.
# Needs:   bash 3.2 and a POSIX/BSD awk (no gawk extensions).

files=()
for f in compose*.y*ml docker-compose*.y*ml deploy/compose*.y*ml deploy/docker-compose*.y*ml; do
  if [ -f "$f" ]; then
    files+=("$f")
  fi
done
if [ "${#files[@]}" -eq 0 ]; then
  echo "ports-scan: no compose file at the root or under deploy/; nothing to scan"
  exit 0
fi

IFS= read -r -d '' prog <<'AWK_PROG' || true
function trim(s) { sub(/^[ \t]+/, "", s); sub(/[ \t]+$/, "", s); return s }
function unquote(s) { s = trim(s); sub(/^["']/, "", s); sub(/["']$/, "", s); return s }
# every ${...} (nested braces counted) becomes one opaque word @Tn@, so its colons and dashes never split a field
function opaque(s,   out, i, j, c, d, depth) {
  out = ""; i = 1; ntok = 0
  while (i <= length(s)) {
    c = substr(s, i, 1)
    if (c == "$" && substr(s, i + 1, 1) == "{") {
      depth = 0; j = i + 1
      while (j <= length(s)) {
        d = substr(s, j, 1)
        if (d == "{") depth++
        else if (d == "}") { depth--; if (depth == 0) break }
        j++
      }
      ntok++; tok[ntok] = substr(s, i, j - i + 1); out = out "@T" ntok "@"; i = j + 1
    } else { out = out c; i++ }
  }
  return out
}
function restore(s,   i, k, tag) {
  for (i = 1; i <= ntok; i++) {
    tag = "@T" i "@"
    while ((k = index(s, tag)) > 0) s = substr(s, 1, k - 1) tok[i] substr(s, k + length(tag))
  }
  return s
}
function hit(entry, why) { printf "%s:%d: literal host port \"%s\" %s\n", FILENAME, FNR, entry, why; hits++ }
# h: the host side, tokens opaque; disp: the entry as written
function judge(h, disp,   t, d) {
  if (h ~ /^@T[0-9]+@$/) {
    t = tok[substr(h, 3, length(h) - 3)]
    if (t ~ /^[$][{][A-Za-z_][A-Za-z0-9_]*:?-[0-9]+(-[0-9]+)?[}]$/) {
      d = t; sub(/^[$][{][A-Za-z_][A-Za-z0-9_]*:?-/, "", d); sub(/[}]$/, "", d)
      hit(disp, "- default " d " hardcodes a host port; config/ports.env sets it")
    }
    return
  }
  if (h ~ /^[0-9]+(-[0-9]+)?$/)
    hit(disp, "- config/ports.env is the one place a host port is written: declare <PREFIX>_<ROLE>_PORT there and write ${NAME} here")
}
function short(e,   s, n, f, host) {
  s = unquote(e)
  sub(/\/(tcp|udp)$/, "", s)
  host = ""
  if (substr(s, 1, 1) == "[") {
    n = index(s, "]")
    if (n == 0) return
    s = substr(s, n + 1); sub(/^:/, "", s)
    n = split(s, f, ":")
    if (n >= 2) host = f[1]
  } else {
    n = split(s, f, ":")
    if (n >= 3) host = f[2]
    else if (n == 2) host = f[1]
  }
  if (host == "") return
  judge(host, restore(unquote(e)))
}
function published(v,   s) {
  v = trim(v); sub(/[ \t]*[}]$/, "", v)
  s = unquote(v)
  judge(s, "published: " restore(s))
}
# one entry of a ports: list (tokens opaque): a long-form key, a flow-map piece, or a short form string
function item(t) {
  t = trim(t)
  sub(/^[{][ \t]*/, "", t)
  if (t == "") return
  if (t ~ /^published:/) { sub(/^published:[ \t]*/, "", t); published(t); return }
  if (t ~ /^[A-Za-z_][A-Za-z0-9_-]*:([ \t]|$)/) return
  short(t)
}
function flow(s,   n, parts, i) {
  s = trim(s)
  if (substr(s, length(s)) == "]") { s = substr(s, 1, length(s) - 1); inflow = 0 } else inflow = 1
  n = split(s, parts, ",")
  for (i = 1; i <= n; i++) item(parts[i])
}
FNR == 1 { inblk = 0; inflow = 0 }
{
  line = $0; sub(/\r$/, "", line)
  if (line ~ /^[ \t]*$/) next
  t = line; sub(/^ +/, "", t)
  if (t ~ /^#/) next
  sub(/[ \t]+#.*$/, "", line)
  t = line; sub(/^ +/, "", t); ind = length(line) - length(t); t = trim(t)
  if (inflow) { flow(opaque(t)); next }
  if (inblk) {
    isitem = (t ~ /^-([ \t]|$)/)
    if (ind > kind || (ind == kind && isitem)) {
      t = opaque(t)
      if (isitem) { sub(/^-[ \t]*/, "", t); item(t) }
      else if (t ~ /^published:/) item(t)
      next
    }
    inblk = 0
  }
  if (t ~ /^ports:/) {
    kind = ind; rest = t; sub(/^ports:[ \t]*/, "", rest)
    if (rest == "") inblk = 1
    else if (substr(rest, 1, 1) == "[") { rest = opaque(substr(rest, 2)); flow(rest) }
  }
}
END { exit 0 }
AWK_PROG

out="$(awk "$prog" "${files[@]}")"
rc=$?
if [ "$rc" -ne 0 ]; then
  echo "ports-scan: the awk scan failed (exit $rc)" >&2
  exit 2
fi
if [ -n "$out" ]; then
  printf '%s\n' "$out"
  echo "ports-scan: FAILED - a literal host port in a compose file; config/ports.env is the one place a host port is written (standards/ports.md)" >&2
  exit 1
fi
echo "ports-scan: ok (${#files[@]} compose file(s) scanned)"
exit 0
