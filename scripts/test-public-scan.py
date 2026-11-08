#!/usr/bin/env python3
"""Teeth for scripts/public-scan.py, offline: PUBLIC_DENYLIST replaces gh, so no real private name sits here.

Each case builds a throwaway git repo, plants one wrong thing and wants a refusal naming it; the clean repo, a
substring, an allowlisted name and an exempt path must pass. The secret is generated at run time and never committed.
"""
import os
import random
import string
import subprocess
import sys
import tempfile
from pathlib import Path

HERE = Path(__file__).resolve().parent
TOOL = HERE / "public-scan.py"
FAKE = "zorblat"  # the planted private name; listed only in the throwaway denylist below
# Fixtures are assembled at run time so this file never trips the scan it tests.
HOME = "/" + "Users/"
MAIL = "me" + "@" + "example.com"
ALLOW = "name ydnikolaev\nname openthing\n"


def git(root, *args):
    env = {**os.environ, "GIT_CONFIG_GLOBAL": os.devnull, "GIT_CONFIG_NOSYSTEM": "1"}
    subprocess.run(["git", "-C", str(root), "-c", "user.name=t", "-c", "user.email=t@users.noreply.github.com",
                    "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false", *args],
                   check=True, capture_output=True, env=env)


def repo(t, name, files, msg="x", allow=ALLOW):
    root = Path(t) / name
    (root / "config").mkdir(parents=True)
    (root / "config" / "public-allowlist.txt").write_text(allow)
    for p, text in files.items():
        (root / p).parent.mkdir(parents=True, exist_ok=True)
        (root / p).write_text(text)
    git(root, "init", "-q")
    git(root, "add", "-A")
    git(root, "commit", "-q", "-m", msg)
    return root


def scan(root, deny, *flags):
    env = {**os.environ, "PUBLIC_DENYLIST": str(deny)}
    return subprocess.run([sys.executable, str(TOOL), "scan", "--root", str(root), *flags],
                          capture_output=True, text=True, env=env)


def token():
    rnd = random.SystemRandom()
    return "ghp_" + "".join(rnd.choice(string.ascii_letters + string.digits) for _ in range(36))


def main():
    failed = []

    def want(name, r, code, needle=None):
        if r.returncode != code or (needle and needle not in r.stdout + r.stderr):
            failed.append("%s: exit %d (want %d), wanted %r in:\n%s%s" % (name, r.returncode, code, needle, r.stdout, r.stderr))

    with tempfile.TemporaryDirectory() as t:
        deny = Path(t) / "deny.txt"
        deny.write_text(FAKE + "\nopenthing\n")
        ok = {"a.md": "Uses openthing and ydnikolaev; zorblatty is another word.\nssh: git@github.com:o/r\n"}

        want("clean repo passes", scan(repo(t, "clean", ok), deny), 0, "clean")
        want("planted name refused", scan(repo(t, "name", {"a.md": "see the Zorblat repo\n"}), deny), 1,
             "name a.md:1:9: Zorblat")
        want("name in a path refused", scan(repo(t, "path", {"zorblat/x.md": "x\n"}), deny, "--tree"), 1,
             "name path:1:1: zorblat")
        want("host path refused", scan(repo(t, "home", {"a.md": "src: %ssomeone/x\n" % HOME}), deny), 1,
             "host-path a.md:1:6: %ssomeone" % HOME)
        want("email refused", scan(repo(t, "mail", {"a.md": "mail %s\n" % MAIL}), deny), 1,
             "email a.md:1:6: " + MAIL)
        want("noreply email passes", scan(repo(t, "noreply", {"a.md": "Co-Authored-By: x <noreply@example.com>\n"}),
                                          deny), 0)
        want("generated secret refused", scan(repo(t, "secret", {"a.md": "token = %s\n" % token()}), deny, "--tree"),
             1, "secret tree")
        want("name in a commit message refused", scan(repo(t, "msg", ok, msg="port it for zorblat"), deny,
                                                      "--history"), 1, "name commit ")
        want("exempt path passes its rule only",
             scan(repo(t, "exempt", {"m.txt": HOME + "x\n"}, allow=ALLOW + "exempt m.txt host-path managed upstream\n"),
                  deny), 0)
        want("exempt without a reason refused",
             scan(repo(t, "badexempt", ok, allow=ALLOW + "exempt m.txt host-path\n"), deny), 2, "public-allowlist.txt:3")

        root = repo(t, "hist", {"a.md": "zorblat\n"})
        (root / "a.md").write_text("gone\n")
        git(root, "commit", "-q", "-am", "y")
        want("tree passes once HEAD is clean", scan(root, deny, "--tree"), 0)
        want("history still refuses the old blob", scan(root, deny, "--history"), 1, "name blob ")

        root = repo(t, "ignored", {".gitignore": "cache/\n", "a.md": "x\n"})
        (root / "cache").mkdir()
        (root / "cache" / "x.md").write_text("zorblat %s\n" % token())
        want("gitignored files are not judged", scan(root, deny, "--tree"), 0)

        empty = Path(t) / "empty.txt"
        empty.write_text("openthing\n")
        want("a denylist the allowlist empties is refused", scan(repo(t, "nothing", ok), empty), 2,
             "nothing to refuse")

    for f in failed:
        print("FAIL " + f)
    if failed:
        print("test-public-scan: %d failure(s)" % len(failed))
        return 1
    print("test-public-scan: 14 cases (name, path, host path, email, secret, history, exemptions), ok")
    return 0


if __name__ == "__main__":
    sys.exit(main())
