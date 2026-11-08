#!/usr/bin/env python3
"""consumers: who uses which unit at which version, derived, never kept by hand (R-5).

    python3 scripts/consumers.py generate [--list FILE] [--out FILE] [--gh GH]   write the registry table
    python3 scripts/consumers.py show     [--list FILE] [--gh GH]                print it to stdout

The one hand-kept input is the consumer list: where to look, never what is there. It names private repositories, so
it lives in the host registry, never in this public repository (R-5, R-6): --list, else $FOUNDATION_CONSUMERS, else
~/Developer/_ops/registry/foundation-consumers.txt. The table goes to --out, else the same directory's
foundation-consumers.md, and is committed there by the host registry's owner, never here. Each row is
`<owner/repo> [<go.mod path> ...]` (default: go.mod at the repo root). For each row it reads:
    imports  every go.mod named, through `gh api repos/<repo>/contents/<path>`, for
             `github.com/ydnikolaev/go-foundation/units/<unit>[/vN] <version>` requires
    copies   a code search for the stamp `go-foundation-copy: <unit>@<sha>` in that repo: code that copies a unit
             instead of importing it. A copy is entropy: it is listed until it is gone (R-5).
The latest version of each unit is the highest local tag `units/<unit>/vX.Y.Z` (fetch tags first). Status per cell:
current, behind (names latest), ahead (a tag this checkout lacks: fetch), copy. A unit no row uses is `unused`.

Network and gh auth are required, so `make consumers` is never in `make check`; run it at a unit release.
--gh names the gh executable (the test passes a fake). A repo gh cannot read is a named refusal, never a blank row.
"""
import argparse
import base64
import json
import os
import re
import subprocess
import sys
from pathlib import Path

MODULE_ROOT = "github.com/ydnikolaev/go-foundation"
REQ = re.compile(r"^\s*(?:require\s+)?%s/units/([a-z0-9][a-z0-9-]*)(?:/v\d+)?\s+(v\S+)" % re.escape(MODULE_ROOT), re.M)
STAMP = re.compile(r"go-foundation-copy:\s*([a-z0-9][a-z0-9-]*)@([0-9a-f]{7,40})")


def semver_key(v):
    m = re.fullmatch(r"v?(\d+)\.(\d+)\.(\d+)(-[0-9A-Za-z.-]+)?", v)
    if not m:
        return None
    return (int(m.group(1)), int(m.group(2)), int(m.group(3)), 0 if m.group(4) else 1, m.group(4) or "")


class GhError(Exception):
    pass


def gh(exe, *args):
    r = subprocess.run([exe, "api", *args], capture_output=True, text=True)
    if r.returncode != 0:
        raise GhError("gh api %s: exit %d: %s" % (" ".join(args), r.returncode, r.stderr.strip()[:300]))
    return json.loads(r.stdout or "null")


DEFAULT_LIST = "~/Developer/_ops/registry/foundation-consumers.txt"


def list_path(arg):
    return Path(os.path.expanduser(arg or os.environ.get("FOUNDATION_CONSUMERS") or DEFAULT_LIST))


def read_rows(path):
    rows = []
    try:
        lines = path.read_text().splitlines()
    except FileNotFoundError:
        raise SystemExit("consumers: %s: missing; the consumer list lives in the host registry (R-5)" % path)
    for n, line in enumerate(lines, 1):
        line = line.split("#")[0].strip()
        if not line:
            continue
        words = line.split()
        if not re.fullmatch(r"[\w.-]+/[\w.-]+", words[0]):
            raise SystemExit("consumers: %s:%d: %r is not <owner/repo>" % (path.name, n, words[0]))
        rows.append((words[0], words[1:] or ["go.mod"]))
    return rows


def latest_tags(root):
    r = subprocess.run(["git", "-C", str(root), "tag", "-l", "units/*/v*"], capture_output=True, text=True, check=True)
    best = {}
    for t in r.stdout.split():
        unit, _, v = t[len("units/"):].partition("/")
        k = semver_key(v)
        if k and (unit not in best or k > semver_key(best[unit])):
            best[unit] = v
    return best


def units(root):
    base = root / "units"
    return sorted(d.name for d in base.iterdir() if d.is_dir() and (d / "unit.json").is_file()) if base.is_dir() else []


def scan(exe, repo, paths):
    """{unit: [(kind, value, where)]} for one consumer repo."""
    found = {}
    for p in paths:
        doc = gh(exe, "repos/%s/contents/%s" % (repo, p))
        text = base64.b64decode(doc["content"]).decode()
        for unit, ver in REQ.findall(text):
            found.setdefault(unit, []).append(("import", ver, p))
    res = gh(exe, "-H", "Accept: application/vnd.github.text-match+json", "-X", "GET", "search/code",
             "-f", "q=go-foundation-copy repo:%s" % repo)
    for item in (res or {}).get("items", []):
        for tm in item.get("text_matches", []):
            for unit, sha in STAMP.findall(tm.get("fragment", "")):
                found.setdefault(unit, []).append(("copy", sha, item.get("path", "?")))
    return found


def status(kind, value, latest):
    if kind == "copy":
        return "copy @%s" % value
    if not latest:
        return "%s (no local tag: fetch tags)" % value
    a, b = semver_key(value), semver_key(latest)
    if a is None:
        return "%s (not semver)" % value
    if a == b:
        return "%s current" % value
    return "%s behind (latest %s)" % (value, latest) if a < b else "%s ahead (fetch tags)" % value


def render(root, exe, lst):
    rows = read_rows(lst)
    latest = latest_tags(root)
    names = units(root)
    cells, errors = {}, []
    for repo, paths in rows:
        try:
            for unit, uses in scan(exe, repo, paths).items():
                for kind, value, where in uses:
                    cells.setdefault(unit, []).append((repo, status(kind, value, latest.get(unit)), where))
        except (GhError, KeyError, ValueError) as e:
            errors.append("%s: %s" % (repo, e))
    if errors:
        raise SystemExit("consumers: refusing a partial registry:\n  " + "\n  ".join(errors))
    out = ["# Consumers", "",
           "GENERATED by go-foundation `make consumers` from each repo in %s (go.mod requires and" % lst.name,
           "`go-foundation-copy:` stamps, read through gh); never edit by hand. A `copy` is entropy: replace it",
           "with an import, then delete the stamp.", "",
           "| unit | latest | repo | status | where |", "|---|---|---|---|---|"]
    for unit in sorted(set(names) | set(cells)):
        uses = cells.get(unit)
        if not uses:
            out.append("| `%s` | %s | - | unused | - |" % (unit, latest.get(unit, "untagged")))
            continue
        for repo, st, where in sorted(uses):
            out.append("| `%s` | %s | %s | %s | `%s` |" % (unit, latest.get(unit, "untagged"), repo, st, where))
    return "\n".join(out) + "\n"


def main(argv=None):
    ap = argparse.ArgumentParser(prog="consumers.py")
    ap.add_argument("verb", choices=("generate", "show"))
    ap.add_argument("--gh", default="gh")
    ap.add_argument("--root", default=".")
    ap.add_argument("--list", default=None)
    ap.add_argument("--out", default=None)
    a = ap.parse_args(argv)
    root = Path(a.root).resolve()
    lst = list_path(a.list)
    text = render(root, a.gh, lst)
    if a.verb == "show":
        sys.stdout.write(text)
        return 0
    Path(os.path.expanduser(a.out) if a.out else lst.with_suffix(".md")).write_text(text)
    return 0


if __name__ == "__main__":
    sys.exit(main())
