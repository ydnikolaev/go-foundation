#!/usr/bin/env python3
"""units: the one reader of go-foundation's units (R-1..R-4).

    python3 scripts/units.py check     [--root DIR]   every rule below; exit 1 with one line per problem
    python3 scripts/units.py catalogue [--root DIR]   write UNITS.md (to $KIT_COMPOSE_OUT when set)
    python3 scripts/units.py list      [--root DIR]   one line per unit: id kind stability module

A unit is a root directory that holds a go.mod. Rules, each refused by name:
    unit.json    present, valid JSON; id equals the directory; kind in KINDS; stability in STABILITIES; stack "go";
                 summary non-empty; pairs a list of {stack, repo, unit} objects (contract optional)
    module path  go.mod's `module` line is MODULE_ROOT/<id>, or MODULE_ROOT/<id>/vN for N >= 2
    go floor     go.mod's `go` line is at most the floor in config/go-floor (R-4)
    go.work      lists every unit with `use ./<id>`, and nothing that is not a unit
    README.md    has the headings in README_HEADINGS, in that order
    releasenotes <id>/releasenotes/*.yaml exists; each file's `version:` line equals its file name
UNITS.md freshness is not judged here: config/modules.ini declares it `[compose] generated`, and kit-modules runs
`make units-catalogue` twice and compares, the one freshness mechanism devkit owns.

Stdlib only, python3 >= 3.9. No YAML parser: the release-note check reads the one `version:` line, not the document.
"""
import argparse
import json
import os
import re
import sys
from pathlib import Path

MODULE_ROOT = "github.com/ydnikolaev/go-foundation"
KINDS = ("primitive", "module")
STABILITIES = ("experimental", "stable", "deprecated")
README_HEADINGS = ("## Install", "## Wire", "## Configure", "## Extension points", "## Do not")
SKIP = {".git", ".cache", ".artifacts", ".devkit", ".claude", "docs", "config", "scripts", "mk"}


def units(root):
    out = []
    for d in sorted(root.iterdir()):
        if d.is_dir() and d.name not in SKIP and not d.name.startswith(".") and (d / "go.mod").is_file():
            out.append(d)
    return out


def version_tuple(v):
    m = re.fullmatch(r"(\d+)\.(\d+)(?:\.(\d+))?", v.strip())
    if not m:
        return None
    return tuple(int(x or 0) for x in m.groups())


def gomod_line(text, key):
    m = re.search(r"^%s\s+(\S+)\s*$" % key, text, re.M)
    return m.group(1) if m else None


def load_unit(d):
    """Return (meta, problems) for one unit directory."""
    probs = []
    name = d.name
    try:
        meta = json.loads((d / "unit.json").read_text())
    except FileNotFoundError:
        return None, ["%s: no unit.json; next: add one (see adoption/unit.json)" % name]
    except ValueError as e:
        return None, ["%s/unit.json: not valid JSON: %s" % (name, e)]
    if not isinstance(meta, dict):
        return None, ["%s/unit.json: not a JSON object" % name]
    if meta.get("id") != name:
        probs.append("%s/unit.json: id %r is not the directory name %r" % (name, meta.get("id"), name))
    if meta.get("kind") not in KINDS:
        probs.append("%s/unit.json: kind %r is not one of %s" % (name, meta.get("kind"), "|".join(KINDS)))
    if meta.get("stability") not in STABILITIES:
        probs.append("%s/unit.json: stability %r is not one of %s" % (name, meta.get("stability"), "|".join(STABILITIES)))
    if meta.get("stack") != "go":
        probs.append("%s/unit.json: stack %r is not \"go\"" % (name, meta.get("stack")))
    if not str(meta.get("summary", "")).strip():
        probs.append("%s/unit.json: summary is empty" % name)
    pairs = meta.get("pairs", [])
    if not isinstance(pairs, list):
        probs.append("%s/unit.json: pairs is not a list" % name)
    else:
        for i, p in enumerate(pairs):
            if not isinstance(p, dict) or not all(isinstance(p.get(k), str) and p.get(k) for k in ("stack", "repo", "unit")):
                probs.append("%s/unit.json: pairs[%d] needs non-empty stack, repo and unit" % (name, i))
    return meta, probs


def check_gomod(d, floor):
    probs = []
    text = (d / "go.mod").read_text()
    mod = gomod_line(text, "module")
    allowed = re.compile(r"%s/%s(/v([2-9]|[1-9]\d+))?" % (re.escape(MODULE_ROOT), re.escape(d.name)))
    if not mod or not allowed.fullmatch(mod):
        probs.append("%s/go.mod: module %r is not %s/%s" % (d.name, mod, MODULE_ROOT, d.name))
    gov = gomod_line(text, "go")
    gt = version_tuple(gov or "")
    if gt is None:
        probs.append("%s/go.mod: no comparable `go` line" % d.name)
    elif floor is not None and gt > floor:
        probs.append("%s/go.mod: go %s is above the floor %s in config/go-floor (R-4); a consumer on the floor could not build it"
                     % (d.name, gov, ".".join(map(str, floor))))
    return probs


def check_readme(d):
    try:
        text = (d / "README.md").read_text()
    except FileNotFoundError:
        return ["%s: no README.md" % d.name]
    pos = -1
    for h in README_HEADINGS:
        m = re.search(r"^%s\s*$" % re.escape(h), text, re.M)
        if not m or m.start() < pos:
            return ["%s/README.md: needs the headings %s in that order; %r is missing or out of order"
                    % (d.name, ", ".join(README_HEADINGS), h)]
        pos = m.start()
    return []


def check_notes(d):
    notes = sorted((d / "releasenotes").glob("*.yaml")) if (d / "releasenotes").is_dir() else []
    if not notes:
        return ["%s: no releasenotes/*.yaml; every release ships its adoption notes (R-5)" % d.name]
    probs = []
    for f in notes:
        v = re.search(r"^version:\s*['\"]?([^'\"\s]+)", f.read_text(), re.M)
        if not v or v.group(1) != f.stem:
            probs.append("%s/releasenotes/%s: version %r is not the file name %r"
                         % (d.name, f.name, v.group(1) if v else None, f.stem))
    return probs


def check_gowork(root, unit_dirs):
    try:
        text = (root / "go.work").read_text()
    except FileNotFoundError:
        return ["go.work: missing; next: go work init %s" % " ".join("./" + u.name for u in unit_dirs)]
    used = set()
    for block in re.findall(r"^use\s*\(([^)]*)\)", text, re.M):
        used.update(x.strip() for x in block.split() if x.strip())
    used.update(re.findall(r"^use\s+(\S+)\s*$", text, re.M))
    used.discard("(")
    want = {"./" + u.name for u in unit_dirs}
    probs = ["go.work: unit %s is not in `use`" % u for u in sorted(want - used)]
    probs += ["go.work: `use %s` is not a unit" % u for u in sorted(used - want)]
    return probs


def catalogue(root):
    rows = []
    for d in units(root):
        meta, _ = load_unit(d)
        meta = meta or {}
        pairs = ", ".join("%s:%s/%s" % (p.get("stack"), p.get("repo"), p.get("unit")) for p in meta.get("pairs", []) if isinstance(p, dict)) or "-"
        rows.append("| [`%s`](%s/README.md) | %s | %s | `%s/%s` | %s | %s |" % (
            d.name, d.name, meta.get("kind", "?"), meta.get("stability", "?"), MODULE_ROOT, d.name, pairs,
            str(meta.get("summary", "")).replace("|", "\\|")))
    head = ("# Units\n\nGENERATED by `make units-catalogue` from each `<unit>/unit.json`; never edit by hand.\n"
            "Who uses which unit at which version: [CONSUMERS.md](CONSUMERS.md).\n\n"
            "| unit | kind | stability | import path | pairs | summary |\n|---|---|---|---|---|---|\n")
    return head + "\n".join(rows) + ("\n" if rows else "")


def read_floor(root):
    try:
        raw = (root / "config" / "go-floor").read_text().split("#")[0].strip()
    except FileNotFoundError:
        return None, ["config/go-floor: missing (R-4); next: write the lowest consumer's go version, e.g. 1.25.0"]
    t = version_tuple(raw)
    if t is None:
        return None, ["config/go-floor: %r is not a version" % raw]
    return t, []


def check(root):
    floor, probs = read_floor(root)
    unit_dirs = units(root)
    if not unit_dirs:
        probs.append("no unit: a root directory with a go.mod")
    for d in unit_dirs:
        _, p = load_unit(d)
        probs += p
        probs += check_gomod(d, floor)
        probs += check_readme(d)
        probs += check_notes(d)
    probs += check_gowork(root, unit_dirs)
    return probs, len(unit_dirs)


def main(argv=None):
    ap = argparse.ArgumentParser(prog="units.py")
    ap.add_argument("verb", choices=("check", "catalogue", "list"))
    ap.add_argument("--root", default=".")
    a = ap.parse_args(argv)
    root = Path(a.root).resolve()
    if a.verb == "catalogue":
        out = os.environ.get("KIT_COMPOSE_OUT") or str(root / "UNITS.md")
        Path(out).write_text(catalogue(root))
        return 0
    if a.verb == "list":
        for d in units(root):
            meta, _ = load_unit(d)
            meta = meta or {}
            print(d.name, meta.get("kind", "?"), meta.get("stability", "?"), "%s/%s" % (MODULE_ROOT, d.name))
        return 0
    probs, n = check(root)
    if probs:
        for p in sorted(probs):
            print("units: " + p, file=sys.stderr)
        return 1
    print("units: %d unit(s), ok" % n)
    return 0


if __name__ == "__main__":
    sys.exit(main())
