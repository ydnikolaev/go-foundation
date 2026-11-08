#!/usr/bin/env python3
"""Teeth for scripts/consumers.py, offline: a fake gh answers go.mod reads and the code search."""
import base64
import json
import os
import subprocess
import sys
import tempfile
from pathlib import Path

HERE = Path(__file__).resolve().parent
TOOL = HERE / "consumers.py"

FAKE = r'''#!/usr/bin/env python3
import base64, json, sys
a = sys.argv[1:]
data = json.load(open(%r))
if "search/code" in a:
    q = a[a.index("-f") + 1]
    repo = q.split("repo:")[1]
    print(json.dumps({"items": data["search"].get(repo, [])}))
    sys.exit(0)
path = a[1]
if path not in data["files"]:
    sys.stderr.write("HTTP 404: Not Found\n"); sys.exit(1)
print(json.dumps({"content": base64.b64encode(data["files"][path].encode()).decode()}))
'''


def git(root, *args):
    env = {**os.environ, "GIT_CONFIG_GLOBAL": os.devnull, "GIT_CONFIG_NOSYSTEM": "1"}
    subprocess.run(["git", "-C", str(root), "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false",
                    "-c", "tag.gpgsign=false", *args], check=True, capture_output=True, env=env)


def setup(t, files, search, rows):
    root = Path(t) / "repo"
    (root / "config").mkdir(parents=True)
    for u in ("adoption", "clock"):
        (root / "units" / u).mkdir(parents=True)
        (root / "units" / u / "unit.json").write_text("{}")
    (Path(t) / "consumers.txt").write_text(rows)
    git(root, "init", "-q")
    git(root, "commit", "-q", "--allow-empty", "-m", "x")
    for tag in ("units/adoption/v0.1.0", "units/adoption/v0.2.0", "units/adoption/v0.10.0", "adoption/v9.0.0"):
        git(root, "tag", tag)
    data = Path(t) / "data.json"
    data.write_text(json.dumps({"files": files, "search": search}))
    fake = Path(t) / "gh"
    fake.write_text(FAKE % str(data))
    fake.chmod(0o755)
    return root, fake


def run(root, fake):
    return subprocess.run([sys.executable, str(TOOL), "show", "--root", str(root), "--gh", str(fake),
                           "--list", str(root.parent / "consumers.txt")],
                          capture_output=True, text=True)


def main():
    failed = []
    with tempfile.TemporaryDirectory() as t:
        files = {
            "repos/o/alpha/contents/go.mod": "module x\n\nrequire (\n\tgithub.com/ydnikolaev/go-foundation/units/adoption v0.2.0\n)\n",
            "repos/o/beta/contents/go.mod": "module y\n\nrequire github.com/ydnikolaev/go-foundation/units/adoption v0.10.0\n",
            "repos/o/a2a/contents/go.mod": "module z\n",
        }
        search = {"o/a2a": [{"path": "internal/notes/doc.go", "text_matches": [
            {"fragment": "// go-foundation-copy: adoption@b5f12155"}]}]}
        root, fake = setup(t, files, search, "o/alpha\no/beta  # comment\no/a2a\n")
        r = run(root, fake)
        out = r.stdout
        for want in ("| `adoption` | v0.10.0 | o/alpha | v0.2.0 behind (latest v0.10.0) | `go.mod` |",
                     "| `adoption` | v0.10.0 | o/beta | v0.10.0 current | `go.mod` |",
                     "| `adoption` | v0.10.0 | o/a2a | copy @b5f12155 | `internal/notes/doc.go` |",
                     "| `clock` | untagged | - | unused | - |"):
            if want not in out:
                failed.append("missing row %r (exit %d)\n%s%s" % (want, r.returncode, out, r.stderr))
    with tempfile.TemporaryDirectory() as t:
        root, fake = setup(t, {}, {}, "o/gone\n")
        r = run(root, fake)
        if r.returncode == 0 or "refusing a partial registry" not in r.stderr or "o/gone" not in r.stderr:
            failed.append("unreadable repo was not refused by name: exit %d\n%s" % (r.returncode, r.stderr))
    with tempfile.TemporaryDirectory() as t:
        root, fake = setup(t, {}, {}, "not-a-repo\n")
        r = run(root, fake)
        if r.returncode == 0 or "is not <owner/repo>" not in r.stderr:
            failed.append("malformed row accepted: exit %d\n%s" % (r.returncode, r.stderr))
    for f in failed:
        print("FAIL " + f)
    if failed:
        return 1
    print("test-consumers: behind, current, copy, unused and two refusals, ok")
    return 0


if __name__ == "__main__":
    sys.exit(main())
