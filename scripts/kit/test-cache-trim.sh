#!/usr/bin/env bash
# managed by devkit — do not edit; fix upstream in ~/Developer/projects/devkit
# Purpose: black-box tests for cache-trim.sh in a throwaway git repo, with a fake `go` on PATH.
# Usage:   bash scripts/kit/test-cache-trim.sh        Exit: 0 all passed, 1 any failure.
set -uo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
pass=0; fail=0; rc=0; out=""
expect() { if [ "$2" -eq 0 ]; then pass=$((pass+1)); else fail=$((fail+1)); echo "FAIL: $1 (rc=$rc out=[$out])"; fi; }
t() { [ "$1" = "$2" ] && echo 0 || echo 1; }
has() { [[ "$1" == *"$2"* ]] && echo 0 || echo 1; }
run() { out="$("$@" 2>&1)"; rc=$?; }
old() { touch -t 202001010000 "$@"; }   # an mtime far past every age limit

# --- sandbox: a real git repo carrying the real script; a fake `go` that only records its calls ---
repo="$tmp/repo"; bin="$tmp/bin"
mkdir -p "$repo/scripts/kit" "$bin"
cp "$here/cache-trim.sh" "$repo/scripts/kit/"
git -C "$repo" init -q
cat >"$bin/go" <<'SH'
#!/usr/bin/env bash
echo "go $*" >>"$FAKE_CALLS"
# FAKE_GO_HOLD set: stay alive like a running build until that file appears (or its .up marker is removed)
if [ -n "${FAKE_GO_HOLD:-}" ]; then
  : >"$FAKE_GO_HOLD.up"
  while [ -e "$FAKE_GO_HOLD.up" ] && [ ! -e "$FAKE_GO_HOLD" ]; do sleep 0.1; done
fi
SH
chmod +x "$bin/go"
# Hermetic: the guard's pgrep sees the whole host, where other runs hold real go processes (FB-158). A pgrep on PATH
# that lists only descendants of this script keeps the fakes started here the only ones that count.
real_pgrep="$(command -v pgrep || true)"
if [ -n "$real_pgrep" ]; then
  cat >"$bin/pgrep" <<SH
#!/usr/bin/env bash
for p in \$("$real_pgrep" "\$@"); do
  a=\$p
  while [ -n "\$a" ] && [ "\$a" -gt 1 ]; do
    if [ "\$a" = "$$" ]; then echo "\$p"; break; fi
    a="\$(ps -o ppid= -p "\$a" 2>/dev/null | tr -d ' ')"
  done
done
SH
  chmod +x "$bin/pgrep"
fi
export FAKE_CALLS="$tmp/calls" PATH="$bin:$PATH"
unset GOCACHE GOMODCACHE GOTMPDIR GOLANGCI_LINT_CACHE KIT_CACHE_AGE_DAYS KIT_CACHE_CAP_GB KIT_MODCACHE_CAP_GB KIT_GOLANGCI_CAP_GB KIT_STAMP_DIR
cd "$repo" || exit 1
trim() { run bash scripts/kit/cache-trim.sh "$@"; }
seed() {
  rm -rf "$repo/.cache" "$repo/.artifacts" "$FAKE_CALLS"
  mkdir -p "$repo/.cache/go/build" "$repo/.cache/go/mod" "$repo/.cache/go/tmp" "$repo/.cache/golangci"
  echo x >"$repo/.cache/go/build/aged"; old "$repo/.cache/go/build/aged"
  echo x >"$repo/.cache/go/build/fresh"
  mkdir "$repo/.cache/go/tmp/go-build-old" "$repo/.cache/go/tmp/go-build-new"; old "$repo/.cache/go/tmp/go-build-old"
}
gone() { [ -e "$1" ] && echo 1 || echo 0; }   # 1 if still there

# ---------- age trim ----------
seed; trim --force
expect "force: rc 0" "$(t $rc 0)"
expect "force: aged build file removed" "$(t "$(gone .cache/go/build/aged)" 0)"
expect "force: fresh build file kept" "$(t "$(gone .cache/go/build/fresh)" 1)"
expect "force: old tmp entry removed" "$(t "$(gone .cache/go/tmp/go-build-old)" 0)"
expect "force: new tmp entry kept" "$(t "$(gone .cache/go/tmp/go-build-new)" 1)"
expect "force: summary line" "$(has "$out" 'cache-trim: removed 1 build files')"
expect "force: stamp written" "$(t "$(gone .artifacts/stamps/cache-trim)" 1)"
expect "force: .artifacts ignores itself" "$(t "$(gone .artifacts/.gitignore)" 1)"
expect "force: git sees nothing under .artifacts" "$(t "$(git status --porcelain -- .artifacts)" '')"

# ---------- dry-run changes nothing ----------
seed; trim --dry-run
expect "dry-run: rc 0" "$(t $rc 0)"
expect "dry-run: aged file survives" "$(t "$(gone .cache/go/build/aged)" 1)"
expect "dry-run: old tmp entry survives" "$(t "$(gone .cache/go/tmp/go-build-old)" 1)"
expect "dry-run: no stamp" "$(t "$(gone .artifacts/stamps/cache-trim)" 0)"
expect "dry-run: says so" "$(has "$out" '(dry-run)')"

# ---------- --auto: at most once per 24h ----------
seed; trim --auto
expect "auto: first run trims" "$(t "$(gone .cache/go/build/aged)" 0)"
echo x >.cache/go/build/aged2; old .cache/go/build/aged2
trim --auto
expect "auto: second run skipped" "$(has "$out" 'skipped (ran within the last 24h)')"
expect "auto: skipped run left the aged file" "$(t "$(gone .cache/go/build/aged2)" 1)"
trim --force
expect "force ignores the stamp" "$(t "$(gone .cache/go/build/aged2)" 0)"

# ---------- size cap goes through the owning tool ----------
seed; head -c 2048 /dev/zero >.cache/go/mod/blob
KIT_MODCACHE_CAP_GB=0 KIT_CACHE_CAP_GB=0 trim --force
expect "cap: go clean -modcache called" "$(has "$(cat "$FAKE_CALLS" 2>/dev/null)" 'go clean -modcache')"
expect "cap: go clean -cache called" "$(has "$(cat "$FAKE_CALLS" 2>/dev/null)" 'go clean -cache')"
seed; head -c 2048 /dev/zero >.cache/go/mod/blob; trim --force
expect "under cap: go never called" "$(t "$(cat "$FAKE_CALLS" 2>/dev/null)" '')"

# ---------- a cache path outside <main>/.cache is refused, and nothing is touched ----------
seed; mkdir -p "$tmp/elsewhere"; echo x >"$tmp/elsewhere/aged"; old "$tmp/elsewhere/aged"
GOCACHE="$tmp/elsewhere" trim --force
expect "foreign path: rc 1" "$(t $rc 1)"
expect "foreign path: refusal names the variable" "$(has "$out" 'refusing GOCACHE=')"
expect "foreign path: foreign aged file survives" "$(t "$(gone "$tmp/elsewhere/aged")" 1)"
expect "foreign path: own aged file survives too (nothing touched)" "$(t "$(gone .cache/go/build/aged)" 1)"
GOCACHE="$repo/.cache" trim --force
expect "the .cache root itself is refused" "$(t $rc 1)"
GOMODCACHE="$repo/.cache/../scripts" trim --force
expect "a .. escape is refused" "$(t $rc 1)"
expect "a .. escape: scripts untouched" "$(t "$(gone scripts/kit/cache-trim.sh)" 1)"
ln -s "$tmp/elsewhere" "$repo/.cache/link"
GOTMPDIR="$repo/.cache/link" trim --force
expect "a symlink out of .cache is refused" "$(t $rc 1)"
expect "a symlink out: target survives" "$(t "$(gone "$tmp/elsewhere/aged")" 1)"
rm -f "$repo/.cache/link"

# ---------- FB-158: busy guard. A go build/test in this checkout family skips the whole run ----------
hold="$tmp/hold"; mkdir -p "$tmp/other"; gopid=""
hold_go() { # hold_go <cwd> <go subcommand>: a fake go alive in the background with that cwd
  rm -f "$hold" "$hold.up"
  ( cd "$1" && FAKE_GO_HOLD="$hold" exec bash "$bin/go" "$2" ) >/dev/null 2>&1 &
  gopid=$!
  local n=0; while [ ! -e "$hold.up" ] && [ "$n" -lt 100 ]; do sleep 0.1; n=$((n + 1)); done
}
release_go() { : >"$hold"; wait "$gopid" 2>/dev/null; }
mkdir -p "$repo/sub"
seed; hold_go "$repo" test; trim --force
expect "busy: rc 0" "$(t $rc 0)"
expect "busy: names the skip and the pid" "$(has "$out" "cache-trim: skipped (go or golangci-lint running in this checkout, pid $gopid)")"
expect "busy: --force does not override; aged build file kept" "$(t "$(gone .cache/go/build/aged)" 1)"
expect "busy: old tmp entry kept" "$(t "$(gone .cache/go/tmp/go-build-old)" 1)"
expect "busy: no stamp written" "$(t "$(gone .artifacts/stamps/cache-trim)" 0)"
trim --auto --force
expect "busy: --auto --force still skips" "$(t "$(has "$out" 'skipped (go or golangci-lint running')$(gone .cache/go/build/aged)" 01)"
trim --dry-run
expect "busy: dry-run prints the same line with (dry-run)" "$(has "$out" "cache-trim (dry-run): skipped (go or golangci-lint running in this checkout, pid $gopid)")"
release_go
seed; hold_go "$repo/sub" vet; trim --force
expect "busy: a go vet with its cwd in a subdirectory of the checkout skips too" "$(t "$(gone .cache/go/build/aged)" 1)"
release_go
seed; hold_go "$tmp/other" test; trim --force
expect "busy: FB-158 a matching go test with its cwd outside the family never blocks" "$(t "$(gone .cache/go/build/aged)" 0)"
expect "busy: elsewhere: stamp written" "$(t "$(gone .artifacts/stamps/cache-trim)" 1)"
release_go
# FB-158: a go process that ends between pgrep listing it and the cwd read (lsof prints nothing) is not busy, a live
# one with an unreadable cwd is. Only where lsof is the reader (no /proc).
if [ ! -e "/proc/$$/cwd" ] && command -v lsof >/dev/null 2>&1; then
  cat >"$bin/lsof" <<'SH'
#!/usr/bin/env bash
# fake lsof: [KILL_ON_READ=1] ends the pid asked about, then reads nothing
if [ "${KILL_ON_READ:-0}" = 1 ]; then kill -9 "$3" 2>/dev/null; n=0; while kill -0 "$3" 2>/dev/null && [ "$n" -lt 30 ]; do sleep 0.1; n=$((n + 1)); done; fi
exit 1
SH
  chmod +x "$bin/lsof"
  seed; hold_go "$repo" test; KILL_ON_READ=1 trim --force
  expect "busy: FB-158 a pid that ended before its cwd was read is not busy (cleans)" "$(t "$(gone .cache/go/build/aged)" 0)"
  release_go
  seed; hold_go "$repo" test; trim --force
  expect "busy: a live pid whose cwd cannot be read counts as busy" "$(t "$(gone .cache/go/build/aged)" 1)"
  release_go
  rm -f "$bin/lsof"
fi
seed; hold_go "$repo" run; trim --force
expect "busy: go run is not in the set (cleans)" "$(t "$(gone .cache/go/build/aged)" 0)"
release_go
seed; hold_go "$repo" test; release_go; trim --force
expect "busy: once the build ended the run cleans" "$(t "$(gone .cache/go/build/aged)" 0)"

# ---------- outside a checkout / bad usage ----------
mkdir -p "$tmp/nogit"
( cd "$tmp/nogit" && GIT_CEILING_DIRECTORIES="$tmp" bash "$repo/scripts/kit/cache-trim.sh" --force ) >"$tmp/o" 2>&1; rc=$?; out="$(cat "$tmp/o")"
expect "outside a git checkout: rc 1" "$(t $rc 1)"
trim --bogus; expect "bad usage: rc 64" "$(t $rc 64)"

# ---------- a linked worktree trims the MAIN checkout's cache ----------
seed
git -c user.email=t@t -c user.name=t -c commit.gpgsign=false add scripts >/dev/null
git -c user.email=t@t -c user.name=t -c commit.gpgsign=false commit -qm init
git worktree add -q --detach "$tmp/wt" HEAD
( cd "$tmp/wt" && bash scripts/kit/cache-trim.sh --force ) >"$tmp/o" 2>&1; rc=$?; out="$(cat "$tmp/o")"
expect "worktree: rc 0" "$(t $rc 0)"
expect "worktree: main's aged file removed" "$(t "$(gone .cache/go/build/aged)" 0)"
expect "worktree: no cache created inside the worktree" "$(t "$(gone "$tmp/wt/.cache")" 0)"
# the family includes every worktree git lists: a go build there blocks a trim run from the main checkout
seed; hold_go "$tmp/wt" build; trim --force
expect "busy: a go build in a linked worktree skips the main checkout's trim" "$(t "$(has "$out" 'skipped (go or golangci-lint running')$(gone .cache/go/build/aged)" 01)"
release_go

# ---------- FB-545: the 24h stamp lives beside the shared caches (<main>/.artifacts/stamps), not per checkout ----------
wtrim() { ( cd "$tmp/wt" && bash scripts/kit/cache-trim.sh "$@" ) >"$tmp/o" 2>&1; rc=$?; out="$(cat "$tmp/o")"; }
seed; rm -rf "$tmp/wt/.artifacts"
mkdir -p .artifacts/stamps; touch .artifacts/stamps/cache-trim
wtrim --auto
expect "FB-545: a worktree with no local stamp skips on main's fresh stamp" "$(has "$out" 'skipped (ran within the last 24h)')"
expect "FB-545: that skipped run left main's aged file" "$(t "$(gone .cache/go/build/aged)" 1)"
seed; rm -rf "$tmp/wt/.artifacts"
wtrim --auto
expect "FB-545: a worktree's trim cleans the shared cache" "$(t "$(gone .cache/go/build/aged)" 0)"
expect "FB-545: it writes main's stamp" "$(t "$(gone .artifacts/stamps/cache-trim)" 1)"
expect "FB-545: and none in the worktree" "$(t "$(gone "$tmp/wt/.artifacts")" 0)"
echo x >.cache/go/build/aged2; old .cache/go/build/aged2
wtrim --auto
expect "FB-545: the next worktree --auto skips on that stamp" "$(has "$out" 'skipped (ran within the last 24h)')"
# an old per-checkout stamp is ignored and never deleted
seed; mkdir -p "$tmp/wt/.artifacts/stamps"; touch "$tmp/wt/.artifacts/stamps/cache-trim"
wtrim --auto
expect "FB-545: a local stamp in a worktree is ignored" "$(t "$(gone .cache/go/build/aged)" 0)"
expect "FB-545: and kept" "$(t "$(gone "$tmp/wt/.artifacts/stamps/cache-trim")" 1)"
rm -rf "$tmp/wt/.artifacts"
# KIT_STAMP_DIR still overrides
seed; KIT_STAMP_DIR="$tmp/mystamps" trim --auto
expect "FB-545: KIT_STAMP_DIR overrides: stamp there" "$(t "$(gone "$tmp/mystamps/cache-trim")" 1)"
expect "FB-545: KIT_STAMP_DIR overrides: none in main" "$(t "$(gone .artifacts/stamps/cache-trim)" 0)"
# lock: a held lock skips with one line, no stamp, nothing trimmed; a stale one (>1h) is removed
seed; mkdir -p .artifacts/stamps/cache-trim.lock
trim --auto
expect "FB-545: a held lock skips" "$(has "$out" 'cache-trim: skipped (another trim is running')"
expect "FB-545: a held lock: rc 0" "$(t $rc 0)"
expect "FB-545: a held lock: one line" "$(t "$(printf '%s\n' "$out" | wc -l | tr -d ' ')" 1)"
expect "FB-545: a held lock: nothing trimmed" "$(t "$(gone .cache/go/build/aged)" 1)"
expect "FB-545: a held lock: no stamp" "$(t "$(gone .artifacts/stamps/cache-trim)" 0)"
expect "FB-545: a held lock is left alone" "$(t "$(gone .artifacts/stamps/cache-trim.lock)" 1)"
trim --force
expect "FB-545: --force does not override the lock" "$(t "$(gone .cache/go/build/aged)" 1)"
touch -t 202001010000 .artifacts/stamps/cache-trim.lock
trim --auto
expect "FB-545: a stale lock (>1h) is removed and the trim runs" "$(t "$(gone .cache/go/build/aged)" 0)"
expect "FB-545: the lock is released after the trim" "$(t "$(gone .artifacts/stamps/cache-trim.lock)" 0)"
expect "FB-545: stamp written after a stale lock" "$(t "$(gone .artifacts/stamps/cache-trim)" 1)"
# the lock is released on a skipped (busy) run too
seed; hold_go "$repo" test; trim --auto
expect "FB-545: a busy run releases the lock" "$(t "$(gone .artifacts/stamps/cache-trim.lock)" 0)"
release_go

echo "test-cache-trim: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
