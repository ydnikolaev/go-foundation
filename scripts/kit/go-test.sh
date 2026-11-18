#!/usr/bin/env bash
# managed by devkit — do not edit; fix upstream in ~/Developer/projects/devkit
# Purpose: the go stack's adapter of the kit unit driver (scripts/kit/check-units.py go): a unit is a package dir. Registered by
#          `@units kit-go-test scripts/kit/go-test.sh` in .devkit/check-map.d/go.map; the selection, the tiers and the store are the driver's.
# Usage:   go-test.sh units --list
#          go-test.sh units --run --tier <light|full> --out FILE [--filter T1,T2] <package dir>...
#          go-test.sh flaky [<package dir or file>...]     (make kit-go-test-flaky COUNT=<n>; KIT_CHECK_PATHS names packages too)
#   --list   one JSON line per package of every go.mod of the tree (a path segment testdata, vendor, _x or .x leaves a module out):
#            {"id": <repo-relative package dir, `.` the root>, "inputs": [...], "needs": ["db"]|[], "tier": "slow"|"smoke"|"unit"|null}.
#            One `go list -e -deps -test` per module. inputs = the package's own files, its test and embedded files, its testdata
#            files, every file of each package of the repo it imports (its tests' imports too) and the go.mod and go.sum of their
#            modules (DK-046). needs db = its closure holds a path of KIT_GO_DB_IMPORTS. tier = a `//go:build slow` line or a
#            `// tier: <unit|smoke|slow>` comment above the package clause of a _test.go. It runs no test.
#   --run    one `go test -json` call per module over the named packages; one result line per package to FILE: {"id", "exit",
#            "wall_s", "cached", "tests", "skips", "tier"}, `tier` = light with -short (the light ask), else full. A package whose
#            floor scripts/kit/cover-gate.py refuses, or whose skip the ledger lacks, is red. --filter T1,T2 runs only those
#            tests (-run), judges no coverage and fails unless every named test ran and passed (FB-1136): a pass prints
#            `go-test: pass <test> (<pkg>)`, a missing one `go-test: <test>: did not run`
#   flaky    every named package (a dir, or a file of one; none: every package of every module) in one call with -count=N appended,
#            no -short, no coverage, only the named packages, never their importers (FB-1144); config/go-flaky.ledger judges it
# Env:     KIT_GO_TEST_FLAGS   extra flags for the test command, word-split (e.g. "-race"). A run-count flag there or in GOFLAGS turns the
#                              test cache off (R-271): exit 1 outside the flaky goal
#          KIT_GO_FLAKY_COUNT  the flaky goal's run count N (make kit-go-test-flaky COUNT=<n>); exit 2 when it is no whole number >= 1
#          KIT_GO_DB_IMPORTS   space-separated import paths that mark a database package (empty by default; a repo sets its helper's)
#          KIT_GO_TEST_TAGS_LIGHT, KIT_GO_TEST_TAGS_FULL   build tags of each tier (environment only; comma or space list;
#                              mk/kit-go.mk exports the project Makefile's variable of that name, FB-1193): `//go:build integration`
#                              runs in check-full with `KIT_GO_TEST_TAGS_FULL = integration` in the Makefile (FB-1141); the full tier
#                              always carries the tag `slow`
#          KIT_GO_TEST_RUNNER  a command prefix, word-split, empty by default: it runs before `go test` in every module
#                              (`KIT_GO_TEST_RUNNER="./wrap.sh"` runs `<repo>/wrap.sh go test ...`); a first word holding a slash and
#                              not absolute is taken from the repo root, a bare word is found on PATH; echoed in the module line
#          GOTMPDIR            set for each `go test` to <base>/<project>-gotmp-<hash>/ (R-236, FB-960, R-270; <base> is DEVKIT_TMP_BASE,
#                              else $HOME/Developer/.tmp when $HOME/Developer exists, else ${TMPDIR:-/tmp}): outside any work tree, one
#                              path per repo, the same for every worktree of it and every run, so a test that calls t.TempDir() hits
#                              go's test cache from any worktree. It is kept between runs and held under a shared flock (python3's
#                              fcntl: flock(1) is not on macOS) while a run is in it; at start a run takes the lock exclusively when no
#                              other run holds it and removes what is older than a day. No python3: no lock, no sweep, the run goes on
#          GOFLAGS             `go test` runs with -trimpath added (R-270): the build holds no work tree path, so a copy of the tree
#                              in another directory (a lane's worktree) hits the same test cache. runtime.Caller paths are then
#                              module-relative: a test finds its files from os.Getwd, which `go test` sets to the package dir (FB-1148)
#          HEAVY_RUN_CORES     the grant (the queue's, or the GNUmakefile shim's size): `-p <it>` goes first, unless KIT_GO_TEST_FLAGS
#                              carries its own `-p`
#          the same `go test` writes a coverage profile (a -coverprofile of the project's own is read, not replaced); after every module
#          passed, scripts/kit/cover-gate.py judges the packages that ran against config/coverage-go.yaml (a reused package is not
#          judged again, and its floor row is no stale one). -coverpkg: not judged. A package whose test skips under -short is not
#          judged in that call: one line names it
# Output:  the `go test` output filtered by go-events.py, a summary line. Exit 0 green, 1 a package or a floor red, 2 no go or usage.
set -uo pipefail
set -f  # paths and flags are data: never glob them

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"  # go-events.py sits beside this script

mode=""
case "${1:-}" in
  units) case "${2:-}" in --list) mode=list ;; --run) mode=run ;; esac; shift 2 2>/dev/null ;;
  flaky) mode=flaky; shift ;;
esac
if [ -z "$mode" ]; then
  echo "usage: go-test.sh units --list | units --run --tier <light|full> --out FILE [--filter T1,T2] <pkg dir>... | flaky [<pkg dir>...]" >&2
  exit 2
fi
if ! command -v go >/dev/null 2>&1; then
  echo "go-test: go is not on PATH: install Go, or run through a shell that has it" >&2
  exit 2
fi
top="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$top" || exit 2

# a dir path cut at the first segment `go test ./...` never enters (testdata, vendor, _x, .x); "." for the root
trim_special() {
  local segs s out=""
  IFS=/ read -r -a segs <<<"$1"
  for s in "${segs[@]}"; do
    case "$s" in testdata|vendor|_*|.?*) break ;; esac
    out="${out:+$out/}$s"
  done
  printf '%s' "${out:-.}"
}

# every module dir of the tree, one per line: each go.mod still on disk, not under testdata/, vendor/, _x or .x
all_mods() {
  local gomod dir
  while IFS= read -r gomod; do
    [ -f "$gomod" ] || continue  # tracked but deleted from the work tree: the module is gone
    dir="$(dirname "$gomod")"
    [ "$(trim_special "$dir")" = "$dir" ] && printf '%s\n' "$dir"
  done < <(git ls-files -co --exclude-standard -- go.mod '*/go.mod' | sort -u)
}

# the nearest dir at or above $1 holding a go.mod; fails when there is none
nearest_mod() {
  local d="$1"
  while :; do
    [ -f "$d/go.mod" ] && { printf '%s' "$d"; return 0; }
    [ "$d" = "." ] && return 1
    case "$d" in */*) d="${d%/*}" ;; *) d="." ;; esac
  done
}

# a key of the environment, else of .devkit/config.env (KEY=VALUE)
conf_val() {
  local v="${!1:-}"
  [ -n "$v" ] || v="$(sed -n "s/^$1=//p" "$top/.devkit/config.env" 2>/dev/null | tail -n 1)"
  printf '%s' "$v"
}

# the -tags list of a tier: the tags GOFLAGS already carries (a -tags on the command line replaces them), then the tier's own
tags_for() {
  local tier="$1" word keep="" extra
  for word in ${GOFLAGS:-}; do
    case "$word" in -tags=*|--tags=*) keep="${keep:+$keep,}${word#*=}" ;; esac
  done
  # FB-1193: the environment only (the Makefile variable, exported by mk/kit-go.mk); .devkit/config.env is not read for the tags
  if [ "$tier" = light ]; then extra="${KIT_GO_TEST_TAGS_LIGHT:-}"; else extra="slow ${KIT_GO_TEST_TAGS_FULL:-}"; fi
  extra="$(printf '%s' "$extra" | tr ' ' ',' | sed -e 's/,,*/,/g' -e 's/^,//' -e 's/,$//')"
  printf '%s' "${keep:+$keep${extra:+,}}$extra"
}

db_imports="$(conf_val KIT_GO_DB_IMPORTS)"  # empty: no package is a database package

# ---- units --list ----------------------------------------------------------------------------------------------------------
# One `go list -e -deps -test` per module (R-268: its cost grows with the package count). Fields are \x1f apart, list items \x1e
# apart: neither is in a path or an import path.
list_fmt='{{.ImportPath}}{{"\x1f"}}{{.ForTest}}{{"\x1f"}}{{.Dir}}{{"\x1f"}}{{.DepOnly}}{{"\x1f"}}{{.Standard}}{{"\x1f"}}{{join .GoFiles "\x1e"}}{{"\x1e"}}{{join .CgoFiles "\x1e"}}{{"\x1e"}}{{join .CFiles "\x1e"}}{{"\x1e"}}{{join .CXXFiles "\x1e"}}{{"\x1e"}}{{join .HFiles "\x1e"}}{{"\x1e"}}{{join .SFiles "\x1e"}}{{"\x1e"}}{{join .IgnoredGoFiles "\x1e"}}{{"\x1f"}}{{join .TestGoFiles "\x1e"}}{{"\x1e"}}{{join .XTestGoFiles "\x1e"}}{{"\x1e"}}{{join .IgnoredGoFiles "\x1e"}}{{"\x1f"}}{{join .EmbedFiles "\x1e"}}{{"\x1e"}}{{join .TestEmbedFiles "\x1e"}}{{"\x1e"}}{{join .XTestEmbedFiles "\x1e"}}{{"\x1f"}}{{join .Deps "\x1e"}}{{"\x1f"}}{{with .Module}}{{.Dir}}{{end}}'
# shellcheck disable=SC2016  # an awk program: its $0 is awk's, not the shell's
unit_awk='
function esc(s) { gsub(/\\/, "\\\\", s); gsub(/"/, "\\\"", s); return s }
function relto(p,  r) {  # the repo-relative form of an absolute path inside the repo and outside the module cache, else ""
  if (p == p1 || p == p2) return "."
  if (index(p, p1 "/") == 1) r = substr(p, length(p1) + 2)
  else if (index(p, p2 "/") == 1) r = substr(p, length(p2) + 2)
  else return ""
  if (index(p, gm1 "/") == 1 || index(p, gm2 "/") == 1) return ""
  return r
}
function put(path) { if (path != "" && !(path in have)) { have[path] = 1; ins = ins (ins == "" ? "" : ", ") "\"" esc(path) "\"" } }
function files(rel, list,  n, i, parts) {
  n = split(list, parts, "\036")
  for (i = 1; i <= n; i++) if (parts[i] != "") put(rel == "." ? parts[i] : rel "/" parts[i])
}
function modfiles(mdir,  m) { m = relto(mdir); if (m != "") { put(m == "." ? "go.mod" : m "/go.mod"); put(m == "." ? "go.sum" : m "/go.sum") } }
function declared(abs, name,  line, t, rank, r) {  # the tier a _test.go file declares above its package clause
  rank = 0
  while ((getline line < (abs "/" name)) > 0) {
    if (line ~ /^package[ \t]/) break
    if (line ~ /^\/\/go:build/ && line ~ /(^|[^A-Za-z0-9_!])slow([^A-Za-z0-9_]|$)/) t = "slow"
    else if (match(line, /^\/\/ tier: *(unit|smoke|slow)/)) { t = substr(line, RSTART, RLENGTH); sub(/^\/\/ tier: */, "", t) }
    else continue
    r = (t == "slow" ? 3 : t == "smoke" ? 2 : 1)
    if (r > rank) rank = r
  }
  close(abs "/" name)
  return rank
}
BEGIN { FS = "\037"; n = split(dbs, w, " "); for (i = 1; i <= n; i++) db[w[i]] = 1 }
FILENAME == tracked {  # a tracked file under a testdata dir belongs to the package dir above it
  s = "/" $0; at = index(s, "/testdata/")
  if (at > 0) { k = substr(s, 2, at - 2); if (k == "") k = "."; td[k] = td[k] "\036" $0 }
  next
}
{
  ip = $1; sub(/ \[.*$/, "", ip)
  if ($5 == "true") next
  rel = relto($3); if (rel == "") next
  base = ip
  if (ip ~ /\.test$/) base = substr(ip, 1, length(ip) - 5)
  else if ($2 != "" && ip == $2 "_test") base = $2
  if (!(base in drel)) { drel[base] = rel; dabs[base] = $3; order[++nb] = base }
  if (ip !~ /\.test$/) { src[base] = src[base] "\036" $6; emb[base] = emb[base] "\036" $8 }
  tst[base] = tst[base] "\036" $7; dep[base] = dep[base] "\036" $9; mod[base] = $10
  if ($4 == "false") listed[base] = 1
}
END {
  for (b = 1; b <= nb; b++) {
    base = order[b]
    if (!(base in listed) || (src[base] "" tst[base]) ~ /^[\036]*$/) continue
    delete have; ins = ""; needs = ""; rank = 0
    files(drel[base], src[base]); files(drel[base], tst[base]); files(drel[base], emb[base])
    if (drel[base] in td) { m = split(td[drel[base]], t, "\036"); for (i = 1; i <= m; i++) put(t[i]) }
    modfiles(mod[base])
    nd = split(dep[base], ds, "\036")
    for (i = 1; i <= nd; i++) {
      d = ds[i]; sub(/ \[.*$/, "", d)
      if (d == "") continue
      if (d in db) needs = "\"db\""
      if (d != base && (d in drel)) { files(drel[d], src[d]); files(drel[d], emb[d]); modfiles(mod[d]) }
    }
    nt = split(tst[base], tn, "\036")
    for (i = 1; i <= nt; i++) if (tn[i] ~ /_test\.go$/) { r = declared(dabs[base], tn[i]); if (r > rank) rank = r }
    printf "{\"id\": \"%s\", \"inputs\": [%s], \"needs\": [%s], \"tier\": %s}\n", esc(drel[base]), ins, needs, (rank == 3 ? "\"slow\"" : rank == 2 ? "\"smoke\"" : rank == 1 ? "\"unit\"" : "null")
  }
}'

work=""
list_units() {
  local mod p2 gm2 gmc tags rc=0
  work="$(mktemp -d "${TMPDIR:-/tmp}/kit-go-list.XXXXXX")" || exit 2
  trap '[ -z "$work" ] || rm -rf "$work"' EXIT
  git ls-files -co --exclude-standard -- '*testdata*' >"$work/tracked"
  p2="$(pwd -P)"
  gmc="$(go env GOMODCACHE 2>/dev/null)"; gm2="$(cd "$gmc" 2>/dev/null && pwd -P)"
  tags="$(printf '%s,%s' "$(tags_for light)" "$(tags_for full)" | sed -e 's/,,*/,/g' -e 's/^,//' -e 's/,$//')"
  while IFS= read -r mod; do
    [ -n "$mod" ] || continue
    if ! (cd "$mod" && go list -e -deps -test ${tags:+-tags "$tags"} -f "$list_fmt" ./... >"$work/list" 2>"$work/err" </dev/null); then
      echo "go-test: go list failed in $mod:" >&2
      head -n 10 "$work/err" >&2
      rc=2
      continue
    fi
    awk -v tracked="$work/tracked" -v p1="$top" -v p2="$p2" -v gm1="${gmc:-/nonexistent-gomodcache}" -v gm2="${gm2:-/nonexistent-gomodcache}" \
      -v dbs="$db_imports" "$unit_awk" "$work/tracked" "$work/list" || rc=2
  done < <(all_mods)
  return "$rc"
}

if [ "$mode" = list ]; then
  list_units
  exit $?
fi

# ---- the run: units --run and flaky -----------------------------------------------------------------------------------------
flags="${KIT_GO_TEST_FLAGS:-}"
if [ -n "${HEAVY_RUN_CORES:-}" ]; then
  case " $(printf '%s' "$flags" | tr '\t' ' ') " in
    *" -p "*|*" -p="*|*" --p "*|*" --p="*) ;;
    *) flags="-p $HEAVY_RUN_CORES${flags:+ $flags}" ;;
  esac
fi

# R-271: a run count turns the test cache off, so it belongs to the flaky goal alone
flaky_n=""
if [ "$mode" = flaky ]; then
  flaky_n="${KIT_GO_FLAKY_COUNT:-}"
  case "$flaky_n" in
    ""|*[!0-9]*) flaky_n="bad" ;;
    *) if [ "$((10#$flaky_n))" -ge 1 ]; then flaky_n="$((10#$flaky_n))"; else flaky_n="bad"; fi ;;
  esac
  if [ "$flaky_n" = bad ]; then
    echo "go-test: KIT_GO_FLAKY_COUNT must be a whole number >= 1, got '${KIT_GO_FLAKY_COUNT:-}'" >&2
    exit 2
  fi
else
  # shellcheck disable=SC2086
  for word in ${KIT_GO_TEST_FLAGS:-} ${GOFLAGS:-}; do
    case "$word" in
      -count|-count=*|--count|--count=*)
        echo "go-test: -count disables the test cache (R-271): make kit-go-test-flaky COUNT=<n>" >&2
        exit 1 ;;
    esac
  done
fi

tier="full"; out=""; filter=""
paths=()
if [ "$mode" = run ]; then
  while [ $# -gt 0 ]; do
    case "$1" in
      --tier) tier="${2:-}"; shift 2 ;;
      --out) out="${2:-}"; shift 2 ;;
      --filter) filter="${2:-}"; shift 2 ;;
      *) paths+=("$1"); shift ;;
    esac
  done
  case "$tier" in light|full) ;; *) echo "go-test: --tier takes light or full, got '$tier'" >&2; exit 2 ;; esac
  [ -n "$out" ] || { echo "go-test: units --run needs --out FILE" >&2; exit 2; }
  [ "${#paths[@]}" -gt 0 ] || { echo "go-test: units --run names no unit" >&2; exit 2; }
  case "$filter" in *[!A-Za-z0-9_,]*) echo "go-test: --filter takes test names separated by commas (RUN=TestA,TestB), got '$filter'" >&2; exit 2 ;; esac
else
  paths=("$@")
  # FB-1144: KIT_CHECK_PATHS names packages too: a file stands for its dir, a dir for itself
  # shellcheck disable=SC2206
  paths+=(${KIT_CHECK_PATHS:-})
fi

# KIT_GO_TEST_RUNNER (FB-957): the words of the prefix; each module runs in its own dir, so a first word that names a path
# (a slash in it, not absolute) is resolved from the repo root here, once, and passed on absolute
runner_words=()
runner_echo=""
if [ -n "${KIT_GO_TEST_RUNNER:-}" ]; then
  read -r -a runner_words <<<"$KIT_GO_TEST_RUNNER"
  case "${runner_words[0]:-}" in
    /*|"") ;;
    */*) runner_words[0]="$top/${runner_words[0]#./}" ;;
  esac
  runner_echo="${runner_words[*]:-}"
  [ -z "$runner_echo" ] || runner_echo="$runner_echo "
fi

# coverage: the same one `go test` per call writes a profile; scripts/kit/cover-gate.py judges the measure lines. A project's own
# -coverprofile in the flags is read, not replaced; -coverpkg makes a figure depend on which packages ran, so nothing is judged
# then. A flaky or a filtered run judges nothing either (a figure of part of the tests).
proj_prof=""
coverpkg=0
want_path=0
# shellcheck disable=SC2086
for word in $flags; do
  if [ "$want_path" = 1 ]; then proj_prof="$word"; want_path=0; continue; fi
  case "$word" in
    -coverprofile=*|--coverprofile=*) proj_prof="${word#*=}" ;;
    -coverprofile|--coverprofile) want_path=1 ;;
    -coverpkg|-coverpkg=*|--coverpkg|--coverpkg=*) coverpkg=1 ;;
  esac
done
cover=1
if [ "$coverpkg" = 1 ] || [ -n "$flaky_n" ] || [ -n "$filter" ]; then cover=0; fi

cover_dir=""
tests_tmp=""   # R-236, R-270: this work tree's test temp parent, made before the first module and kept after the run
# shellcheck disable=SC2329  # installed by the EXIT trap below, which shellcheck does not follow
cleanup() {
  [ -z "$cover_dir" ] || rm -rf "$cover_dir"
  [ -z "$tests_tmp" ] || find "$tests_tmp" -maxdepth 1 -name "kit-go-$$.*" -exec rm -f {} + 2>/dev/null
  return 0
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM
if [ "$cover" = 0 ]; then
  if [ "$coverpkg" = 1 ] && [ -z "$flaky_n" ] && [ -z "$filter" ]; then
    echo "warn: -coverpkg makes a figure depend on which packages ran; coverage not judged" >&2
  fi
else
  mkdir -p "$top/.artifacts/tmp" || { echo "go-test: cannot create $top/.artifacts/tmp" >&2; exit 2; }
  # a killed run (devkit gate's timeout) leaves its dir behind: sweep the ones older than a day
  find "$top/.artifacts/tmp" -maxdepth 1 -type d -name 'kit-go-cover.*' -mtime +0 -exec rm -rf {} + 2>/dev/null
  cover_dir="$(mktemp -d "$top/.artifacts/tmp/kit-go-cover.XXXXXX")" || { echo "go-test: cannot create a temp dir under $top/.artifacts/tmp" >&2; exit 2; }
fi

# FB-638: a floor refusal keeps the refused packages' profiles (a green run keeps nothing). Home: DEVKIT_GATE_COVER_KEEP
# (devkit gate sets it beside the gate log, outside any queue snapshot worktree, and sweeps it after 7 days); a direct
# make check has no gate, so a dir under the run's own checkout's .artifacts/tmp (FB-918: the worktree the run is in, not
# the main checkout), named kit-go-kept.*, which this script sweeps after 7 days.
# The cover sweep above takes only kit-go-cover.* and the day-old rule, so neither takes the other's.
keep_home=""
if [ "$cover" = 1 ]; then
  if [ -n "${DEVKIT_GATE_COVER_KEEP:-}" ]; then
    keep_home="$DEVKIT_GATE_COVER_KEEP"
  else
    find "$top/.artifacts/tmp" -maxdepth 1 -type d -name 'kit-go-kept.*' -mtime +6 -exec rm -rf {} + 2>/dev/null
    keep_home="$top/.artifacts/tmp/kit-go-kept.$(date -u +%Y%m%dT%H%M%S).$$"
  fi
fi
prof_list=""

# measure lines `<unit> <covered> <total> <kind>` from one profile: blocks deduped by file and span at the highest count,
# the module path stripped to a repo-relative dir, a path of another module dropped
profile_lines() {
  awk -v modpath="$1" -v modrel="$2" -v kind="$3" '
    NF < 3 || $1 ~ /^mode:/ { next }
    { if (!($1 in cnt) || $3 + 0 > cnt[$1]) cnt[$1] = $3 + 0; stm[$1] = $2 + 0 }
    END {
      for (key in cnt) {
        dir = key; sub(/:[^:]*$/, "", dir)
        if (sub(/\/[^\/]*$/, "", dir) == 0) dir = ""
        if (dir == modpath) rel = "."
        else if (index(dir, modpath "/") == 1) rel = substr(dir, length(modpath) + 2)
        else continue
        unit = rel
        if (modrel != ".") unit = (rel == "." ? modrel : modrel "/" rel)
        tot[unit] += stm[key]
        if (cnt[key] > 0) cov[unit] += stm[key]
      }
      for (unit in tot) printf "%s %d %d %s\n", unit, cov[unit], tot[unit], kind
    }' "$4" | sort
}

# the module path of the go.mod in dir $1 (quoted allowed)
module_path() {
  awk '$1 == "module" { p = $2; gsub(/["`]/, "", p); print p; exit }' "$1/go.mod"
}

# FB-638: from the gate's refusal lines (`coverage: <unit>: ...`), copy each refused unit's blocks out of its module's
# profile into $keep_home/<unit, / as _>.out (a mode line, then that package's blocks: `go tool cover -func` reads it).
# A module can have two profiles: a profile without the unit's blocks leaves a file already kept alone
keep_refused() {
  local unit entry mod prof modpath rel want out kept="" n
  while IFS= read -r unit; do
    [ -n "$unit" ] || continue
    while IFS= read -r entry; do
      [ -n "$entry" ] || continue
      mod="${entry%%|*}"; prof="${entry#*|}"
      modpath="$(module_path "$mod")"
      if [ "$mod" = "." ]; then rel="$unit"
      elif [ "$unit" = "$mod" ]; then rel="."
      else case "$unit" in "$mod"/*) rel="${unit#"$mod"/}" ;; *) continue ;; esac; fi
      if [ "$rel" = "." ]; then want="$modpath"; else want="$modpath/$rel"; fi
      mkdir -p "$keep_home" || return 0
      out="$keep_home/$( [ "$unit" = "." ] && echo root || echo "${unit//\//_}" ).out"
      awk -v want="$want" '
        /^mode:/ { print; next }
        NF < 3 { next }
        { dir = $1; sub(/:[^:]*$/, "", dir); if (sub(/\/[^\/]*$/, "", dir) == 0) dir = ""; if (dir == want) print }
      ' "$prof" >"$out.new"
      n="$(wc -l <"$out.new" | tr -d ' ')"
      if [ "$n" -le 1 ]; then rm -f "$out.new"; else mv "$out.new" "$out"; kept="${kept:+$kept, }$out"; fi
    done <<<"$prof_list"
  done < <(sed -n 's/^coverage: \([^ ]*\): .*/\1/p' "$1" | sort -u)
  [ -z "$kept" ] || echo "go-test: kept the refused profile(s): $kept (go tool cover -func=<file>)" >&2
  [ -d "$keep_home" ] && rmdir "$keep_home" 2>/dev/null
  return 0
}

# R-236 (FB-960), R-270: `go test` hands GOTMPDIR to t.TempDir(), and the Makefile's GOTMPDIR lies inside the git work tree. A
# test's temp dir is a test temp folder, so it lives outside any work tree, under one parent per repo:
# <base>/<project>-gotmp-<hash> (<base>: DEVKIT_TMP_BASE when set, else $HOME/Developer/.tmp when $HOME/Developer is a directory
# (outside Spotlight's /var/folders), else ${TMPDIR:-/tmp}; made on demand, falling back to $TMPDIR when it cannot be made; <project>:
# the main checkout's directory name, lowercase; <hash>: the cksum of the absolute git-common-dir, so two repos of one name differ
# and every worktree of one repo shares it). go keys the test cache on the path. It is made here and kept; only entries of it older
# than a day go, and only when no run holds it (the exclusive lock below).
make_tests_tmp() {
  local base common main proj key hash path fallback
  fallback="${TMPDIR:-/tmp}"; fallback="${fallback%/}"
  if [ -n "${DEVKIT_TMP_BASE:-}" ]; then base="${DEVKIT_TMP_BASE%/}"
  elif [ -n "${HOME:-}" ] && [ -d "$HOME/Developer" ]; then base="${HOME%/}/Developer/.tmp"
  else base="$fallback"; fi
  # a base that cannot be made falls back to $TMPDIR rather than fail (the leaf we create is 0700)
  if [ ! -d "$base" ]; then
    if mkdir -p "$base" 2>/dev/null; then chmod 700 "$base" 2>/dev/null; else base="$fallback"; fi
  fi
  mkdir -p "$base" || { echo "go-test: cannot create $base" >&2; return 1; }
  common="$(git rev-parse --git-common-dir 2>/dev/null)" && common="$(cd "$common" 2>/dev/null && pwd -P)"
  if [ -n "$common" ]; then main="$(dirname "$common")"; key="$common"; else main="$top"; key="$(pwd -P)"; fi
  proj="$(basename "$main" | tr '[:upper:]' '[:lower:]')"
  hash="$(printf '%s' "$key" | cksum | cut -d' ' -f1)"
  case "$hash" in ""|*[!0-9]*) echo "go-test: cannot hash $key" >&2; return 1 ;; esac
  path="$base/$proj-gotmp-$hash"
  if [ -L "$path" ] || { [ -e "$path" ] && [ ! -d "$path" ]; }; then rm -f "$path"; fi  # a link or a file at our name is not ours to follow
  [ -d "$path" ] || mkdir -m 700 "$path" || { echo "go-test: cannot create $path" >&2; return 1; }  # $base exists: no -p, so -m holds
  [ -O "$path" ] || { echo "go-test: $path is not owned by this user: remove it or set DEVKIT_TMP_BASE to a private directory" >&2; return 1; }
  exec 9>>"$path/.lock" || return 1
  # exclusive only while no run holds the parent: a killed run's leftovers older than a day go; then this run holds it shared
  if lock_fd9 exnb; then find "$path" -mindepth 1 -maxdepth 1 ! -name .lock -mtime +0 -exec rm -rf {} + 2>/dev/null; fi
  lock_fd9 sh || true
  tests_tmp="$path"
}
# flock(1) is not on macOS: the kernel lock through python3's stdlib fcntl. fd 9 is this run's lock file; the lock belongs to the
# open file description, so it outlives the helper and ends when this script and its children close fd 9. Without python3 this
# fails: the run then holds nothing and sweeps nothing.
lock_fd9() { python3 -I -c 'import fcntl, sys; fcntl.flock(9, {"sh": fcntl.LOCK_SH, "exnb": fcntl.LOCK_EX | fcntl.LOCK_NB}[sys.argv[1]])' "$1" 2>/dev/null; }
make_tests_tmp || exit 2

rc=0
calls=0
cover_lines=""
cov_skipped=""
res_files=""
seen_files=""
go_flags="${GOFLAGS:+$GOFLAGS }-trimpath"  # R-270: through the go environment, so the echoed argv stays the project's own
tests_run=""
[ -z "$filter" ] || tests_run="^($(printf '%s' "$filter" | tr ',' '|'))\$"
mark_red() { [ "$rc" -ge 2 ] || rc=1; }

# one `go test -json` call in module $1 over the packages $2 (`./a ./b`, one per word), their dirs $3 (git-top relative, one per
# line: the coverage fillers of a light run). The verdict is go test's exit or go-events' (kept apart: PIPESTATUS); a flaky run's
# is go-events' alone (0 green, 1 red, 2 build failure). A green call's profile is read into $cover_lines; its result lines go
# to a file of their own, $res_files.
run_call() {
  local cmod="$1" cargs="$2" cdirs="$3"
  local short_word="" prof="" kept_prof="" cov_word="" skipf resf seenf sub g e lines kept_lines skiplist l dir sp tags kind
  local ev_args=() count_word=""
  calls=$((calls + 1))
  [ "$tier" != light ] || short_word="-short"
  [ -z "$flaky_n" ] || count_word="-count=$flaky_n"
  tags="$(tags_for "$tier")"
  echo "go-test: $cmod: ${runner_echo}go test${flags:+ $flags}${count_word:+ $count_word}${short_word:+ $short_word}${tags:+ -tags $tags}${tests_run:+ -run $tests_run} $cargs"
  if [ "$cover" = 1 ]; then
    if [ -n "$proj_prof" ]; then
      case "$proj_prof" in /*) prof="$proj_prof" ;; *) prof="$cmod/$proj_prof" ;; esac
      rm -f "$prof"  # the file a failed or older run left must not pass for this run's
    else
      prof="$cover_dir/$calls.out"
      cov_word="-coverprofile=$prof"
    fi
  fi
  skipf="$tests_tmp/kit-go-$$.skip.$calls"
  resf="$tests_tmp/kit-go-$$.res.$calls"
  seenf="$tests_tmp/kit-go-$$.seen.$calls"
  res_files="$res_files $resf"
  seen_files="$seen_files $seenf"
  ev_args=(--module-path "$(module_path "$cmod")" --module-dir "$cmod" --skip-pkgs "$skipf" --results "$resf" --tier "$tier" --seen "$seenf")
  [ -z "$filter" ] || ev_args+=(--expect "$filter")
  if [ -n "$flaky_n" ]; then
    ev_args+=(--count "$flaky_n" --flaky-ledger "$top/config/go-flaky.ledger" --ledger "$top/config/go-skip.ledger")
  elif [ "$tier" = light ]; then
    ev_args+=(--short)
  else
    ev_args+=(--ledger "$top/config/go-skip.ledger")
  fi
  # the subshell exits with go test's verdict (bit 0) and go-events' (the rest: 0, 1, 2, 3 = any other)
  sub=0
  (
    cd "$cmod" || exit 99
    # shellcheck disable=SC2086
    GOTMPDIR="$tests_tmp" GOFLAGS="$go_flags" ${runner_words[@]+"${runner_words[@]}"} go test -json $flags $count_word $short_word ${tags:+-tags "$tags"} ${tests_run:+-run "$tests_run"} ${cov_word:+"$cov_word"} $cargs </dev/null |
      python3 "$here/go-events.py" "${ev_args[@]}"
    st=("${PIPESTATUS[@]}")
    g="${st[0]}"; e="${st[1]}"
    [ "$e" -le 3 ] || e=3
    exit $(( (g != 0 ? 1 : 0) + 2 * e ))
  ) || sub=$?
  g=$((sub & 1)); e=$((sub >> 1))
  if [ "$g" -ne 0 ] && [ -f "$resf" ] && ! grep -q '"exit": 1' "$resf"; then
    sed -i.bak 's/"exit": 0/"exit": 1/' "$resf" && rm -f "$resf.bak"  # go test failed and no package says so: none is green
  fi
  if [ -n "$flaky_n" ]; then
    case "$e" in 0) ;; 2) rc=2 ;; *) mark_red ;; esac
    return 0
  fi
  if [ "$g" -ne 0 ] || [ "$e" -ne 0 ]; then
    mark_red  # a failing run's profile is not read
    return 0
  fi
  [ -n "$prof" ] || return 0
  if [ ! -f "$prof" ]; then
    echo "go-test: $cmod: no coverage profile" >&2
    mark_red
    return 0
  fi
  kept_prof="$prof"
  if [ -n "$proj_prof" ]; then  # the project's file is rewritten by the module's next call: keep this call's own
    cp "$prof" "$cover_dir/$calls.out" 2>/dev/null && kept_prof="$cover_dir/$calls.out"
  fi
  prof_list="${prof_list:+$prof_list$'\n'}$cmod|$kept_prof"
  kind=whole
  [ "$tier" != light ] || kind=changed
  lines="$(profile_lines "$(module_path "$cmod")" "$cmod" "$kind" "$prof")"
  skiplist=""
  if [ "$tier" = light ] && [ -s "$skipf" ]; then skiplist="$(cat "$skipf")"; fi
  if [ -n "$skiplist" ]; then  # a package with a skip under -short ran partly: its figure is not judged here
    kept_lines=""
    while IFS= read -r l; do
      [ -n "$l" ] || continue
      case $'\n'"$skiplist"$'\n' in *$'\n'"${l%% *}"$'\n'*) continue ;; esac
      kept_lines="${kept_lines:+$kept_lines$'\n'}$l"
    done <<<"$lines"
    lines="$kept_lines"
    while IFS= read -r sp; do
      [ -n "$sp" ] || continue
      case " $cov_skipped " in *" $sp "*) ;; *) cov_skipped="${cov_skipped:+$cov_skipped }$sp" ;; esac
    done <<<"$skiplist"
  fi
  if [ "$tier" = light ]; then  # a package that ran and left no figure (no statements, no test files) is a zero, so a floor on it is refused
    while IFS= read -r dir; do
      [ -n "$dir" ] || continue
      case $'\n'"$skiplist"$'\n' in *$'\n'"$dir"$'\n'*) continue ;; esac
      case $'\n'"$lines" in *$'\n'"$dir "*) ;; *) lines="${lines:+$lines$'\n'}$dir 0 0 changed" ;; esac
    done <<<"$cdirs"
  fi
  [ -z "$lines" ] || cover_lines="${cover_lines:+$cover_lines$'\n'}$lines"
  return 0
}

# the units to run, as `<module dir>|<package dir>` lines: a package dir of a unit id; for flaky, the dir of a path
pkgs=""
mods=""
asked=""
for path in ${paths[@]+"${paths[@]}"}; do
  p="${path#./}"; p="${p%/}"; [ -n "$p" ] || p="."
  asked="${asked:+$asked$'\n'}$p"
  if [ "$mode" = flaky ]; then
    case "$p" in
      *.go|go.mod|go.sum|*/go.mod|*/go.sum) case "$p" in */*) p="${p%/*}" ;; *) p="." ;; esac ;;
      *) [ -d "$p" ] || continue ;;
    esac
  fi
  if ! mod="$(nearest_mod "$p")"; then
    echo "go-test: $p: no go.mod above it: not a unit" >&2
    mark_red
    continue
  fi
  case $'\n'"$mods"$'\n' in *$'\n'"$mod"$'\n'*) ;; *) mods="${mods:+$mods$'\n'}$mod" ;; esac
  case $'\n'"$pkgs"$'\n' in *$'\n'"$mod|$p"$'\n'*) ;; *) pkgs="${pkgs:+$pkgs$'\n'}$mod|$p" ;; esac
done
if [ "$mode" = flaky ] && [ -z "$pkgs" ] && [ "$rc" = 0 ]; then  # no name: every module whole
  while IFS= read -r mod; do mods="${mods:+$mods$'\n'}$mod"; pkgs="${pkgs:+$pkgs$'\n'}$mod|"; done < <(all_mods)
fi
# a red run owes a row to every unit asked for: a package that left no event (a path with no go.mod, a dir go cannot load) is red there
red_rows() {
  [ "$rc" -ne 0 ] || return 0
  local id
  while IFS= read -r id; do
    [ -n "$id" ] || continue
    case "$(cat "$out" 2>/dev/null)" in *"{\"id\": \"$id\","*) ;; *) printf '{"id": "%s", "exit": 1, "wall_s": 0.0, "cached": false, "tests": 0, "skips": 0, "tier": "%s"}\n' "$id" "$tier" >>"$out" ;; esac
  done <<<"$asked"
}
if [ -z "$mods" ]; then
  [ "$rc" -ne 0 ] || echo "go-test: no Go module: nothing to test"
  if [ "$mode" = run ]; then : >"$out"; red_rows; fi
  exit "$rc"
fi

ran=0
while IFS= read -r mod; do
  args=""; dirs=""
  while IFS= read -r line; do
    [ "${line%%|*}" = "$mod" ] || continue
    pkg="${line#*|}"
    if [ -z "$pkg" ]; then args="./..."; dirs=""; break; fi  # flaky with no name
    if [ "$pkg" = "$mod" ]; then rel="."
    elif [ "$mod" = "." ]; then rel="./$pkg"
    else rel="./${pkg#"$mod"/}"; fi
    args="${args:+$args }$rel"; dirs="${dirs:+$dirs$'\n'}$pkg"
  done <<<"$pkgs"
  run_call "$mod" "$args" "$dirs"
  ran=$((ran + 1))
done <<<"$mods"

# FB-1136: every test RUN= names ran and passed in some package, else the run is red and says which
missing=""
if [ -n "$filter" ]; then
  # shellcheck disable=SC2086
  seen="$(cat $seen_files 2>/dev/null)"
  for name in $(printf '%s' "$filter" | tr ',' ' '); do
    case $'\n'"$seen"$'\n' in *$'\n'"$name"$'\n'*) ;; *) missing="${missing:+$missing }$name" ;; esac
  done
  for name in $missing; do echo "go-test: $name: did not run, or did not pass (RUN=$filter)" >&2; done
  [ -z "$missing" ] || mark_red
fi

[ -z "$cov_skipped" ] || echo "go-test: coverage not judged (skip under -short): $cov_skipped"

# the floors: only after every module passed. A floor refused makes its package red, so a reuse never hides it. Only a whole-tier
# run writes the snapshot `make coverage-raise` reads (it merges the packages of this run into the last one)
refused=""
if [ "$rc" -eq 0 ] && [ "$cover" = 1 ] && [ -f scripts/kit/cover-gate.py ]; then
  gate_err="$cover_dir/gate.err"
  whole=""
  [ "$tier" != full ] || whole="--whole"
  printf '%s\n' "$cover_lines" | python3 scripts/kit/cover-gate.py check --stack go $whole 2>"$gate_err" || rc=1
  cat "$gate_err" >&2
  if [ "$rc" -ne 0 ]; then
    refused="$(sed -n 's/^coverage: \([^ ]*\): .*/\1/p' "$gate_err" | sort -u)"
    keep_refused "$gate_err"
  fi
fi

if [ "$mode" = run ]; then
  # shellcheck disable=SC2086
  cat $res_files >"$out" 2>/dev/null
  if [ -n "$missing" ]; then sed -i.bak 's/"exit": 0/"exit": 1/' "$out" && rm -f "$out.bak"; fi
  if [ -n "$refused" ]; then  # a refused floor is a red package: the store must not hold it green
    awk -v ids="$refused" 'BEGIN { n = split(ids, a, "\n"); for (i = 1; i <= n; i++) bad["{\"id\": \"" a[i] "\", \"exit\": 0,"] = 1 }
      { for (k in bad) if (index($0, k) == 1) sub(/"exit": 0/, "\"exit\": 1"); print }' "$out" >"$out.new" && mv "$out.new" "$out"
  fi
  red_rows
fi

if [ "$rc" -eq 0 ]; then echo "go-test: $ran module(s) green"; else echo "go-test: red" >&2; fi
exit "$rc"
