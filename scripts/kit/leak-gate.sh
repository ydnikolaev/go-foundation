#!/usr/bin/env bash
# managed by devkit — do not edit; fix upstream in ~/Developer/projects/devkit
# Purpose: fail `make check` (and check-full, check-docker) when a process or a container that the make run started is
#          still alive once every other check prerequisite is done. It names each survivor and kills nothing: the target
#          that started it must stop it (R-129: a gate decides, it never reaps).
# Usage:   scripts/kit/leak-gate.sh <make-pid> [goal...]     (run from the repo root; mk/kit-leak.mk passes $$PPID)
# Process: a survivor meets all four: its pgid is make's pgid; it is not make, an ancestor of make, or the gate's own
#          chain; it is a descendant of make or has ppid 1; it started at or after make's start. Start times compare as
#          ps ages (etime, one snapshot, one-second steps), so make's start is read now, never at parse time.
# Docker:  only in a repo that renders the docker module (mk/kit-docker.mk; any other repo: no check, no note), and
#          only when `docker` is on PATH and the daemon answers within KIT_LEAK_DOCKER_TIMEOUT seconds (default 5);
#          otherwise one skip note and no failure. A survivor container is running, was created at or after make's
#          start, and carries both the owner label dev.$KIT_PROJECT.owner=$KIT_PROJECT (the docker standard's) and this
#          run's id, label dev.$KIT_PROJECT.run=<make-pid> (FB-906: what the run starts gets it from $KIT_RUN_ID, which
#          mk/kit-e2e.mk sets to make's pid (KIT_MAKE_PID, else $PPID); a container another session starts while make runs has no such label and is
#          not counted, so a project's own target that starts a container passes `--label dev.<project>.run=$KIT_RUN_ID` or
#          the gate cannot see it). KIT_PROJECT: the environment, else .devkit/config.env. A container that also carries
#          label dev.$KIT_PROJECT.shared=true (set by the project on a service worktrees share by design, in its own
#          compose file; no knob here) is not a survivor: the gate prints `kit-leak: shared container <name> kept` for
#          each one that would otherwise count, and kills nothing. Another project's key, or a value other than true,
#          does not exempt (FB-580). A candidate whose label com.docker.compose.project.working_dir is set and is not this
#          checkout's top (git rev-parse --show-toplevel) or under it, both resolved with pwd -P, belongs to another checkout:
#          it is not counted, and one line `kit-leak: <n> container(s) of other checkouts not counted` says so. No compose
#          label, or a git that cannot name the top: the candidate counts (fail closed).
# Exit:    0 no survivor (`kit-leak: ok`); 1 at least one survivor; 2 the gate itself could not read the process table.
# Needs:   bash 3.2, a POSIX/BSD awk, ps with -o pid,ppid,pgid,stat,etime,command (macOS and Linux procps).

mkpid="${1-}"
shift
goals="$*"
case "$mkpid" in '' | *[!0-9]*)
  echo "kit-leak: usage: leak-gate.sh <make-pid> [goal...]" >&2
  exit 2
  ;;
esac
self=$$
cut_to=160

tmp="$(mktemp -d "${TMPDIR:-/tmp}/kit-leak.XXXXXX")" || { echo "kit-leak: cannot create a temp dir" >&2; exit 2; }
trap 'rm -rf "$tmp"' EXIT

# ---- processes: one snapshot, judged in awk ----
now="$(date +%s)"
if ! ps -A -o pid=,ppid=,pgid=,stat=,etime=,command= >"$tmp/ps.txt" 2>"$tmp/ps.err"; then
  echo "kit-leak: cannot read the process table (ps -A failed): $(head -c 200 "$tmp/ps.err")" >&2
  exit 2
fi

IFS= read -r -d '' judge <<'AWK_PROG' || true
function secs(e,   d, r, n, p) {
  d = 0; r = e
  if (index(e, "-") > 0) { split(e, p, "-"); d = p[1] + 0; r = p[2] }
  n = split(r, p, ":")
  if (n == 3) return d * 86400 + p[1] * 3600 + p[2] * 60 + p[3]
  if (n == 2) return d * 86400 + p[1] * 60 + p[2]
  return d * 86400 + p[1]
}
function human(s) {
  if (s >= 3600) return sprintf("%dh%02dm", s / 3600, (s % 3600) / 60)
  if (s >= 60) return sprintf("%dm%02ds", s / 60, s % 60)
  return sprintf("%ds", s)
}
# walks up from p; 1 when `target` is met before the chain ends
function under(p, target,   k) {
  for (k = 0; k < 256 && (p in par); k++) {
    if (p == target) return 1
    if (p <= 1) return 0
    p = par[p]
  }
  return 0
}
{
  pid = $1; par[pid] = $2; grp[pid] = $3; stt[pid] = $4; age[pid] = secs($5)
  c = $0
  for (i = 0; i < 5; i++) sub(/^[ \t]*[^ \t]+/, "", c)
  sub(/^[ \t]+/, "", c)
  cmd[pid] = c; ids[++n] = pid
}
END {
  if (!(mk in grp)) { print "NOMAKE"; exit 0 }
  # ancestors of make, and the path from the gate up to make
  p = par[mk]
  for (k = 0; k < 256 && (p in par) && p > 1; k++) { skip[p] = 1; p = par[p] }
  p = self
  for (k = 0; k < 256 && (p in par) && p != mk && p > 1; k++) { skip[p] = 1; p = par[p] }
  for (j = 1; j <= n; j++) {
    pid = ids[j]
    if (pid == mk || pid in skip || stt[pid] ~ /^Z/) continue
    if (under(pid, self)) continue                      # the gate's own children: ps, awk, subshells
    if (grp[pid] != grp[mk]) continue
    if (age[pid] > age[mk]) continue                    # older than make: not its child
    if (!(under(par[pid], mk) || par[pid] == 1)) continue
    c = cmd[pid]
    if (length(c) > cut) c = substr(c, 1, cut - 3) "..."
    printf "P\t%s\t%s\t%s\n", pid, human(age[pid]), c
  }
  printf "M\t%d\n", age[mk]
}
AWK_PROG

if ! awk -v mk="$mkpid" -v self="$self" -v cut="$cut_to" "$judge" "$tmp/ps.txt" >"$tmp/proc.out"; then
  echo "kit-leak: the process scan failed (awk); the gate cannot say the run left nothing alive" >&2
  exit 2
fi
if grep -q '^NOMAKE' "$tmp/proc.out"; then
  echo "kit-leak: make pid $mkpid is not in the process table; the gate was not given make's pid" >&2
  exit 2
fi
make_age="$(sed -n 's/^M	//p' "$tmp/proc.out" | sed -n 1p)"
start=$((now - make_age))

survivors="$tmp/survivors.txt"
sed -n 's/^P	\([^	]*\)	\([^	]*\)	\(.*\)$/process \1  age \2  \3/p' "$tmp/proc.out" >"$survivors"

# ---- containers ----
# run <secs> <outfile> cmd...: cmd's stdout to outfile; 124 when it did not finish in time (the gate started it, so it stops it)
run() {
  local t="$1" out="$2" p i=0 limit
  shift 2
  "$@" >"$out" 2>/dev/null </dev/null &
  p=$!
  limit=$((t * 10))
  while kill -0 "$p" 2>/dev/null; do
    if [ "$i" -ge "$limit" ]; then
      kill "$p" 2>/dev/null
      wait "$p" 2>/dev/null
      return 124
    fi
    sleep 0.1
    i=$((i + 1))
  done
  wait "$p"
}

IFS= read -r -d '' to_epoch <<'AWK_EPOCH' || true
# RFC 3339 (docker's .Created) to epoch seconds; a fraction is ignored
function epoch(s,   y, m, d, hh, mi, ss, era, yoe, doy, doe, off, sign, o) {
  if (s !~ /^[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]/) return -1
  y = substr(s, 1, 4) + 0; m = substr(s, 6, 2) + 0; d = substr(s, 9, 2) + 0
  hh = substr(s, 12, 2) + 0; mi = substr(s, 15, 2) + 0; ss = substr(s, 18, 2) + 0
  if (m <= 2) y--
  era = int(y / 400); yoe = y - era * 400
  doy = int((153 * (m + (m > 2 ? -3 : 9)) + 2) / 5) + d - 1
  doe = yoe * 365 + int(yoe / 4) - int(yoe / 100) + doy
  off = 0
  o = substr(s, 20); sub(/^\.[0-9]+/, "", o)
  if (o ~ /^[+-][0-9][0-9]:[0-9][0-9]$/) {
    sign = (substr(o, 1, 1) == "-") ? -1 : 1
    off = sign * (substr(o, 2, 2) * 3600 + substr(o, 5, 2) * 60)
  }
  return (era * 146097 + doe - 719468) * 86400 + hh * 3600 + mi * 60 + ss - off
}
# a container created after the gate's own clock (now) has a negative age: clamp at 0 (FB-603)
{ e = epoch($1); if (e >= start) { a = now - e; if (a < 0) a = 0; n = $2; sub(/^\//, "", n); printf "container %s  age %ds  %s\n", id, a, substr(n, 1, cut) } }
AWK_EPOCH

skip_note=""
timeout_s="${KIT_LEAK_DOCKER_TIMEOUT:-5}"
case "$timeout_s" in '' | *[!0-9]*) timeout_s=5 ;; esac
proj="${KIT_PROJECT-}"
if [ -z "$proj" ] && [ -f .devkit/config.env ]; then
  proj="$(sed -n 's/^KIT_PROJECT=//p' .devkit/config.env | sed -n 1p | tr -d "\"'")"
fi
if [ ! -f mk/kit-docker.mk ]; then
  :  # the repo does not render the docker module: no container check, no note
elif ! command -v docker >/dev/null 2>&1; then
  skip_note="containers not checked: docker is not on PATH"
elif [ -z "$proj" ]; then
  skip_note="containers not checked: KIT_PROJECT is not set (environment or .devkit/config.env)"
elif ! run "$timeout_s" "$tmp/info.out" docker info; then
  skip_note="containers not checked: the docker daemon did not answer within ${timeout_s}s"
else
  # FB-906: only what this run started: the owner label AND this run's id (make's pid, label dev.$proj.run); two --filter
  # label flags are ANDed. A container another session started during the run carries no such label.
  : >"$tmp/ids.txt"
  if run "$timeout_s" "$tmp/ps.out" docker ps -q --filter "label=dev.$proj.owner=$proj" --filter "label=dev.$proj.run=$mkpid"; then
    cat "$tmp/ps.out" >>"$tmp/ids.txt"
  fi
  cnow="$(date +%s)"
  sort -u "$tmp/ids.txt" >"$tmp/ids.sorted"
  # shared: the project's own opt-out label; when the lookup fails the list stays empty and the container counts (fail closed)
  : >"$tmp/shared.txt"
  if run "$timeout_s" "$tmp/shared.out" docker ps -q --filter "label=dev.$proj.shared=true"; then
    cat "$tmp/shared.out" >"$tmp/shared.txt"
  fi
  # this checkout's top, both resolved; empty when git cannot say (the gate then counts every candidate: fail closed)
  top="$(git rev-parse --show-toplevel 2>/dev/null)" && top="$(cd "$top" 2>/dev/null && pwd -P)" || top=""
  top="${top%/}"
  : >"$tmp/other.txt"
  while IFS= read -r id; do
    if run "$timeout_s" "$tmp/insp.out" docker inspect -f '{{.Created}} {{.Name}} {{index .Config.Labels "com.docker.compose.project.working_dir"}}' "$id"; then
      awk -v id="$id" -v start="$start" -v now="$cnow" -v cut="$cut_to" "$to_epoch" "$tmp/insp.out" >"$tmp/one.txt"
      # the optional trailing field is the compose working_dir (it may hold spaces): empty means no compose label
      wd="$(sed -n '1s/^[^ ]* [^ ]* \{0,1\}//p' "$tmp/insp.out")"
      if [ -n "$wd" ] && [ -n "$top" ] && [ -s "$tmp/one.txt" ]; then
        rwd="$(cd "$wd" 2>/dev/null && pwd -P)" || rwd="$wd"
        rwd="${rwd%/}"
        case "$rwd/" in
          "$top"/*) ;;
          *) cat "$tmp/one.txt" >>"$tmp/other.txt"; continue ;;
        esac
      fi
      if grep -q -x -F "$id" "$tmp/shared.txt"; then
        # FB-603: the awk clamps the age at 0; the optional '-' is a second guard
        sed 's/^container [^ ]*  age -\{0,1\}[0-9]*s  \(.*\)$/kit-leak: shared container \1 kept/' "$tmp/one.txt" >>"$tmp/kept.txt"
      else
        cat "$tmp/one.txt" >>"$survivors"
      fi
    fi
  done <"$tmp/ids.sorted"
fi

# ---- verdict ----
[ -n "$skip_note" ] && echo "kit-leak: $skip_note"
[ -s "$tmp/kept.txt" ] && cat "$tmp/kept.txt"
if [ -s "$tmp/other.txt" ]; then
  echo "kit-leak: $(wc -l <"$tmp/other.txt" | tr -d ' ') container(s) of other checkouts not counted"
fi
count="$(wc -l <"$survivors" | tr -d ' ')"
if [ "$count" -eq 0 ]; then
  echo "kit-leak: ok"
  exit 0
fi
echo "kit-leak: $count survivor(s) of make ${goals:-check}:"
sed 's/^/  /' "$survivors"
echo "kit-leak: the target that started each one must stop it (trap, --rm, a stop step); this gate kills nothing"
exit 1
