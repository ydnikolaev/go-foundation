#!/usr/bin/env python3
# managed by devkit — do not edit; fix upstream in ~/Developer/projects/devkit
"""kit-select: `python3 scripts/kit/check-select.py [--map FILE]... [--strict] [--only TARGET...]`, one content-keyed plan.

Map (first match wins): config/check-map, config/check-map.d/*.map, .devkit/check-map.d/*.map; a module check
(config/modules.ini) adds its deps, [shared] and the registry files to its inputs; `@every <goal>...` names goals whose inputs
are the whole tree, `@units <target> <adapter>` a target scripts/kit/check-units.py tiers unit by unit. A target runs iff
`green(digest(name, inputs), tier)` has no answer; rows go to <git common dir>/kit/runs.jsonl, every worktree's store. The digest
holds the Go test tags (FB-1217); a full ask reuses no `@units` target whose stack's measure is not of this tree (FB-1212). The tier
is derived (check-units.py: needs, declared, price): light leaves slow targets and what does not fit 180 s of summed price for
check-full, with docker unreachable; sweep is full with the store ignored; `--only` runs the named targets as check-full does,
skips one that rows name but an earlier row hides (one `skipped` line names the pack and that row) and refuses one no row names.
Clocks KIT_LIGHT_S (900) and KIT_FULL_S (3600) are hang guards: they kill, go red and name the slowest three (a loaded host
must not trip them).
Targets run at once up to make's jobserver (a client: one in kit-select's slot, a fifo token per further one), each printed
as one block when it ends; `n` in MAKEFLAGS runs nothing. The light and unclassed classes run every target past a red; the
heavy class starts only when they all ended green, one at a time, and stops at its first red; a red run ends on `red <n>:
<target> (exit <c>), ...`. SIGTERM or SIGINT kills every running target and exits 143 or 130. `--explain <path>...` prints each path's map line (or its module), targets
and `hit` or `runs`, runs nothing (exit 1 for a path with no row and no module check).
Exit: 0 green; 1 red; 2 usage or a malformed row; 3 refusal. Stdlib only, python3 >= 3.9, POSIX."""
from __future__ import annotations

import argparse
import datetime
import fcntl
import fnmatch
import hashlib
import importlib.util
import json
import os
import queue
import re
import select
import shlex
import signal
import subprocess
import sys
import tempfile
import threading
import time
from pathlib import Path

HAND_STEP_VERSION = "unreleased"
TAG = "check-select:"
CAP = 10
FULL_GOALS = ("check-full", "check-docker")
REGISTRY_FILES = ("config/modules.ini", "scripts/kit/modules.py")
REFUSE_FULL = "KIT_TIER=full is set by check-full and check-docker only; run one of them as the make goal"
STALE_DAYS = 7  # a green row older than this is not reused (core/verdict.py STALE_DAYS)
KEEP_DAYS = 14  # the first append of a day drops rows and logs older than this
GRACE = 7  # seconds between SIGTERM and SIGKILL of a killed target: core/run.py HOLD_GRACE (no devkit import)
CLOCKS = {"light": ("KIT_LIGHT_S", 900.0), "full": ("KIT_FULL_S", 3600.0), "sweep": ("KIT_FULL_S", 3600.0)}
TARGET_ENV = ("KIT_CHECK_PATHS", "KIT_CHECK_MODULES", "KIT_COVER_BASE", "UNITS_BUDGET_S")
CLASS_ORDER = ("light", "unclassed", "heavy")  # the run order of a tier; the tier itself is units.tier_of
TOOLS = "uv --version; uv run --no-project --no-python-downloads -q python --version; python3 --version; go version"

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
               "image", "image-%", "%-image", "sweep")
CLASS_DOCKER = ("check-docker",)
CLASS_DEV = ("dev-up", "dev-down", "dev-status", "run", "serve", "dev-%", "%-run", "serve-%", "run-%", "watch%")
# --- end vendored (GNUmakefile) ---

class Stop(Exception):
    """End the run: print `lines` (stderr) and exit `code`."""
    def __init__(self, code: int, *lines: str) -> None:
        super().__init__(lines[0] if lines else "")
        self.code, self.lines = code, lines

def say(line: str, err: bool = False) -> None:
    print(line, file=sys.stderr if err else sys.stdout, flush=True)

def read(path: Path) -> str:
    try:
        return path.read_text(encoding="utf-8", errors="replace")
    except OSError:
        return ""

def git_call(*args: str, stdin: bytes = b"", env: dict[str, str] | None = None) -> subprocess.CompletedProcess:
    return subprocess.run(["git", *args], capture_output=True, input=stdin, env=env)

GIT = git_call
CLOCK = time.monotonic

def run_git(*args: str, stdin: bytes = b"", env: dict[str, str] | None = None):
    try:
        return GIT(*args, stdin=stdin, env=env)
    except OSError as exc:
        raise Stop(3, f"{TAG} git {' '.join(args)}: {exc}")

def git_out(*args: str, stdin: bytes = b"") -> str:
    """git's stdout, stripped; a failure is a refusal naming git (exit 3), never an empty answer."""
    proc = run_git(*args, stdin=stdin)
    if proc.returncode != 0:
        lines = proc.stderr.decode("utf-8", "replace").strip().splitlines()
        raise Stop(3, f"{TAG} git {' '.join(args)}: {lines[0] if lines else f'exit {proc.returncode}'}")
    return proc.stdout.decode("utf-8", "replace").strip()

def git_try(*args: str) -> str | None:
    """git's stdout, stripped, or None on any failure (a probe, not a requirement)."""
    proc = run_git(*args)
    return proc.stdout.decode("utf-8", "replace").strip() if proc.returncode == 0 else None

def nul_list(*args: str) -> list[str]:
    proc = run_git(*args)
    if proc.returncode != 0:
        git_out(*args)  # raises the refusal with git's own words
    return [os.fsdecode(item) for item in proc.stdout.split(b"\0") if item]

class Row:
    def __init__(self, where: str, glob: str, targets: list[str]) -> None:
        self.where, self.glob, self.targets = where, glob, targets  # targets empty = `-`
        self.segs = [None if seg == "**" else seg_regex(seg) for seg in glob.split("/") if seg]

def seg_regex(seg: str):
    return re.compile("".join("[^/]*" if ch == "*" else "[^/]" if ch == "?" else re.escape(ch) for ch in seg))

def glob_match(segs: list, path: str) -> bool:
    """Anchored match of a compiled glob (None = `**`) against a repo-relative path split on `/`."""
    parts = [p for p in path.split("/") if p]
    here = {0}
    for seg in segs:
        nxt = set()
        for pos in here:
            if seg is None:
                nxt.update(range(pos, len(parts) + 1))
            elif pos < len(parts) and seg.fullmatch(parts[pos]):
                nxt.add(pos + 1)
        if not (here := nxt):
            return False
    return len(parts) in here

def read_map(path: Path, root: Path) -> list[Row]:
    name = path.relative_to(root).as_posix() if path.is_relative_to(root) else str(path)
    try:
        text = path.read_text(encoding="utf-8")
    except UnicodeDecodeError as exc:
        raise Stop(2, f"{TAG} {name}: not utf-8: {exc.reason}")
    rows = []
    for number, raw in enumerate(text.splitlines(), 1):
        line = raw.split("#", 1)[0].strip()
        if not line or line.startswith("@"):
            continue
        fields = line.split()
        flags = [t for t in fields[1:] if t != "-" and (t.startswith("-") or "=" in t)]
        bad = ("want `<glob> <target>...` or `<glob> -`" if len(fields) < 2 else
               "`-` stands alone" if "-" in fields[1:] and fields[1:] != ["-"] else
               f"{flags[0]!r} is not a make target" if flags else "")
        if bad:
            raise Stop(2, f"{TAG} {name}:{number}: malformed row {line!r}: {bad}")
        rows.append(Row(f"{name}:{number}", fields[0], [] if fields[1:] == ["-"] else fields[1:]))
    return rows

def map_files(root: Path, explicit: list[Path]) -> list[Path]:
    for path in explicit:
        if not path.is_file():
            raise Stop(2, f"{TAG} --map {path}: no such file")
    if explicit:
        return list(explicit)
    files = [p for p in [root / "config" / "check-map"] if p.is_file()]
    return files + sorted((root / "config" / "check-map.d").glob("*.map")) + sorted((root / ".devkit" / "check-map.d").glob("*.map"))

def load_rows(root: Path, explicit: list[Path]) -> list[Row]:
    return [row for path in map_files(root, explicit) for row in read_map(path, root)]

def first_row(rows: list[Row], path: str) -> Row | None:
    return next((row for row in rows if glob_match(row.segs, path)), None)

def project_has_tiers(root: Path) -> bool:
    files = [root / "Makefile"] + [p for p in sorted((root / "mk").glob("*.mk")) if not p.name.startswith("kit-")]
    return any(tiers_line(read(path)) for path in files)

def config_value(root: Path, key: str) -> str:
    """The last `[export ]KEY=value` line of .devkit/config.env, quotes and ` #` comment dropped."""
    value = ""
    for raw in read(root / ".devkit" / "config.env").splitlines():
        line = raw.strip()
        name, eq, val = (line[len("export "):].lstrip() if line.startswith("export ") else line).partition("=")
        if eq and not line.startswith("#") and name.strip() == key:
            value = val.split(" #", 1)[0].strip().strip("'\"")
    return value

def csv_names(root: Path, key: str) -> list[str]:
    """A comma-separated name list the shim reads: the environment first, else .devkit/config.env."""
    raw = os.environ.get(key, "") or config_value(root, key)
    return [name for name in raw.replace(",", " ").split() if name]

def class_lists(root: Path) -> tuple[list[str], list[str], list[str], list[str]]:
    """(KIT_HEAVY_TARGETS, KIT_DOCKER_TARGETS, KIT_LIGHT_TARGETS, KIT_DEV_TARGETS) entries: names or `%` patterns."""
    return tuple(csv_names(root, f"KIT_{key}_TARGETS") for key in ("HEAVY", "DOCKER", "LIGHT", "DEV"))

def class_match(name: str, entries) -> bool:
    """Make's `filter` rule: an entry is a name, or a pattern where `%` is the one wildcard."""
    return any(name == entry or ("%" in entry and fnmatch.fnmatchcase(name, entry.replace("%", "*"))) for entry in entries)

def target_class(name: str, heavy_names, docker_names, light_names=None, dev_names=None) -> str:
    """light, heavy or unclassed, as the queue shim classes a goal (template/GNUmakefile; FB-619, FB-1087)."""
    if name in CLASS_LIGHT and not class_match(name, heavy_names):
        return "light"
    heavy_listed = class_match(name, heavy_names) or class_match(name, docker_names) or name in CLASS_DOCKER
    if not heavy_listed and class_match(name, [*CLASS_DEV, *(dev_names or [])]):
        return "unclassed"
    if light_names and class_match(name, light_names) and not heavy_listed:
        return "light"
    return "heavy" if heavy_listed or class_match(name, CLASS_HEAVY) else "unclassed"

def target_tier(name: str, history: list[dict], classes) -> str:
    """unit or slow: needs (a docker target), then declared (KIT_HEAVY_TARGETS, KIT_LIGHT_TARGETS; `unit` and the light names always),
    then price; the name (the heavy class) decides only a target with no row yet."""
    heavy, docker, light, _dev = classes
    declared = "slow" if class_match(name, heavy) else "unit" if name in ("unit", *CLASS_LIGHT) or class_match(name, light) else None
    return units().tier_of(class_match(name, docker) or name in CLASS_DOCKER, declared, history,
                           target_class(name, *classes) == "heavy")

def load_beside(file: str, name: str):
    """A script beside this one, loaded afresh by path (no bytecode written); None when it is absent."""
    path = Path(__file__).resolve().parent / file
    if not path.is_file():
        return None
    sys.dont_write_bytecode = True
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module

_UNITS = None

def units():
    """check-units.py (the tier rule, the price, the cut) loaded by path once; it sits beside this script."""
    global _UNITS
    _UNITS = _UNITS or load_beside("check-units.py", "kit_units")
    if _UNITS is None:
        raise Stop(2, f"{TAG} scripts/kit/check-units.py is missing; next: devkit update --apply")
    return _UNITS

def load_registry(root: Path, explicit: list[Path], listed: list[str]):
    """The module registry over `listed`, or None (`--map` given, no config/modules.ini, no module); exit 2 when bad."""
    if explicit or not (root / "config" / "modules.ini").is_file():
        return None
    reader = load_beside("modules.py", "kit_modules")
    if reader is None:
        raise Stop(2, f"{TAG} config/modules.ini needs scripts/kit/modules.py; next: devkit update --apply")
    try:
        sections, compose = reader.parse(read(root / "config" / "modules.ini"))
        reg = reader.build(sections, sorted(listed) if sections or compose.rows else [], compose)
    except reader.RegistryError as exc:
        raise Stop(2, f"{TAG} {exc}")
    return reg if reg.modules else None

class Tree:
    """The work tree read once: `files` {path: index blob, or `git hash-object` when dirty or untracked}, sorted `paths`,
    `untracked`, `clean` (tracked files match the index), `common` (git common dir), the devkit `tag`, the `maps`, their text, `sets` and `every`."""
    def __init__(self, root: Path, maps: list[Path]) -> None:
        self.files = {}
        for item in nul_list("ls-files", "-s", "-z"):
            meta, _, path = item.partition("\t")
            self.files[path] = meta.split()[1]
        changed = set(nul_list("ls-files", "-z", "-m", "-o", "--exclude-standard"))  # dirty, deleted, conflicted, new
        self.untracked = changed - set(self.files)
        self.clean = not changed - self.untracked
        gone = {path for path in changed if not os.path.lexists(root / path)}
        self.files = {path: blob for path, blob in self.files.items() if path not in gone}
        todo = sorted(p for p in changed - gone if "\n" not in p and not (root / p).is_dir())
        if todo:
            self.files.update(zip(todo, git_out("hash-object", "--stdin-paths", stdin="\n".join(todo).encode()).split()))
        self.paths = sorted(self.files)
        self.common = Path(c) if (c := git_try("rev-parse", "--path-format=absolute", "--git-common-dir")) else None
        found = re.search(r"^_commit:\s*(\S+)", read(root / ".copier-answers.devkit.yml"), re.M)
        self.tag = found.group(1).strip("'\"") if found else ""
        self.maps, self.map_text = maps, "".join(read(path) for path in maps)
        self.sets, self.every = units().directives([read(path) for path in maps])  # `@units` targets, `@every` goals

    def under(self, paths) -> list[str]:
        """The files `paths` name: each a file, or a directory (every file under it; `.` or empty is the whole tree)."""
        names = {str(raw).strip().strip("/") for raw in paths}
        if names & {"", "."}:
            return self.paths
        dirs = tuple(name + "/" for name in names)
        return [path for path in self.paths if path in names or path.startswith(dirs)]

_TREE: Tree | None = None
_ROWS: list[dict] | None = None  # the store as read once by this process

def reset() -> None:
    """Forget the tree and the store read so far (a caller that changed either)."""
    global _TREE, _ROWS
    _TREE = _ROWS = None

def tree(root: Path | None = None, maps: list[Path] | None = None) -> Tree:
    global _TREE
    if _TREE is None:
        top = Path(root or git_out("rev-parse", "--show-toplevel")).resolve()
        _TREE = Tree(top, map_files(top, []) if maps is None else maps)
    return _TREE

def digest(name: str, paths) -> str:
    """sha256 of the name, devkit tag, toolchain (TOOLS' output: Tree.tools), map text, Go test tags, sorted (path, blob) pairs."""
    t, Tree.tools = tree(), getattr(Tree, "tools", "") or subprocess.run(["sh", "-c", TOOLS], capture_output=True).stdout
    tags = [os.environ.get(key, "") for key in ("KIT_GO_TEST_TAGS_LIGHT", "KIT_GO_TEST_TAGS_FULL")]  # FB-1217: make exports both
    sha = hashlib.sha256("\0".join((name, t.tag, Tree.tools.decode("utf-8", "replace"), t.map_text, *tags, "")).encode("utf-8"))
    for path in t.under(paths):
        sha.update(f"{path}\0{t.files[path]}\n".encode("utf-8"))
    return sha.hexdigest()

def utc_now() -> datetime.datetime:
    return datetime.datetime.now(datetime.timezone.utc)

def when(row) -> datetime.datetime | None:
    try:
        at = datetime.datetime.fromisoformat(str(row.get("ts")))
    except (ValueError, AttributeError):
        return None
    return at if at.tzinfo else at.replace(tzinfo=datetime.timezone.utc)

def store() -> Path | None:
    return tree().common / "kit" / "runs.jsonl" if tree().common else None

def parsed(text: str) -> list[dict]:
    """The JSON object lines of `text`; any other line is skipped."""
    out = []
    for raw in text.splitlines():
        try:
            out.append(json.loads(raw))
        except ValueError:
            continue
    return [row for row in out if isinstance(row, dict)]

def rows() -> list[dict]:
    """The store's rows, oldest first, as this process first read them (its own appends are not read back)."""
    global _ROWS
    _ROWS = _ROWS if _ROWS is not None else parsed(read(store())) if store() else []
    return _ROWS

def green(digest: str, tier: str = "light") -> dict | None:
    """The newest target row of `digest` that proves `tier`, when green and under STALE_DAYS old, else None: a light row
    answers a light ask only, a full row both, a row with no tier is light; a later red voids a green."""
    for row in reversed(rows()):
        if row.get("digest") != digest:
            continue
        if row.get("exit") != 0:
            return None
        if tier != "light" and row.get("tier") != "full":
            continue
        at = when(row)
        return row if at is not None and utc_now() - at < datetime.timedelta(days=STALE_DAYS) else None
    return None

def append(row: dict) -> None:
    """Append one row under the flock; the first append of a day prunes first. A failure is one `warn:` line."""
    path = store()
    if path is None:
        return
    try:
        path.parent.mkdir(parents=True, exist_ok=True)
        with open(path.parent / "runs.jsonl.lock", "a") as lock:
            fcntl.flock(lock, fcntl.LOCK_EX)  # released when the lock file closes
            prune(path)
            with open(path, "a", encoding="utf-8") as out:
                out.write(json.dumps(row, separators=(",", ":")) + "\n")
    except OSError as exc:
        say(f"warn: run row not written: {exc}", err=True)

def prune(path: Path) -> None:
    """When the store was last written before today (UTC): drop its rows and logs over KEEP_DAYS old."""
    now = utc_now()
    if not path.is_file() or datetime.datetime.fromtimestamp(path.stat().st_mtime, now.tzinfo).date() >= now.date():
        return
    cutoff = now - datetime.timedelta(days=KEEP_DAYS)
    kept = [json.dumps(row, separators=(",", ":")) + "\n" for row in parsed(read(path)) if (when(row) or cutoff) > cutoff]
    scratch = path.with_name(f"runs.jsonl.{os.getpid()}.tmp")
    scratch.write_text("".join(kept), encoding="utf-8")
    os.replace(scratch, path)  # readers take no lock: they see the old file or the new one
    for log in (path.parent / "logs").glob("*.log"):
        if log.stat().st_mtime < cutoff.timestamp():
            log.unlink()

def base_tree(target: str | None = None) -> str | None:
    """The tree of the run that holds the newest green row (of `target`; None: of any run), when git still has it."""
    found = rows()
    for row in reversed(found):
        if row.get("exit") == 0 and (row.get("target") == target if target else "target" not in row):  # a run row names no target
            base = next((r.get("tree") for r in reversed(found) if r.get("run") == row.get("run") and "target" not in r), None)
            return base if isinstance(base, str) and run_git("cat-file", "-e", base + "^{tree}").returncode == 0 else None
    return None

class Target:
    """A map target: its `inputs`, its `own` paths (its row's or module's, no dependency's) and the `modules` it checks."""
    def __init__(self) -> None:
        self.inputs, self.own, self.modules = set(), set(), set()

def plan(t: Tree, rows_: list[Row], reg) -> tuple[dict[str, Target], list[str]]:
    """({target: Target} in map order, then the module checks; the paths no row and no module check maps)."""
    out, won, unmapped = {}, {}, []
    owner = {path: reg.of(path) for path in t.paths} if reg is not None else {}
    checked = sorted(name for name, mod in (reg.modules.items() if reg is not None else []) if mod.check)
    for path in t.paths:
        row = first_row(rows_, path)
        if row is not None:
            won.setdefault(id(row), []).append(path)
        elif owner.get(path) not in checked:
            unmapped.append(path)
    for row in rows_:
        for name in row.targets if id(row) in won else []:
            item = out.setdefault(name, Target())
            item.own.update(won[id(row)])
            item.inputs.update(won[id(row)])
    for module in checked:
        deps, todo = set(), [module]
        while todo:  # what the module depends on, transitively ([shared] included)
            fresh = set(reg.deps(todo.pop())) - deps
            deps |= fresh
            todo += fresh
        own = [path for path in t.paths if owner[path] == module]
        for name in reg.modules[module].check:
            item = out.setdefault(name, Target())
            item.own.update(own)
            item.inputs.update(own, (path for path in t.paths if owner[path] in deps or path in REGISTRY_FILES))
            item.modules.add(module)
    for name in t.every:  # a whole-tree goal: any change reruns it; no path narrows it (own stays empty)
        out.setdefault(name, Target()).inputs.update(t.paths)
    return out, unmapped

def hidden_by(t: Tree, rows_: list[Row], name: str) -> tuple[str, str] | None:
    """FB-1208: (pack, why) of a target rows name that the plan holds not: an earlier row takes its paths, or no path matches
    them; None when no row names it."""
    own = [row for row in rows_ if name in row.targets]
    if not own:
        return None
    pack = Path(own[0].where.rsplit(":", 1)[0]).stem
    for path in t.paths:
        if any(glob_match(row.segs, path) for row in own):
            row = first_row(rows_, path)
            return pack, f"{row.where} {row.glob} {' '.join(row.targets) or '-'} maps its paths first"
    return pack, "no path of the tree matches its rows"

def target_env(item: Target, name: str, tier: str) -> dict[str, str]:
    """A running target's KIT_CHECK_PATHS, KIT_COVER_BASE (none on a sweep) and KIT_CHECK_MODULES; never the ambient ones."""
    env = {key: value for key, value in os.environ.items() if key not in TARGET_ENV}
    base = None if tier == "sweep" else base_tree(name)
    if base is not None:
        env["KIT_COVER_BASE"] = base
        changed = set(nul_list("diff", "--name-only", "-z", "--no-renames", base, "--")) | tree().untracked
        paths = changed & item.inputs
        if paths and paths <= item.own:
            env["KIT_CHECK_PATHS"] = "\n".join(sorted(paths))
    if item.modules:
        env["KIT_CHECK_MODULES"] = " ".join(sorted(item.modules))
    return env

def kill_group(proc: subprocess.Popen) -> None:
    """SIGTERM to the group `proc` leads, GRACE seconds, SIGKILL; a group already gone is fine (macOS: EPERM, a zombie)."""
    try:
        os.killpg(proc.pid, signal.SIGTERM)
        try:
            proc.wait(GRACE)
        except subprocess.TimeoutExpired:
            pass
        os.killpg(proc.pid, signal.SIGKILL)  # the leader gone or not: the rest of its group goes too
    except (ProcessLookupError, PermissionError):
        pass
    proc.wait()

def run_make(argv: list[str], env: dict[str, str], timeout: float | None, log: Path | None, stop=None) -> int | None:
    """One target in its own process group, output to `log`: its exit, or None when the clock or `stop` killed it."""
    end = None if timeout is None else time.monotonic() + timeout
    with open(log or os.devnull, "wb") as sink:
        try:
            proc = subprocess.Popen(argv, env=env, stdout=sink, stderr=subprocess.STDOUT, start_new_session=True)
        except OSError as exc:
            raise Stop(2, f"{TAG} cannot run {argv[0]}: {exc}")
        try:
            while True:
                try:
                    return proc.wait(timeout=0.1)
                except subprocess.TimeoutExpired:
                    if (stop is not None and stop.is_set()) or (end is not None and time.monotonic() >= end):
                        kill_group(proc)
                        return None
        except BaseException:
            kill_group(proc)
            raise

RUN = run_make

class Jobs:
    """make's jobserver, joined as a client (make manual 13.1.1): the `-jN` and fifo of MAKEFLAGS. One target runs in
    kit-select's own slot; each further one takes a byte of the fifo and the same byte is written back when it ends."""
    def __init__(self, flags: str) -> None:
        found = re.findall(r"(?:^|\s)-j(\d*)(?=\s|$)", flags)
        self.width = (int(found[-1]) if found[-1] else 0) if found else 1  # 0: `-j` with no number
        fifo = re.findall(r"--jobserver-auth=fifo:(\S+)", flags)
        self.fd = None
        if fifo and self.width != 1:
            try:
                self.fd = os.open(fifo[-1], os.O_RDWR | os.O_NONBLOCK)
            except OSError:
                self.fd = None
        if self.fd is None and self.width != 1:
            say(f"{TAG} no jobserver for this recipe: targets run one at a time")

    def take(self, wait: float) -> bytes | None:
        """One token, or None when none came within `wait` seconds."""
        if self.fd is None or not select.select([self.fd], [], [], max(0.0, wait))[0]:
            return None
        try:
            return os.read(self.fd, 1) or None
        except BlockingIOError:  # another client of the jobserver read it first
            return None

    def give(self, token: bytes | None) -> None:
        if token is not None and self.fd is not None:
            os.write(self.fd, token)

    def close(self) -> None:
        if self.fd is not None:
            os.close(self.fd)

def clock(tier: str) -> tuple[str, float]:
    """(the clock's name, its seconds): the environment, then .devkit/config.env, else the default."""
    key, default = CLOCKS[tier]
    raw = os.environ.get(key, "").strip() or config_value(Path.cwd(), key) or f"{default:g}"
    if not re.fullmatch(r"\d+(\.\d+)?", raw) or float(raw) <= 0:
        raise Stop(2, f"{TAG} {key}={raw}: expected a positive number of seconds")
    return key, float(raw)

def make_cmd() -> list[str]:
    return shlex.split(os.environ.get("KIT_MAKE", "") or "make")

def run_all(targets: dict[str, Target], todo: dict[str, str], tier: str, run: str, began: float, classes, room=None,
            cover: int = 0) -> tuple:
    """Run `todo` {target: digest} under the tier's clock and make's jobserver, a target row each: (ran, red, killed, spent).
    Light and unclassed start in that order as slots free and run past a red; heavy starts when they all ended green, one at
    a time, and stops at its first red; `@units` sets never overlap. Light: docker is unreachable, and a set is handed `room`
    seconds of price less what earlier sets spent. A red `cover` (coverage-config) opens the red list."""
    key, limit = clock(tier)
    deadline, sets = began + limit, tree().sets
    groups: dict[str, list[str]] = {name: [] for name in CLASS_ORDER}
    for name in todo:
        groups[target_class(name, *classes)].append(name)
    if re.search(r"(?:^|\s)--shuffle=reverse(?:\s|$)", os.environ.get("MAKEFLAGS", "")):
        for names in groups.values():
            names.reverse()
    scratch = None if store() else tempfile.TemporaryDirectory(prefix="kit-select-")
    logs = store().parent / "logs" if store() else Path(scratch.name)
    logs.mkdir(parents=True, exist_ok=True)
    ran, spent, wentred, killed, late = [], {}, {name: [] for name in CLASS_ORDER}, [], []
    red, exits = (["coverage-config"], {"coverage-config": cover}) if cover else ([], {})
    pending, heavy = groups["light"] + groups["unclassed"], list(groups["heavy"])
    running: dict[str, threading.Thread] = {}
    ended: queue.Queue = queue.Queue()
    stop, caught, jobs = threading.Event(), [], Jobs(os.environ.get("MAKEFLAGS", ""))

    def work(name: str, env: dict, log: Path, token: bytes | None, start: float) -> None:
        out = (None, None)
        try:
            out = (RUN([*make_cmd(), name], env, deadline - start, log, stop=stop) if deadline > start else None, None)
        except BaseException as exc:  # handed to the main thread, which stops the rest and raises it
            out = (None, exc)
        finally:
            try:
                ended.put((name, *out, round(CLOCK() - start, 3)))  # before the token: a token taken finds this result
            finally:
                jobs.give(token)

    def begin(name: str, token: bytes | None) -> None:
        log = logs / f"{run}-{re.sub(r'[^A-Za-z0-9._-]', '_', name)}.log"
        env = target_env(targets[name], name, tier)
        if tier == "light":
            env["DOCKER_HOST"] = units().FENCE  # check never starts docker: a target that calls it is red
        if room is not None and name in sets:
            env["UNITS_BUDGET_S"] = f"{max(0.0, room - sum(v for k, v in spent.items() if k in sets)):g}"
        running[name] = threading.Thread(target=work, args=(name, env, log, token, CLOCK()), daemon=True)
        running[name].start()

    def finish(name: str, code: int | None, exc: BaseException | None, wall_s: float) -> None:
        running.pop(name).join()
        if exc is not None:
            stop.set()
            raise exc
        if caught:  # a signal: the killed targets prove nothing and write no row
            return
        log = logs / f"{run}-{re.sub(r'[^A-Za-z0-9._-]', '_', name)}.log"
        spent[name], exits[name] = wall_s, code
        ran.append(name)
        append({"run": run, "ts": utc_now().isoformat(timespec="seconds"), "target": name, "digest": todo[name],
                "exit": code, "wall_s": wall_s, "free": tier == "sweep", "log": None if scratch else str(log),
                "tier": "light" if tier == "light" else "full"})
        text = read(log)
        if text:
            sys.stdout.write(text if text.endswith("\n") else text + "\n")
        say(f"{TAG} ran {name} {'killed' if code is None else code} {wall_s:g}")
        if code and tier == "light" and units().FENCE_MARK in text:
            say(f"next: name {name} in docker_targets", err=True)
        if code is None:
            stop.set()  # the clock ran out: every running target is killed, nothing more starts
            killed.append(name)
            red.append(name)
        elif code != 0:
            klass = target_class(name, *classes)
            red.append(name)
            wentred[klass].append(name)
            say(f"{TAG} red {name} (exit {code})", err=True)
            if klass == "heavy" and heavy:
                say(f"{TAG} stopped before heavy {' '.join(heavy)}: heavy red {name}", err=True)

    def ready() -> str | None:
        if stop.is_set():
            return None
        name = next((n for n in pending if n not in sets or not any(other in sets for other in running)), None)
        if name is None and not (pending or running or wentred["light"] or wentred["unclassed"] or wentred["heavy"]):
            name = heavy[0] if heavy else None
        if name is not None and CLOCK() >= deadline:  # the clock ran out before it started: not killed, and red
            stop.set()
            late.extend(pending + heavy)
            red.extend(late)
            return None
        return name

    def on_signal(signum: int, _frame) -> None:
        caught.append(signum)
        stop.set()
    main_thread = threading.current_thread() is threading.main_thread()
    before = {sig: signal.signal(sig, on_signal) for sig in (signal.SIGTERM, signal.SIGINT)} if main_thread else {}
    try:
        while True:
            while not ended.empty():
                finish(*ended.get())
            name = ready()
            if name is None and not running:
                break
            token = None
            if name is not None and running:
                token = jobs.take(max(0.0, min(0.05, deadline - CLOCK())))
                if token is not None and not ended.empty():  # a target ended meanwhile: read its result first
                    jobs.give(token)
                    continue
                name = name if token is not None else None
            if name is None:
                try:
                    finish(*ended.get(timeout=0.05))
                except queue.Empty:
                    pass
                continue
            (heavy if name in heavy else pending).remove(name)
            begin(name, token)
    finally:
        if running:
            stop.set()
            for thread in list(running.values()):
                thread.join()
        for sig, handler in before.items():
            signal.signal(sig, handler)
        jobs.close()
        if scratch:
            scratch.cleanup()
    if caught:
        name = signal.Signals(caught[0]).name
        raise Stop(128 + caught[0], f"{TAG} stopped by {name}: nothing more starts and no row is written")
    if killed or late:
        slow = ", ".join(f"{n} {s:g} s" for n, s in sorted(spent.items(), key=lambda item: -item[1])[:3])
        parts = [f"killed {' '.join(killed)}" if killed else "", f"not started {' '.join(late)}" if late else "", f"slowest {slow}" if slow else ""]
        say(f"{TAG} clock {key}={limit:g} s ran out: " + "; ".join(p for p in parts if p), err=True)
    elif heavy and (wentred["light"] or wentred["unclassed"]):  # a light or unclassed red: the heavy targets do not start (FB-889)
        say(f"{TAG} stopped before heavy {' '.join(heavy)}: " + ", ".join(f"{k} red {' '.join(wentred[k])}" for k in CLASS_ORDER if wentred[k]), err=True)
    if red:  # every red run ends on one line of every red target in the order they ended (FB-1142, FB-1187)
        mark = {n: "not started" if n in late else "killed" if exits[n] is None else f"exit {exits[n]}" for n in red}
        say(f"{TAG} red {len(red)}: " + ", ".join(f"{n} ({mark[n]})" for n in red), err=True)
    return ran, red, bool(killed or late), spent

def make_age(pid: str) -> int:
    """make's wall time so far in whole seconds (ps etime, `[[dd-]hh:]mm:ss`)."""
    proc = subprocess.run(["ps", "-o", "etime=", "-p", pid], capture_output=True, text=True)
    if proc.returncode != 0 or not proc.stdout.strip():
        raise RuntimeError(f"make pid {pid} is not in the process table")
    days, _, rest = proc.stdout.strip().rpartition("-")
    seconds = sum(int(part) * 60 ** at for at, part in enumerate(reversed(rest.split(":"))))
    return seconds + (int(days) * 86400 if days else 0)

def wall(began: float) -> float:
    """The run's wall seconds: the age of make (KIT_MAKE_PID), else of this run."""
    pid = os.environ.get("KIT_MAKE_PID", "").strip()
    try:
        return float(make_age(pid)) if pid.isdigit() else round(CLOCK() - began, 3)
    except (RuntimeError, ValueError, OSError):
        return round(CLOCK() - began, 3)

def listed(prefix: str, paths: list[str], err: bool) -> None:
    for path in paths[:CAP]:
        say(f"{TAG} {prefix} {path}", err=err)
    if len(paths) > CAP:
        say(f"{TAG} +{len(paths) - CAP} more", err=err)

def unmeasured() -> set[str]:
    """FB-1212: the `@units` targets of the stacks (a map's stem) whose coverage measure here is not of this tree (cover-gate.py)."""
    cover = load_beside("cover-gate.py", "kit_cover_gate")
    here = (git_try("write-tree") or "") if tree().clean else ""  # a dirty tree: no measure is of it
    stacks = set(cover.unmeasured(here)) if cover is not None else set()
    return {name for path in tree().maps if path.stem in stacks for name in units().directives([read(path)])[0]}

def cover_config() -> int:
    """The floors files (cover-gate.py `config`, skipped when absent) against the newest green run's tree: 0 or 1."""
    try:
        cover = load_beside("cover-gate.py", "kit_cover_gate")
        code = cover.run_config(base_tree()) if cover is not None else 0
    except Exception as exc:  # a broken script is red, never a silent pass
        say(f"{TAG} cover-gate.py: {exc}", err=True)
        code = 1
    if code:
        say(f"{TAG} red coverage-config (exit {code})", err=True)
    return code

def explain(paths: list[str], maps: list[str]) -> int:
    """`--explain`: for each path its map line, the row's targets (tier, class, `hit` or `runs`); runs nothing, writes no row.
    A path's targets are the plan's own: its row's, then its module's checks. 0 when every path has a row or a module check
    (a `-` row prints `no targets`), 1 when one has neither."""
    root = Path(git_out("rev-parse", "--show-toplevel")).resolve()
    explicit = [Path(m).resolve() for m in maps]
    t = tree(root, map_files(root, explicit))
    map_rows, reg = load_rows(root, explicit), load_registry(root, explicit, t.paths)
    mapped, _unmapped = plan(t, map_rows, reg)
    classes, book, code = class_lists(root), units().history(rows()), 0
    for path in paths:
        key = path.removeprefix("./")
        row = first_row(map_rows, key)
        names = list(row.targets) if row else []
        names += [name for name, item in mapped.items() if item.modules and key in item.own and name not in names]
        if not row and not names:
            say(f"{TAG} explain {path}: no row")
            code = 1
            continue
        say(f"{TAG} explain {path}: {f'{row.where} {row.glob}' if row else f'module {reg.of(key)}'} {' '.join(names) or 'no targets'}")
        for name in names:
            tier = "set" if name in t.sets else target_tier(name, book.get(name, []), classes)
            hit = green(digest(name, mapped[name].inputs if name in mapped else []), "full" if tier == "slow" else "light")
            say(f"{TAG}   {name} {tier} {target_class(name, *classes)} {'hit' if hit else 'runs'}")
    return code

def execute(args: argparse.Namespace) -> int:
    if args.explain:
        return explain(args.explain, args.map)
    began = CLOCK()
    tier = os.environ.get("KIT_TIER", "").strip() or "light"
    if tier not in CLOCKS:
        raise Stop(2, f"{TAG} KIT_TIER={tier}: expected light, full or sweep")
    if args.only and tier == "light":
        tier = "full"  # `--only` asks what check-full asks, of the named targets
    if tier != "light" and not args.only and not any(word in FULL_GOALS for word in os.environ.get("KIT_GOALS", "").split()):
        raise Stop(3, f"{TAG} {REFUSE_FULL}")
    root = Path(git_out("rev-parse", "--show-toplevel")).resolve()
    os.chdir(str(root))
    explicit = [Path(m).resolve() for m in args.map]
    strict = args.strict or project_has_tiers(root)
    tier = tier if strict else "light"  # not strict: a full ask runs the light tier, never a full run
    clock(tier)  # a bad clock value stops the run before anything runs
    t = tree(root, map_files(root, explicit))
    map_rows = load_rows(root, explicit)
    targets, unmapped = plan(t, map_rows, load_registry(root, explicit, t.paths))
    tracked = [path for path in unmapped if path not in t.untracked]
    if strict:
        if tracked:
            listed("unmapped", tracked, True)
            raise Stop(3, "next: add a row to config/check-map")
    if tier != "light" and not t.paths:
        raise Stop(3, f"{TAG} full tier selected no map target",
                   "next: add a row to config/check-map that a tracked path matches (an unborn or empty tree has none)")
    classes, book = class_lists(root), units().history(rows())
    hidden = {name: why for name in args.only if name not in targets and (why := hidden_by(t, map_rows, name))}
    if missing := [name for name in args.only if name not in targets and name not in hidden]:
        raise Stop(2, f"{TAG} --only {missing[0]}: not a target of the map")
    for name, (pack, why) in hidden.items():
        say(f"{TAG} skipped {name} (pack {pack}): {why}")
    todo, reused, left, room, stale = {}, [], [], None, unmeasured() if tier == "full" else set()
    for name in [name for name in args.only if name not in hidden] if args.only else targets:
        dig = digest(name, targets[name].inputs)
        hit = None if tier == "sweep" or name in stale else green(dig, "light" if tier == "light" else "full")
        if hit is None:
            todo[name] = dig
        else:
            reused.append(name)
            say(f"{TAG} reused {name} green {hit.get('ts')}")
    if tier == "light":  # slow targets and what does not fit LIGHT_S of summed price wait; sets (@units) tier inside
        kinds = {name: "set" if name in t.sets else target_tier(name, book.get(name, []), classes) for name in todo}
        costs = {name: units().cost(book.get(name, [])) for name, kind in kinds.items() if kind == "unit"}
        _taken, over, used = units().cut(costs, units().LIGHT_S)
        left, room = [name for name, kind in kinds.items() if kind == "slow"] + over, units().LIGHT_S - used
        todo = {name: dig for name, dig in todo.items() if name not in left}
        for name in left:
            say(f"{TAG} left for check-full: {name}")
    flags = os.environ.get("MAKEFLAGS", "")
    if "n" in (flags.split()[0] if flags.split() and not flags.startswith((" ", "-")) else ""):  # `make -n` runs a `+` line
        say(f"{TAG} -n: would run {' '.join(todo) or 'nothing'}")
        return 0
    cover = cover_config()
    run = f"{utc_now():%Y%m%dT%H%M%SZ}-{os.getpid()}"
    ran, red, killed, spent = run_all(targets, todo, tier, run, began, classes, room, cover)
    code, total = 1 if red else 0, wall(began)
    append({"run": run, "ts": utc_now().isoformat(timespec="seconds"), "tier": tier,
            "tree": git_try("write-tree") if t.clean else None, "exit": code, "wall_s": total, "killed": killed,
            "ran": ran, "reused": reused})
    if not strict:
        listed("not checked", unmapped, False)
        say(f"warn: no gate tiers yet; CHANGELOG {HAND_STEP_VERSION} hand step")
    done = " ".join(ran) or ("nothing (reused)" if reused else "nothing")
    say(f"{TAG} {tier} · ran {done} · reused {len(reused)} · left {len(left)}")
    return code

def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(prog="check-select.py", description="Run the map targets not green on this content.")
    parser.add_argument("--map", action="append", default=[], metavar="FILE", help="a map file (repeatable; replaces the list)")
    parser.add_argument("--trunk", default="", help=argparse.SUPPRESS)  # an old caller's flag: no diff base is read
    parser.add_argument("--strict", action="store_true", help="strict without reading the Makefile")
    parser.add_argument("--only", nargs="+", default=[], metavar="TARGET", help="run just these map targets, as check-full does")
    parser.add_argument("--explain", nargs="+", default=[], metavar="PATH", help="print the map line and targets of each path; run nothing")
    args = parser.parse_args(argv)
    try:
        return execute(args)
    except Stop as stop:
        for line in stop.lines:
            say(line, err=True)
        return stop.code

if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
