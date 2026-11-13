#!/usr/bin/env bash
# managed by devkit — do not edit; fix upstream in ~/Developer/projects/devkit
# Purpose: `go test` what the changed paths select; the go pack's `kit-go-test` target (runs under check-select.py).
# Usage:   scripts/kit/go-test.sh          (KIT_CHECK_PATHS: changed paths separated by any whitespace; empty/unset = whole tree)
#   no paths    `go test ./...` in every go.mod dir of the work tree (the root included)
#   paths       each changed .go file's package, from its nearest go.mod, deduped; go.mod/go.sum -> that module's ./...;
#               a directory -> every package under it (FB-917; testdata/, vendor/, _x, .x left out), one with no .go
#               file selects nothing; a package dir that no longer holds a .go file is skipped; a path under testdata/, vendor/, _x or .x
#               stands for its nearest package above; other paths are not Go's and are ignored; a Go path with no go.mod
#               above it, when nothing else is selected, fails (exit 1) naming the path
# Env:     KIT_GO_TEST_FLAGS   extra `go test` flags, word-split (e.g. "-race -count=1")
#          HEAVY_RUN_CORES     the grant (the queue's, or the GNUmakefile shim's size): `-p <it>` goes first, unless
#                              KIT_GO_TEST_FLAGS carries its own `-p`
#          the same `go test` writes a coverage profile (a -coverprofile of the project's own is read, not replaced);
#          after every module passed, scripts/kit/cover-gate.py judges it against config/coverage-go.yaml. -coverpkg: not judged
# Output:  one line per module, the `go test` output, one summary line. Exit 0 green, 1 a module red (all run), a floor
#          refused or an unmapped Go path, 2 no go.
set -uo pipefail
set -f  # paths and flags are data: never glob them

flags="${KIT_GO_TEST_FLAGS:-}"
if [ -n "${HEAVY_RUN_CORES:-}" ]; then
  case " $(printf '%s' "$flags" | tr '\t' ' ') " in
    *" -p "*|*" -p="*|*" --p "*|*" --p="*) ;;
    *) flags="-p $HEAVY_RUN_CORES${flags:+ $flags}" ;;
  esac
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

# the nearest dir at or above $1 holding a go.mod; fails when there is none
nearest_mod() {
  local d="$1"
  while :; do
    [ -f "$d/go.mod" ] && { printf '%s' "$d"; return 0; }
    [ "$d" = "." ] && return 1
    case "$d" in */*) d="${d%/*}" ;; *) d="." ;; esac
  done
}

has_go_file() {
  local f found=1
  set +f  # the one place a glob is wanted
  for f in "$1"/*.go; do [ -f "$f" ] && { found=0; break; }; done
  set -f
  return "$found"
}

# a changed directory (FB-917): the packages under it, each dir holding a .go file, except what `go test ./...` never
# enters (testdata, vendor, _x, .x); a dir cut by trim_special stands for the package above it, as a file there does
dir_pkg_dirs() {
  local d="$1" t
  t="$(trim_special "$d")"
  if [ "$t" != "$d" ]; then  # only when the dir itself holds Go: .github or docs/_build with none selects nothing
    [ -z "$(find "$d" -type f -name '*.go' -print -quit)" ] || printf '%s\n' "$t"
    return 0
  fi
  find "$d" \( -name testdata -o -name vendor -o -name '_*' -o -name '.?*' \) -prune -o -type f -name '*.go' -print |
    sed -e 's|/[^/]*$||' -e 's|^\./||' | sort -u
}

whole_mods=""   # module dirs that run ./...
pkgs=""         # "<module dir>|<package dir>" lines
mods=""         # every module touched, in first-seen order
note=""
unmapped=0      # 1 once a Go path had no go.mod above it
add_mod() { case $'\n'"$mods"$'\n' in *$'\n'"$1"$'\n'*) ;; *) mods="${mods:+$mods$'\n'}$1" ;; esac; }

if [ -z "${KIT_CHECK_PATHS:-}" ]; then
  while IFS= read -r gomod; do
    [ -n "$gomod" ] || continue
    [ -f "$gomod" ] || continue  # tracked but deleted from the work tree: the module is gone
    dir="$(dirname "$gomod")"
    [ "$(trim_special "$dir")" = "$dir" ] || continue
    add_mod "$dir"
    whole_mods="${whole_mods:+$whole_mods$'\n'}$dir"
  done < <(git ls-files -co --exclude-standard -- go.mod '*/go.mod' | sort -u)
else
  # unquoted on purpose: any whitespace (space, tab, newline) separates paths (globbing is off: set -f)
  # shellcheck disable=SC2086
  for path in $KIT_CHECK_PATHS; do
    case "$path" in
      *.go) ;;
      *)
        if [ -d "$path" ]; then
          d="${path%/}"; d="${d#./}"; [ -n "$d" ] || d="."
          while IFS= read -r pdir; do
            [ -n "$pdir" ] || continue
            has_go_file "$pdir" || continue
            mod="$(nearest_mod "$pdir")" || { note="$note"$'\n'"go-test: $pdir: no go.mod above it, not tested"; unmapped=1; continue; }
            add_mod "$mod"
            pkgs="${pkgs:+$pkgs$'\n'}$mod|$pdir"
          done < <(dir_pkg_dirs "$d")
          continue
        fi ;;
    esac
    case "$path" in
      *.go|*/go.mod|go.mod|*/go.sum|go.sum) ;;
      *) continue ;;
    esac
    case "$path" in */*) dir="${path%/*}" ;; *) dir="." ;; esac
    dir="$(trim_special "$dir")"
    [ -d "$dir" ] || continue  # a deleted package dir
    mod="$(nearest_mod "$dir")" || { note="$note"$'\n'"go-test: $path: no go.mod above it, not tested"; unmapped=1; continue; }
    case "$path" in
      *go.mod|*go.sum)
        add_mod "$mod"
        whole_mods="${whole_mods:+$whole_mods$'\n'}$mod" ;;
      *)
        has_go_file "$dir" || continue
        add_mod "$mod"
        pkgs="${pkgs:+$pkgs$'\n'}$mod|$dir" ;;
    esac
  done
fi

[ -z "$note" ] || printf '%s\n' "${note#?}" >&2

if [ -z "$mods" ] && [ "$unmapped" = 1 ]; then
  echo "go-test: a changed Go path maps to no module and nothing else was selected: not a pass (see above)" >&2
  exit 1
fi

if [ -z "$mods" ]; then
  echo "go-test: no Go module or package selected: nothing to test"
  exit 0
fi

# coverage: the same one `go test` per module writes a profile; scripts/kit/cover-gate.py judges the measure lines.
# A project's own -coverprofile in the flags is read, not replaced; -coverpkg makes a figure depend on which packages
# ran, so nothing is judged then.
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

cover_dir=""
if [ "$coverpkg" = 1 ]; then
  echo "warn: -coverpkg makes a figure depend on which packages ran; coverage not judged" >&2
else
  mkdir -p "$top/.artifacts/tmp" || { echo "go-test: cannot create $top/.artifacts/tmp" >&2; exit 2; }
  # a killed run (devkit gate's timeout) leaves its dir behind: sweep the ones older than a day
  find "$top/.artifacts/tmp" -maxdepth 1 -type d -name 'kit-go-cover.*' -mtime +0 -exec rm -rf {} + 2>/dev/null
  cover_dir="$(mktemp -d "$top/.artifacts/tmp/kit-go-cover.XXXXXX")" || { echo "go-test: cannot create a temp dir under $top/.artifacts/tmp" >&2; exit 2; }
  trap 'rm -rf "$cover_dir"' EXIT
fi

# FB-638: a floor refusal keeps the refused packages' profiles (a green run keeps nothing). Home: DEVKIT_GATE_COVER_KEEP
# (devkit gate sets it beside the gate log, outside any queue snapshot worktree, and sweeps it after 7 days); a direct
# make check has no gate, so a dir under the run's own checkout's .artifacts/tmp (FB-918: the worktree the run is in, not
# the main checkout), named kit-go-kept.*, which this script sweeps after 7 days.
# The cover sweep above takes only kit-go-cover.* and the day-old rule, so neither takes the other's.
keep_home=""
if [ "$coverpkg" = 0 ]; then
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
# profile into $keep_home/<unit, / as _>.out (a mode line, then that package's blocks: `go tool cover -func` reads it)
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
      ' "$prof" >"$out"
      n="$(wc -l <"$out" | tr -d ' ')"
      if [ "$n" -le 1 ]; then rm -f "$out"; else kept="${kept:+$kept, }$out"; fi
    done <<<"$prof_list"
  done < <(sed -n 's/^coverage: \([^ ]*\): .*/\1/p' "$1" | sort -u)
  [ -z "$kept" ] || echo "go-test: kept the refused profile(s): $kept (go tool cover -func=<file>)" >&2
  [ -d "$keep_home" ] && rmdir "$keep_home" 2>/dev/null
  return 0
}

rc=0
ran=0
cover_lines=""
sel_dirs=""
while IFS= read -r mod; do
  if case $'\n'"$whole_mods"$'\n' in *$'\n'"$mod"$'\n'*) true ;; *) false ;; esac; then
    args="./..."
    kind=whole
  else
    kind=changed
    args=""
    sel_dirs=""
    while IFS= read -r line; do
      [ "${line%%|*}" = "$mod" ] || continue
      pkg="${line#*|}"
      if [ "$pkg" = "$mod" ]; then rel="."
      elif [ "$mod" = "." ]; then rel="./$pkg"
      else rel="./${pkg#"$mod"/}"; fi
      case " $args " in *" $rel "*) ;; *) args="${args:+$args }$rel"; sel_dirs="${sel_dirs:+$sel_dirs$'\n'}$pkg" ;; esac
    done <<<"$pkgs"
  fi
  echo "go-test: $mod: go test${flags:+ $flags} $args"
  prof=""
  if [ "$coverpkg" = 0 ]; then
    if [ -n "$proj_prof" ]; then
      case "$proj_prof" in /*) prof="$proj_prof" ;; *) prof="$mod/$proj_prof" ;; esac
      rm -f "$prof"  # the file a failed or older run left must not pass for this run's
    else
      prof="$cover_dir/$ran.out"
    fi
  fi
  mod_rc=0
  if [ -n "$prof" ] && [ -z "$proj_prof" ]; then
    # shellcheck disable=SC2086
    (cd "$mod" && go test $flags "-coverprofile=$prof" $args </dev/null) || mod_rc=1
  else
    # shellcheck disable=SC2086
    (cd "$mod" && go test $flags $args </dev/null) || mod_rc=1
  fi
  ran=$((ran + 1))
  if [ "$mod_rc" -ne 0 ]; then
    rc=1  # a failing run's profile is not read
  elif [ -n "$prof" ]; then
    if [ -f "$prof" ]; then
      prof_list="${prof_list:+$prof_list$'\n'}$mod|$prof"
      lines="$(profile_lines "$(module_path "$mod")" "$mod" "$kind" "$prof")"
      if [ "$kind" = changed ]; then
        while IFS= read -r dir; do
          [ -n "$dir" ] || continue
          case $'\n'"$lines" in *$'\n'"$dir "*) ;; *) lines="${lines:+$lines$'\n'}$dir 0 0 changed" ;; esac
        done <<<"$sel_dirs"
      fi
      [ -z "$lines" ] || cover_lines="${cover_lines:+$cover_lines$'\n'}$lines"
    else
      echo "go-test: $mod: no coverage profile" >&2
      rc=1
    fi
  fi
done <<<"$mods"

# the floors: only after every module passed, and only where the gate script is part of the render
if [ "$rc" -eq 0 ] && [ "$coverpkg" = 0 ] && [ -f scripts/kit/cover-gate.py ]; then
  if [ -z "${KIT_CHECK_PATHS:-}" ]; then whole_flag="--whole"; else whole_flag=""; fi
  gate_err="$cover_dir/gate.err"
  printf '%s\n' "$cover_lines" | python3 scripts/kit/cover-gate.py check --stack go ${whole_flag:+"$whole_flag"} 2>"$gate_err" || rc=1
  cat "$gate_err" >&2
  if [ "$rc" -ne 0 ]; then keep_refused "$gate_err"; fi
fi

if [ "$rc" -eq 0 ]; then echo "go-test: $ran module(s) green"; else echo "go-test: red" >&2; fi
exit "$rc"
