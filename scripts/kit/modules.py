#!/usr/bin/env python3
# managed by devkit — do not edit; fix upstream in ~/Developer/projects/devkit
"""kit-modules: the one reader of config/modules.ini, the module registry (library and CLI).

    python3 scripts/kit/modules.py list [--json] [--root DIR]
    python3 scripts/kit/modules.py of PATH... [--root DIR]
    python3 scripts/kit/modules.py paths NAME [--root DIR]
    python3 scripts/kit/modules.py dependents NAME... [--root DIR]
    python3 scripts/kit/modules.py lint [--root DIR]
    python3 scripts/kit/modules.py check [--root DIR]
    python3 scripts/kit/modules.py adopted [--root DIR] --retired PATH...

Stdlib only, python3 >= 3.9. The file is INI (configparser, no interpolation, keys case-sensitive): a full line starting
with `#` or `;` is a comment, an inline comment starts with `;` only. One section per module:

    paths    required; globs separated by whitespace or newlines, anchored at the root: `*` and `?` stay inside one
             segment, a `**` segment matches zero or more segments (the check-map grammar)
    deps     optional; names this module may import: a module, a family member, or a whole family by its section name
    deps.V   family only; extra deps of the one member the value V binds (may name a sibling; the siblings stay acyclic)
    check    optional; make targets that check this module
    planned  optional; `<epic>:<PHASE>`, the phase that creates the module's files (a module declared first)

A section name with one `*` is a family: one member per value of that `*`, bound by the family's globs, each with one lone
`*` in one segment. Members are the values the first glob binds among listed files no earlier section owns. A path belongs
to the first section, in file order, whose glob matches it. `[shared]` (paths only) is every module's implicit dep and
imports only itself. Listed files: git ls-files --cached --others --exclude-standard, plus the paths a caller passes.
Once a module is declared every listed code path (extension in CODE) belongs to a module or to `[shared]` (lint).

`[compose]` is no module: it declares the files every module edits, one row `<exact path> <value>` per line.
    generated  `<path> <make target>`: the file is that target's output, never written by hand; the target writes to
               $KIT_COMPOSE_OUT when it is set, else to the path. CHANGELOG.md is a built-in row (its writer is the
               `devkit release` verb, so its target is "" and freshness never runs it), present whether declared or not
    split      `<path> <fragment glob>`: the file is composed from per-module fragments; a phase names its fragment
A row without exactly two fields, or another key, is a structural refusal; the rest (a glob for a path, a path both
generated and split, a row for the built-in file, a target that is no make target name, a fragment glob without exactly
one `*`, a split file its own glob matches or no section owns, a listed fragment in [shared] or in no module) is lint's.

Library: load(root, extra=()) -> Registry; Registry.modules (name -> Module), .of(path), .paths(name), .dependents(names),
.deps(name), .code_paths(), .generated() (path -> target, the built-in row first), .split() (path -> fragment glob); every
refusal is one RegistryError naming the file, section and key. load raises the structural refusals only; coverage,
unmatched globs and `planned` are lint's alone (a selection never stops on an uncovered file).

`check` is the boundary gate: lint, then generated-file freshness (each project `generated` row's target run twice with
KIT_COMPOSE_OUT set to two temp paths: the outputs must match each other and the file in the tree), then the edge walk.
A lister per stack (CODE below, beside this file, run with the repo root as cwd) prints `<source> <target>` lines for the
imports that resolve inside the repo; an edge from module A to module B is refused unless B is in deps(A) (`shared`
included in deps, `shared` itself depends on nothing). Stack rules live in each lister's header, not here.

`adopted` is the closing check of standards/monorepo.md#adopt: exit 0 only when a registry declares a module, lint and `check`
are green, `make -n --trace check` shows `update target 'kit-modules'`, and no `--retired` path (the old gate's files; `''` states
there was none) is still tracked or named by `make -n --trace check` or `check-full`. A repo with no `check-full` target skips that
dry run. Green prints `modules: adopted, N modules, M edges`.

CLI: --root DIR (default: the git top level of the cwd). Exit 0 ok, 1 refused, 2 usage.
"""
from __future__ import annotations

import argparse
import configparser
import json
import os
import re
import subprocess
import sys
import tempfile
from pathlib import Path

FILE = "config/modules.ini"
# The one stack table: code extension -> (lister script beside this file, unit). Unit `file`: the lister prints
# `<file>:<line> <file>`; unit `dir`: `<dir>/ <dir>/` (a package dir, which belongs to one module). A new stack is a new
# lister file and one row here.
CODE = {
    ".py": ("modules-py.py", "file"),
    ".go": ("modules-go.sh", "dir"),
    ".ts": ("modules-ts.py", "file"),
    ".tsx": ("modules-ts.py", "file"),
    ".mts": ("modules-ts.py", "file"),
    ".cts": ("modules-ts.py", "file"),
    ".js": ("modules-ts.py", "file"),
    ".jsx": ("modules-ts.py", "file"),
    ".mjs": ("modules-ts.py", "file"),
    ".cjs": ("modules-ts.py", "file"),
}
# The copier module that renders a lister which is not always rendered (the others come with every render).
LISTER_MODULE = {"modules-go.sh": "go"}
SHARED = "shared"
COMPOSE = "compose"
COMPOSE_KEYS = {"generated": "make target", "split": "fragment glob"}  # key -> what its second field is
BUILT_IN = {"CHANGELOG.md": ""}  # the built-in generated row: written by the `devkit release` verb, no make target
GLOB_CHARS = re.compile(r"[*?\[\]{}]")
KNOWN_KEYS = ("paths", "deps", "deps.<value>", "check", "planned")
VALUE = r"[A-Za-z0-9._-]+"
NAME = re.compile(r"[A-Za-z0-9._*-]+")
VALUE_RE = re.compile(VALUE)
PLANNED = re.compile(r"([A-Za-z0-9][A-Za-z0-9._-]*):(" + VALUE + r")\Z")
NO_DEFAULT = "\0no-default-section"
LS_FILES = ("git", "ls-files", "--cached", "--others", "--exclude-standard")


class RegistryError(Exception):
    """A refusal: one line naming the file, the section and the key."""


def where(section: str, key: str = "") -> str:
    return f"{FILE} [{section}]" + (f" {key}" if key else "")


# ---- globs -----------------------------------------------------------------------------------------------------

def seg_regex(seg: str, bind: bool = False):
    """One segment as a regex: `*` stays in the segment, `?` is one character; the bound `*` captures a value."""
    out = []
    for ch in seg:
        if ch == "*":
            out.append("(" + VALUE + ")" if bind else "[^/]*")
        else:
            out.append("[^/]" if ch == "?" else re.escape(ch))
    return re.compile("".join(out))


class Glob:
    """A compiled glob. A family glob has exactly one lone `*`; `match` then returns the value it binds."""

    def __init__(self, text: str, family: bool, section: str) -> None:
        self.text = text
        self.raw = [seg for seg in text.split("/") if seg]
        self.star = -1
        stars = [i for i, seg in enumerate(self.raw) if seg != "**" and "*" in seg]
        if family:
            if len(stars) != 1 or self.raw[stars[0]].count("*") != 1:
                raise RegistryError(f"{where(section, 'paths')}: family glob {text!r} needs exactly one lone `*` "
                                    f"in one segment to bind the member")
            self.star = stars[0]
        self.segs = [None if seg == "**" else seg_regex(seg, i == self.star) for i, seg in enumerate(self.raw)]
        # a `/**` tail right after the `*` needs a segment below it: modules/README.md binds no member README.md
        self.below = family and self.raw[-1] == "**" and self.star == len(self.raw) - 2

    def match(self, parts: list[str]) -> str | None:
        """None when the path (its segments) does not match; else the bound value, "" for a glob that binds none."""
        count = len(parts)
        segs = self.segs

        def walk(si: int, pi: int):
            if si == len(segs):
                return "" if pi == count else None
            seg = segs[si]
            if seg is None:
                for nxt in range(pi, count + 1):
                    got = walk(si + 1, nxt)
                    if got is not None:
                        return got
                return None
            if pi >= count:
                return None
            hit = seg.fullmatch(parts[pi])
            if hit is None:
                return None
            if si == self.star and self.below and pi + 1 >= count:
                return None
            got = walk(si + 1, pi + 1)
            if got is None:
                return None
            return hit.group(1) if si == self.star else got

        return walk(0, 0)

    def bound(self, value: str) -> str:
        """The glob with its `*` replaced by the member's value."""
        return "/".join(seg.replace("*", value) if i == self.star else seg for i, seg in enumerate(self.raw))


def split(path: str) -> list[str]:
    return [seg for seg in path.split("/") if seg]


# ---- the file --------------------------------------------------------------------------------------------------

class Section:
    def __init__(self, name: str) -> None:
        self.name = name
        self.family = "*" in name
        self.globs: list[Glob] = []
        self.deps: list[str] = []
        self.member_deps: dict[str, list[str]] = {}
        self.check: list[str] = []
        self.planned: str | None = None

    def member(self, value: str) -> str:
        return self.name.replace("*", value)


class CaseParser(configparser.ConfigParser):
    def optionxform(self, optionstr: str) -> str:
        return optionstr


class Compose:
    """The `[compose]` rows as written, in file order: (key, path, value)."""

    def __init__(self) -> None:
        self.rows: list[tuple[str, str, str]] = []


def parse_compose(cp: configparser.ConfigParser, compose: Compose) -> None:
    for key, value in cp[COMPOSE].items():
        if key not in COMPOSE_KEYS:
            raise RegistryError(f"{where(COMPOSE, key)}: unknown key, want {' '.join(COMPOSE_KEYS)}")
        for line in value.splitlines():
            words = line.split()
            if not words:
                continue
            if len(words) != 2:
                raise RegistryError(f"{where(COMPOSE, key)}: row {line.strip()!r} needs two fields, "
                                    f"<exact path> <{COMPOSE_KEYS[key]}>")
            compose.rows.append((key, words[0], words[1]))


def parse(text: str) -> tuple:
    """(sections, compose): the module sections in file order and the `[compose]` rows."""
    cp = CaseParser(interpolation=None, inline_comment_prefixes=(";",), default_section=NO_DEFAULT)
    try:
        cp.read_string(text, source=FILE)
    except configparser.Error as exc:
        raise RegistryError(f"{FILE}: {' '.join(str(exc).split())}")
    sections = []
    compose = Compose()
    for name in cp.sections():
        if name == COMPOSE:
            parse_compose(cp, compose)
            continue
        if not NAME.fullmatch(name) or name.count("*") > 1:
            raise RegistryError(f"{where(name)}: bad name, want {VALUE} with at most one `*`")
        sec = Section(name)
        shared = name == SHARED
        for key, value in cp[name].items():
            words = value.split()
            if key == "paths":
                if not words:
                    raise RegistryError(f"{where(name, key)}: empty, want one glob or more")
                sec.globs = [Glob(word, sec.family, name) for word in words]
            elif shared:
                raise RegistryError(f"{where(name, key)}: [shared] takes `paths` only")
            elif key == "deps":
                sec.deps = words
            elif key.startswith("deps."):
                bound = key[len("deps."):]
                if not sec.family:
                    raise RegistryError(f"{where(name, key)}: `deps.<value>` is for a family (a section with one `*`)")
                if not VALUE_RE.fullmatch(bound):
                    raise RegistryError(f"{where(name, key)}: bad value {bound!r}, want {VALUE}")
                sec.member_deps[bound] = words
            elif key == "check":
                sec.check = words
            elif key == "planned":
                sec.planned = value.strip()
            else:
                raise RegistryError(f"{where(name, key)}: unknown key, want {' '.join(KNOWN_KEYS)}")
        if not sec.globs:
            raise RegistryError(f"{where(name, 'paths')}: required")
        sections.append(sec)
    return sections, compose


# ---- git -------------------------------------------------------------------------------------------------------

def run_git(cwd: Path, *args: str) -> bytes:
    cmd = " ".join(args)
    try:
        proc = subprocess.run(list(args), cwd=str(cwd), capture_output=True)
    except OSError as exc:
        raise RegistryError(f"{cmd}: {exc}")
    if proc.returncode != 0:
        lines = proc.stderr.decode("utf-8", "replace").strip().splitlines()
        raise RegistryError(f"{cmd}: {lines[0] if lines else 'exit ' + str(proc.returncode)}")
    return proc.stdout


def listed_files(root: Path) -> list[str]:
    raw = run_git(root, *LS_FILES, "-z")
    return [os.fsdecode(item) for item in raw.split(b"\0") if item]


def toplevel(cwd: Path) -> Path:
    return Path(run_git(cwd, "git", "rev-parse", "--show-toplevel").decode("utf-8", "replace").strip())


# ---- the registry ----------------------------------------------------------------------------------------------

class Module:
    """One module: its (bound) globs, declared deps (families expanded, no implicit shared), check targets, the family
    section it is a member of, and its `planned` key."""

    def __init__(self, name: str, paths: tuple, deps: tuple, check: tuple, family: str | None,
                 planned: str | None) -> None:
        self.name = name
        self.paths = paths
        self.deps = deps
        self.check = check
        self.family = family
        self.planned = planned


class Registry:
    def __init__(self, sections: list[Section], listed: list[str], members: dict[str, list[str]],
                 modules: dict[str, Module], compose: Compose | None = None) -> None:
        self.sections = sections
        self.listed = listed
        self.members = members
        self.modules = modules
        self.compose = compose or Compose()
        self.declared = sum(1 for sec in sections if sec.name != SHARED)
        self._values = {name: set(values) for name, values in members.items()}

    def generated(self) -> dict:
        """path -> make target of every generated file: the built-in row first (target "": a verb writes it), then the
        project's rows in file order (lint refuses a project row for the built-in path)."""
        out = dict(BUILT_IN)
        for key, path, value in self.compose.rows:
            if key == "generated" and path not in out:
                out[path] = value
        return out

    def split(self) -> dict:
        """path -> fragment glob of every split file, in file order."""
        return {path: value for key, path, value in self.compose.rows if key == "split"}

    def _owner(self, parts: list[str]) -> str | None:
        for sec in self.sections:
            for glob in sec.globs:
                value = glob.match(parts)
                if value is None:
                    continue
                if not sec.family:
                    return sec.name
                if value in self._values[sec.name]:
                    return sec.member(value)
        return None

    def of(self, path: str) -> str | None:
        """The module owning `path` (the first section whose glob matches), None for none; `shared` for a shared file."""
        return self._owner(split(path))

    def known(self) -> list[str]:
        names = []
        for sec in self.sections:
            names.append(sec.name)
            names += [sec.member(value) for value in self.members.get(sec.name, [])] if sec.family else []
        return names

    def _need(self, name: str) -> Module:
        if name not in self.modules:
            raise RegistryError(f"unknown module {name!r}; known: {' '.join(self.known()) or 'none'}")
        return self.modules[name]

    def paths(self, name: str) -> tuple:
        """A member's globs with the `*` bound; a family name gives them unbound."""
        for sec in self.sections:
            if sec.family and sec.name == name:
                return tuple(glob.text for glob in sec.globs)
        return self._need(name).paths

    def deps(self, name: str) -> tuple:
        """The module's deps, families expanded, `shared` included."""
        if name == SHARED:
            return ()
        mod = self._need(name)
        return mod.deps if SHARED in mod.deps else mod.deps + (SHARED,)

    def _expand(self, name: str) -> list[str]:
        if name == SHARED or name in self.modules:
            return [name]
        for sec in self.sections:
            if sec.family and sec.name == name:
                return [sec.member(value) for value in self.members[name]]
        raise RegistryError(f"unknown module {name!r}; known: {' '.join(self.known()) or 'none'}")

    def dependents(self, names) -> list[str]:
        """Modules that depend on any of `names`, transitively, sorted, the names themselves excluded; every module for
        `shared` (each module has it implicitly)."""
        start = {n for name in names for n in self._expand(name)}
        reverse: dict[str, set[str]] = {}
        for mod in self.modules.values():
            if mod.name != SHARED:
                for dep in self.deps(mod.name):
                    reverse.setdefault(dep, set()).add(mod.name)
        seen: set[str] = set()
        stack = list(start)
        while stack:
            for nxt in reverse.get(stack.pop(), ()):
                if nxt not in seen:
                    seen.add(nxt)
                    stack.append(nxt)
        return sorted(seen - start)

    def code_paths(self) -> list[str]:
        return [p for p in self.listed if os.path.splitext(p)[1] in CODE]


def build(sections: list[Section], listed: list[str], compose: Compose | None = None) -> Registry:
    plain = {sec.name for sec in sections if not sec.family}
    parts_of = {path: split(path) for path in listed}
    members: dict[str, list[str]] = {}
    names: dict[str, str] = {}  # member name -> family
    unowned = list(listed)
    for sec in sections:
        if not sec.family:
            unowned = [p for p in unowned if not any(g.match(parts_of[p]) is not None for g in sec.globs)]
            continue
        values = sorted({v for v in (sec.globs[0].match(parts_of[p]) for p in unowned) if v is not None})
        for value in values:
            name = sec.member(value)
            if name in plain or name in names:
                other = f"[{name}]" if name in plain else f"a member of [{names[name]}]"
                raise RegistryError(f"{where(sec.name)}: duplicate name {name}, also {other}")
            names[name] = sec.name
        members[sec.name] = values
        taken = set(values)
        unowned = [p for p in unowned
                   if not any(v is not None and v in taken for v in (g.match(parts_of[p]) for g in sec.globs))]
    families = {sec.name: sec for sec in sections if sec.family}

    def expand(sec: Section, key: str, token: str) -> list[str]:
        if sec.family and token == sec.name:
            raise RegistryError(f"{where(sec.name, key)}: family {token} in its own deps is a cycle")
        if token == SHARED:
            return [SHARED]
        if "*" in token:
            if token not in families:
                raise RegistryError(f"{where(sec.name, key)}: {token!r} names no family")
            return [families[token].member(v) for v in members[token]]
        if token in plain or token in names:
            return [token]
        raise RegistryError(f"{where(sec.name, key)}: {token!r} resolves to no module")

    for sec in sections:  # a family's deps are judged whether or not a member exists yet
        if sec.family:
            for key, tokens in [("deps", sec.deps)] + [(f"deps.{v}", t) for v, t in sec.member_deps.items()]:
                for token in tokens:
                    expand(sec, key, token)

    modules: dict[str, Module] = {}
    edges: dict[str, list[tuple]] = {}  # module -> [(dep, section, key)]
    for sec in sections:
        if not sec.family:
            todo = [(None, sec.name)]
        else:
            todo = [(value, sec.member(value)) for value in members[sec.name]]
        for value, name in todo:
            found: list[tuple] = []
            for key, tokens in [("deps", sec.deps)] + ([(f"deps.{value}", sec.member_deps[value])]
                                                       if value in sec.member_deps else []):
                for token in tokens:
                    found += [(dep, sec.name, key) for dep in expand(sec, key, token)]
            edges[name] = found
            deps = tuple(dict.fromkeys(dep for dep, _, _ in found))
            paths = tuple(g.text if value is None else g.bound(value) for g in sec.globs)
            modules[name] = Module(name, paths, deps, tuple(sec.check), sec.name if sec.family else None, sec.planned)
    cycle_check(edges)  # a deps.<value> for a value no member binds yet is not an error: it is never read
    return Registry(sections, listed, members, modules, compose)


def cycle_check(edges: dict[str, list[tuple]]) -> None:
    state: dict[str, int] = {}
    trail: list[str] = []

    def visit(node: str) -> None:
        state[node] = 1
        trail.append(node)
        for dep, sec, key in edges.get(node, ()):
            if dep == SHARED:
                continue
            if state.get(dep) == 1:
                loop = trail[trail.index(dep):] + [dep]
                raise RegistryError(f"{where(sec, key)}: dependency cycle {' -> '.join(loop)}")
            if dep not in state:
                visit(dep)
        trail.pop()
        state[node] = 2

    for node in edges:
        if node not in state:
            visit(node)


def load(root, extra=()) -> Registry:
    """The registry of `root`: empty when config/modules.ini is absent; `extra` adds paths to the listed set."""
    root = Path(root)
    path = root / FILE
    if not path.is_file():
        return Registry([], [], {}, {})
    try:
        text = path.read_text(encoding="utf-8")
    except (OSError, UnicodeDecodeError) as exc:
        raise RegistryError(f"{FILE}: {exc}")
    sections, compose = parse(text)
    listed: list[str] = []
    if sections or compose.rows:  # a file with any section or row needs the work tree; an absent or empty one needs none
        listed = sorted(set(listed_files(root)) | {str(p) for p in extra})
    return build(sections, listed, compose)


# ---- lint ------------------------------------------------------------------------------------------------------

def tracker_names(root: Path, epic: str, phase: str) -> bool | None:
    """True when the epic's tracker.yaml has an `id: <phase>` line, False when it has none, None when it is absent."""
    try:
        text = (root / "docs" / "features" / "active" / epic / "tracker.yaml").read_text(encoding="utf-8")
    except (OSError, UnicodeDecodeError):
        return None
    return re.search(r"(?m)^\s*(?:-\s+)?id:\s*[\"']?" + re.escape(phase) + r"[\"']?\s*(?:#.*)?$", text) is not None


def lint(root) -> tuple:
    """(problems, notes, declared): one line per problem; declared is None for a file with no section; load's structural
    refusal raises RegistryError."""
    root = Path(root)
    reg = load(root)
    problems: list[str] = []
    notes: list[str] = []
    parts_of = {path: split(path) for path in reg.listed}
    for sec in reg.sections:
        values = set(reg.members.get(sec.name, ()))
        hits = []
        for glob in sec.globs:
            found = (glob.match(parts) for parts in parts_of.values())
            hits.append(any(v is not None and (not sec.family or v in values) for v in found))
        matched = any(hits)
        for target in sec.check:
            if target.startswith("-") or "=" in target:
                problems.append(f"{where(sec.name, 'check')}: {target!r} is not a make target name")
        if sec.planned is not None:
            problems += planned_problems(root, sec, matched, notes)
        else:
            problems += [f"{where(sec.name, 'paths')}: glob {glob.text!r} matches no listed file"
                         for glob, hit in zip(sec.globs, hits) if not hit]
    if reg.declared:
        bare = [p for p in reg.code_paths() if reg.of(p) is None]
        if bare:
            problems.append(f"{FILE}: {len(bare)} code path{'s' if len(bare) > 1 else ''} in no module or [shared] "
                            f"(first {min(5, len(bare))}): {', '.join(bare[:5])}")
        problems += package_dir_problems(reg)
    problems += compose_problems(reg)
    return problems, notes, reg.declared if reg.sections else None


def compose_problems(reg: Registry) -> list[str]:
    """Lint's refusals of the `[compose]` rows, one line each (the structural ones are load's)."""
    generated = {path for key, path, _ in reg.compose.rows if key == "generated"}
    out = []
    for key, path, value in reg.compose.rows:
        at = where(COMPOSE, key)
        if GLOB_CHARS.search(path):
            out.append(f"{at}: {path!r} is a glob; a row names one exact path")
        elif path in BUILT_IN:
            out.append(f"{at}: {path} is built in (written by `devkit release`); drop the row")
        elif key == "generated":
            if value.startswith("-") or "=" in value:
                out.append(f"{at}: {value!r} is not a make target name")
        elif path in generated:
            out.append(f"{at}: {path} is both generated and split; keep one row")
        elif value.count("*") != 1:
            out.append(f"{at}: fragment glob {value!r} of {path} needs exactly one `*` (one fragment per module)")
        else:
            out += split_problems(reg, at, path, value)
    return out


def split_problems(reg: Registry, at: str, path: str, value: str) -> list[str]:
    glob = Glob(value, False, COMPOSE)
    if glob.match(split(path)) is not None:
        return [f"{at}: {path} matches its own fragment glob {value}; a fragment is never the file"]
    if reg.of(path) is None:
        return [f"{at}: {path} is in no module or [shared]; declare its owner, so `--area module:<owner>` names it"]
    bad = [(p, reg.of(p)) for p in reg.listed if glob.match(split(p)) is not None and reg.of(p) in (None, SHARED)]
    if not bad:
        return []
    who = ", ".join(f"{p} ({'in [shared]' if owner == SHARED else 'in no module'})" for p, owner in bad[:5])
    return [f"{at}: a fragment of {path} sits in a module, never in [shared] or outside every module: {who}"]


def package_dir_problems(reg: Registry) -> list[str]:
    """LR-6: a directory whose listed `dir`-unit files (a Go package dir) belong to two modules is refused."""
    held: dict[tuple, set] = {}
    for path in reg.code_paths():
        ext = os.path.splitext(path)[1]
        owner = reg.of(path)
        if CODE[ext][1] == "dir" and owner is not None:
            held.setdefault((os.path.dirname(path), ext), set()).add(owner)
    out = []
    for (folder, ext), owners in sorted(held.items()):
        if len(owners) > 1:
            names = sorted(owners)
            who = f"{', '.join(names[:-1])} and {names[-1]}"
            out.append(f"modules: {folder + '/' if folder else './'} holds {ext} files of {who}; "
                       f"a package dir belongs to one module")
    return out


def planned_problems(root: Path, sec: Section, matched: bool, notes: list[str]) -> list[str]:
    hit = PLANNED.match(sec.planned or "")
    if hit is None:
        return [f"{where(sec.name, 'planned')}: {sec.planned!r} is not <epic>:<PHASE>"]
    if matched:
        notes.append(f"note: {where(sec.name, 'planned')} = {sec.planned} is stale, the module has files; "
                     f"drop the key when you next edit {FILE}")
        return []
    epic, phase = hit.group(1), hit.group(2)
    if not (root / "docs" / "features" / "active" / epic).is_dir():
        return [f"{where(sec.name, 'planned')}: epic folder docs/features/active/{epic} is absent and no file matches "
                f"(the epic closed without making them)"]
    named = tracker_names(root, epic, phase)
    if named is None:
        return [f"{where(sec.name, 'planned')}: docs/features/active/{epic}/tracker.yaml is absent"]
    if not named:
        return [f"{where(sec.name, 'planned')}: phase {phase} is not named in docs/features/active/{epic}/tracker.yaml"]
    return []


# ---- check: the edge walk --------------------------------------------------------------------------------------

STDERR_LINES = 5  # a failing lister: this many of its first stderr lines
FILE_EDGE = re.compile(r"(.+?:\d+) (.+)\Z")
DIR_EDGE = re.compile(r"(.+?/) (.+/)\Z")


def freshness(root: Path, reg: Registry) -> list:
    """The generated-file freshness stage (between lint and the edge walk): one problem line per project `generated` row
    that is red. The built-in row (target "") is a verb's output and never run."""
    out: list[str] = []
    for path, target in reg.generated().items():
        if target:
            out += fresh_problems(root, path, target)
    return out


def fresh_problems(root: Path, path: str, target: str) -> list:
    """`make -s <target>` twice (cwd the root, the environment as inherited plus KIT_COMPOSE_OUT set to a temp path): the
    two outputs must be equal and equal to the file in the tree."""
    got = []
    with tempfile.TemporaryDirectory(prefix="kit-compose-") as tmp:
        for name in ("one", "two"):
            dest = Path(tmp) / name
            try:
                proc = subprocess.run(["make", "-s", target], cwd=str(root), capture_output=True,
                                      env=dict(os.environ, KIT_COMPOSE_OUT=str(dest)))
            except OSError as exc:
                return [f"modules: make {target} ({path}): {exc}"]
            err = proc.stderr.decode("utf-8", "replace").strip().splitlines()[:STDERR_LINES]
            more = "".join(f"\n{line}" for line in err)
            if proc.returncode != 0:
                return [f"modules: make {target} exited {proc.returncode} ({path})" + more]
            if not dest.is_file():
                return [f"modules: make {target} wrote nothing to KIT_COMPOSE_OUT ({path}); the target writes to "
                        f"$KIT_COMPOSE_OUT when it is set, else to the path" + more]
            got.append(dest.read_bytes())
    if got[0] != got[1]:
        return [f"modules: make {target} is not deterministic ({path})"]
    file = root / path
    if not file.is_file() or file.read_bytes() != got[0]:
        return [f"modules: {path} is stale; next: make {target}, and name {path} in the lane's area "
                f"(devkit lane amend <lane> --area {path})"]
    return []


def lister_command(path: Path) -> list:
    """The command for a lister. KIT_MODULES_PY (non-empty; empty counts as unset) is the interpreter of modules-py.py
    only: the project's python parses the project's syntax. modules-ts.py keeps sys.executable, .sh keeps bash. A value
    naming a missing file fails in run_lister's OSError branch, which names it."""
    if path.suffix == ".sh":
        return ["bash", str(path)]
    if path.name == "modules-py.py" and os.environ.get("KIT_MODULES_PY"):
        return [os.environ["KIT_MODULES_PY"], str(path)]
    return [sys.executable, str(path)]


def run_lister(root: Path, name: str) -> list:
    """The lines a lister prints (repo root as cwd); RegistryError when it is absent or exits non-zero."""
    path = Path(os.path.abspath(__file__)).parent / name
    if not path.is_file():
        module = LISTER_MODULE.get(name)
        why = (f"turn on the {module} module in .copier-answers.yml, then devkit update --apply" if module
               else "run devkit update --apply to restore it")
        needs = f"the {module} copier module" if module else "the devkit template"
        raise RegistryError(f"modules: lister {name} is absent beside modules.py; it comes with {needs}; next: {why}")
    try:
        proc = subprocess.run(lister_command(path), cwd=str(root), capture_output=True)
    except OSError as exc:
        raise RegistryError(f"modules: lister {name}: {exc}")
    if proc.returncode != 0:
        err = proc.stderr.decode("utf-8", "replace").strip().splitlines()[:STDERR_LINES]
        raise RegistryError(f"modules: lister {name} exited {proc.returncode}" + "".join(f"\n{line}" for line in err))
    return [line for line in proc.stdout.decode("utf-8", "replace").splitlines() if line.strip()]


def walk(root: Path, reg: Registry) -> tuple:
    """(refused lines, judged edge count): every lister the listed code paths need, every edge mapped to modules."""
    code = reg.code_paths()
    by_dir: dict[str, set] = {}  # a package dir -> the modules of its listed dir-unit code files directly inside it
    for path in code:
        if CODE[os.path.splitext(path)[1]][1] == "dir" and reg.of(path) is not None:
            by_dir.setdefault(os.path.dirname(path), set()).add(reg.of(path))
    needed = sorted({CODE[os.path.splitext(path)[1]] for path in code})
    refused: list[str] = []
    judged: set = set()

    def module_of(end: str, unit: str):
        if unit == "dir":
            owners = by_dir.get("" if end in ("./", "/") else end.rstrip("/"), set())
            return next(iter(owners)) if len(owners) == 1 else None
        path = end.rsplit(":", 1)[0] if re.search(r":\d+\Z", end) else end
        return reg.of(path) if os.path.splitext(path)[1] in CODE else None

    for name, unit in needed:
        pattern = DIR_EDGE if unit == "dir" else FILE_EDGE
        for line in sorted(set(run_lister(root, name))):
            hit = pattern.fullmatch(line)
            if hit is None:
                raise RegistryError(f"modules: lister {name} printed a line it should not: {line!r}")
            source, target = hit.group(1), hit.group(2)
            a, b = module_of(source, unit), module_of(target, unit)
            if a is None or b is None or a == b:
                continue  # a non-code end or an edge inside one module is not judged
            if b in reg.deps(a):  # deps("shared") is empty: shared imports nothing
                judged.add((source, target))
            else:
                refused.append(f"modules: {source} ({a}) imports {target} ({b}); {b} is not a dep of {a}; "
                               f"next: add it to [{a}] deps in config/modules.ini, or move the code")
    # M counts the distinct (source, target) lines that crossed two modules and were allowed; intra-module edges, edges
    # with an unjudged end and refused edges are not in it (a green run has no refused edge).
    return sorted(refused), len(judged)


# ---- CLI -------------------------------------------------------------------------------------------------------

def say(line: str, err: bool = False) -> None:
    stream = sys.stderr if err else sys.stdout
    stream.write(line + "\n")
    stream.flush()


def verb_list(root: Path, args: argparse.Namespace) -> int:
    reg = load(root)
    if args.json:
        say(json.dumps([{"name": m.name, "paths": list(m.paths), "deps": list(m.deps), "check": list(m.check),
                         "family": m.family, "planned": m.planned} for m in reg.modules.values()], indent=2))
        return 0
    for mod in reg.modules.values():
        say(f"{mod.name} {' '.join(mod.paths)} : {' '.join(mod.deps)}".rstrip())
    return 0


def verb_of(root: Path, args: argparse.Namespace) -> int:
    reg = load(root, extra=[os.path.normpath(p) for p in args.paths])
    for path in args.paths:
        say(f"{path} {reg.of(path) or '-'}")
    return 0


def verb_paths(root: Path, args: argparse.Namespace) -> int:
    for glob in load(root).paths(args.name):
        say(glob)
    return 0


def verb_dependents(root: Path, args: argparse.Namespace) -> int:
    for name in load(root).dependents(args.names):
        say(name)
    return 0


def verb_lint(root: Path, args: argparse.Namespace) -> int:
    problems, notes, declared = lint(root)
    for note in notes:
        say(note)
    for problem in problems:
        say(problem, err=True)
    if problems:
        return 1
    say("modules: none declared" if declared is None else f"modules: {declared} declared")
    return 0


def check_core(root: Path, out, err) -> tuple:
    """The boundary gate (`check`): (exit code, modules, edges), every line through `out` (stdout) or `err` (stderr)."""
    problems, notes, _ = lint(root)  # 1: a lint problem is red and nothing after it runs
    for note in notes:
        out(note)
    for problem in problems:
        err(problem)
    if problems:
        return 1, 0, 0
    reg = load(root)
    stale = freshness(root, reg)  # 2: generated-file freshness; red, but the walk still runs so both kinds print
    for line in stale:
        err(line)
    rows = len(reg.generated()) - len(BUILT_IN)
    if not reg.declared:  # only [compose] rows (or nothing): freshness, then no walk
        if stale:
            return 1, 0, 0
        if rows:
            out(f"modules: {rows} generated file{'s' if rows > 1 else ''} fresh")
        out("modules: none declared")
        return 0, 0, 0
    refused, edges = walk(root, reg)  # 3-7: the edge walk
    if refused:
        for line in refused:
            err(line)
        err(f"modules: {len(refused)} edge{'s' if len(refused) > 1 else ''} refused")
        return 1, len(reg.modules), edges
    if stale:
        return 1, len(reg.modules), edges
    out(f"modules: {len(reg.modules)} modules, {edges} edges, ok")
    return 0, len(reg.modules), edges


def verb_check(root: Path, args: argparse.Namespace) -> int:
    return check_core(root, say, lambda line: say(line, err=True))[0]


# ---- adopted: the closing check of the adopt runbook -----------------------------------------------------------

def make_dry(root: Path, target: str) -> tuple:
    """(returncode, stdout, first stderr line) of `make -n --trace <target>` at the root (the make variables of a parent make
    are dropped, so the dry run reads the Makefile alone)."""
    env = {k: v for k, v in os.environ.items() if k not in ("MAKEFLAGS", "MFLAGS", "MAKELEVEL", "MAKEOVERRIDES")}
    try:
        proc = subprocess.run(["make", "-n", "--trace", target], cwd=str(root), capture_output=True, env=env)
    except OSError as exc:
        return 127, "", f"make: {exc}"
    lines = proc.stderr.decode("utf-8", "replace").strip().splitlines()
    return proc.returncode, proc.stdout.decode("utf-8", "replace"), lines[0] if lines else f"make exited {proc.returncode}"


def names_path(text: str, path: str) -> bool:
    """Does `text` name `path` as a whole word (a longer path, or the same name with a longer extension, is another path)?
    A leading `./` is the same path; a `$(VAR)/` prefix is invisible to a text match."""
    return re.search(r"(?<![\w./-])(?:\./)?" + re.escape(path) + r"(?![\w-]|\.\w)", text) is not None


def adopt_refused(reasons: list) -> int:
    for reason in reasons:
        say(f"modules: not adopted: {reason}", err=True)
    return 1


def verb_adopted(root: Path, args: argparse.Namespace) -> int:
    retired = [os.path.normpath(p) for p in args.retired if p.strip()]  # `--retired ''` states there was no old gate
    reg = load(root)
    if not reg.declared:
        return adopt_refused([f"{FILE} declares no module"])
    problems, _, _ = lint(root)
    if problems:
        for problem in problems:
            say(problem, err=True)
        return adopt_refused([f"lint is red ({len(problems)} problem{'s' if len(problems) > 1 else ''} above)"])
    errs: list[str] = []
    code, count, edges = check_core(root, lambda line: None, errs.append)
    if code != 0:
        for line in errs:
            say(line, err=True)
        return adopt_refused([f"check is red ({len(errs)} line{'s' if len(errs) > 1 else ''} above)"])
    status, text, first = make_dry(root, "check")
    if status != 0:
        return adopt_refused([f"make -n --trace check failed: {first}"])
    refused: list[str] = []
    if "update target 'kit-modules'" not in text:
        refused.append("make -n --trace check does not run kit-modules (no `update target 'kit-modules'`); "
                       "include mk/kit-modules.mk in the Makefile")
    dry = {"check": text}
    status, full, first = make_dry(root, "check-full")
    note = ""
    if status == 0:
        dry["check-full"] = full
    elif re.search(r"No rule to make target [`'‘]check-full['’]", first):
        note = "modules: no check-full target; its dry run is skipped"
    else:
        return adopt_refused([f"make -n --trace check-full failed: {first}"])
    tracked = {os.fsdecode(p) for p in run_git(root, "git", "ls-files", "-z").split(b"\0") if p}
    for path in retired:
        if path in tracked or any(t.startswith(path.rstrip("/") + "/") for t in tracked):
            refused.append(f"retired path {path} is still tracked; delete it")
        for target, got in dry.items():
            if names_path(got, path):
                refused.append(f"retired path {path} is still named by `make -n --trace {target}`; remove its wiring")
    if refused:
        return adopt_refused(refused)
    if note:
        say(note)
    say(f"modules: adopted, {count} modules, {edges} edges")
    return 0


def make_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(prog="modules.py", description="Read the module registry config/modules.ini.")
    common = argparse.ArgumentParser(add_help=False)
    common.add_argument("--root", metavar="DIR", default="", help="the repo to judge (default: the git top level of the cwd)")
    sub = parser.add_subparsers(dest="verb", metavar="verb")
    sub.required = True
    one = sub.add_parser("list", parents=[common], help="one line per module, members expanded")
    one.add_argument("--json", action="store_true", help="a JSON array instead")
    one = sub.add_parser("of", parents=[common], help="the module of each path (`-` for none)")
    one.add_argument("paths", nargs="+", metavar="PATH")
    one = sub.add_parser("paths", parents=[common], help="the globs of a module (a family unbound)")
    one.add_argument("name")
    one = sub.add_parser("dependents", parents=[common], help="modules depending on NAME, transitively")
    one.add_argument("names", nargs="+", metavar="NAME")
    sub.add_parser("lint", parents=[common], help="refuse a file the registry rules reject")
    sub.add_parser("check", parents=[common], help="the boundary gate: lint, freshness, then every import edge")
    one = sub.add_parser("adopted", parents=[common], help="the closing check of the adopt runbook")
    one.add_argument("--retired", nargs="+", required=True, metavar="PATH",
                     help="each path of the deleted hand-written gate (an empty value states there was none)")
    return parser


VERBS = {"list": verb_list, "of": verb_of, "paths": verb_paths, "dependents": verb_dependents, "lint": verb_lint,
         "check": verb_check, "adopted": verb_adopted}


def main(argv: list[str]) -> int:
    args = make_parser().parse_args(argv)
    try:
        if args.root:
            root = Path(args.root)
            if not root.is_dir():
                raise RegistryError(f"--root {args.root}: not a directory")
        else:
            root = toplevel(Path.cwd())
        return VERBS[args.verb](root, args)
    except RegistryError as exc:
        say(str(exc), err=True)
        return 1


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
