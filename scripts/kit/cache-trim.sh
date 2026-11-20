#!/usr/bin/env bash
# managed by devkit — do not edit; fix upstream in ~/Developer/projects/devkit
# Purpose: keep the repo-local Go caches and the Go temp dir small. Safe when .cache/ does not exist yet.
# Usage:   scripts/kit/cache-trim.sh [--dry-run] [--force] [--auto]
#   --auto     run at most once per 24h per checkout family (stamp <main checkout>/.artifacts/stamps/cache-trim, beside the
#              shared caches: a trim from any worktree counts; an old per-checkout stamp is ignored, never deleted);
#              --force ignores the stamp; a build cache over 2x its cap (hard, DK-071) bypasses it. It never runs `go clean
#              -cache` or `go clean -modcache` (FB-1241, FB-1252: a sibling worktree may be reading them): over a cap it ages
#              build files out and names `make cache-trim`
#   lock       one trim at a time per family (mkdir <stamp dir>/cache-trim.lock, held for the run, stale after 1h): the
#              loser prints one skip line, exit 0, no stamp; --force does not override. Builds are not locked (see busy)
#   --dry-run  report what would happen, change nothing, write no stamp
#   busy       a go build|test|vet|install|generate or golangci-lint run of this user with its cwd in this checkout
#              or one of its worktrees: prints one skip line, exit 0, touches nothing (no stamp); --force does not override
#   hard       (DK-071) the build cache (GOCACHE) is over 2x KIT_CACHE_CAP_GB: the stamp skip does not starve the size. Not busy:
#              the normal run, stamp bypassed under --auto. Busy (FB-1152): deferred whole, one line, nothing deleted, no stamp,
#              for --force and a missing cwd reader too; no GOCACHE file goes while a go process of this family runs
# Env:     KIT_CACHE_AGE_DAYS=7  KIT_CACHE_CAP_GB=5 (build)  KIT_MODCACHE_CAP_GB=5  KIT_GOLANGCI_CAP_GB=1
#          GOCACHE GOMODCACHE GOTMPDIR GOLANGCI_LINT_CACHE (default: <main checkout>/.cache/go/{build,mod}, .cache/golangci, and
#          GOTMPDIR <base>/devkit-go/<main checkout path, `_` doubled then `/` made `_-`>, the Makefile's path)
#          DEVKIT_TMP_BASE (<base>; default $HOME/Developer/.tmp when $HOME/Developer is a directory, else ${TMPDIR:-/tmp})
#          KIT_STAMP_DIR (default <main checkout>/.artifacts/stamps)
# Safety:  GOCACHE, GOMODCACHE and GOLANGCI_LINT_CACHE must lie strictly inside <main checkout>/.cache/ (main from `git rev-parse
#          --git-common-dir`); GOTMPDIR strictly inside <main checkout>/.cache/ (the old layout, still accepted) or strictly
#          inside <base>/devkit-go/. Paths are canonicalised first, so a symlink or `..` escape is refused. Anything else: one line
#          is printed and nothing is touched (exit 1).
# Output:  one summary line. Exit 0 (64 on bad usage, 1 on a refused path).
set -uo pipefail

dry=0; force=0; auto=0
for a in "$@"; do
  case "$a" in
    --dry-run) dry=1 ;;
    --force) force=1 ;;
    --auto) auto=1 ;;
    *) echo "usage: cache-trim.sh [--dry-run] [--force] [--auto]" >&2; exit 64 ;;
  esac
done

age_days="${KIT_CACHE_AGE_DAYS:-7}"
build_cap="${KIT_CACHE_CAP_GB:-5}"
mod_cap="${KIT_MODCACHE_CAP_GB:-5}"
lint_cap="${KIT_GOLANGCI_CAP_GB:-1}"

common="$(git rev-parse --path-format=absolute --git-common-dir 2>/dev/null || true)"
if [ -z "$common" ]; then echo "cache-trim: refusing to run outside a git checkout (cannot locate <main>/.cache/)" >&2; exit 1; fi
main="$(dirname "$common")"
# FB-545: the stamp and lock sit beside the shared caches they guard, so every worktree of the family sees them
stamp_dir="${KIT_STAMP_DIR:-$main/.artifacts/stamps}"
stamp="$stamp_dir/cache-trim"
lock="$stamp_dir/cache-trim.lock"
: "${GOCACHE:=$main/.cache/go/build}"
: "${GOMODCACHE:=$main/.cache/go/mod}"
# DK-061: GOTMPDIR lives outside the work tree and outside /var/folders (Spotlight indexes it): <base>/devkit-go/<leaf>,
# the same path the Makefile makes (<base>: DEVKIT_TMP_BASE, else $HOME/Developer/.tmp when $HOME/Developer is a directory, else
# ${TMPDIR:-/tmp}; the leaf is the main checkout path with each `_` doubled and then each `/` made `_-`: prefix-free, so injective, no hash).
if [ -n "${DEVKIT_TMP_BASE:-}" ]; then tmp_base="${DEVKIT_TMP_BASE%/}"
elif [ -n "${HOME:-}" ] && [ -d "$HOME/Developer" ]; then tmp_base="${HOME%/}/Developer/.tmp"
else tmp_base="${TMPDIR:-/tmp}"; tmp_base="${tmp_base%/}"; fi
go_key="$(printf '%s' "$main" | sed -e 's/_/__/g' -e 's#/#_-#g')"
: "${GOTMPDIR:=$tmp_base/devkit-go/$go_key}"
: "${GOLANGCI_LINT_CACHE:=$main/.cache/golangci}"
export GOCACHE GOMODCACHE GOTMPDIR GOLANGCI_LINT_CACHE

# canon <path>: physical absolute path, also for a path that does not exist yet (deepest existing ancestor resolved).
canon() {
  local p="$1" rest="" d
  while [ ! -d "$p" ] && [ "$p" != / ] && [ "$p" != . ]; do rest="/$(basename "$p")$rest"; p="$(dirname "$p")"; done
  d="$(cd "$p" 2>/dev/null && pwd -P)" || return 1
  printf '%s%s' "$d" "$rest"
}
allowed="$(canon "$main")/.cache"
allowed_tmp="$(canon "$tmp_base")/devkit-go"
for var in GOCACHE GOMODCACHE GOTMPDIR GOLANGCI_LINT_CACHE; do
  c="$(canon "${!var}")" || c=""
  case "$c" in
    ""|*/..|*/../*) c="" ;;
    "$allowed"/*) continue ;;
    "$allowed_tmp"/*) [ "$var" = GOTMPDIR ] && continue ;;
  esac
  if [ "$var" = GOTMPDIR ]; then where="$allowed/ or $allowed_tmp/"; else where="$allowed/"; fi
  echo "cache-trim: refusing $var=${!var}: not inside $where (nothing touched)" >&2
  exit 1
done

kb() { if [ -d "$1" ]; then du -sk "$1" 2>/dev/null | awk '{print $1}'; else echo 0; fi; }
over() { awk -v kb="$1" -v gb="$2" -v f="${3:-1}" 'BEGIN { exit !(kb > f * gb * 1048576) }'; }   # over <size_kb> <cap_gb> [factor]
human() { awk -v kb="$1" 'BEGIN { printf "%.1fM", kb / 1024 }'; }

actions=""
note() { actions="${actions:+$actions, }$1"; }
verb="removed"; [ "$dry" -eq 1 ] && verb="would remove"

# DK-071: hard = the build cache is over 2x its cap. Measured on first use only (a normal fresh run pays no du before the
# stamp check), then kept. Only the build cache counts; the mod and golangci caches keep their plain caps.
hard_v=""
hard() {
  if [ -z "$hard_v" ]; then
    hard_v=1
    if [ -d "$GOCACHE" ] && over "$(kb "$GOCACHE")" "$build_cap" 2; then hard_v=0; fi
  fi
  return "$hard_v"
}
finish() { # the shared tail: stamp (not on a dry-run) and the one summary line
  if [ "$dry" -eq 0 ]; then
    mkdir -p "$stamp_dir" && touch "$stamp"
    # .artifacts/ ignores itself, so a repo needs no .gitignore edit for it
    case "$stamp_dir" in "$main/.artifacts"|"$main/.artifacts/"*) [ -f "$main/.artifacts/.gitignore" ] || printf '*\n' >"$main/.artifacts/.gitignore" ;; esac
  fi
  local pre=""; [ "$dry" -eq 1 ] && pre=" (dry-run)"
  echo "cache-trim${pre}: ${actions:-nothing to trim} | build $(human "$(kb "$GOCACHE")") mod $(human "$(kb "$GOMODCACHE")") golangci $(human "$(kb "$GOLANGCI_LINT_CACHE")")"
}

if [ "$auto" -eq 1 ] && [ "$force" -eq 0 ] && [ -f "$stamp" ] && [ -n "$(find "$stamp" -mtime -1 -print 2>/dev/null)" ] && ! hard; then
  echo "cache-trim: skipped (ran within the last 24h)"
  exit 0
fi

# Trim lock (FB-545): two cold runs of one family must not trim together. An atomic mkdir in the stamp dir, held until
# this process exits; the loser prints one skip line and leaves everything (no stamp). A lock older than 1h is stale
# (its owner died) and is removed. A dry-run changes nothing, so it takes no lock. Builds are not locked: go has no such
# protocol, so the busy guard below keeps its check-then-act window.
if [ "$dry" -eq 0 ]; then
  mkdir -p "$stamp_dir" 2>/dev/null || true
  [ -n "$(find "$lock" -maxdepth 0 -mmin +60 -print 2>/dev/null)" ] && rmdir "$lock" 2>/dev/null
  if mkdir "$lock" 2>/dev/null; then
    trap 'rmdir "$lock" 2>/dev/null' EXIT
  elif [ -d "$lock" ]; then
    echo "cache-trim: skipped (another trim is running in this checkout family)"
    exit 0
  fi
fi

# Busy guard (FB-158): a go build/test/vet/install/generate or `golangci-lint run` of this user, with its working
# directory in this checkout family (the main checkout or any worktree git lists), may be using these caches. Then the
# run changes nothing at all (caches, GOTMPDIR and the stamp stay) and the next --auto run tries again; --force does
# not override it. pgrep and the cwd reader (proc-cwd.py, one run per scan for every pid pgrep listed; no lsof) only read.
# A live pid whose cwd cannot be read counts as busy (skipping is the safe side); one that ended after pgrep listed it does
# not (a host shared with other runs sees many short go processes). A missing reader or python3 reads every pid as
# unreadable. `go run` is not in the set: it lives as long as a dev server and would starve the trim.
reader="${KIT_PROC_CWD:-$(cd "$(dirname "$0")" && pwd)/proc-cwd.py}"
cands="$(pgrep -u "$(id -u)" -f '(^|/)go (build|test|vet|install|generate)( |$)|(^|/)golangci-lint run( |$)' 2>/dev/null || true)"
cwds=""; noreader=0
if [ -n "$cands" ]; then
  if [ -f "$reader" ] && command -v python3 >/dev/null 2>&1; then
    cand_arr=()
    while IFS= read -r p; do [ -n "$p" ] && cand_arr+=("$p"); done <<<"$cands"
    cwds="$(python3 "$reader" "${cand_arr[@]}" 2>/dev/null || true)"   # <pid> <cwd> lines, one per readable pid
  else
    noreader=1
  fi
fi
cwd_of() { # cwd_of <pid>: its working directory from the one reader run above, or nothing
  printf '%s\n' "$cwds" | sed -n "s/^$1 //p" | sed -n 1p
}
busy_pid() { # prints the first busy pid and returns 0, else returns 1
  local fam=() w c p r
  c="$(canon "$main")" && [ -n "$c" ] && fam+=("$c")
  while IFS= read -r w; do c="$(canon "$w")" && [ -n "$c" ] && fam+=("$c"); done < <(git worktree list --porcelain 2>/dev/null | sed -n 's/^worktree //p')
  for p in $cands; do
    c="$(cwd_of "$p")"
    if [ -z "$c" ]; then
      kill -0 "$p" 2>/dev/null || continue   # ended between pgrep and the read: nothing left to protect (FB-158)
      echo "$p"; return 0
    fi
    c="$(canon "$c")" || { echo "$p"; return 0; }
    for r in ${fam[@]+"${fam[@]}"}; do
      case "$c" in "$r" | "$r"/*) echo "$p"; return 0 ;; esac
    done
  done
  return 1
}
if bp="$(busy_pid)"; then
  # FB-1152: a hard run does not delete anything while go runs. DK-071 trimmed the build files untouched for an hour on the claim that
  # go touches an entry it uses at most hourly; a long `go test` of a big cache, or a build that reads an entry once, loses a file it
  # is about to open and fails with a missing cache file. The run is deferred whole: no file, no clean, no stamp, so the next
  # run (when go has ended) takes the hard path again; --force does not override it.
  if hard; then
    pre=""; [ "$dry" -eq 1 ] && pre=" (dry-run)"
    echo "cache-trim${pre}: deferred (go running, pid $bp; build cache over 2x cap): nothing deleted until it ends"
    exit 0
  fi
  if [ "$noreader" -eq 1 ]; then echo "cache-trim: no cwd reader ($reader): skipped"; exit 0; fi
  pre=""; [ "$dry" -eq 1 ] && pre=" (dry-run)"
  echo "cache-trim${pre}: skipped (go or golangci-lint running in this checkout, pid $bp)"
  exit 0
fi

hard && note "build cache over 2x cap"

# 1. build-cache entry files past the age limit
aged=0
if [ -d "$GOCACHE" ]; then
  aged="$(find "$GOCACHE" -type f -mtime "+$age_days" 2>/dev/null | wc -l | tr -d ' ')"
  if [ "$dry" -eq 0 ] && [ "$aged" -gt 0 ]; then find "$GOCACHE" -type f -mtime "+$age_days" -delete 2>/dev/null; fi
fi
[ "$aged" -gt 0 ] && note "$verb $aged build files >${age_days}d"

# 2. size caps -> full clean via the owning tool
run_clean() { # run_clean <label> <cmd...>
  local label="$1"; shift
  if [ "$dry" -eq 1 ]; then note "would clean $label"; return; fi
  if "$@" >/dev/null 2>&1; then note "cleaned $label"; else note "$label clean failed"; fi
}
# FB-1241: --auto never cleans the whole build cache. `go clean -cache` removes fresh entries and their directories too, so a go
# of a sibling worktree the busy scan did not see (started after it) loses an entry it already looked up and fails with a missing
# cache file. The age delete above stays (go marks an entry used on lookup, so a file older than the age is one nobody reads);
# the cap is the manual trim's: `make cache-trim`.
if [ -d "$GOCACHE" ] && over "$(kb "$GOCACHE")" "$build_cap"; then
  if [ "$auto" -eq 1 ]; then note "build cache over ${build_cap}GB left for \`make cache-trim\` (a sibling build may read it)"
  elif command -v go >/dev/null 2>&1; then run_clean "build cache (>${build_cap}GB)" go clean -cache; else note "go missing, build cache not cleaned"; fi
fi
if [ -d "$GOMODCACHE" ] && over "$(kb "$GOMODCACHE")" "$mod_cap"; then
  if [ "$auto" -eq 1 ]; then note "mod cache over ${mod_cap}GB left for \`make cache-trim\` (FB-1252: a sibling build may read it)"
  elif command -v go >/dev/null 2>&1; then run_clean "mod cache (>${mod_cap}GB)" go clean -modcache; else note "go missing, mod cache not cleaned"; fi
fi
if [ -d "$GOLANGCI_LINT_CACHE" ] && over "$(kb "$GOLANGCI_LINT_CACHE")" "$lint_cap"; then
  if command -v golangci-lint >/dev/null 2>&1; then run_clean "golangci cache (>${lint_cap}GB)" golangci-lint cache clean; else note "golangci-lint missing, its cache skipped"; fi
fi

# 3. tmp entries (top level: one go-build dir per command) older than one day
tmp_n=0
if [ -d "$GOTMPDIR" ]; then
  tmp_n="$(find "$GOTMPDIR" -mindepth 1 -maxdepth 1 -mtime +0 2>/dev/null | wc -l | tr -d ' ')"
  if [ "$dry" -eq 0 ] && [ "$tmp_n" -gt 0 ]; then
    find "$GOTMPDIR" -mindepth 1 -maxdepth 1 -mtime +0 -exec rm -rf {} + 2>/dev/null
  fi
fi
[ "$tmp_n" -gt 0 ] && note "$verb $tmp_n tmp entries >1d"

finish
exit 0
