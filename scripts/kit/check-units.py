#!/usr/bin/env python3
# managed by devkit — do not edit; fix upstream in ~/Developer/projects/devkit
"""kit-units: `python3 scripts/kit/check-units.py <stack> [--filter EXPR] [<unit>...]`, the one driver of a stack's unit set,
and `check-units.py --tests [--filter EXPR] <unit>...`, which finds each unit's stack; also the tier rule, the price and the cut that check-select.py loads by path.

A stack registers `@units <target> <adapter>` in .devkit/check-map.d/<stack>.map (the stack id is the file's stem); the set's
recipe is this driver. The adapter has two verbs: `<adapter> units --list` (one JSON line per unit: id, inputs, needs, tier)
and `<adapter> units --run --tier <light|full> --out FILE [--filter EXPR] <id>...` (one result line per unit: id, exit,
wall_s, cached, tests, skips, tier). A unit id is a repo-relative path, `<stack>:<id>` its target in the store.

A tier is derived, in this order: needs (docker, a browser, a database) is slow; else the declared marker; else a price over
UNIT_S seconds is slow. Price is the lowest wall of the green rows that ran among the unit's newest WINDOW rows (a cached row
is a green and never a price); none green: the highest wall, and a killed row is slow outright; no row: UNIT_S. A name decides
only a target with no row yet (check-select). A unit runs unless `green(digest, tier)` answers: a light row answers a light ask
only, a full row both; a full ask reuses none while the stack's coverage measure here is not of this tree (FB-1212), and the
digest holds the Go test tags (FB-1217). The light tier runs the unit-tier units cheapest first within the budget (UNITS_BUDGET_S,
else LIGHT_S of summed price) and prints `left for check-full: <stack>:<id>` for the rest; the full tier runs every unit with no full row.
A directory in KIT_CHECK_PATHS narrows either tier to the units under it (a file there narrows the light tier only).
A run with --filter proves no unit and writes no row; typed units always run, at full. `--tests` asks each stack that registers
`@units` and whose map inputs hold a file the typed paths name (or that no map row names) which units it lists, hands each typed
unit to the stack that lists it (none, or two: usage) and drives each stack with its own units; it is `make test TESTS=<unit>...`,
whose RUN= is the --filter. Exit: 0 green, 1 red, 2 usage.
Stdlib only, python3 >= 3.9, POSIX."""
from __future__ import annotations

import argparse
import importlib.util
import json
import math
import os
import subprocess
import sys
import tempfile
from pathlib import Path

TAG = "check-units:"
UNIT_S = 30  # a unit or target priced over this is slow; an unpriced one counts this much in the cut
LIGHT_S = 180  # the light tier's budget of summed price
WINDOW = 10  # the newest rows a price reads
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


def history(rows: list[dict]) -> dict[str, list[dict]]:
    """{target: its target rows, oldest first} of the store."""
    out: dict[str, list[dict]] = {}
    for row in rows:
        if isinstance(row.get("target"), str):
            out.setdefault(row["target"], []).append(row)
    return out


def seconds(row: dict) -> float:
    wall = row.get("wall_s")
    return float(wall) if isinstance(wall, (int, float)) and not isinstance(wall, bool) else 0.0


def price(rows: list[dict]) -> float | None:
    """Seconds from the newest WINDOW rows of a target; None with no measure, infinity for a killed run with no green."""
    recent = rows[-WINDOW:]
    green = [row for row in recent if row.get("exit") == 0]
    ran = [row for row in green if not row.get("cached")]
    if ran:
        return min(seconds(row) for row in ran)
    if green or not recent:
        return None
    return math.inf if any(row.get("exit") is None for row in recent) else max(seconds(row) for row in recent)


def cost(rows: list[dict]) -> float:
    """What the cut counts for a target: its price, UNIT_S when it has none."""
    found = price(rows)
    return UNIT_S if found is None else found


def tier_of(needs: bool, declared: str | None, rows: list[dict], named_slow: bool = False) -> str:
    """`unit` or `slow`: needs, then declared (`unit` or another marker), then price; a target with no row starts as its name says."""
    if needs:
        return "slow"
    if declared:
        return "unit" if declared == "unit" else "slow"
    found = price(rows)
    return ("slow" if named_slow else "unit") if found is None else "slow" if found > UNIT_S else "unit"


def cut(costs: dict[str, float], budget: float) -> tuple[list[str], list[str], float]:
    """(taken, left, summed): the names of `costs` cheapest first while their summed seconds stay within `budget`."""
    taken, left, used = [], [], 0.0
    for name in sorted(costs, key=costs.get):
        if used + costs[name] <= budget:
            taken.append(name)
            used += costs[name]
        else:
            left.append(name)
    return taken, left, used


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
    book = history(cs.rows())
    asked = {name.strip().removeprefix("./").strip("/") or "." for name in os.environ.get("KIT_CHECK_PATHS", "").split("\n") if name.strip()}
    dirs = {name for name in asked if name not in t.files and t.under([name])}  # FB-1204: a dir selects the units under it, both tiers
    changed = asked - dirs
    set_inputs, stale = None, tier == "full" and target in cs.unmeasured()  # FB-1212: no unit reuse while the measure is stale
    todo, digests, reused, left, costs = [], {}, [], [], {}
    for uid in typed or listed:
        unit, name = listed[uid], f"{stack}:{uid}"
        if unit.get("inputs") is None and set_inputs is None:  # no list of its own: the inputs of the set's target
            plan, _ = cs.plan(t, cs.load_rows(root, []), cs.load_registry(root, [], t.paths))
            set_inputs = sorted(plan[target].inputs) if target in plan else t.paths
        inputs = t.under(unit["inputs"] if unit.get("inputs") is not None else set_inputs)
        if not typed and (dirs or ask == "light" and changed) and not (
                changed & set(inputs) or any(d == "." or uid == d or uid.startswith(d + "/") for d in dirs)):
            continue  # neither the diff nor a named dir reaches it; a unit never run stays for check-full
        digests[uid] = cs.digest(name, inputs)
        hit = None if typed or tier == "sweep" or stale else cs.green(digests[uid], ask)
        if hit is not None:
            reused.append(name)
            cs.say(f"{TAG} reused {name} green {hit.get('ts')}")
        elif typed or ask == "full":
            todo.append(uid)
        elif tier_of(bool(unit.get("needs")), unit.get("tier"), book.get(name, [])) == "slow":
            left.append(name)
        else:
            costs[uid] = cost(book.get(name, []))
    if ask == "light":
        try:
            budget = float(os.environ.get("UNITS_BUDGET_S", "") or LIGHT_S)
        except ValueError:
            raise cs.Stop(2, f"{TAG} UNITS_BUDGET_S={os.environ['UNITS_BUDGET_S']}: expected a number of seconds")
        taken, over, _used = cut(costs, budget)
        todo, left = [uid for uid in listed if uid in set(taken)], left + [f"{stack}:{uid}" for uid in over]
    for name in left:
        cs.say(f"{TAG} left for check-full: {name}")
    red = run(cs, stack, adapter, ask, tier == "sweep", todo, digests, filt) if todo else []
    cs.say(f"{TAG} {stack} {tier} · ran {len(todo)} · reused {len(reused)} · left {len(left)}")
    return 1 if red else 0


def run(cs, stack: str, adapter: str, ask: str, free: bool, todo: list[str], digests: dict[str, str], filt: str) -> list[str]:
    """Run `todo` through the adapter, a unit row each (none under a filter); the names that went red."""
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
                       "skips": row.get("skips", 0), "cached": bool(row.get("cached"))})
    return red


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(prog="check-units.py", description="Run a stack's units not green on this content.")
    tests = argv[:1] == ["--tests"]
    if not tests:
        parser.add_argument("stack")
    parser.add_argument("units", nargs="*", help="run exactly these units, at full, whatever the store holds")
    parser.add_argument("--filter", default="", help="narrow the run inside each unit (RUN=, E2E_ARGS=); no row is written")
    args = parser.parse_args(argv[1:] if tests else argv)
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
