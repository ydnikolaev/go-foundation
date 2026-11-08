#!/usr/bin/env python3
"""public-scan: nothing this public repository publishes names a private repo, a host path, a personal email or a
secret (R-6). Run before any push or visibility change: `make public-scan`. It needs gh, so public CI never runs it.

    python3 scripts/public-scan.py scan [--tree | --history] [--root DIR] [--gh EXE] [--gitleaks EXE]

Where it looks:
    tree     every file git would publish from the working tree (`git ls-files -co --exclude-standard`): path, content
    history  every blob reachable from any ref (`git rev-list --all --objects`) with its path, every commit message,
             every tag's name and message
`scan` with neither flag runs both.

Rules, each hit printed as `<rule> <where>:<line>:<col>: <match>`:
    name       a private repository name of the owner, derived at run time (`gh repo list <owner> --visibility
               private`), so a new private repo is covered the day it exists and no list of private names is ever
               committed here. PUBLIC_DENYLIST=<file> (one name per line) replaces the gh call: the teeth use it.
               Whole-word, case-insensitive: `zorb` does not match `zorbing`.
    host-path  an absolute home path, `/Users/` or `/home/`
    email      an email address, except a noreply one and `git@<host>` (an ssh remote)
    secret     gitleaks default rules: `gitleaks dir` over a copy of the tree's publishable files (a gitignored module
               cache is not judged), `gitleaks git --log-opts=--all` over history
config/public-allowlist.txt, the one place an exception lives:
    name <word>                          a name never refused (public repos, the owner's handle, the tooling)
    exempt <path> <rule> <reason...>     one rule off for one tracked path; the reason is mandatory
Commit author and committer fields are not judged: R-6 says why.
Exit 0 clean, 1 a hit, 2 the scan could not run (no git, gh or gitleaks). Stdlib only, python3 >= 3.9.
"""
import argparse
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

OWNER = "ydnikolaev"
ALLOWLIST = "config/public-allowlist.txt"
RULES = ("name", "host-path", "email", "secret")
HOST_PATH = re.compile(r"/(?:Users|home)/[^\s/'\"`)]+")
EMAIL = re.compile(r"[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}")


class ScanError(Exception):
    pass


def read_allowlist(root):
    names, exempt = set(), {}
    p = root / ALLOWLIST
    try:
        lines = p.read_text().splitlines()
    except FileNotFoundError:
        raise ScanError("%s: missing" % ALLOWLIST)
    for n, raw in enumerate(lines, 1):
        line = raw.split("#")[0].strip()
        if not line:
            continue
        w = line.split()
        if w[0] == "name" and len(w) == 2:
            names.add(w[1].lower())
        elif w[0] == "exempt" and len(w) >= 4 and w[2] in RULES:
            exempt.setdefault(w[1], set()).add(w[2])
        else:
            raise ScanError("%s:%d: want `name <word>` or `exempt <path> <rule> <reason>` (rule: %s)"
                            % (ALLOWLIST, n, "|".join(RULES)))
    return names, exempt


def denied_names(gh_exe, allowed):
    override = os.environ.get("PUBLIC_DENYLIST")
    if override:
        names = [l.split("#")[0].strip() for l in Path(override).read_text().splitlines()]
    else:
        if not shutil.which(gh_exe):
            raise ScanError("gh not found (%s): the private-repo list is derived through it" % gh_exe)
        r = subprocess.run([gh_exe, "repo", "list", OWNER, "--visibility", "private", "--limit", "1000",
                            "--json", "name"], capture_output=True, text=True)
        if r.returncode != 0:
            raise ScanError("gh repo list: exit %d: %s" % (r.returncode, r.stderr.strip()[:300]))
        names = [x["name"] for x in json.loads(r.stdout)]
    names = sorted({n.lower() for n in names if n} - allowed)
    if not names:
        raise ScanError("no private repo name to refuse: a gate with nothing to refuse judges nothing")
    return names


def matcher(names):
    alt = "|".join(re.escape(n) for n in sorted(names, key=len, reverse=True))
    return re.compile(r"(?<![a-z0-9])(?:%s)(?![a-z0-9])" % alt, re.I)


def email_ok(m):
    return "noreply" in m.lower() or m.lower().startswith("git@")


def judge(where, path, text, name_re, exempt, out):
    off = exempt.get(path, set()) if path else set()
    for ln, line in enumerate(text.splitlines(), 1):
        if "name" not in off:
            for m in name_re.finditer(line):
                out.append("name %s:%d:%d: %s" % (where, ln, m.start() + 1, m.group()))
        if "host-path" not in off:
            for m in HOST_PATH.finditer(line):
                out.append("host-path %s:%d:%d: %s" % (where, ln, m.start() + 1, m.group()))
        if "email" not in off:
            for m in EMAIL.finditer(line):
                if not email_ok(m.group()):
                    out.append("email %s:%d:%d: %s" % (where, ln, m.start() + 1, m.group()))


def git(root, *args):
    r = subprocess.run(["git", "-C", str(root), *args], capture_output=True, text=True)
    if r.returncode != 0:
        raise ScanError("git %s: exit %d: %s" % (" ".join(args), r.returncode, r.stderr.strip()[:300]))
    return r.stdout


def decode(blob):
    if b"\0" in blob[:8192]:
        return None
    return blob.decode("utf-8", errors="replace")


def tree_files(root):
    raw = git(root, "ls-files", "-z", "-co", "--exclude-standard")
    return [p for p in raw.split("\0") if p and (root / p).is_file()]


def scan_tree(root, name_re, exempt):
    out = []
    for p in tree_files(root):
        judge("path", None, p, name_re, {}, out)
        text = decode((root / p).read_bytes())
        if text is not None:
            judge(p, p, text, name_re, exempt, out)
    return out


def scan_history(root, name_re, exempt):
    out = []
    first = {}
    for line in git(root, "rev-list", "--all", "--objects").splitlines():
        sha, _, path = line.partition(" ")
        if path and sha not in first:
            first[sha] = path
    if first:
        r = subprocess.run(["git", "-C", str(root), "cat-file", "--batch"],
                           input=("\n".join(first) + "\n").encode(), capture_output=True)
        if r.returncode != 0:
            raise ScanError("git cat-file --batch: exit %d" % r.returncode)
        data, i = r.stdout, 0
        while i < len(data):
            nl = data.index(b"\n", i)
            sha, typ, size = data[i:nl].decode().split()
            body = data[nl + 1:nl + 1 + int(size)]
            i = nl + 1 + int(size) + 1
            path = first[sha]
            judge("path", None, path, name_re, {}, out)
            text = decode(body) if typ == "blob" else None
            if text is not None:
                judge("blob %s (%s)" % (sha[:12], path), path, text, name_re, exempt, out)
    for rec in git(root, "log", "--all", "--format=%H%x00%B%x1e").split("\x1e"):
        rec = rec.strip("\n")
        if rec:
            sha, _, msg = rec.partition("\x00")
            judge("commit %s" % sha[:12], None, msg, name_re, {}, out)
    for rec in git(root, "for-each-ref", "refs/tags", "--format=%(refname)%00%(contents)%1e").split("\x1e"):
        rec = rec.strip("\n")
        if rec:
            ref, _, msg = rec.partition("\x00")
            judge(ref, None, ref + "\n" + msg, name_re, {}, out)
    return sorted(set(out))


def gitleaks(exe, *args):
    if not shutil.which(exe):
        raise ScanError("gitleaks not found (%s); next: brew install gitleaks" % exe)
    r = subprocess.run([exe, *args, "--no-banner", "--redact", "--exit-code", "1"], capture_output=True, text=True)
    if r.returncode not in (0, 1):
        raise ScanError("gitleaks %s: exit %d: %s" % (args[0], r.returncode, r.stderr.strip()[-300:]))
    return r.returncode == 1, (r.stdout + r.stderr).strip()


def secrets_tree(root, exe, exempt):
    with tempfile.TemporaryDirectory(prefix="public-scan.") as t:
        for p in tree_files(root):
            if "secret" in exempt.get(p, set()):
                continue
            dst = Path(t) / p
            dst.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(root / p, dst)
        found, report = gitleaks(exe, "dir", t)
    return ["secret tree: gitleaks found a secret:\n" + report.replace(t + "/", "")] if found else []


def secrets_history(root, exe):
    found, report = gitleaks(exe, "git", str(root), "--log-opts=--all")
    return ["secret history: gitleaks found a secret:\n" + report] if found else []


def main(argv=None):
    ap = argparse.ArgumentParser(prog="public-scan.py")
    ap.add_argument("verb", choices=("scan",))
    ap.add_argument("--tree", action="store_true")
    ap.add_argument("--history", action="store_true")
    ap.add_argument("--root", default=".")
    ap.add_argument("--gh", default="gh")
    ap.add_argument("--gitleaks", default="gitleaks")
    a = ap.parse_args(argv)
    root = Path(a.root).resolve()
    tree, hist = (a.tree or not a.history), (a.history or not a.tree)
    try:
        allowed, exempt = read_allowlist(root)
        names = denied_names(a.gh, allowed)
        name_re = matcher(names)
        probs = []
        if tree:
            probs += scan_tree(root, name_re, exempt) + secrets_tree(root, a.gitleaks, exempt)
        if hist:
            probs += scan_history(root, name_re, exempt) + secrets_history(root, a.gitleaks)
    except (ScanError, OSError, ValueError) as e:
        print("public-scan: cannot scan: %s" % e, file=sys.stderr)
        return 2
    scope = "+".join(x for x, on in (("tree", tree), ("history", hist)) if on)
    if probs:
        for p in probs:
            print("public-scan: " + p, file=sys.stderr)
        print("public-scan: %d problem(s) in %s; this repository is public (R-6)" % (len(probs), scope),
              file=sys.stderr)
        return 1
    print("public-scan: %s clean (%d private names, host paths, emails, gitleaks default rules)" % (scope, len(names)))
    return 0


if __name__ == "__main__":
    sys.exit(main())
