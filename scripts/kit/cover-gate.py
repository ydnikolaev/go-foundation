#!/usr/bin/env python3
# managed by devkit — do not edit; fix upstream in ~/Developer/projects/devkit
"""cover-gate: the coverage floors of a repo, one table per stack, judged from measure lines a stack's test step hands in.

    python3 scripts/kit/cover-gate.py check --stack <stack> [--whole]    (measure lines on stdin)
    python3 scripts/kit/cover-gate.py config
    python3 scripts/kit/cover-gate.py raise [--into config/coverage-<stack>.d/<name>.yaml]    (make coverage-raise)

Floors file: config/coverage-<stack>.yaml, git-tracked, written only by `raise` or by hand. A stack's table is that file plus
every fragment config/coverage-<stack>.d/*.yaml, read as one: a unit may stand in one file only (two files holding it are
refused, both named), and a row moved between files is neither a removal nor a new floor. With no .d directory it is one file:

    exempt:
      cmd/tool: "why its figure cannot be measured here"
    floors:
      # a reason comment directly above a lowered row
      pkg/a: 93.7

A unit is a repo-relative dir (`.` is the root). A figure is integer tenths, `covered * 1000 // total`: no float, no slack.

`check` reads lines `<unit> <covered> <total> <changed|whole>`. A floored unit may not fall below its floor; a unit new at
the diff base with statements and no covered statement is refused (also with no floors file). `--whole` marks a run over
the whole tree: it also writes the snapshot `.artifacts/coverage/<stack>.measure` that `raise` reads.
`config` (run by the change selector on every check): each floors file parses, and against the table at the diff base
(KIT_COVER_BASE, else check-select.py's base) a floor is not lowered, a row whose dir stands is not removed, and a table
holding floors is not deleted, unless a new comment gives the reason.
`raise` only raises: from the snapshot of this checkout, never lowers, never commits, refuses a lane branch, a stale or
dirty snapshot and a dirty tree. A raised floor is written to the file that holds the row; a new unit goes to the stack file,
or to the fragment `--into` names.

Exit: 0 ok, 1 refused, 2 usage or malformed input. Stdlib only, python3 >= 3.9.
"""
from __future__ import annotations

import argparse
import glob
import importlib.util
import os
import re
import subprocess
import sys
from pathlib import Path

sys.dont_write_bytecode = True

TAG = "cover-gate:"
KINDS = ("changed", "whole")
ROAD = "make check-full in this checkout"
MEASURE_DIR = Path(".artifacts") / "coverage"
STACK = re.compile(r"[a-z0-9][a-z0-9-]*\Z")
FIGURE = re.compile(r"(\d+)(?:\.(\d))?\Z")
ROW = re.compile(r"(?P<head>[ \t]+(?P<unit>\"[^\"]+\"|[^\s:#\"][^:]*?)[ \t]*:[ \t]*)(?P<val>\S.*?)[ \t]*\Z")
FIRST = 3


class Usage(Exception):
    """Malformed input: exit 2."""


def out(line: str) -> None:
    sys.stdout.write(line + "\n")
    sys.stdout.flush()


def err(line: str) -> None:
    sys.stderr.write(line + "\n")
    sys.stderr.flush()


def fmt(tenths: int) -> str:
    return f"{tenths // 10}.{tenths % 10}"


def first(units: list[str], limit: int = FIRST) -> str:
    more = f" (+{len(units) - limit} more)" if len(units) > limit else ""
    return ", ".join(units[:limit]) + more


def git(*args: str) -> tuple[int, str]:
    try:
        proc = subprocess.run(["git", *args], capture_output=True, text=True, errors="replace")
    except OSError:
        return 127, ""
    return proc.returncode, proc.stdout


# ---- the floors file --------------------------------------------------------------------------------------------

class Floors:
    def __init__(self, name: str, text: str) -> None:
        self.name = name
        self.lines = text.split("\n")
        if self.lines and self.lines[-1] == "":
            self.lines.pop()
        self.exempt: dict[str, str] = {}
        self.floors: dict[str, int] = {}
        self.rows: dict[str, tuple[str, int, str]] = {}  # unit -> (section, line index, text before the value)
        self.header: dict[str, int] = {}
        section = ""
        for number, raw in enumerate(self.lines):
            where = f"{name}:{number + 1}"
            if not raw.strip() or raw.lstrip().startswith("#"):
                continue
            if raw[0] not in " \t":
                word = raw.rstrip()
                if word not in ("exempt:", "floors:"):
                    raise Usage(f"{where}: malformed line {raw!r}: want `exempt:`, `floors:`, a row or a # comment")
                section = word[:-1]
                self.header[section] = number
                continue
            match = ROW.fullmatch(raw)
            if not section or match is None:
                raise Usage(f"{where}: malformed line {raw!r}: want an indented `<unit>: <value>` row under exempt: or floors:")
            unit = match["unit"].strip().strip('"')
            value = match["val"]
            if unit in self.rows:
                raise Usage(f"{where}: unit {unit!r} appears twice")
            if section == "exempt":
                reason = value.strip('"').strip() if value.startswith('"') and value.endswith('"') and len(value) > 1 else value
                if not reason:
                    raise Usage(f"{where}: exempt row {unit!r} needs a non-empty reason")
                self.exempt[unit] = reason
            else:
                fig = FIGURE.fullmatch(value)
                if fig is None or int(fig[1]) * 10 + int(fig[2] or 0) > 1000:
                    raise Usage(f"{where}: floor for {unit!r} is {value!r}: want a figure 0 to 100 with at most one decimal")
                self.floors[unit] = int(fig[1]) * 10 + int(fig[2] or 0)
            self.rows[unit] = (section, number, match["head"])

    def comment_above(self, unit: str) -> str | None:
        number = self.rows[unit][1]
        if number > 0 and self.lines[number - 1].lstrip().startswith("#"):
            return self.lines[number - 1].strip()
        return None


STACK_FILE = re.compile(r"config/coverage-(?P<stack>[^/]+)\.yaml\Z")
FRAGMENT = re.compile(r"config/coverage-(?P<stack>[^/]+)\.d/[^/]+\.yaml\Z")
INTO = re.compile(r"config/coverage-(?P<stack>[a-z0-9][a-z0-9-]*)\.d/[^/]+\.yaml\Z")


def stack_of(path: str) -> str | None:
    match = STACK_FILE.fullmatch(path) or FRAGMENT.fullmatch(path)
    return match["stack"] if match else None


def group(paths: list[str]) -> dict[str, list[str]]:
    """Floors paths by stack: the stack file first, then its fragments by name."""
    groups: dict[str, list[str]] = {}
    for path in sorted(set(paths), key=lambda p: (FRAGMENT.fullmatch(p) is not None, p)):
        stack = stack_of(path)
        if stack:
            groups.setdefault(stack, []).append(path)
    return dict(sorted(groups.items()))


def coverage_files() -> list[str]:
    return glob.glob("config/coverage-*.yaml") + glob.glob("config/coverage-*.d/*.yaml")


class Table:
    """One stack's floors, its files merged: a unit stands in one file only."""

    def __init__(self, files: dict[str, Floors]) -> None:
        self.files = files
        self.where: dict[str, str] = {}  # unit -> the file holding its row
        self.exempt: dict[str, str] = {}
        self.floors: dict[str, int] = {}
        for name, parsed in files.items():
            for unit in parsed.rows:
                if unit in self.where:
                    raise Usage(f"unit {unit!r} appears in {self.where[unit]} and {name}: keep it in one file")
                self.where[unit] = name
            self.exempt.update(parsed.exempt)
            self.floors.update(parsed.floors)

    def known_lines(self) -> set[str]:
        return {ln.strip() for parsed in self.files.values() for ln in parsed.lines}


def load_table(stack: str) -> Table | None:
    names = group(coverage_files()).get(stack, [])
    if not names:
        return None
    return Table({name: Floors(name, Path(name).read_text(encoding="utf-8")) for name in names})


def parse_measure(text: str, where: str) -> list[tuple[str, int, int, str]]:
    rows = []
    for number, raw in enumerate(text.splitlines(), 1):
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        fields = line.split()
        ok = len(fields) == 4 and fields[1].isdigit() and fields[2].isdigit() and fields[3] in KINDS
        if not ok or int(fields[1]) > int(fields[2]):
            raise Usage(f"{where}:{number}: malformed measure line {line!r}: want `<unit> <covered> <total> <changed|whole>`")
        rows.append((fields[0], int(fields[1]), int(fields[2]), fields[3]))
    return rows


# ---- the base ---------------------------------------------------------------------------------------------------

def load_selector():
    path = Path(__file__).resolve().parent / "check-select.py"
    if not path.is_file():
        return None
    spec = importlib.util.spec_from_file_location("kit_check_select", path)
    if spec is None or spec.loader is None:
        return None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def resolve_base() -> str | None:
    """KIT_COVER_BASE (the selector sets it once per gate), else the selector's own base_of; None when there is none."""
    given = os.environ.get("KIT_COVER_BASE", "").strip()
    if given:
        return given
    try:
        selector = load_selector()
        if selector is None:
            return None
        top = selector.git_try("rev-parse", "--show-toplevel")
        return selector.base_of(Path(top or os.getcwd()), "")[1]
    except Exception:  # a Stop (no base, a git failure) or an unreadable selector: no base
        return None


def exists_at(base: str, unit: str) -> bool | None:
    args = ["ls-tree", "--name-only", base]
    if unit != ".":
        args += ["--", unit]
    code, listing = git(*args)
    if code != 0:
        return None
    return bool(listing.strip())


# ---- check ------------------------------------------------------------------------------------------------------

def dirty_paths() -> list[str]:
    """Tracked changes and untracked, non-ignored files outside docs/ (and the ignored-by-name .artifacts/)."""
    code, listing = git("status", "--porcelain=v1", "-z", "--untracked-files=all")
    entries = listing.split("\0")
    paths, i = [], 0
    while i < len(entries):
        entry = entries[i]
        i += 1
        if len(entry) < 4:
            continue
        if "R" in entry[:2] or "C" in entry[:2]:
            i += 1  # the original path follows
        paths.append(entry[3:])
    return [p for p in paths if not p.startswith(("docs/", ".artifacts/"))]


def head_label() -> str:
    code, sha = git("rev-parse", "HEAD")
    label = sha.strip() if code == 0 and sha.strip() else "unborn"
    return label + ("+dirty" if dirty_paths() else "")


def atomic_write(path: Path, text: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_name(f"{path.name}.tmp{os.getpid()}")
    try:
        tmp.write_text(text, encoding="utf-8")
        os.replace(tmp, path)
    finally:
        if tmp.exists():
            tmp.unlink()


def cmd_check(stack: str, whole: bool) -> int:
    if not STACK.fullmatch(stack):
        raise Usage(f"--stack {stack!r}: want lowercase letters, digits and dashes")
    rows = parse_measure(sys.stdin.read(), "stdin")
    name = f"config/coverage-{stack}.yaml"
    floors = load_table(stack)
    if floors is None:
        err(f"warn: no coverage baseline for {stack}; make check-full, then make coverage-raise, then commit {name}")
    exempt = floors.exempt if floors else {}
    floor_of = floors.floors if floors else {}
    if whole:
        atomic_write(MEASURE_DIR / f"{stack}.measure",
                     f"# head {head_label()}\n" + "".join(f"{u} {c} {t} {k}\n" for u, c, t, k in rows))
    base: list[str | None] = []  # resolved at most once, and only for a zero-covered unit

    def base_once() -> str | None:
        if not base:
            base.append(resolve_base())
        return base[0]

    judged = n_exempt = rise = 0
    refused: list[str] = []
    stale: list[str] = []
    zero: list[str] = []
    seen = set()
    for unit, covered, total, kind in rows:
        seen.add(unit)
        if unit in exempt:
            n_exempt += 1
            continue
        if unit in floor_of:
            floor = floor_of[unit]
            if total == 0:
                if kind == "changed":
                    judged += 1
                    refused.append(f"coverage: {unit}: floor {fmt(floor)} but no measure this run "
                                   f"(no statements or no test files); restore its tests, or lower the floor by hand with a reason above the row")
                else:
                    stale.append(unit)
                continue
            judged += 1
            figure = covered * 1000 // total
            if figure < floor:
                refused.append(f"coverage: {unit}: {fmt(figure)} ({covered} of {total} statements) is below the floor {fmt(floor)}. "
                               f"With no code change, look for a timing branch, a test skipped on a missing env or flag, "
                               f"or a build tag or OS file that differs here (#injected-time)")
            elif figure > floor:
                rise += 1
            continue
        if total == 0:
            continue
        judged += 1
        if covered > 0:
            continue
        known = exists_at(base_once(), unit) if base_once() else None
        if known is False:
            refused.append(f"coverage: {unit}: a new unit with {total} statements and no covered statement; add tests")
        elif known and floors is not None and kind == "changed":
            refused.append(f"coverage: {unit}: {total} statements and no covered statement, and no floor; "
                           f"add tests, or ask the lead for an exempt row")
        else:
            zero.append(unit)
    if whole:
        stale += [u for u in floor_of if u not in seen and u not in exempt]
    for line in refused:
        err(line)
    if stale:
        err(f"warn: coverage: {len(stale)} floor row(s) with no measure this run, stale: {first(stale)} "
            f"(make coverage-raise drops a row whose dir is gone)")
    if zero:
        note = "" if base and base[0] else " (no diff base: the new-unit check was skipped)"
        err(f"warn: coverage: {len(zero)} unit(s) with statements and no covered statement and no floor: {first(zero)}{note}")
    out(f"coverage: {stack} {'full' if whole else 'light'} · {judged} judged · {n_exempt} exempt · "
        f"{len(refused)} refused · {rise} can rise")
    return 1 if refused else 0


# ---- config -----------------------------------------------------------------------------------------------------

def run_config(base: str | None) -> int:
    """The floors tables against the table at `base` (None: shape only). Returns 0 or 1; prints the refusals."""
    groups = group(coverage_files())
    problems: list[str] = []
    tables: dict[str, Table] = {}
    broken: set[str] = set()
    for stack, names in groups.items():
        parsed: dict[str, Floors] = {}
        for name in names:
            try:
                parsed[name] = Floors(name, Path(name).read_text(encoding="utf-8"))
            except (Usage, UnicodeDecodeError, OSError) as exc:
                problems.append(f"{TAG} {exc}" if isinstance(exc, Usage) else f"{TAG} {name}: {exc}")
                broken.add(stack)
        if stack in broken:
            continue
        try:
            tables[stack] = Table(parsed)
        except Usage as exc:
            problems.append(f"{TAG} {exc}")
            broken.add(stack)
    if base is None:
        if groups:
            err("warn: coverage floors checked for shape only: no diff base to compare them with")
    else:
        code, listing = git("ls-tree", "-r", "--name-only", base, "config/")
        at_base = group(listing.splitlines()) if code == 0 else {}
        for stack in sorted(set(at_base) | set(tables)):
            if stack in at_base and stack not in broken:
                old = base_table(base, at_base[stack])
                if old is not None:
                    problems += compare(old, tables.get(stack))
    for line in problems:
        err(line)
    return 1 if problems else 0


def base_table(base: str, names: list[str]) -> Table | None:
    """The stack's table as it stood at `base`; None when a file of it never parsed (it gives nothing to compare)."""
    parsed: dict[str, Floors] = {}
    for name in names:
        code, text = git("show", f"{base}:{name}")
        if code != 0:
            return None
        try:
            parsed[name] = Floors(name, text)
        except Usage:
            return None
    try:
        return Table(parsed)
    except Usage:
        return None


def compare(old: Table, new: Table | None) -> list[str]:
    """One stack's table at the base against its table now. A row moved between files is neither a removal nor a new
    floor; the reason comment of a lowered floor is read in the file that holds the row."""
    if new is None:
        if old.floors:
            return [f"{TAG} {next(iter(old.files))}: deleted, and it held {len(old.floors)} floor row(s) at the base; "
                    f"empty its rows with a reason each first, then delete it"]
        return []
    out_: list[str] = []
    known = old.known_lines()
    for unit, floor in new.floors.items():
        was = old.floors.get(unit)
        if was is None or floor >= was:
            continue
        holder = new.where[unit]
        above = new.files[holder].comment_above(unit)
        if above is None or above in known:
            out_.append(f"{TAG} {holder}: the floor of {unit} is lowered {fmt(was)} -> {fmt(floor)} with no new reason; "
                        f"put a new comment line directly above the row saying why")
    fresh = [ln.strip() for parsed in new.files.values() for ln in parsed.lines if ln.strip() not in known]
    for unit in old.floors:
        if unit in new.floors or unit in new.exempt or not os.path.isdir(unit):
            continue
        reason = re.compile(rf"#\s*{re.escape(unit)}:\s*\S")
        if not any(reason.match(ln) for ln in fresh):
            out_.append(f"{TAG} {old.where[unit]}: the row of {unit} is removed while the dir stands; add `# {unit}: <reason>` "
                        f"or an exempt row with a reason")
    return out_


def cmd_config() -> int:
    code = run_config(resolve_base())
    return code


# ---- raise ------------------------------------------------------------------------------------------------------

def lane_branch() -> str | None:
    code, branch = git("symbolic-ref", "--quiet", "--short", "HEAD")
    if code != 0 or not branch.strip():
        return None
    code, remote = git("config", "--get", f"branch.{branch.strip()}.remote")
    return branch.strip() if code == 0 and remote.strip() == "." else None


def rewrite(floors: Floors | None, measure: list[tuple[str, int, int, str]]) -> tuple[str, list[str], list[str], list[str], list[str]]:
    """(new text, raised, new, dropped, zero-covered units not written)."""
    lines = list(floors.lines) if floors else []
    rows = dict(floors.rows) if floors else {}
    current = dict(floors.floors) if floors else {}
    exempt = set(floors.exempt) if floors else set()
    raised, added, dropped, zero = [], [], [], []
    drop_at: set[int] = set()
    fresh: list[tuple[str, int]] = []
    for unit, covered, total, kind in sorted(measure):
        if unit in exempt:
            continue
        figure = covered * 1000 // total if total else 0
        if unit in current:
            if total and figure > current[unit]:
                _, index, head = rows[unit]
                lines[index] = head + fmt(figure)
                raised.append(f"{unit} {fmt(current[unit])} -> {fmt(figure)}")
            continue
        if total and covered > 0:
            fresh.append((unit, figure))
            added.append(f"{unit} {fmt(figure)}")
        elif total:
            zero.append(unit)
    for unit in sorted(current):
        if not os.path.isdir(unit):
            drop_at.add(rows[unit][1])
            dropped.append(unit)
    kept = [ln for i, ln in enumerate(lines) if i not in drop_at]
    if fresh:
        if floors is not None and "floors" in floors.header:
            last = max([rows[u][1] for u in floors.floors] + [floors.header["floors"]])
            at = last + 1 - len([i for i in drop_at if i <= last])
        else:
            if kept and kept[-1].strip():
                kept.append("")
            kept.append("floors:")
            at = len(kept)
        kept[at:at] = [f"  {unit}: {fmt(fig)}" for unit, fig in fresh]
    return "\n".join(kept) + ("\n" if kept else ""), raised, added, dropped, zero


def raise_stack(table: Table | None, measure: list[tuple[str, int, int, str]], target: str):
    """`rewrite` per file of the stack's table: a row is raised or dropped in the file holding it, a unit with no row goes
    to `target`. Returns ({file: new text} for the files that change, raised, new, dropped, zero)."""
    names = list(table.files) if table else []
    if target not in names:
        names.append(target)
    owner = table.where if table else {}
    writes: dict[str, str] = {}
    raised: list[str] = []
    added: list[str] = []
    dropped: list[str] = []
    zero: list[str] = []
    for name in names:
        here = [m for m in measure if owner.get(m[0]) == name or (name == target and m[0] not in owner)]
        text, r, a, d, z = rewrite(table.files.get(name) if table else None, here)
        raised, added, dropped, zero = raised + r, added + a, dropped + d, zero + z
        if r or a or d:
            writes[name] = text
    return writes, raised, added, dropped, zero


def cmd_raise(into: str | None = None) -> int:
    refusals: list[str] = []
    into_match = INTO.fullmatch(into) if into is not None else None
    if into is not None and into_match is None:
        raise Usage(f"--into {into!r}: want config/coverage-<stack>.d/<name>.yaml")
    branch = lane_branch()
    if branch:
        err(f"coverage-raise: {branch} is a lane branch (its upstream is a local branch): floors are raised on the branch "
            f"lanes merge into, after {ROAD}. If it is no lane, `git branch --unset-upstream` first")
        return 1
    measures = {Path(p).name[: -len(".measure")]: Path(p) for p in sorted(glob.glob(str(MEASURE_DIR / "*.measure")))}
    files = {stack: names[0] for stack, names in group(coverage_files()).items()}
    if into_match is not None and into_match["stack"] not in measures:
        raise Usage(f"--into {into!r}: this checkout holds no measure for that stack: run {ROAD} first")
    if not measures and not files:
        out("coverage-raise: nothing measured in this checkout: make check-full first")
        return 0
    for stack, path in files.items():
        if stack not in measures:
            refusals.append(f"coverage-raise: {path} exists but this checkout holds no measure for {stack}: run {ROAD} first "
                            f"(a reused gate run measures nothing here)")
    heads = {}
    for stack, path in measures.items():
        lines = path.read_text(encoding="utf-8").splitlines()
        head = lines[0][len("# head "):].strip() if lines and lines[0].startswith("# head ") else ""
        heads[stack] = head
        if not head:
            refusals.append(f"coverage-raise: {path} has no `# head` line: run {ROAD} first")
        elif head.endswith("+dirty"):
            refusals.append(f"coverage-raise: the {stack} measure was taken on a dirty tree ({head}): commit or stash, then run {ROAD}")
        else:
            code, now = git("rev-parse", "HEAD")
            if code != 0 or head != now.strip():
                code, changed = git("diff", "--name-only", "-z", head, "HEAD")
                names = [n for n in changed.split("\0") if n]
                outside = [n for n in names if not n.startswith("docs/")]
                if code != 0:
                    refusals.append(f"coverage-raise: the {stack} measure head {head[:12]} is not in this history: run {ROAD}")
                elif outside:
                    refusals.append(f"coverage-raise: the {stack} measure is stale: HEAD changed {outside[0]}"
                                    f"{' and more' if len(outside) > 1 else ''} since it: run {ROAD}")
    dirty = dirty_paths()
    if dirty:
        refusals.append(f"coverage-raise: the tree is dirty ({dirty[0]}{' and more' if len(dirty) > 1 else ''}): commit it, "
                        f"then run {ROAD}")
    if refusals:
        for line in refusals:
            err(line)
        return 1
    writes: dict[str, str] = {}
    n_raised = n_new = n_dropped = 0
    for stack, path in measures.items():
        target = into if into_match is not None and into_match["stack"] == stack and into else f"config/coverage-{stack}.yaml"
        wrote, raised, added, dropped, zero = raise_stack(load_table(stack), parse_measure(path.read_text(encoding="utf-8"), str(path)), target)
        for line in raised:
            out(f"coverage-raise: {stack}: raised {line}")
        for line in added:
            out(f"coverage-raise: {stack}: new {line}")
        for unit in dropped:
            out(f"coverage-raise: {stack}: dropped {unit} (the dir is gone)")
        if zero:
            out(f"coverage-raise: {stack}: {len(zero)} zero-covered unit(s) not written, add tests or an exempt row: {first(zero, 10)}")
        n_raised, n_new, n_dropped = n_raised + len(raised), n_new + len(added), n_dropped + len(dropped)
        writes.update(wrote)
    for name, text in writes.items():
        atomic_write(Path(name), text)
    out(f"coverage-raise: {n_raised} raised, {n_new} new, {n_dropped} dropped; commit {' '.join(sorted(writes)) or 'nothing'}")
    return 0


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(prog="cover-gate.py", description="Coverage floors: check, config, raise.")
    sub = parser.add_subparsers(dest="command", required=True)
    check = sub.add_parser("check", help="judge measure lines on stdin against the floors file")
    check.add_argument("--stack", required=True)
    check.add_argument("--whole", action="store_true", help="the lines cover the whole tree: write the snapshot")
    sub.add_parser("config", help="the floors files parse and are not weakened against the diff base")
    lift = sub.add_parser("raise", help="raise the floors from this checkout's last whole-tree measure")
    lift.add_argument("--into", help="config/coverage-<stack>.d/<name>.yaml: the file new units go to (default: the stack file)")
    args = parser.parse_args(argv)
    try:
        if args.command == "check":
            return cmd_check(args.stack, args.whole)
        if args.command == "config":
            return cmd_config()
        return cmd_raise(args.into)
    except Usage as exc:
        err(f"{TAG} {exc}")
        return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
