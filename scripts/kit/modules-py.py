#!/usr/bin/env python3
# managed by devkit — do not edit; fix upstream in ~/Developer/projects/devkit
"""modules-py: the Python import-edge lister of the module boundary gate (scripts/kit/modules.py check).

    python3 scripts/kit/modules-py.py        (run at the repo root; modules.py runs it so)

Prints one line `<file>:<line> <target-file>` (repo-relative, POSIX) per import that resolves to a file of the repo,
deduped; the standard library and installed packages resolve to nothing and print nothing. Stdlib only, python3 >= 3.9.

Rules
  files     the listed `*.py` files: git ls-files --cached --others --exclude-standard (an untracked file is read, an
            ignored one is not); a listed file that is gone from the work tree is skipped
  parse     `ast` over every file; imports in function bodies and under `if TYPE_CHECKING` count like any other
  absolute  `import a.b.c` / `from a.b.c import x` resolve against, in order: the importing file's directory (a script
            imported by its sibling name), each source root (the parent of a top-level package: a directory with an
            `__init__.py` whose parent has none), the repo root. In each, the first of `a/b/c.py`, `a/b/c/__init__.py`,
            `a/b/c/` holding a listed `.py` (a PEP 420 namespace package, which has no file: no edge) wins
  relative  `from .m import x` resolves against the importing file's package directory, one level up per extra dot
  from      `from p import name` is the edge to `p/name.py` (or `p/name/__init__.py`) when that module exists, else to
            `p` itself (its `.py` or `__init__.py`; a namespace directory has no file: no edge). A re-export through
            `p/__init__.py` is therefore an edge to that `__init__.py`; the module the `__init__.py` imports is its
            own edge. `import a.b as c` and `from p import x as y` change nothing: aliases are not looked at
  red       a file that does not parse under the running python3 stops the lister: exit 1, stderr names the file, the
            line and the interpreter version

Known gaps (not edges): importlib.import_module, __import__, by-path loaders (SourceFileLoader, runpy), `sys.path`
edits and `.pth` files, imports built from strings, conditional imports are all read as if taken.
"""
from __future__ import annotations

import ast
import os
import subprocess
import sys

LS_FILES = ("git", "ls-files", "--cached", "--others", "--exclude-standard", "-z")


def listed_files() -> list:
    proc = subprocess.run(list(LS_FILES), capture_output=True)
    if proc.returncode != 0:
        lines = proc.stderr.decode("utf-8", "replace").strip().splitlines()
        sys.stderr.write(f"modules-py: git ls-files: {lines[0] if lines else 'exit ' + str(proc.returncode)}\n")
        sys.exit(1)
    return [os.fsdecode(item) for item in proc.stdout.split(b"\0") if item]


def join(*parts: str) -> str:
    return "/".join(p for p in parts if p)


class Tree:
    """The listed .py files, as the lookups the resolution needs."""

    def __init__(self, files: list) -> None:
        self.files = set(files)
        self.dirs = set()  # a directory holding a listed .py at any depth
        for f in files:
            d = os.path.dirname(f)
            while d and d not in self.dirs:
                self.dirs.add(d)
                d = os.path.dirname(d)
        inits = {os.path.dirname(f) for f in files if os.path.basename(f) == "__init__.py"}
        roots = {""}  # the parent of a top-level package: a dir with __init__.py whose parent has none
        for d in inits:
            if d and os.path.dirname(d) not in inits:
                roots.add(os.path.dirname(d))
        self.roots = sorted(roots)

    def module(self, base: str, parts: list):
        """('file', path) | ('pkg', init-path, dir) | ('ns', dir) for the first form that exists, else None."""
        path = join(base, *parts)
        if not path:
            return None
        if path + ".py" in self.files:
            return ("file", path + ".py")
        if join(path, "__init__.py") in self.files:
            return ("pkg", join(path, "__init__.py"), path)
        if path in self.dirs:
            return ("ns", path)
        return None

    def file_of(self, found):
        return found[1] if found and found[0] != "ns" else None

    def folder(self, base: str):
        """The package a relative import names: the directory itself."""
        if join(base, "__init__.py") in self.files:
            return ("pkg", join(base, "__init__.py"), base)
        return ("ns", base)


def sub_of(tree: Tree, found, name: str):
    """`from p import name` where p is `found`: the edge target file, the submodule when it exists, else p's file."""
    if found is not None and found[0] in ("pkg", "ns"):
        sub = tree.module(found[-1], [name])
        if sub is not None:
            return tree.file_of(sub)
    return tree.file_of(found)


def edges_of(tree: Tree, path: str, module: ast.AST) -> list:
    out = []
    here = os.path.dirname(path)
    bases = list(dict.fromkeys([here] + tree.roots))

    def absolute(parts: list):
        for base in bases:
            found = tree.module(base, parts)
            if found is not None:
                return found
        return None

    for node in ast.walk(module):
        if isinstance(node, ast.Import):
            for alias in node.names:
                target = tree.file_of(absolute(alias.name.split(".")))
                if target:
                    out.append((node.lineno, target))
        elif isinstance(node, ast.ImportFrom):
            if node.level:
                base = here
                for _ in range(node.level - 1):
                    if not base:
                        base = None
                        break
                    base = os.path.dirname(base)
                if base is None:
                    continue
                found = tree.module(base, node.module.split(".")) if node.module else tree.folder(base)
            else:
                found = absolute(node.module.split(".")) if node.module else None
            if found is None:
                continue
            for alias in node.names:
                target = tree.file_of(found) if alias.name == "*" else sub_of(tree, found, alias.name)
                if target:
                    out.append((node.lineno, target))
    return out


def main() -> int:
    files = [f for f in listed_files() if f.endswith(".py") and os.path.isfile(f)]
    tree = Tree(files)
    version = ".".join(str(n) for n in sys.version_info[:3])
    lines, red = [], []
    for path in sorted(files):
        try:
            with open(path, "rb") as handle:
                module = ast.parse(handle.read(), filename=path)
        except SyntaxError as exc:
            red.append(f"modules-py: {path}:{exc.lineno or 0}: does not parse under python {version}: {exc.msg}")
            continue
        except (ValueError, OSError) as exc:
            red.append(f"modules-py: {path}:0: cannot be read under python {version}: {exc}")
            continue
        seen = set()
        for lineno, target in sorted(edges_of(tree, path, module)):
            if (lineno, target) not in seen:
                seen.add((lineno, target))
                lines.append(f"{path}:{lineno} {target}")
    if red:
        sys.stderr.write("\n".join(red) + "\n")
        return 1
    if lines:
        sys.stdout.write("\n".join(lines) + "\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
