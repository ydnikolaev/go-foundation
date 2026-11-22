#!/usr/bin/env python3
# managed by devkit — do not edit; fix upstream in ~/Developer/projects/devkit
"""kit-units: `python3 scripts/kit/check-units.py <stack> [--filter EXPR] [<unit>...]`, the one driver of a stack's unit set,
and `check-units.py --tests [--filter EXPR] <unit>...`, which finds each unit's stack; also the tier rule that check-select.py loads by path.

A stack registers `@units <target> <adapter>` in .devkit/check-map.d/<stack>.map (the stack id is the file's stem); the set's
recipe is this driver. The adapter has two verbs: `<adapter> units --list` (one JSON line per unit: id, inputs, needs, tier)
and `<adapter> units --run --tier <light|full> --out FILE [--filter EXPR] <id>...` (one result line per unit: id, exit,
wall_s, cached, tests, skips, tier). A unit id is a repo-relative path, `<stack>:<id>` its target in the store.

A tier is derived, in this order: needs (docker, a browser, a database) is slow; else the declared marker (`unit` is unit, any
other marker slow); else unit. A price decides nothing; a name (the heavy class) decides a target of check-select only. A unit runs unless `green(digest, tier)` answers: a light row answers a light ask
only, a full row both; a full ask on a stack with a floors file reuses only a green full row that carries its `cover` figure and
hands every reused unit's `<id> <covered> <total> reused` line to cover-gate.py, which refuses one under a floor raised since (REUSE),
and the digest holds the Go test tags (FB-1217) and the values of KIT_GO_TEST_FLAGS and KIT_GO_TEST_RUNNER (FB-1303). The light tier runs every unit-tier unit with no green row, whatever its price, and prints `left for check-full: <stack>:<id>`
for the slow ones only; the full tier runs every unit with no full row. The run line tallies by reason why the units that ran had
no answer: `ran 1 (inputs 1) · reused 66` (check-select.py `miss`, or `measure` for a green row with no `cover` on a floored stack).
A directory in KIT_CHECK_PATHS narrows either tier to the units under it (a file there narrows the light tier only).
A run with --filter proves no unit and writes no row; typed units always run, at full. `--tests` asks each stack that registers
`@units` and whose map inputs hold a file the typed paths name (or that no map row names) which units it lists, hands each typed
unit to the stack that lists it (none, or two: usage) and drives each stack with its own units; it is `make test TESTS=<unit>...`,
whose RUN= is the --filter. Exit: 0 green, 1 red, 2 usage.
Stdlib only, python3 >= 3.9, POSIX."""
from __future__ import annotations

import argparse
import contextlib
import importlib.util
import io
import json
import os
import subprocess
import sys
import tempfile
from pathlib import Path

TAG = "check-units:"
FENCE = "unix:///kit-no-docker/docker.sock"  # DOCKER_HOST of a light target: no such socket, so docker is red there
FENCE_MARK = "kit-no-docker"
SELECTOR = None  # check-select.py, loaded by path on first use (a test sets its own)
ADAPTER = None  # (argv, capture) -> (exit, stdout); a test sets its own


def directives(texts: list[str]) -> tuple[dict[str, str], list[str]]:
    """({set target: adapter}, the `@every` goals) of the map files' texts."""
    sets, every = {}, []
    for text in texts:
        for raw in text.splitlines():
            words = raw.split("#", 1)[0].split()
            if len(words) == 3 and words[0] == "@units":
                sets[words[1]] = words[2]
            elif words[:1] == ["@every"]:
                every += words[1:]
    return sets, every


def seconds(row: dict) -> float:
    wall = row.get("wall_s")
    return float(wall) if isinstance(wall, (int, float)) and not isinstance(wall, bool) else 0.0


def tier_of(needs: bool, declared: str | None, named_slow: bool = False) -> str:
    """`unit` or `slow`: needs, then declared (`unit` or another marker), then the name (the heavy class); a price decides nothing."""
    if needs:
        return "slow"
    if declared:
        return "unit" if declared == "unit" else "slow"
    return "slow" if named_slow else "unit"


# ---- the driver ---------------------------------------------------------------------------------------------------
def selector():
    global SELECTOR
    if SELECTOR is None:
        sys.dont_write_bytecode = True
        spec = importlib.util.spec_from_file_location("kit_check_select_by_units", Path(__file__).resolve().parent / "check-select.py")
        SELECTOR = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(SELECTOR)
    return SELECTOR


def adapter_call(argv: list[str], capture: bool) -> tuple[int, str]:
    proc = subprocess.run(argv, stdout=subprocess.PIPE if capture else None, text=True)
    return proc.returncode, proc.stdout or ""


def call(argv: list[str], capture: bool = False) -> tuple[int, str]:
    return (ADAPTER or adapter_call)(argv, capture)


def adapter_of(cs, root: Path, stack: str) -> tuple[str, str]:
    """(the set target, its adapter) from the `@units` line of the stack's map file."""
    for path in cs.map_files(root, []):
        if path.stem == stack:
            for target, adapter in directives([cs.read(path)])[0].items():
                return target, adapter
    raise cs.Stop(2, f"{TAG} no `@units <target> <adapter>` line in a {stack}.map")


def dispatch(typed: list[str], filt: str) -> int:
    """`make test TESTS=<unit>...`: each typed unit goes to the stack that lists it; the worst exit of the stacks driven. Only a
    stack whose set target's map inputs hold a file the typed paths name is asked (FB-1202), or one no row maps (no inputs to judge)."""
    cs = selector()
    root = Path(cs.git_out("rev-parse", "--show-toplevel")).resolve()
    os.chdir(str(root))
    t, map_rows = cs.tree(root), cs.load_rows(root, [])
    plan, _ = cs.plan(t, map_rows, cs.load_registry(root, [], t.paths))
    named, files = {name for row in map_rows for name in row.targets}, set(t.under(typed))
    owners: dict[str, list[str]] = {}
    for path in cs.map_files(root, []):
        sets = directives([cs.read(path)])[0]
        for adapter in dict.fromkeys(adapter for target, adapter in sets.items()
                                     if (files & plan[target].inputs if target in plan else target not in named)):
            code, out = call([adapter, "units", "--list"], True)
            if code != 0:
                raise cs.Stop(2, f"{TAG} {adapter} units --list exited {code}")
            for unit in cs.parsed(out):
                if unit.get("id") and path.stem not in owners.setdefault(str(unit["id"]), []):
                    owners[str(unit["id"])].append(path.stem)
    for name in typed:
        if len(owners.get(name, [])) != 1:
            raise cs.Stop(2, f"{TAG} TESTS={name}: " + (f"listed by {' and '.join(owners[name])}; name one stack: check-units.py <stack> {name}"
                                                   if owners.get(name) else "no stack lists this unit"))
    worst = 0
    for stack in dict.fromkeys(owners[name][0] for name in typed):
        worst = max(worst, drive(stack, [name for name in typed if owners[name][0] == stack], filt))
    return worst


def drive(stack: str, typed: list[str], filt: str) -> int:
    cs = selector()
    root = Path(cs.git_out("rev-parse", "--show-toplevel")).resolve()
    os.chdir(str(root))
    tier = os.environ.get("KIT_TIER", "").strip() or "light"
    if tier not in ("light", "full", "sweep"):
        raise cs.Stop(2, f"{TAG} KIT_TIER={tier}: expected light, full or sweep")
    ask = "full" if typed or tier != "light" else "light"
    t, (target, adapter) = cs.tree(root), adapter_of(cs, root, stack)
    code, out = call([adapter, "units", "--list"], True)
    if code != 0:
        raise cs.Stop(2, f"{TAG} {adapter} units --list exited {code}")
    listed = {str(unit["id"]): unit for unit in cs.parsed(out) if unit.get("id")}
    if unknown := [name for name in typed if name not in listed]:
        raise cs.Stop(2, f"{TAG} {stack}: no unit {unknown[0]}")
    asked = {name.strip().removeprefix("./").strip("/") or "." for name in os.environ.get("KIT_CHECK_PATHS", "").split("\n") if name.strip()}
    dirs = {name for name in asked if name not in t.files and t.under([name])}  # FB-1204: a dir selects the units under it, both tiers
    changed = asked - dirs
    cover = cs.load_beside("cover-gate.py", "kit_cover_gate_by_units") if ask == "full" and not typed and tier != "sweep" else None
    floored = cover is not None and has_floors(cs, cover, stack)  # REUSE: a floored full ask reuses only a row that carries its measure
    set_inputs, todo, digests, parts, why, reused, left, held = None, [], {}, {}, {}, [], [], {}
    for uid in typed or listed:
        unit, name = listed[uid], f"{stack}:{uid}"
        if unit.get("inputs") is None and set_inputs is None:  # no list of its own: the inputs of the set's target
            plan, _ = cs.plan(t, cs.load_rows(root, []), cs.load_registry(root, [], t.paths))
            set_inputs = sorted(plan[target].inputs) if target in plan else t.paths
        inputs = t.under(unit["inputs"] if unit.get("inputs") is not None else set_inputs)
        if not typed and (dirs or ask == "light" and changed) and not (
                changed & set(inputs) or any(d == "." or uid == d or uid.startswith(d + "/") for d in dirs)):
            continue  # neither the diff nor a named dir reaches it; a unit never run stays for check-full
        parts[uid] = cs.digest_parts(name, inputs)
        digests[uid] = cs.key_of(parts[uid])
        hit = None if typed or tier == "sweep" else cs.green(digests[uid], ask)
        figure = measure_of(hit)
        if hit is not None and floored and figure is None:
            hit, why[uid] = None, "measure"  # a green row with no figure: the floor cannot be judged from it, so it reruns
        if hit is not None:
            reused.append(name)
            cs.say(f"{TAG} reused {name} green {hit.get('ts')}")
            if floored:
                held[uid] = figure
        elif ask == "light" and tier_of(bool(unit.get("needs")), unit.get("tier")) == "slow":
            left.append(name)
        else:
            todo.append(uid)
            why.setdefault(uid, "typed" if typed else "sweep" if tier == "sweep" else cs.miss(name, parts[uid], ask) or "other")
    for name in left:
        cs.say(f"{TAG} left for check-full: {name}")
    red = run(cs, stack, adapter, ask, tier == "sweep", todo, digests, parts, filt) if todo else []
    red += refused(cs, cover, stack, held, parts) if held else []
    tally = {reason: sum(1 for uid in todo if why[uid] == reason) for reason in sorted({why[uid] for uid in todo})}
    cs.say(f"{TAG} {stack} {tier} · ran {len(todo)}{' (' + ', '.join(f'{r} {n}' for r, n in tally.items()) + ')' if tally else ''}"
           f" · reused {len(reused)} · left {len(left)}")
    return 1 if red else 0


def has_floors(cs, cover, stack: str) -> bool:
    """Whether the stack has a floors file (cover-gate.py's table); a malformed one stops the run."""
    try:
        return cover.load_table(stack) is not None
    except cover.Usage as exc:
        raise cs.Stop(2, f"{TAG} {exc}")


def measure_of(row: dict | None) -> list[int] | None:
    """`[covered, total]` a green row carries from its adapter line, else None."""
    figure = (row or {}).get("cover")
    ok = isinstance(figure, list) and len(figure) == 2 and all(isinstance(n, int) and not isinstance(n, bool) for n in figure)
    return figure if ok and 0 <= figure[0] <= figure[1] else None


def refused(cs, cover, stack: str, held: dict[str, list[int]], parts: dict[str, dict[str, str]]) -> list[str]:
    """Hand the reused units' `<id> <covered> <total> reused` lines to cover-gate.py `check --stack <stack> --whole`, in process,
    and repeat its output. A unit it names in a refusal is red, with a red row that voids its green so the next run reruns it; when
    it refuses with no unit named (a usage error), every reused unit is."""
    text, out, err, stdin = "".join(f"{uid} {c} {t} reused\n" for uid, (c, t) in held.items()), io.StringIO(), io.StringIO(), sys.stdin
    sys.stdin = io.StringIO(text)
    try:
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = cover.main(["check", "--stack", stack, "--whole"])
    finally:
        sys.stdin = stdin
    for line in out.getvalue().splitlines():
        cs.say(line)
    for line in err.getvalue().splitlines():
        cs.say(line, err=True)
    named = [uid for uid in held if any(line.startswith(f"coverage: {uid}: ") for line in err.getvalue().splitlines())]
    red = named or (list(held) if code else [])
    run_id = f"{cs.utc_now():%Y%m%dT%H%M%SZ}-{os.getpid()}"
    for uid in red:
        cs.say(f"{TAG} red {stack}:{uid} (floor raised since its last green run: make test TESTS={uid})", err=True)
        cs.append({"run": run_id, "ts": cs.utc_now().isoformat(timespec="seconds"), "target": f"{stack}:{uid}", "digest": cs.key_of(parts[uid]),
                   "exit": 1, "wall_s": 0.0, "free": False, "log": None, "tier": "full", "skips": 0, "cached": False, "parts": parts[uid]})
    return [f"{stack}:{uid}" for uid in red]


def run(cs, stack: str, adapter: str, ask: str, free: bool, todo: list[str], digests: dict[str, str], parts: dict[str, dict[str, str]],
        filt: str) -> list[str]:
    """Run `todo` through the adapter, a unit row each (none under a filter), carrying the key's parts and the adapter's `cover`; the
    names that went red."""
    with tempfile.TemporaryDirectory(prefix="kit-units-") as scratch:
        out = Path(scratch) / "results.jsonl"
        code, _ = call([adapter, "units", "--run", "--tier", ask, "--out", str(out), *(["--filter", filt] if filt else []), *todo])
        results = {str(row.get("id")): row for row in cs.parsed(cs.read(out))}
    run_id, red = f"{cs.utc_now():%Y%m%dT%H%M%SZ}-{os.getpid()}", []
    for uid in todo:
        row = results.get(uid)
        if row is None or row.get("exit") != 0:
            red.append(f"{stack}:{uid}")
            cs.say(f"{TAG} red {stack}:{uid} (exit {row.get('exit') if row else code or 1})", err=True)
        if row is not None and not filt:
            cs.append({"run": run_id, "ts": cs.utc_now().isoformat(timespec="seconds"), "target": f"{stack}:{uid}",
                       "digest": digests[uid], "exit": row.get("exit"), "wall_s": seconds(row), "free": free, "log": None,
                       "tier": row["tier"] if row.get("tier") in ("light", "full") else ask,
                       "skips": row.get("skips", 0), "cached": bool(row.get("cached")), "parts": parts[uid],
                       **({"cover": row["cover"]} if measure_of(row) else {})})
    return red


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(prog="check-units.py", description="Run a stack's units not green on this content.")
    tests = argv[:1] == ["--tests"]
    if not tests:
        parser.add_argument("stack")
    parser.add_argument("units", nargs="*", help="run exactly these units, at full, whatever the store holds")
    parser.add_argument("--filter", default="", help="narrow the run inside each unit (RUN=, E2E_ARGS=); no row is written")
    args = parser.parse_args(argv[1:] if tests else argv)
    args.units = [u.strip().removeprefix("./").strip("/") or "." for u in args.units]  # FB-1292: `./internal/x` names the unit `internal/x`
    try:
        if tests:
            if not args.units:
                parser.error("--tests names the units to run: check-units.py --tests <unit>...")
            return dispatch(args.units, args.filter)
        return drive(args.stack, args.units, args.filter)
    except selector().Stop as stop:
        for line in stop.lines:
            selector().say(line, err=True)
        return stop.code


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
