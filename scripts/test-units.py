#!/usr/bin/env python3
"""Teeth for scripts/units.py: a good tree passes, and each rule refuses one specific wrong thing by name."""
import json
import os
import subprocess
import sys
import tempfile
from pathlib import Path

HERE = Path(__file__).resolve().parent
UNITS = HERE / "units.py"
README = "# x\n\n## Install\n\n## Wire\n\n## Configure\n\n## Extension points\n\n## Do not\n"


def good(root):
    (root / "config").mkdir()
    (root / "config" / "go-floor").write_text("1.25.0  # floor\n")
    u = root / "clock"
    (u / "releasenotes").mkdir(parents=True)
    (u / "go.mod").write_text("module github.com/ydnikolaev/go-foundation/clock\n\ngo 1.25.0\n")
    (u / "unit.json").write_text(json.dumps({
        "id": "clock", "kind": "primitive", "stack": "go", "summary": "A clock.", "stability": "experimental",
        "pairs": [{"stack": "nuxt", "repo": "ydnikolaev/nuxt-foundation", "unit": "clock"}], "provenance": {}}))
    (u / "README.md").write_text(README)
    (u / "releasenotes" / "0.1.0.yaml").write_text("schema: x\nversion: 0.1.0\n")
    (root / "go.work").write_text("go 1.25.0\n\nuse ./clock\n")


def run(root, *verb):
    return subprocess.run([sys.executable, str(UNITS), *(verb or ("check",)), "--root", str(root)],
                          capture_output=True, text=True)


def edit_json(root, **kv):
    p = root / "clock" / "unit.json"
    d = json.loads(p.read_text())
    d.update(kv)
    p.write_text(json.dumps(d))


CASES = [
    ("no unit.json", lambda r: (r / "clock" / "unit.json").unlink(), "no unit.json"),
    ("id is not the dir", lambda r: edit_json(r, id="time"), "is not the directory name"),
    ("kind outside enum", lambda r: edit_json(r, kind="component"), "kind 'component'"),
    ("stability outside enum", lambda r: edit_json(r, stability="beta"), "stability 'beta'"),
    ("empty summary", lambda r: edit_json(r, summary=" "), "summary is empty"),
    ("pair without unit", lambda r: edit_json(r, pairs=[{"stack": "nuxt", "repo": "x"}]), "pairs[0]"),
    ("wrong module path", lambda r: (r / "clock" / "go.mod").write_text(
        "module github.com/ydnikolaev/kit/clock\n\ngo 1.25.0\n"), "module 'github.com/ydnikolaev/kit/clock'"),
    ("go above floor", lambda r: (r / "clock" / "go.mod").write_text(
        "module github.com/ydnikolaev/go-foundation/clock\n\ngo 1.27\n"), "above the floor"),
    ("README heading missing", lambda r: (r / "clock" / "README.md").write_text(README.replace("## Wire\n", "")),
     "'## Wire' is missing"),
    ("no release notes", lambda r: (r / "clock" / "releasenotes" / "0.1.0.yaml").unlink(), "no releasenotes"),
    ("note version mismatch", lambda r: (r / "clock" / "releasenotes" / "0.1.0.yaml").write_text("version: 0.2.0\n"),
     "is not the file name"),
    ("unit missing from go.work", lambda r: (r / "go.work").write_text("go 1.25.0\n"), "unit ./clock is not in `use`"),
    ("go.work names a non-unit", lambda r: (r / "go.work").write_text("go 1.25.0\n\nuse (\n\t./clock\n\t./gone\n)\n"),
     "`use ./gone` is not a unit"),
    ("no floor", lambda r: (r / "config" / "go-floor").unlink(), "config/go-floor: missing"),
]


def main():
    failed = 0
    with tempfile.TemporaryDirectory() as t:
        root = Path(t) / "ok"
        root.mkdir()
        good(root)
        r = run(root)
        if r.returncode != 0:
            print("FAIL good tree refused:\n" + r.stderr)
            failed += 1
        out = Path(t) / "cat.md"
        r = subprocess.run([sys.executable, str(UNITS), "catalogue", "--root", str(root)],
                           env={**os.environ, "KIT_COMPOSE_OUT": str(out)}, capture_output=True, text=True)
        if r.returncode != 0 or "| [`clock`](clock/README.md) | primitive |" not in out.read_text() \
                or (root / "UNITS.md").exists():
            print("FAIL catalogue did not honour KIT_COMPOSE_OUT or lacks the clock row")
            failed += 1
        for i, (name, mutate, want) in enumerate(CASES):
            root = Path(t) / ("c%d" % i)
            root.mkdir()
            good(root)
            mutate(root)
            r = run(root)
            if r.returncode == 0 or want not in r.stderr:
                print("FAIL %s: exit %d, wanted %r in:\n%s" % (name, r.returncode, want, r.stderr))
                failed += 1
    if failed:
        print("test-units: %d failure(s)" % failed)
        return 1
    print("test-units: %d refusal(s) and the good tree, ok" % len(CASES))
    return 0


if __name__ == "__main__":
    sys.exit(main())
