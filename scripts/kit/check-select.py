#!/usr/bin/env python3
# managed by devkit — do not edit; fix upstream in ~/Developer/projects/devkit
"""kit-select: run the targets the changed paths select (`make check`), or the target of every map row that wins for a
tracked path (`make check-full`).

    python3 scripts/kit/check-select.py [--map FILE]... [--trunk BRANCH] [--strict]
    python3 scripts/kit/check-select.py --adopted

Env: KIT_TIER (empty/light, or full), KIT_CHECK_BASE (a ref to diff against), KIT_CHECK_HEAD (the commit the diff ends at), KIT_GOALS (make's goals),
KIT_MAKE (the make binary each selected target runs under; default `make`), KIT_MAKE_PID (make's pid). Out, to each target:
KIT_CHECK_PATHS, KIT_CHECK_MODULES. Stdlib only, python3 >= 3.9.

Map rows: `<glob> <target>...` or `<glob> -` (nothing extra); `#` comments; `@` lines are another reader's. Globs are
anchored at the repo root and split on `/`: a `**` segment matches zero or more segments, `*` and `?` stay in one
segment. Files in order, first matching row wins per path: config/check-map, then config/check-map.d/*.map sorted
(project fragments), then .devkit/check-map.d/*.map sorted (packs) (`--map` replaces that list). Base, the first that resolves: KIT_CHECK_BASE, HEAD's local upstream (branch.<b>.remote
is `.`), the local trunk (`--trunk`, else KIT_TRUNK in .devkit/config.env), the empty tree on an unborn HEAD. A trunk
answer that names a branch absent locally counts as unset (LR-5): local main, then master is tried, a found one is used
with a `warn:` naming the `trunk` answer (KIT_TRUNK) to set. Unset, or nothing found: strict refuses (exit 3, naming
`trunk`), else `not checked`, exit 0. Changed set: git diff <merge-base> (committed, staged, unstaged) plus untracked.
KIT_CHECK_HEAD (FB-882, with KIT_CHECK_BASE only; unset: HEAD and the tree, as above) names the commit the range ends at: the
merge-base is taken against it and the changed set is `git diff <merge-base> <KIT_CHECK_HEAD>`, no untracked paths. A caller
that checks one lane's own work after main moved on passes the lane's head; without KIT_CHECK_BASE it is refused (exit 3).

light: each selected target runs as `$KIT_MAKE <target>` with KIT_CHECK_PATHS (newline-separated paths that chose it),
in tier order (KIT-ONCE, R-196): light targets first, then unclassed ones, then heavy ones, each in map order, classed as
the queue shim classes goals (template/GNUmakefile; KIT_HEAVY_TARGETS, KIT_DOCKER_TARGETS and KIT_LIGHT_TARGETS from the environment, else
.devkit/config.env). Every target of a tier runs, so one run reports every red of it; a red tier stops every later tier
(`check-select: stopped before <tier> <names> [+ CHECK_EXTRA <goals after kit-select>]: <tier> red <targets>`, exit 1; FB-889), and within the heavy tier no target starts
after a red one. full: only when KIT_GOALS names check-full or check-docker; the target of every row that wins for
at least one tracked path (git ls-files) runs with KIT_CHECK_PATHS unset (empty means the whole tree), then every module's
`check` target once; no base, no diff.

Modules (config/modules.ini, read by scripts/kit/modules.py beside this file; `--map` replaces it, no registry then):
beside what the map chose, a changed path in a module adds that module's `check` targets (KIT_CHECK_PATHS: its own changed
paths), and so do its dependents' (KIT_CHECK_PATHS empty); a change in [shared], or an edit of the registry or its reader,
runs every module's. KIT_CHECK_MODULES names (sorted, space-separated) the modules that selected a target, never set for a
map-only target (an ambient value is dropped); on the full tier it names every module whose `check` holds the target.
A path a module checks counts as mapped. Registry absent or empty: nothing is added. A bad registry, or the file with its
reader absent, exits 2. One line `check-select: modules <changed> + dependents <names>|none` precedes the last line.
No row winning (an unborn or empty tree, LR-6) refuses: `full tier selected no map target`, exit 3. Strict (a
`check-full: KIT_TIER := full` line in Makefile or a project mk/*.mk, or --strict): a tracked or staged path no row maps
refuses; an untracked one no row maps is warned (one line naming it) and skipped, an untracked one a row maps still selects.
Not strict: it is listed, with a warn, and a full request runs the light selection (never a full run there).

Coverage floors (scripts/kit/cover-gate.py, skipped when absent): once per run, before the targets, the base is exported to
them as KIT_COVER_BASE (dropped when there is none) and `config` checks config/coverage-*.yaml and its .d/*.yaml fragments in process; its red joins the
exit. The full tier resolves a base for this alone; with none it warns and skips what needs one.

One run per tree (KIT-ONCE, R-196): a green run is a verdict kept outside the tree, one JSON line in
<git common dir>/kit/verdicts.jsonl (last 200 lines, appended under flock on verdicts.jsonl.lock): v, tree (HEAD^{tree}),
dirty (the work tree was not clean at start or at end), tier (the tier that ran), targets [{target, seconds, tier, exit}]
(each target that started, in run order), exit, finished. After selection and every refusal (and the coverage floors),
the newest line that satisfies the ask wins: same tree, dirty false, exit 0, under 7 days old, every asked target in its
targets with exit 0, and tier full for a full ask. A hit runs no target and appends nothing; it prints
`check-select: reuse <tier> verdict of <finished> for tree <key12>` and the summary says `ran nothing (reused)`. A miss
prints `check-select: no reuse: <reason>`, the reason one of no-verdict, other-tree, dirty, red-last, stale, other-targets
(a line of this tree did not run every asked target, or a light line was asked for full), no-measure (a full ask, and a stack
with a floors file has no whole-tree measure of this tree in this checkout: the run takes it, FB-711). KIT_REUSE=0 forces a run. A
selection that selects nothing runs and records nothing; outside a git work tree, or on an unborn HEAD, no verdict is read
or written.

Budgets, quarantine, flakes (KIT-WATCH, R-196): the verdict line also carries, per target, `sel` (first 12 hex of the
sha256 of its sorted KIT_CHECK_PATHS, `all` on the full tier) and `quarantined`; per line `swap` {start, end, max} in MB
(null when unreadable), `quiet`, `over_budget` [{unit, seconds, budget, flag}], `flaked`. A target (and a tier, as
`tier:<light|unclassed|heavy>`, by the sum of its targets when it has two or more, judged after the tier) is judged against its own green seconds
in the quiet lines of the history with the same (target, sel) key, by tests/run.py's rule without a run factor: `grown`
warns; a flagged unit fails a quiet run (`over budget`, exit 1; a red of its tier) and only warns in a loud one. Quiet
(LR-1): swap in use, sampled at start, after each target and at end, never above KIT_QUIET_SWAP_MB (env, then
.devkit/config.env, else 11650); macOS `sysctl -n vm.swapusage`, Linux /proc/meminfo; unreadable is loud. Also (D-2,
FB-620) the 1-minute load average, read at the same points with os.getloadavg(), never above KIT_QUIET_LOAD (env, then
.devkit/config.env, else 1 x CPUs, FB-775); no load reading is judged by swap alone. The over-budget line ends
`quiet run, load max <x>` or `loud: warn only, load max <x>` (the highest load read; no part when none was). The optional
config/kit-watch.ini (never seeded) has `[budget] <unit> = <seconds> <ticket> <YYYY-MM-DD>` (a raise: the cap until
the date) and `[quarantine] <target> = <ticket> <YYYY-MM-DD>` (the target still runs once; its red sets no exit, stops
no tier and is never reused). It is checked before reuse: a past or more than 14-day date, no ticket or an unnamed unit
is exit 1, invalid ini or an unknown section exit 2. A flake is a target red in the newest earlier clean line of this
tree and green in this clean run (`check-select: flake <target> (red at <finished>)`); nothing is ever rerun.

Light-run telemetry (FB-586): a bare `make check` (the goals are exactly `check`, no HEAVY_RUN_SLOT, so never inside a
hold, never check-full or check-docker; make's pid arrives as KIT_MAKE_PID, from `$$PPID`) appends one JSON line to
light-runs.jsonl, beside the heavy-runs file: the directory of HEAVY_TELEMETRY when set, else ~/.devkit/telemetry.
Row: v, project, label, cwd, session (DEVKIT_SESSION, else null), start, end (ISO UTC), run_s (make's own age at the end,
whole seconds from `ps -o etime=`: lint and every CHECK_EXTRA item included), exit, cmd, targets [{target, seconds}].
Directory 0700, file 0600, no rotation, nothing queued. One row per run, written at the end of make check:
  - a green selector leaves its timings in <tmpdir>/kit-light-<make pid>.json; the recorder, `check-select.py --record
    --make-pid N --exit C --goals "<goals>"` (the last step of the check chain: the leak gate's recipe in a render,
    devkit's own `check` recipe), merges them (a state file older than make's start is ignored; none leaves `targets`
    []), writes the row, removes the file, prints nothing and always exits 0;
  - a red selector (a target, or the coverage config) writes the row itself, exit 1: make stops right after it.
A red prerequisite that is no selector, or a refusal, leaves no row. A failure to write is one `warn:` line on stderr and
never changes the exit code or stdout.

Adoption (ADOPT, R-190): `python3 scripts/kit/check-select.py --adopted` runs no target and selects nothing. It reads the
make database (`make -pRrq :` in the repo root, under KIT_MAKE when set), classes each target as above, and exits 1, one line
`check-select: not adopted <target>` each, for a heavy target that no map row and no module check names; exit 0 when none.
Not counted, by what they are and never by a name prefix: the gate targets (check, check-full, check-docker), every target
a `^<name>:` rule line defines in a mk/*.mk whose first line carries `managed by devkit`, and every name in CHECK_EXTRA (from
the database). A heavy suite under a name the classes do not call heavy is unclassed and unseen: name it in
KIT_HEAVY_TARGETS first. See standards/testing.md, `## Adoption`.

Exit: 0 ok, 1 a target red (or, with --adopted, a heavy target unmapped), 2 usage or a malformed row, 3 refusal (unmapped
path, git failure, no base when strict, full tier by alias or with no winning row).
Last line: `check-select: <tier> · base <name>@<sha7> · <n> changed · ran <targets>|nothing selected`.
"""
from __future__ import annotations

import argparse
import configparser
import datetime
import fnmatch
import hashlib
import importlib.util
import json
import os
import re
import shlex
import statistics
import subprocess
import sys
import tempfile
import time
from pathlib import Path

try:
    import fcntl
except ImportError:  # no flock on this platform: no verdict is kept
    fcntl = None

HAND_STEP_VERSION = "unreleased"
TAG = "check-select:"
CAP = 10
FULL_GOALS = ("check-full", "check-docker")
REGISTRY_FILES = ("config/modules.ini", "scripts/kit/modules.py")
REFUSE_FULL = "KIT_TIER=full is set by check-full and check-docker only; run one of them as the make goal"
HEX = re.compile(r"[0-9a-f]{7,64}\Z")

# --- vendored from lib/devkit/checks/makefile_wired.py: edit there ---
TIER_LINE = re.compile(r"^check-full\s*:\s*(?:(?:export|override|private)\s+)*KIT_TIER\s*(?:::=|:=|=)\s*full\s*$")
COND_OPEN = re.compile(r"^(?:ifeq|ifneq|ifdef|ifndef)\b")
COND_END = re.compile(r"^endif\b")


def logical_lines(text: str) -> list[str]:
    """The Makefile's lines with `\\` continuations joined and `#` comments dropped."""
    joined = re.sub(r"\\\n", " ", text)
    return [line.split("#", 1)[0] for line in joined.splitlines()]


def tiers_line(text: str) -> bool:
    """True when a column-0 `check-full: KIT_TIER := full` line sits outside every conditional block (strict mode)."""
    depth = 0
    for line in logical_lines(text):
        head = line.lstrip(" ")
        if COND_OPEN.match(head):
            depth += 1
        elif COND_END.match(head):
            depth = max(0, depth - 1)
        elif depth == 0 and TIER_LINE.match(line):
            return True
    return False
# --- end vendored ---

# --- vendored from template/GNUmakefile: edit both ---
CLASS_LIGHT = ("check", "lint", "fmt", "vet")
CLASS_HEAVY = ("check-%", "%-check", "test", "test-%", "e2e%", "%-gate", "build", "build-%", "%-build",
               "image", "image-%", "%-image")
CLASS_DOCKER = ("check-docker",)
CLASS_DEV = ("dev-up", "dev-down", "dev-status", "run", "serve")
# --- end vendored (GNUmakefile) ---
TIER_ORDER = ("light", "unclassed", "heavy")
VERDICT_LINES = 200
VERDICT_DAYS = 7

# --- vendored from tests/run.py: edit both (no run factor here: a consumer runs a handful of targets, so it is 1) ---
WATCH_RUNS = 10  # samples of a unit: its last this many green seconds
WATCH_MIN = 5  # fewest samples a unit needs to be judged
WATCH_MARGIN = 1.75  # over: seconds / median above this
WATCH_EXCESS_S = 10.0  # and at least this many seconds above the median
WATCH_JUMP = 2.0  # a unit this many times its median (and WATCH_EXCESS_S over) is flagged on the first run
WATCH_FLOOR_S = 2.0  # a median or a current time under this is not judged
WATCH_MAX_S = 86400.0  # a usable time is at most a day
RAISE_DAYS = 14  # a raise or quarantine date is at most this many days after today
# --- end vendored (run.py) ---
QUIET_SWAP_MB = 11650  # vendored from tests/ops/check_gate_time.py: edit both (LR-1: swap used in MB)
QUIET_LOAD = 1 * (os.cpu_count() or 1)  # vendored from tests/ops/check_gate_time.py: edit both (D-2: 1-min load average; FB-775: 1 x CPUs)
WATCH_INI = ("config", "kit-watch.ini")
SWAP_TIMEOUT = 5.0  # seconds one `sysctl` read may take before it counts as unreadable
SYSCTL = "sysctl"  # the seams of the swap reader: tests point these at fixtures
MEMINFO = Path("/proc/meminfo")
CLOCK = time.monotonic  # the seam of a target's seconds: tests inject a clock
SWAP_USED = re.compile(r"\bused\s*=\s*([0-9]+(?:\.[0-9]+)?)\s*([KMGT]?)", re.IGNORECASE)
SWAP_UNIT_MB = {"": 1.0, "K": 1 / 1024, "M": 1.0, "G": 1024.0, "T": 1024.0 * 1024.0}


class Stop(Exception):
    """End the run: print `lines` (stderr) and exit `code`."""

    def __init__(self, code: int, *lines: str) -> None:
        super().__init__(lines[0] if lines else "")
        self.code = code
        self.lines = lines


class NoBase(Stop):
    """No diff base and no trunk answer: a refusal when strict, a `not checked` when not (LR-1)."""


def say(line: str, err: bool = False) -> None:
    stream = sys.stderr if err else sys.stdout
    stream.write(line + "\n")
    stream.flush()


# ---- git -------------------------------------------------------------------------------------------------------

def run_git(*args: str, stdin: bytes = b"") -> subprocess.CompletedProcess:
    try:
        return subprocess.run(["git", *args], capture_output=True, input=stdin)
    except OSError as exc:
        raise Stop(3, f"{TAG} git {' '.join(args)}: {exc}")


def first_line(raw: bytes, rc: int) -> str:
    lines = raw.decode("utf-8", "replace").strip().splitlines()
    return lines[0] if lines else f"exit {rc}"


def git_out(*args: str, hint: str = "") -> str:
    """git's stdout, stripped; a failure is a refusal naming git (exit 3), never an empty answer."""
    proc = run_git(*args)
    if proc.returncode != 0:
        lines = [f"{TAG} git {' '.join(args)}: {first_line(proc.stderr, proc.returncode)}"]
        if hint:
            lines.append(f"next: {hint}")
        raise Stop(3, *lines)
    return proc.stdout.decode("utf-8", "replace").strip()


def git_try(*args: str) -> str | None:
    """git's stdout, stripped, or None on any failure (a probe, not a requirement)."""
    proc = run_git(*args)
    if proc.returncode != 0:
        return None
    return proc.stdout.decode("utf-8", "replace").strip()


def nul_list(*args: str) -> list[str]:
    proc = run_git(*args)
    if proc.returncode != 0:
        raise Stop(3, f"{TAG} git {' '.join(args)}: {first_line(proc.stderr, proc.returncode)}")
    return [os.fsdecode(item) for item in proc.stdout.split(b"\0") if item]


# ---- the map ---------------------------------------------------------------------------------------------------

class Row:
    def __init__(self, where: str, glob: str, targets: list[str]) -> None:
        self.where = where
        self.glob = glob
        self.targets = targets  # empty = `-`
        self.segs = [None if seg == "**" else seg_regex(seg) for seg in glob.split("/") if seg]


def seg_regex(seg: str):
    out = []
    for ch in seg:
        out.append("[^/]*" if ch == "*" else "[^/]" if ch == "?" else re.escape(ch))
    return re.compile("".join(out))


def glob_match(segs: list, path: str) -> bool:
    """Anchored match of a compiled glob (None = `**`) against a repo-relative path split on `/`."""
    parts = [p for p in path.split("/") if p]
    count = len(parts)
    here = {0}
    for seg in segs:
        nxt = set()
        for pos in here:
            if seg is None:
                nxt.update(range(pos, count + 1))
            elif pos < count and seg.fullmatch(parts[pos]):
                nxt.add(pos + 1)
        here = nxt
        if not here:
            return False
    return count in here


def rel(path: Path, root: Path) -> str:
    try:
        return path.relative_to(root).as_posix()
    except ValueError:
        return str(path)


def read_map(path: Path, root: Path) -> list[Row]:
    name = rel(path, root)
    try:
        text = path.read_text(encoding="utf-8")
    except UnicodeDecodeError as exc:
        raise Stop(2, f"{TAG} {name}: not utf-8: {exc.reason}")
    rows = []
    for number, raw in enumerate(text.splitlines(), 1):
        line = raw.split("#", 1)[0].strip()
        if not line or line.startswith("@"):
            continue
        where = f"{name}:{number}"
        fields = line.split()
        if len(fields) < 2:
            raise Stop(2, f"{TAG} {where}: malformed row {line!r}: want `<glob> <target>...` or `<glob> -`")
        glob, targets = fields[0], fields[1:]
        if "-" in targets and targets != ["-"]:
            raise Stop(2, f"{TAG} {where}: malformed row {line!r}: `-` stands alone")
        bad = [t for t in targets if t != "-" and (t.startswith("-") or "=" in t)]
        if bad:
            raise Stop(2, f"{TAG} {where}: malformed row {line!r}: {bad[0]!r} is not a make target")
        rows.append(Row(where, glob, [] if targets == ["-"] else targets))
    return rows


def load_rows(root: Path, explicit: list[Path]) -> list[Row]:
    if explicit:
        files = []
        for path in explicit:
            if not path.is_file():
                raise Stop(2, f"{TAG} --map {path}: no such file")
            files.append(path)
    else:
        files = [p for p in [root / "config" / "check-map"] if p.is_file()]
        files += sorted((root / "config" / "check-map.d").glob("*.map"))
        files += sorted((root / ".devkit" / "check-map.d").glob("*.map"))
    rows: list[Row] = []
    for path in files:
        rows += read_map(path, root)
    return rows


def first_row(rows: list[Row], path: str) -> Row | None:
    for row in rows:
        if glob_match(row.segs, path):
            return row
    return None


# ---- strictness ------------------------------------------------------------------------------------------------

def project_has_tiers(root: Path) -> bool:
    files = [root / "Makefile"] + [p for p in sorted((root / "mk").glob("*.mk")) if not p.name.startswith("kit-")]
    for path in files:
        try:
            text = path.read_text(encoding="utf-8")
        except (OSError, UnicodeDecodeError):
            continue
        if tiers_line(text):
            return True
    return False


# ---- base and changed set --------------------------------------------------------------------------------------

def config_value(root: Path, key: str) -> str:
    try:
        text = (root / ".devkit" / "config.env").read_text(encoding="utf-8")
    except (OSError, UnicodeDecodeError):
        return ""
    value = ""
    for raw in text.splitlines():
        line = raw.strip()
        if line.startswith("export "):
            line = line[len("export "):].lstrip()
        if line.startswith("#") or "=" not in line:
            continue
        name, _, val = line.partition("=")
        if name.strip() == key:
            value = val.split(" #", 1)[0].strip().strip("'\"")
    return value


def resolve(ref: str) -> str | None:
    return git_try("rev-parse", "--verify", "--quiet", ref + "^{commit}")


def check_head() -> str | None:
    """FB-882: KIT_CHECK_HEAD resolved to a commit sha, None when unset; one that names no commit stops the run."""
    given = os.environ.get("KIT_CHECK_HEAD", "").strip()
    if not given:
        return None
    sha = resolve(given)
    if sha is None:
        raise Stop(3, f"{TAG} git rev-parse --verify {given}^{{commit}}: KIT_CHECK_HEAD names no commit",
                   "next: unset KIT_CHECK_HEAD or name a commit that exists in this clone")
    return sha


def base_of(root: Path, trunk_flag: str) -> tuple[str, str]:
    """(name, diff base object). The diff base is a merge-base commit, or the empty tree."""
    given = os.environ.get("KIT_CHECK_BASE", "").strip()
    if given and resolve(given) is None:
        raise Stop(3, f"{TAG} git rev-parse --verify {given}^{{commit}}: KIT_CHECK_BASE names no commit",
                   "next: unset KIT_CHECK_BASE or name a commit that exists in this clone")
    end = check_head()
    if end and not given:
        raise Stop(3, f"{TAG} KIT_CHECK_HEAD needs KIT_CHECK_BASE", "next: set KIT_CHECK_BASE, or unset KIT_CHECK_HEAD")
    head = resolve("HEAD")
    if head is None:  # unborn HEAD: every path of the tree is new
        return "empty-tree", empty_tree()
    if given:
        return ("base" if HEX.match(given) else given), merge_base(given, end or "HEAD")
    branch = git_try("symbolic-ref", "--quiet", "--short", "HEAD")  # None: a detached HEAD skips the upstream step
    if branch:
        remote = git_try("config", "--get", f"branch.{branch}.remote")
        merge = git_try("config", "--get", f"branch.{branch}.merge")
        if remote == "." and merge:
            name = merge[len("refs/heads/"):] if merge.startswith("refs/heads/") else merge
            if name != branch and resolve(merge) is not None:
                return name, merge_base(merge)
    trunk = trunk_flag or config_value(root, "KIT_TRUNK")
    absent = ""
    if trunk:
        ref = f"refs/heads/{trunk}"
        if resolve(ref) is not None:
            return on_trunk(branch, trunk, ref)
        absent = trunk  # an answer naming a branch this clone lacks counts as unset (LR-5)
    for guess in ("main", "master"):  # no usable answer: the repo's own trunk is main or master (LR-1)
        if resolve(f"refs/heads/{guess}") is not None:
            if absent:
                say(f"warn: the trunk answer (KIT_TRUNK) names {absent}, which is no local branch; using {guess}. "
                    f"Set the answer `trunk` (KIT_TRUNK in .devkit/config.env) to {guess}", err=True)
            return on_trunk(branch, guess, f"refs/heads/{guess}")
    why = (f"the trunk answer (KIT_TRUNK) names {absent}, which is no local branch, and there is no local main or master"
           if absent else "no local branch main or master and no trunk answer")
    raise NoBase(3, f"{TAG} no diff base: {why}",
                 "next: set the answer `trunk` (KIT_TRUNK in .devkit/config.env) to your integration branch")


def on_trunk(branch: str | None, trunk: str, ref: str) -> tuple[str, str]:
    """The trunk base. On the trunk itself the merge-base is HEAD and the diff is empty, so select against HEAD's
    first parent (the last merge or commit) and name it (FB-434); a root commit keeps the empty diff."""
    if branch == trunk:
        parent = resolve("HEAD^1")
        if parent is not None:
            return f"{trunk}^1", parent
    return trunk, merge_base(ref)


def empty_tree() -> str:
    proc = run_git("hash-object", "-t", "tree", "--stdin")
    if proc.returncode != 0:
        raise Stop(3, f"{TAG} git hash-object -t tree --stdin: {first_line(proc.stderr, proc.returncode)}")
    return proc.stdout.decode().strip()


def merge_base(ref: str, end: str = "HEAD") -> str:
    proc = run_git("merge-base", end, ref)
    if proc.returncode != 0 or not proc.stdout.strip():
        shallow = git_try("rev-parse", "--is-shallow-repository") == "true"
        reason = first_line(proc.stderr, proc.returncode) if proc.stderr.strip() else "no common ancestor"
        raise Stop(3, f"{TAG} git merge-base HEAD {ref}: {reason}",
                   "next: git fetch --unshallow" if shallow else f"next: HEAD shares no history with {ref}; name a base")
    return proc.stdout.decode().strip()


def changed_set(diff_base: str) -> tuple[list[str], set[str], set[str]]:
    """(changed paths, the untracked ones among them, the deleted ones): the diff is tracked and staged paths, ls-files -o
    the rest. Deleted is git's word (gone from the index against the base, the old side of a rename too), never
    path.exists: after `git rm --cached` the file is still on disk (FB-656). FB-882: with KIT_CHECK_HEAD the range is
    `diff_base` to that commit and the tree is not read: no untracked paths."""
    end = [e] if (e := check_head()) else []
    paths = nul_list("diff", "--name-only", "-z", "--no-renames", diff_base, *end, "--")
    deleted = set(nul_list("diff", "--name-only", "-z", "--no-renames", "--diff-filter=D", diff_base, *end, "--"))
    untracked = set() if end else set(nul_list("ls-files", "-o", "--exclude-standard", "-z"))
    return sorted(set(paths) | untracked), untracked, deleted - untracked


# ---- budgets, quarantine, flakes (KIT-WATCH) -------------------------------------------------------------------

def parse_swapusage(text: str) -> float | None:
    """MB used from a macOS `vm.swapusage` line (`total = 14336.00M  used = 13375.88M  free = 960.12M  (encrypted)`)."""
    found = SWAP_USED.search(text)
    return round(float(found.group(1)) * SWAP_UNIT_MB[found.group(2).upper()], 2) if found else None


def parse_meminfo(text: str) -> float | None:
    """MB of swap in use from a Linux /proc/meminfo: SwapTotal - SwapFree (kB)."""
    kb = {}
    for line in text.splitlines():
        found = re.match(r"(SwapTotal|SwapFree):\s*([0-9]+)\s*kB", line)
        if found:
            kb[found.group(1)] = int(found.group(2))
    if "SwapTotal" not in kb or "SwapFree" not in kb:
        return None
    return round(max(0, kb["SwapTotal"] - kb["SwapFree"]) / 1024, 2)


def swap_sysctl() -> float | None:
    try:
        proc = subprocess.run([SYSCTL, "-n", "vm.swapusage"], stdin=subprocess.DEVNULL, capture_output=True, text=True,
                              timeout=SWAP_TIMEOUT)
    except Exception:  # a swap read never fails the run: any failure is an unreadable sample
        return None
    return parse_swapusage(proc.stdout) if proc.returncode == 0 else None


def swap_meminfo() -> float | None:
    try:
        return parse_meminfo(MEMINFO.read_text(encoding="utf-8"))
    except (OSError, UnicodeDecodeError):
        return None


def read_swap() -> float | None:
    """Swap in use in MB, or None where neither macOS nor Linux answers (the host then counts as loud)."""
    readers = (swap_sysctl, swap_meminfo) if sys.platform == "darwin" else (swap_meminfo, swap_sysctl)
    for reader in readers:
        value = reader()
        if value is not None:
            return value
    return None


def quiet_bound(root: Path, key: str, default: float, unit: str) -> float:
    """`key`: the environment first, then .devkit/config.env, else `default`; anything but a number >= 0 stops the run."""
    raw = os.environ.get(key, "").strip() or config_value(root, key)
    if not raw:
        return float(default)
    try:
        value = float(raw)
    except ValueError:
        value = -1.0
    if not 0 <= value < float("inf"):
        raise Stop(2, f"{TAG} {key}={raw}: expected a number of {unit}")
    return value


def quiet_limit(root: Path) -> float:
    """KIT_QUIET_SWAP_MB: the environment first, then .devkit/config.env, else the vendored default."""
    return quiet_bound(root, "KIT_QUIET_SWAP_MB", QUIET_SWAP_MB, "MB")


def quiet_load(root: Path) -> float:
    """KIT_QUIET_LOAD (D-2): the environment first, then .devkit/config.env, else 1 x the CPU count (FB-775; was 2 x)."""
    return quiet_bound(root, "KIT_QUIET_LOAD", QUIET_LOAD, "load")


def read_load() -> float | None:
    """The 1-minute load average, or None where the host has none (then the run is judged by swap alone)."""
    try:
        return round(os.getloadavg()[0], 2)
    except (OSError, AttributeError):
        return None


def number(value: object) -> float | None:
    """A usable seconds value of the history (an int or float, no bool, in 0..WATCH_MAX_S), else None."""
    if type(value) in (int, float) and 0 <= value <= WATCH_MAX_S:
        return float(value)
    return None


def sel_of(paths: list[str]) -> str:
    """The selection a target ran on: the first 12 hex of the sha256 of its sorted KIT_CHECK_PATHS."""
    return hashlib.sha256("\n".join(sorted(set(paths))).encode("utf-8")).hexdigest()[:12]


def evaluate(prior: list[float], seconds: float) -> tuple[float, bool] | None:
    """(median, over) of `seconds` against the last WATCH_RUNS of `prior`, or None when it is not judged."""
    prior = prior[-WATCH_RUNS:]
    if len(prior) < WATCH_MIN:
        return None
    median = statistics.median(prior)
    if median < WATCH_FLOOR_S or seconds < WATCH_FLOOR_S:
        return None
    return median, seconds > median * WATCH_MARGIN and seconds - median >= WATCH_EXCESS_S


def judge(series: list[float], seconds: float) -> tuple[float, bool, str | None] | None:
    """(median, over, flag) for a unit with its earlier green quiet `series`, or None when it is not judged. The flag is
    `jump` (WATCH_JUMP times the median, on the first run), `repeat` (over now and at its previous evaluation) or None
    (over, not flagged: a warning)."""
    now = evaluate(series, seconds)
    if now is None:
        return None
    median, over = now
    if not over:
        return median, False, None
    if seconds >= WATCH_JUMP * median:
        return median, True, "jump"
    for at in range(len(series) - 1, -1, -1):  # the latest earlier evaluation that judged the unit
        before = evaluate(series[:at], series[at])
        if before is not None:
            return median, True, "repeat" if before[1] else None
    return median, True, None


def shown(value: float) -> str:
    return f"{round(value, 1):g}"


class Ini:
    """config/kit-watch.ini: `raises` {unit: (seconds, ticket, date)}, `quarantine` {target: (ticket, date)}."""

    def __init__(self) -> None:
        self.raises: dict[str, tuple[float, str, datetime.date]] = {}
        self.quarantine: dict[str, tuple[str, datetime.date]] = {}


def read_ini(root: Path, known: set[str], today: datetime.date) -> Ini:
    """Read the optional ini (absent: empty). A file that is not valid ini or has another section is exit 2; a bad entry
    (past, no ticket, more than RAISE_DAYS ahead, a unit nobody names) is exit 1, naming each."""
    out = Ini()
    path = root.joinpath(*WATCH_INI)
    name = "/".join(WATCH_INI)
    try:
        text = path.read_text(encoding="utf-8")
    except FileNotFoundError:
        return out
    except (OSError, UnicodeDecodeError) as exc:
        raise Stop(2, f"{TAG} {name}: unreadable ({exc})")
    parser = configparser.ConfigParser(delimiters=("=",), comment_prefixes=("#", ";"), interpolation=None)
    parser.optionxform = str  # a key is a target name: keep its case
    try:
        parser.read_string(text, source=name)
    except configparser.Error as exc:
        raise Stop(2, f"{TAG} {name}: {str(exc).splitlines()[0]}")
    for section in [*(["DEFAULT"] if parser.defaults() else []), *parser.sections()]:
        if section not in ("budget", "quarantine"):
            raise Stop(2, f"{TAG} {name}: [{section}] is not a section (budget, quarantine)")
    errors = []
    for section in parser.sections():
        for unit, value in parser[section].items():
            words = (value or "").split()
            entry = f"{TAG} {name}: [{section}] {unit} = {value or ''}"
            want = 3 if section == "budget" else 2
            seconds = None
            if section == "budget" and words:
                try:
                    seconds = float(words[0])
                except ValueError:
                    seconds = None
            middle = words[1:-1] if section == "budget" else words[:-1]  # between the seconds and the date
            ticket = middle[0] if middle else ""
            try:
                until = datetime.date.fromisoformat(words[-1]) if words else None
            except ValueError:
                until = None
            if section == "budget" and unit.startswith("tier:"):
                named = unit[len("tier:"):] in TIER_ORDER
            else:
                named = unit in known
            why = None
            if not ticket:
                why = "no ticket"
            elif len(words) != want or until is None or (section == "budget" and not (seconds is not None and seconds > 0)):
                why = "not `<seconds> <ticket> <YYYY-MM-DD>`" if section == "budget" else "not `<ticket> <YYYY-MM-DD>`"
            elif until < today:
                why = f"{until} is past"
            elif until > today + datetime.timedelta(days=RAISE_DAYS):
                why = f"{until} is more than {RAISE_DAYS} days after {today}"
            elif not named:
                why = f"{unit} is no target of any map row or module check" if not unit.startswith("tier:") \
                    else f"{unit} is not a tier (light, unclassed, heavy)"
            if why is not None:
                errors.append(f"{entry}: {why}")
            elif section == "budget":
                out.raises[unit] = (seconds, ticket, until)
            else:
                out.quarantine[unit] = (ticket, until)
    if errors:
        raise Stop(1, *errors)
    return out


def known_targets(rows: list[Row], root: Path, explicit: list[Path]) -> set[str]:
    """Every target a map row or a module check names (the units an ini entry may name)."""
    names = {target for row in rows for target in row.targets}
    reg = load_registry(root, explicit, [])
    for module in reg.modules.values() if reg is not None else []:
        names.update(module.check)
    return names


class Watch:
    """One run's budgets (R-196), quiet reading (LR-1) and flake record. `history` is the verdict lines before this run;
    `samples` the swap in MB at start, after each target and at end."""

    def __init__(self, root: Path, verdicts: "Verdicts", ini: Ini) -> None:
        self.root = root
        self.verdicts = verdicts
        self.ini = ini
        self.history: list[dict] = []
        self.limit = float(QUIET_SWAP_MB)
        self.load_limit = float(QUIET_LOAD)
        self.samples: list[float | None] = []
        self.loads: list[float | None] = []
        self.over: list[dict] = []

    def begin(self) -> None:
        self.limit = quiet_limit(self.root)
        self.load_limit = quiet_load(self.root)
        self.history = self.verdicts.lines() if self.verdicts.tree is not None else []
        self.sample()

    def sample(self) -> None:
        self.samples.append(read_swap())
        self.loads.append(read_load())

    @property
    def quiet(self) -> bool:
        """LR-1: every swap sample was read and none is above the limit; no sample, or an unreadable one, is loud. D-2: no
        load sample is above the load limit (an unreadable load is no sample: the run is judged by swap alone)."""
        return bool(self.samples) and all(value is not None and value <= self.limit for value in self.samples) \
            and all(value is None or value <= self.load_limit for value in self.loads)

    def quiet_lines(self) -> list[dict]:
        return [row for row in self.history if row.get("quiet") is True and isinstance(row.get("targets"), list)]

    def target_series(self, target: str, sel: str) -> list[float]:
        """Green seconds of (target, sel) in the quiet lines, oldest first."""
        out = []
        for row in self.quiet_lines():
            for item in row["targets"]:
                if isinstance(item, dict) and item.get("target") == target and item.get("sel") == sel \
                        and item.get("exit") == 0 and number(item.get("seconds")) is not None:
                    out.append(number(item["seconds"]))
        return out

    def tier_series(self, klass: str, key: list[tuple[str, str]]) -> list[float]:
        """Summed seconds of the tier `klass` with the same (target, sel) set, in quiet lines where all of it was green."""
        out = []
        for row in self.quiet_lines():
            items = [item for item in row["targets"] if isinstance(item, dict) and item.get("tier") == klass]
            if not items or sorted((str(i.get("target")), str(i.get("sel"))) for i in items) != key:
                continue
            seconds = [number(item.get("seconds")) for item in items]
            if all(item.get("exit") == 0 for item in items) and None not in seconds:
                out.append(sum(seconds))
        return out

    def unit(self, unit: str, seconds: float, series: list[float]) -> bool:
        """Judge `unit` (a target name or `tier:<class>`): print the grown or over-budget line, record it in `over`.
        True when it fails the run (flagged in a quiet run)."""
        cap = self.ini.raises.get(unit)
        result = judge(series, seconds)
        if cap is not None:  # a raise replaces the history rule: at or under its cap the unit is fine
            if seconds <= cap[0]:
                return False
            flag, budget = "raise", cap[0]
        elif result is not None and result[1]:
            flag, budget = result[2], max(result[0] * WATCH_MARGIN, result[0] + WATCH_EXCESS_S)
        else:
            return False
        self.over.append({"unit": unit, "seconds": round(seconds, 1), "budget": round(budget, 1), "flag": flag})
        median = f"median {shown(result[0])} s" if result is not None else "no median"
        if flag is None:
            say(f"{TAG} grown {unit} {shown(seconds)} s ({median})", err=True)
            return False
        loads = [value for value in self.loads if value is not None]
        peak = f", load max {round(max(loads), 2):.10g}" if loads else ""  # FB-775: what made the run quiet or loud
        if self.quiet:
            say(f"{TAG} over budget {unit} {shown(seconds)} s ({median}, {flag}; quiet run{peak})", err=True)
            return True
        say(f"{TAG} over budget {unit} {shown(seconds)} s ({median}, {flag}) (loud: warn only{peak})", err=True)
        return False

    def target(self, target: str, sel: str, seconds: float) -> bool:
        return self.unit(target, seconds, self.target_series(target, sel))

    def tier(self, klass: str, entries: list[dict]) -> bool:
        """Judge a finished tier by the sum of its targets' seconds (key: the class and its sorted (target, sel) pairs). A
        tier of one target is that target's own judgement (and its raise), so it is not judged twice."""
        if len(entries) < 2 or any(item["exit"] != 0 for item in entries):
            return False
        key = sorted((item["target"], item["sel"]) for item in entries)
        return self.unit(f"tier:{klass}", sum(item["seconds"] for item in entries), self.tier_series(klass, key))

    def flakes(self, results: list[dict], dirty: bool) -> list[str]:
        """Targets red in the newest earlier clean line of this tree and green now, this run clean too (REQ-5)."""
        if dirty or self.verdicts.tree is None:
            return []
        before = [row for row in self.history if row.get("tree") == self.verdicts.tree and row.get("dirty") is False
                  and isinstance(row.get("targets"), list)]
        if not before:
            return []
        previous = before[-1]
        red = {item.get("target") for item in previous["targets"]
               if isinstance(item, dict) and isinstance(item.get("exit"), int) and item["exit"] != 0}
        flaked = [item["target"] for item in results if item["exit"] == 0 and item["target"] in red]
        for target in flaked:
            say(f"{TAG} flake {target} (red at {previous.get('finished')})")
        return flaked

    def finish(self, results: list[dict], dirty: bool) -> dict:
        """The fields this phase adds to the run's line."""
        readable = bool(self.samples) and all(value is not None for value in self.samples)
        swap = {"start": self.samples[0], "end": self.samples[-1], "max": max(self.samples)} if readable else None
        return {"swap": swap, "quiet": self.quiet, "over_budget": self.over, "flaked": self.flakes(results, dirty)}


# ---- running ---------------------------------------------------------------------------------------------------

def unrun_extras(root: Path) -> list[str]:
    """FB-889: the CHECK_EXTRA goals after kit-select, in make's order: a red selector stops make, so these never start. The
    words come from make's database (parse_database's); a database that cannot be read, or no kit-select in it, names none."""
    try:
        text = make_database(root)
    except Stop:
        return []
    for line in text.splitlines():
        if line.startswith("# Files"):
            break
        found = CHECK_EXTRA_LINE.match(line)
        if found:
            words = [word for word in found.group(1).split() if "$" not in word]
            return words[words.index("kit-select") + 1:] if "kit-select" in words else []
    return []


def stop_line(parts: list[str], klass: str, tier_red: list[str]) -> None:
    """The one stop line (`stopped before <what>: <tier> red <targets>`): `parts` are the later tiers' goals; FB-889: the
    CHECK_EXTRA goals that will not run follow as `CHECK_EXTRA <names>`, so a partial red cannot read as the whole verdict."""
    extras = unrun_extras(Path.cwd())
    if extras:
        parts = [*parts, f"CHECK_EXTRA {' '.join(extras)}"]
    if parts:
        say(f"{TAG} stopped before {' + '.join(parts)}: {klass} red {' '.join(tier_red)}", err=True)


def run_targets(order: list[str], paths_of: dict[str, list[str]], tier: str,
                timings: list[dict] | None = None,
                selectors: dict[str, set[str]] | None = None,
                results: list[dict] | None = None,
                watch: Watch | None = None) -> tuple[list[str], list[str]]:
    """Run the targets in tier order (light, unclassed, heavy; a red tier stops the later ones, and a red heavy target
    stops the heavy ones after it); returns (ran, red). `timings` collects {target, seconds} per target run, `results`
    {target, seconds, tier, exit, sel[, quarantined]} (the verdict's targets); `selectors` maps a target to the modules
    that selected it (KIT_CHECK_MODULES; an ambient value never leaks in). With `watch` a target over its budget, or a
    tier over its own, is a red of its tier when the run is quiet, and a quarantined target's red is not (KIT-WATCH);
    each target runs once."""
    make = shlex.split(os.environ.get("KIT_MAKE", "") or "make")
    ran, red = [], []
    classes = class_lists(Path.cwd())
    groups: dict[str, list[str]] = {name: [] for name in TIER_ORDER}
    for target in order:
        groups[target_class(target, *classes)].append(target)
    done: dict[str, list[dict]] = {name: [] for name in TIER_ORDER}
    if watch is not None and order:
        watch.begin()

    def run_one(target: str, klass: str) -> int:
        env = dict(os.environ)
        env.pop("KIT_CHECK_MODULES", None)
        if tier == "full":
            env.pop("KIT_CHECK_PATHS", None)
        else:
            env["KIT_CHECK_PATHS"] = "\n".join(sorted(set(paths_of[target])))
        if selectors and selectors.get(target):
            env["KIT_CHECK_MODULES"] = " ".join(sorted(selectors[target]))
        sys.stdout.flush()
        began = CLOCK()
        try:
            code = subprocess.call([*make, target], env=env)
        except OSError as exc:
            raise Stop(2, f"{TAG} cannot run {make[0]}: {exc}")
        seconds = round(CLOCK() - began, 3)
        if timings is not None:
            timings.append({"target": target, "seconds": seconds})
        sel = "all" if tier == "full" else sel_of(paths_of[target])
        entry = {"target": target, "seconds": seconds, "tier": klass, "exit": code, "sel": sel}
        quarantine = watch.ini.quarantine.get(target) if watch is not None else None
        if quarantine is not None:
            entry["quarantined"] = True
        if results is not None:
            results.append(entry)
        done[klass].append(entry)
        ran.append(target)
        if watch is not None:
            watch.sample()
        if code != 0:
            if quarantine is not None:  # still run and recorded, but its red sets no exit and stops no tier
                say(f"{TAG} quarantined red {target} ({quarantine[0]}, until {quarantine[1].isoformat()})", err=True)
                return 0
            red.append(target)
            say(f"{TAG} red {target} (exit {code})", err=True)
            return code
        if watch is not None and watch.target(target, sel, seconds):
            red.append(target)
            return 1
        return 0

    for at, klass in enumerate(TIER_ORDER):
        tier_red: list[str] = []
        names = groups[klass]
        for position, target in enumerate(names):
            if run_one(target, klass) != 0:
                tier_red.append(target)
                if klass == "heavy":  # one heavy target at a time: nothing starts after a red one
                    rest = names[position + 1:]
                    stop_line([f"heavy {' '.join(rest)}"] if rest else [], "heavy", tier_red)
                    break
        if watch is not None and not tier_red and watch.tier(klass, done[klass]):  # judged after the tier
            tier_red.append(f"tier:{klass}")
            red.append(f"tier:{klass}")
        if tier_red and klass != "heavy":
            later = [(name, groups[name]) for name in TIER_ORDER[at + 1:] if groups[name]]
            skipped = [target for _, names_ in later for target in names_]
            stop_line([f"{later[0][0]} {' '.join(skipped)}"] if later else [], klass, tier_red)
            break
    if watch is not None and order:
        watch.sample()
    return ran, red


def csv_names(root: Path, key: str) -> list[str]:
    """A comma-separated name list the shim reads: the environment first, else .devkit/config.env."""
    raw = os.environ.get(key, "") or config_value(root, key)
    return [name for name in raw.replace(",", " ").split() if name]


def class_lists(root: Path) -> tuple[list[str], list[str], list[str]]:
    """(KIT_HEAVY_TARGETS, KIT_DOCKER_TARGETS, KIT_LIGHT_TARGETS) entries; each entry is a name or a `%` pattern."""
    return (csv_names(root, "KIT_HEAVY_TARGETS"), csv_names(root, "KIT_DOCKER_TARGETS"),
            csv_names(root, "KIT_LIGHT_TARGETS"))


def class_match(name: str, entries: tuple[str, ...] | list[str]) -> bool:
    """Make's `filter` rule: an entry is a name, or a pattern where `%` is the one wildcard."""
    return any(name == entry or ("%" in entry and fnmatch.fnmatchcase(name, entry.replace("%", "*")))
               for entry in entries)


def target_class(name: str, heavy_names: list[str], docker_names: list[str],
                 light_names: list[str] | None = None) -> str:
    """light, heavy or unclassed, as the queue shim classes a goal (template/GNUmakefile): light is the closed list minus
    KIT_HEAVY_TARGETS; the dev names are unclassed; heavy is a heavy pattern or an entry of the two lists; a goal that
    matches a KIT_LIGHT_TARGETS entry and no heavy or docker entry is light (FB-619). Entries of all three lists are patterns."""
    if name in CLASS_LIGHT and not class_match(name, heavy_names):
        return "light"
    if name in CLASS_DEV:
        return "unclassed"
    heavy_listed = class_match(name, heavy_names) or class_match(name, docker_names) or name in CLASS_DOCKER
    if light_names and class_match(name, light_names) and not heavy_listed:
        return "light"
    if heavy_listed or class_match(name, CLASS_HEAVY):
        return "heavy"
    return "unclassed"


# ---- one run per tree ------------------------------------------------------------------------------------------

def utc_now() -> datetime.datetime:
    return datetime.datetime.now(datetime.timezone.utc)


class Verdicts:
    """The verdict file of this repo (`<git common dir>/kit/verdicts.jsonl`) and the tree key of this run. `tree` is None
    where there is nothing to key on (outside a work tree, an unborn HEAD, no flock): then nothing is read or written."""

    def __init__(self) -> None:
        self.tree: str | None = None
        self.path: Path | None = None
        self.dirty_start = True
        if fcntl is None or git_try("rev-parse", "--is-inside-work-tree") != "true":
            return
        tree = git_try("rev-parse", "HEAD^{tree}")
        common = git_try("rev-parse", "--path-format=absolute", "--git-common-dir")
        if common is None:  # git before 2.31: the relative answer, read from the cwd (the repo root)
            common = git_try("rev-parse", "--git-common-dir")
            common = str(Path(common).resolve()) if common else None
        if not tree or not common:
            return
        self.tree = tree
        self.path = Path(common) / "kit" / "verdicts.jsonl"
        self.dirty_start = self.dirty()

    @staticmethod
    def dirty() -> bool:
        """True when `git status --porcelain` lists anything (or cannot be read)."""
        out = git_try("status", "--porcelain")
        return out is None or bool(out)

    def lines(self) -> list[dict]:
        """The parsed lines, oldest first; an absent or unreadable file, or a bad line, is skipped."""
        try:
            text = self.path.read_text(encoding="utf-8")
        except (OSError, UnicodeDecodeError):
            return []
        rows = []
        for raw in text.splitlines():
            try:
                row = json.loads(raw)
            except ValueError:
                continue
            if isinstance(row, dict):
                rows.append(row)
        return rows

    def find(self, tier: str, order: list[str]) -> tuple[dict | None, str]:
        """(the newest line that satisfies the ask, "") or (None, the miss reason)."""
        if self.dirty_start:
            return None, "dirty"
        rows = self.lines()
        if not rows:
            return None, "no-verdict"
        mine = [row for row in rows if row.get("tree") == self.tree]
        if not mine:
            return None, "other-tree"
        clean = [row for row in mine if row.get("dirty") is False]
        if not clean:
            return None, "dirty"
        for row in reversed(clean):  # newest first, and past a line that does not satisfy
            if row.get("exit") == 0 and self.fresh(row) and self.covers(row, tier, order):
                return row, ""
        newest = clean[-1]
        if newest.get("exit") != 0:
            return None, "red-last"
        if not self.fresh(newest):
            return None, "stale"
        return None, "other-targets"

    @staticmethod
    def fresh(row: dict) -> bool:
        try:
            finished = datetime.datetime.fromisoformat(str(row.get("finished")))
        except ValueError:
            return False
        if finished.tzinfo is None:
            finished = finished.replace(tzinfo=datetime.timezone.utc)
        return utc_now() - finished < datetime.timedelta(days=VERDICT_DAYS)

    @staticmethod
    def covers(row: dict, tier: str, order: list[str]) -> bool:
        if tier == "full" and row.get("tier") != "full":
            return False
        done = row.get("targets")
        green = {item.get("target") for item in done if isinstance(item, dict) and item.get("exit") == 0} \
            if isinstance(done, list) else set()
        return all(target in green for target in order)

    def append(self, tier: str, results: list[dict], code: int, watch: "Watch | None" = None) -> None:
        """Add this run's line (tree dirty at start or now = dirty), under flock, keeping the last VERDICT_LINES. With
        `watch` the line also carries swap, quiet, over_budget and flaked (KIT-WATCH; readers ignore unknown fields). A
        failure is one `warn:` line and never changes the exit code."""
        if self.tree is None:
            return
        dirty = self.dirty_start or self.dirty()
        row = {"v": 1, "tree": self.tree, "dirty": dirty, "tier": tier, "targets": results,
               "exit": code, "finished": utc_now().isoformat(timespec="seconds")}
        if watch is not None:
            row.update(watch.finish(results, dirty))
        try:
            directory = self.path.parent
            directory.mkdir(parents=True, exist_ok=True)
            with open(directory / "verdicts.jsonl.lock", "a") as lock:
                fcntl.flock(lock, fcntl.LOCK_EX)
                try:
                    try:
                        kept = self.path.read_text(encoding="utf-8").splitlines()
                    except (OSError, UnicodeDecodeError):
                        kept = []
                    kept = [*kept, json.dumps(row, separators=(",", ":"))][-VERDICT_LINES:]
                    scratch = directory / f"verdicts.jsonl.{os.getpid()}.tmp"
                    scratch.write_text("\n".join(kept) + "\n", encoding="utf-8")
                    os.replace(scratch, self.path)  # readers take no lock: they see the old file or the new one
                finally:
                    fcntl.flock(lock, fcntl.LOCK_UN)
        except OSError as exc:
            say(f"warn: verdict not written: {exc}", err=True)


def reuse_or_run(verdicts: Verdicts, tier: str, order: list[str], cover_red: int, run,
                 watch: "Watch | None" = None) -> tuple[list[str], list[str], bool]:
    """Decide reuse for the ask (`tier`, `order`), after selection, every refusal and the floors check (`cover_red`, which
    joins the recorded exit). A hit runs nothing and appends nothing. Otherwise `run(results)` runs the targets and the
    line is appended. Returns (ran, red, reused)."""
    if not order:  # a selection that selects nothing runs nothing and records nothing
        ran, red = run(None)
        return ran, red, False
    if verdicts.tree is not None:
        if os.environ.get("KIT_REUSE", "").strip() == "0":
            hit, why = None, "KIT_REUSE=0"
        else:
            hit, why = verdicts.find(tier, order)
            if hit is not None and tier == "full" and unmeasured(verdicts.tree):
                hit, why = None, "no-measure"  # FB-711: only a run takes the measure `coverage-raise` reads
        if hit is not None:
            say(f"{TAG} reuse {hit.get('tier')} verdict of {hit.get('finished')} for tree {verdicts.tree[:12]}")
            return [], [], True
        say(f"{TAG} no reuse: {why}")
    results: list[dict] = []
    ran, red = run(results)
    verdicts.append(tier, results, 1 if red or cover_red else 0, watch)
    return ran, red, False


def make_pid() -> str:
    """make's pid as the recipe handed it over (KIT_MAKE_PID), or "" when absent or not a number."""
    pid = os.environ.get("KIT_MAKE_PID", "").strip()
    return pid if pid.isdigit() else ""


def bare_check(goals: str, pid: str) -> bool:
    """True for a bare `make check` outside any hold that make started: the goals are exactly `check` (never check-full,
    check-docker or a goal beside it), HEAVY_RUN_SLOT is unset (a run in a hold is timed already), make's pid is known."""
    return goals.split() == ["check"] and not os.environ.get("HEAVY_RUN_SLOT") and bool(pid)


def etime_seconds(text: str) -> int:
    """Seconds of a ps etime, `[[dd-]hh:]mm:ss`."""
    days, _, rest = text.strip().rpartition("-")
    parts = [int(part) for part in rest.split(":")]
    if not 1 <= len(parts) <= 3:
        raise ValueError(f"unreadable etime {text.strip()!r}")
    seconds = 0
    for part in parts:
        seconds = seconds * 60 + part
    return seconds + (int(days) * 86400 if days else 0)


def make_age(pid: str) -> int:
    """make's wall time so far in whole seconds (ps etime: one-second resolution)."""
    proc = subprocess.run(["ps", "-o", "etime=", "-p", pid], capture_output=True, text=True)
    if proc.returncode != 0 or not proc.stdout.strip():
        raise RuntimeError(f"make pid {pid} is not in the process table")
    return etime_seconds(proc.stdout)


def telemetry_dir() -> Path:
    """Where light-runs.jsonl lives: the directory of HEAVY_TELEMETRY, else ~/.devkit/telemetry."""
    given = os.environ.get("HEAVY_TELEMETRY", "").strip()
    if given:
        return Path(given).expanduser().parent
    return Path.home() / ".devkit" / "telemetry"


def append_row(directory: Path, row: dict) -> None:
    created = not directory.exists()
    directory.mkdir(mode=0o700, parents=True, exist_ok=True)
    if created:
        os.chmod(directory, 0o700)
    fd = os.open(directory / "light-runs.jsonl", os.O_WRONLY | os.O_APPEND | os.O_CREAT, 0o600)
    try:
        os.write(fd, (json.dumps(row, separators=(",", ":")) + "\n").encode("utf-8"))
    finally:
        os.close(fd)


def state_path(pid: str) -> Path:
    return Path(tempfile.gettempdir()) / f"kit-light-{pid}.json"


def write_state(pid: str, timings: list[dict]) -> None:
    """The green selector's timings for the recorder, in a file keyed by make's pid. A failure is silent: the row then
    carries no targets."""
    path = state_path(pid)
    try:
        try:
            path.unlink()
        except FileNotFoundError:
            pass
        fd = os.open(str(path), os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, "w", encoding="utf-8") as handle:
            json.dump({"v": 1, "targets": timings}, handle)
    except OSError:
        pass


def take_state(pid: str, age: int) -> list[dict]:
    """The selector's timings of this run, and the file is removed; [] when it is absent, unreadable, or older than
    make's start (a run that stopped between the selector and the recorder left it)."""
    path = state_path(pid)
    try:
        fresh = path.stat().st_mtime >= time.time() - age - 1
        data = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, ValueError):
        return []
    finally:
        try:
            path.unlink()
        except OSError:
            pass
    targets = data.get("targets") if isinstance(data, dict) else None
    return targets if fresh and isinstance(targets, list) else []


def write_row(root: Path, pid: str, code: int, timings: list[dict]) -> None:
    """One row (FB-586): run_s is make's age now, `start` is `end` minus it."""
    age = make_age(pid)
    ended = datetime.datetime.now(datetime.timezone.utc)
    row = {
        "v": 1,
        "project": os.environ.get("KIT_PROJECT", "").strip() or config_value(root, "KIT_PROJECT"),
        "label": "check",
        "cwd": str(root),
        "session": os.environ.get("DEVKIT_SESSION") or None,
        "start": (ended - datetime.timedelta(seconds=age)).isoformat(timespec="seconds"),
        "end": ended.isoformat(timespec="seconds"),
        "run_s": age,
        "exit": code,
        "cmd": "make check",
        "targets": timings,
    }
    append_row(telemetry_dir(), row)


def selector_done(root: Path, code: int, timings: list[dict]) -> None:
    """The selector's part of the light-run row (FB-586). Red: make stops right after this target, so the row is written
    here. Green: the timings wait in a state file for the recorder. A failure is one `warn:` line on stderr and never
    changes the exit code or stdout."""
    pid = make_pid()
    if not bare_check(os.environ.get("KIT_GOALS", ""), pid):
        return
    if code == 0:
        write_state(pid, timings)
        return
    try:
        write_row(root, pid, code, timings)
    except Exception as exc:  # telemetry never changes a gate
        say(f"warn: light-run telemetry not written: {exc}", err=True)


def record(argv: list[str]) -> int:
    """`--record --make-pid N --exit C --goals "<goals>"`: the last step of `make check` writes the row. Nothing goes to
    stdout and the return is always 0: a failure is the one `warn:` line on stderr."""
    try:
        opts = {"--make-pid": "", "--exit": "0", "--goals": ""}
        words = iter(argv)
        for word in words:
            if word in opts:
                opts[word] = next(words, "")
        pid = opts["--make-pid"].strip()
        if not pid.isdigit() or not bare_check(opts["--goals"], pid):
            return 0
        top = git_try("rev-parse", "--show-toplevel")
        root = Path(top or os.getcwd()).resolve()
        timings = take_state(pid, make_age(pid))
        write_row(root, pid, int(opts["--exit"]), timings)
    except Exception as exc:  # telemetry never changes a gate (a Stop from git_try is one too)
        say(f"warn: light-run telemetry not written: {exc}", err=True)
    return 0


def cover_module():
    """The cover-gate.py module beside this script, loaded afresh; None when the project has no such script."""
    path = Path(__file__).resolve().parent / "cover-gate.py"
    if not path.is_file():
        return None
    sys.dont_write_bytecode = True
    spec = importlib.util.spec_from_file_location("kit_cover_gate", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def unmeasured(tree: str) -> list[str]:
    """The stacks with a floors file and no current whole-tree measure of `tree` (cover-gate.py `unmeasured`); none where
    the script is absent, older, or unreadable: reuse then stays as it was."""
    try:
        module = cover_module()
        return module.unmeasured(tree) if module is not None and hasattr(module, "unmeasured") else []
    except Exception:
        return []


def cover_config(diff_base: str | None) -> int:
    """The floors files (scripts/kit/cover-gate.py `config`), checked in process against the base this run resolved once.
    KIT_COVER_BASE carries that base to the targets, and is dropped when there is none, so a stale export never wins (LR-1).
    The script absent (a project without it): skipped. Returns 0, or 1 when a floors file is refused."""
    if diff_base:
        os.environ["KIT_COVER_BASE"] = diff_base
    else:
        os.environ.pop("KIT_COVER_BASE", None)
    try:
        module = cover_module()
        if module is None:
            return 0
        code = module.run_config(diff_base or None)
    except Exception as exc:  # a broken script is red, never a silent pass
        say(f"{TAG} cover-gate.py: {exc}", err=True)
        return 1
    if code:
        say(f"{TAG} red coverage-config (exit {code})", err=True)
    return code


def listed(prefix: str, paths: list[str]) -> None:
    for path in paths[:CAP]:
        say(f"{TAG} {prefix} {path}", err=prefix == "unmapped")
    if len(paths) > CAP:
        say(f"{TAG} +{len(paths) - CAP} more", err=prefix == "unmapped")


def summary(tier: str, base: str, changed: str, ran: list[str], reused: bool = False) -> None:
    tail = f"ran {' '.join(ran)}" if ran else "ran nothing (reused)" if reused else "nothing selected"
    say(f"{TAG} {tier} · base {base} · {changed} changed · {tail}")


def load_registry(root: Path, explicit: list[Path], extra: list[str]):
    """The module registry (scripts/kit/modules.py beside this file, read by path), or None when there is none to read:
    `--map` replaces every source, config/modules.ini absent, or a registry with no module. A present file with the reader
    absent is a partial update (exit 2); a RegistryError is exit 2 with its line."""
    if explicit or not (root / "config" / "modules.ini").is_file():
        return None
    path = Path(__file__).resolve().parent / "modules.py"
    if not path.is_file():
        raise Stop(2, f"{TAG} config/modules.ini needs scripts/kit/modules.py; next: devkit update --apply")
    sys.dont_write_bytecode = True
    spec = importlib.util.spec_from_file_location("kit_modules", path)
    reader = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(reader)
    try:
        reg = reader.load(root, extra=extra)
    except reader.RegistryError as exc:
        raise Stop(2, f"{TAG} {exc}")
    return reg if reg.modules else None


def select_modules(reg, changed: list[str], order: list[str], paths_of: dict[str, list[str]],
                   selectors: dict[str, set[str]]) -> tuple[list[str], list[str], set[str]]:
    """Add the `check` targets of the changed modules (with their own changed paths) and then of their dependents (no
    paths) to `order`, each target once. Returns (changed modules, dependents not changed, paths a module checks)."""
    def add(name: str, paths: list[str]) -> None:
        for target in reg.modules[name].check:
            if target not in paths_of:
                paths_of[target] = []
                order.append(target)
            selectors.setdefault(target, set()).add(name)
            paths_of[target] += paths

    names: list[str] = []
    checked: set[str] = set()
    whole = any(path in REGISTRY_FILES for path in changed)  # the edit changes what every selection means
    for path in changed:
        name = reg.of(path)
        if name is None:
            continue
        if name not in names:
            names.append(name)
        if name in reg.modules and reg.modules[name].check:
            checked.add(path)
            add(name, [] if whole else [path])  # a registry edit: every module's whole tree, even a changed one
    found = list(reg.dependents(names))
    if whole:
        found += [n for n in sorted(reg.modules) if n != "shared" and n not in found]
    dependents = [n for n in found if n not in names]
    for name in dependents:
        add(name, [])
    return names, dependents, checked


def goals_name_full() -> bool:
    return any(word in FULL_GOALS for word in os.environ.get("KIT_GOALS", "").split())


def execute(args: argparse.Namespace) -> int:
    tier = os.environ.get("KIT_TIER", "").strip() or "light"
    if tier not in ("light", "full"):
        raise Stop(2, f"{TAG} KIT_TIER={tier}: expected light or full")
    if tier == "full" and not goals_name_full():
        raise Stop(3, f"{TAG} {REFUSE_FULL}")
    explicit = [Path(m).resolve() for m in args.map]
    top = git_try("rev-parse", "--show-toplevel")
    if top is None:
        if tier != "full":
            git_out("rev-parse", "--show-toplevel")  # raises the refusal with git's own words
        top = os.getcwd()
    root = Path(top).resolve()
    os.chdir(str(root))
    strict = args.strict or project_has_tiers(root)
    rows = load_rows(root, explicit)

    if tier == "full" and strict:
        # the light selection over the whole tree (LR-4): a row runs when it wins for at least one tracked path, so a
        # pack row every path of which a project row shadows never runs; outside a work tree every row runs
        tracked = nul_list("ls-files", "-z") if git_try("rev-parse", "--show-toplevel") else None
        winning = {id(first_row(rows, path)) for path in tracked} if tracked is not None else None
        order: list[str] = []
        for row in rows:
            if winning is not None and id(row) not in winning:
                continue
            order += [t for t in row.targets if t not in order]
        if not any(winning is None or id(row) in winning for row in rows):
            raise Stop(3, f"{TAG} full tier selected no map target",
                       "next: add a row to config/check-map that a tracked path matches (an unborn or empty tree has none)")
        selectors: dict[str, set[str]] = {}
        reg = load_registry(root, explicit, [])
        for name in sorted(reg.modules) if reg is not None else []:  # full runs everything: every module's check, once
            for target in reg.modules[name].check:
                if target not in order:
                    order.append(target)
                selectors.setdefault(target, set()).add(name)
        try:  # the base serves the floors check and the targets alone; a full run diffs nothing
            cover_base: str | None = base_of(root, args.trunk)[1]
        except Stop:
            cover_base = None  # what needs a base is warned about and skipped
        ini = read_ini(root, known_targets(rows, root, explicit), utc_now().date())  # before reuse is decided
        verdicts = Verdicts()  # the tree key and its dirty bit, before anything runs
        watch = Watch(root, verdicts, ini)
        cover_red = cover_config(cover_base)
        ran, red, reused = reuse_or_run(
            verdicts, "full", order, cover_red,
            lambda results: run_targets(order, {}, "full", selectors=selectors, results=results, watch=watch), watch)
        summary("full", "-", "all", ran, reused)
        return 1 if red or cover_red else 0

    # light, or a full request on a project without tier lines (never a full run there)
    try:
        name, diff_base = base_of(root, args.trunk)
    except NoBase:
        if strict:
            raise
        say(f"{TAG} not checked: no diff base (set the trunk answer)")
        say(f"warn: no gate tiers yet; CHANGELOG {HAND_STEP_VERSION} hand step")
        summary("light", "-", "?", [])
        return 0
    sha7 = diff_base[:7]
    changed, untracked, deleted = changed_set(diff_base)
    paths_of: dict[str, list[str]] = {}
    order = []
    unmapped = []
    for path in changed:
        row = first_row(rows, path)
        if row is None:
            unmapped.append(path)
            continue
        for target in row.targets:
            if target not in paths_of:
                paths_of[target] = []
                order.append(target)
            paths_of[target].append(path)
    selectors = {}
    changed_modules: list[str] = []
    dependents: list[str] = []
    reg = load_registry(root, explicit, changed)
    if reg is not None:
        changed_modules, dependents, checked = select_modules(reg, changed, order, paths_of, selectors)
        unmapped = [p for p in unmapped if p not in checked]  # a path a module checks is mapped, strict or not
    if strict:
        # the gate is a tracked or staged path no row maps; an untracked one (a stray .bak) or a deleted one (nothing
        # left to check, FB-656) is warned and skipped
        for kind, gone in (("untracked", untracked), ("deleted", deleted)):
            stray = [p for p in unmapped if p in gone]
            unmapped = [p for p in unmapped if p not in gone]
            for path in stray[:CAP]:
                say(f"warn: {kind} path {path} matches no map row; skipped", err=True)
            if len(stray) > CAP:
                say(f"warn: +{len(stray) - CAP} more {kind} paths matched no map row; skipped", err=True)
        if unmapped:
            listed("unmapped", unmapped)
            raise Stop(3, "next: add a row to config/check-map")
    ini = read_ini(root, known_targets(rows, root, explicit), utc_now().date())  # before reuse is decided
    verdicts = Verdicts()  # the tree key and its dirty bit, before anything runs
    watch = Watch(root, verdicts, ini)
    cover_red = cover_config(diff_base)
    timings: list[dict] = []
    ran, red, reused = reuse_or_run(
        verdicts, "light", order, cover_red,
        lambda results: run_targets(order, paths_of, "light", timings, selectors, results, watch=watch), watch)
    code = 1 if red or cover_red else 0
    selector_done(root, code, timings)
    if not strict:
        listed("not checked", unmapped)
        say(f"warn: no gate tiers yet; CHANGELOG {HAND_STEP_VERSION} hand step")
    if changed_modules:
        say(f"{TAG} modules {' '.join(sorted(changed_modules))} + dependents {' '.join(dependents) or 'none'}")
    summary("light", f"{name}@{sha7}", str(len(changed)), ran, reused)
    return code


# ---- adoption (ADOPT) -------------------------------------------------------------------------------------------

GATE_TARGETS = ("check", "check-full", "check-docker")  # the targets that run check-select: nothing to map
MANAGED_HEADER = "managed by devkit"
RULE_NAME = re.compile(r"^([A-Za-z0-9_][A-Za-z0-9_.+-]*)[ \t]*:(?![=:])")
CHECK_EXTRA_LINE = re.compile(r"^CHECK_EXTRA[ \t]*:{0,2}=[ \t]*(.*)$")


def make_database(root: Path) -> str:
    """`make -pRrq :` in `root` (a fresh top-level make; nothing runs): GNU make's database on stdout."""
    make = shlex.split(os.environ.get("KIT_MAKE", "") or "make")
    env = {k: v for k, v in os.environ.items() if k not in ("MAKEFLAGS", "MAKELEVEL", "MFLAGS")}
    try:
        proc = subprocess.run([*make, "-pRrq", ":"], cwd=str(root), capture_output=True, text=True, errors="replace",
                              stdin=subprocess.DEVNULL, env=env)
    except OSError as exc:
        raise Stop(2, f"{TAG} cannot run {make[0]}: {exc}")
    if "\n# Files" not in proc.stdout:
        raise Stop(2, f"{TAG} {' '.join(make)} -pRrq : printed no database: {first_line(proc.stderr.encode(), proc.returncode)}")
    return proc.stdout


def parse_database(text: str) -> tuple[list[str], set[str]]:
    """(the targets of the database's Files section, the words of CHECK_EXTRA). A name under `# Not a target:` is no target;
    a name with `%`, `/` or a leading `.` never matches the rule-name form."""
    targets: list[str] = []
    extra: set[str] = set()
    in_files = skip = False
    for line in text.splitlines():
        if line.startswith("# Files"):
            in_files = True
            continue
        if not in_files:
            found = CHECK_EXTRA_LINE.match(line)
            if found:
                extra = {word for word in found.group(1).split() if "$" not in word}
            continue
        if line.startswith("# files hash-table stats"):
            break
        if not line.strip():
            skip = False
        elif line.startswith("# Not a target"):
            skip = True
        elif not line.startswith(("#", "\t")) and not skip:
            found = RULE_NAME.match(line)
            if found and found.group(1) not in targets:
                targets.append(found.group(1))
    return targets, extra


def managed_rule_names(root: Path) -> set[str]:
    """Every name a `^<name>:` rule line defines in a mk/*.mk whose first line carries the managed header."""
    names: set[str] = set()
    for path in sorted((root / "mk").glob("*.mk")):
        try:
            lines = path.read_text(encoding="utf-8").splitlines()
        except (OSError, UnicodeDecodeError):
            continue
        if not lines or MANAGED_HEADER not in lines[0]:
            continue
        for line in lines:
            found = RULE_NAME.match(line)
            if found:
                names.add(found.group(1))
    return names


def adopted() -> int:
    """`--adopted`: 1, naming each, for a heavy make target that no map row and no module check names; else 0."""
    try:
        top = git_try("rev-parse", "--show-toplevel")
    except Stop:
        top = None
    root = Path(top or os.getcwd()).resolve()
    os.chdir(str(root))
    targets, extra = parse_database(make_database(root))
    named = {target for row in load_rows(root, []) for target in row.targets}
    reg = load_registry(root, [], [])
    for module in reg.modules.values() if reg is not None else []:
        named.update(module.check)
    skipped = set(GATE_TARGETS) | managed_rule_names(root) | extra | named
    classes = class_lists(root)
    open_ = [t for t in targets if t not in skipped and target_class(t, *classes) == "heavy"]
    for target in open_:
        say(f"{TAG} not adopted {target}", err=True)
    if open_:
        say("next: map each in config/check-map, or name it in a module check (standards/testing.md#adoption)", err=True)
        return 1
    say(f"{TAG} adopted: every heavy target is mapped")
    return 0


def main(argv: list[str]) -> int:
    if "--record" in argv:
        return record(argv)
    parser = argparse.ArgumentParser(prog="check-select.py", description="Run the gate targets the changed paths select.")
    parser.add_argument("--map", action="append", default=[], metavar="FILE", help="a map file (repeatable; replaces the list)")
    parser.add_argument("--trunk", default="", metavar="BRANCH", help="the local trunk branch (default KIT_TRUNK; absent or unset: local main, else master)")
    parser.add_argument("--strict", action="store_true", help="strict without reading the Makefile")
    parser.add_argument("--adopted", action="store_true", help="exit 1 naming each heavy make target no map row or module check names")
    args = parser.parse_args(argv)
    try:
        return adopted() if args.adopted else execute(args)
    except Stop as stop:
        for line in stop.lines:
            say(line, err=True)
        return stop.code


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
