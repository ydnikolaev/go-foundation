#!/usr/bin/env python3
# managed by devkit — do not edit; fix upstream in ~/Developer/projects/devkit
"""kit-verbs: `python3 scripts/kit/check-verbs.py`, the gate of the make vocabulary (standards/make.md §1).

It hangs on `check`, `check-full` and `check-docker` as a prerequisite in the managed GNUmakefile, which a project's own
recipe cannot route around, and `devkit update --apply` runs it after a render. It reads make's own database
(`make -pRrq :`, nothing runs, never printed: it holds the environment) and judges six static rules, no time:
V1 names (a required verb missing, a synonym of `check` defined, a goal of the project whose recipe runs what a kit goal
runs: `retire: <goal> -> <kit goal>`, kept by a `<goal>: <reason>` row of config/goal-keep.list), V2 shape (no recipe on a verb outside a managed file;
`check` is `kit-verbs` plus CHECK_EXTRA; the `check-full` tier line; `e2e` only in the e2e module; no `.WAIT` below make 4.4),
V3 reach (`check` reaches no other verb, no `check-<scope>`, no docker goal; one make builds each target: a check-select target
reaches no other and no gate item, and no recipe is reached by two of the gate's makes), V4 stack paths (a project map row does not hide a stack's tests), V5 lists
(heavy_targets, light_targets, docker_targets name no verb), V6 orphans (a map row, an `@every` line or a list names a target
the database lacks; CHECK_DOCKER with no check-docker rule). A refusal prints `file:line` and exits 1. A file whose first
line carries another tool's managed header is not the project's to edit: its names are one `finding:` line, exit 0.
Exit: 0 holds, 1 refused, 2 the database or a map could not be read. `--retire` prints the retire hits as JSON and judges
nothing (the update menu reads it). Stdlib only, python3 >= 3.9, POSIX."""
from __future__ import annotations

import importlib.util
import json
import os
import re
import shlex
import subprocess
import sys
from pathlib import Path

sys.dont_write_bytecode = True  # it loads check-select.py and check-units.py by path: no __pycache__ in scripts/kit/
TAG = "kit-verbs:"
REQUIRED = ("help", "check", "check-full", "test", "lint", "fmt")
VERBS = (*REQUIRED, "check-docker", "check-only", "check-release", "sweep", "e2e")
SYNONYMS = ("check-fast", "check-quick", "check-light", "check-diff", "check-for-diff", "check-changed", "check-all", "ci", "verify")
SHAPED = ("check", "check-full", "check-docker", "test", "lint", "fmt")  # no recipe outside a managed file; check-release is a default a project may own
KEEP = "config/goal-keep.list"  # project-owned seed: `<goal>: <reason>` per line, `#` lines and blank lines ignored
RETIRE = (("go test", "kit-go-test", "go"), ("gofmt", "kit-go-fmt", "go"), ("go get", "kit-go-add", "go"), ("go mod tidy", "kit-go-add", "go"),
          ("compose postgres", "kit-testdb-up", "docker"))  # (what a recipe line runs, the kit goal that does it, the module of that goal)
SHAPE = "`check: $(CHECK_EXTRA)`, `check-full: KIT_TIER := full` and `check-full: $(CHECK_EXTRA) $(CHECK_FULL)`, no recipe"
NEXT = {
    "V1": "next: delete the synonym (and its script) or rename it `<name>-check`; copy the seed lines of the missing verb; "
          f"for a `retire:` line delete the goal and run the kit goal, or keep it as `<goal>: <reason>` in {KEEP}",
    "V2": f"next: the seed is {SHAPE}; copy a missing line into the Makefile; extend a verb with CHECK_EXTRA, CHECK_FULL, CHECK_DOCKER, LINT_ALL, FMT_ALL or a config/check-map row",
    "V3": "next: `check` is the light gate: move the goal to an `@every` line, CHECK_FULL or CHECK_DOCKER, or rename a gate `<name>-check`",
    "V4": "next: name the stack's target in the row (`<glob> <pack target> <yours>`), or delete the row: the pack's rows then apply",
    "V5": "next: delete the verb from the list in .devkit/config.env (the answers file), then `devkit update --apply`",
    "V6": "next: define the target, delete the entry, or add the seed lines of the verb",
}
HEADER = re.compile(r"managed by (\S+)")
ORIGIN = re.compile(r"^# makefile \(from '([^']+)', line (\d+)\)$")
RECIPE = re.compile(r"recipe to execute \(from '([^']+)', line (\d+)\)")
SPECIFIC = re.compile(r"^(\S+): +([A-Za-z_][\w.\-]*) *(?:::|:|\+|\?)?= *(.*)$")
RULE = re.compile(r"^([^\s:#=][^:=]*):(?!=)(.*)$")
VARIABLE = re.compile(r"^([A-Za-z_][\w.\-]*) *(?:::|:|\+|\?)?= ?(.*)$")
FOREIGN_NAME = re.compile(r"-(check|test|lint|fmt|clean)$")
WAIT = re.compile(r"(?<![\w.])\.WAIT(?![\w.])")
LISTS = (("KIT_HEAVY_TARGETS", "heavy_targets"), ("KIT_LIGHT_TARGETS", "light_targets"), ("KIT_DOCKER_TARGETS", "docker_targets"))


class Unreadable(Exception):
    """The database or a map could not be read: exit 2."""


class Rule:
    def __init__(self) -> None:
        self.prereqs: list[str] = []
        self.origin = ""  # `file:line` of the recipe, none for a rule with no recipe
        self.vars: list[tuple[str, str]] = []  # target-specific `VAR := value`
        self.recipe: list[str] = []  # the recipe lines as written, a `\` continuation joined to its line


class Database:
    """The rules, the target-specific variables and the last value (and origin) of each variable of `make -pRrq :`."""

    def __init__(self, text: str) -> None:
        head, _, rest = text.partition("\n# Files")
        self.vars: dict[str, tuple[str, str]] = {}
        self.rules: dict[str, Rule] = {}
        origin = ""
        for line in head.splitlines():
            found = ORIGIN.match(line)
            if found:
                origin = f"{found[1]}:{found[2]}"
            elif not line.startswith("#"):
                var = VARIABLE.match(line)
                if var:
                    self.vars[var[1]] = (var[2], origin)
                origin = ""
        for block in rest.split("\n\n"):
            lines = block.strip("\n").splitlines()
            if not lines or lines[0].startswith("# Not a target"):
                continue
            rule, body = None, False
            for line in lines:
                if line.startswith("#"):
                    found = RECIPE.search(line)
                    if found and rule is not None:
                        rule.origin, body = f"{found[1]}:{found[2]}", True
                elif line.startswith("\t"):
                    if rule is not None and body:
                        last = rule.recipe[-1] if rule.recipe else ""
                        if (len(last) - len(last.rstrip("\\"))) % 2 == 1:  # the line before ends in a `\`: this one continues it
                            rule.recipe[-1] = f"{last[:-1]} {line.strip()}"
                        else:
                            rule.recipe.append(line.lstrip("\t"))
                elif line:
                    var, plain = SPECIFIC.match(line), RULE.match(line)
                    name = (var or plain).group(1) if (var or plain) else ""
                    if name and not name.startswith("."):
                        rule = self.rules.setdefault(name, Rule())
                        if var:
                            rule.vars.append((var[2], var[3]))
                        else:  # `.WAIT` (make 4.4) is an order mark, kept by the database as a word: no walk reads it
                            rule.prereqs += [w for w in plain[2].replace("|", " ").split() if w != ".WAIT"]

    def defined(self, name: str) -> bool:
        rule = self.rules.get(name)
        return rule is not None and bool(rule.prereqs or rule.origin)

    def words(self, name: str) -> list[str]:
        return [w for w in self.vars.get(name, ("", ""))[0].split() if w != ".WAIT"]

    def reach(self, name: str, skip=()) -> set[str]:
        """Every target `name` reaches through prerequisites, `skip` neither entered nor counted."""
        seen, todo = set(), [name]
        while todo:
            for child in self.rules[node].prereqs if (node := todo.pop()) in self.rules else []:
                if child not in seen and child not in skip:
                    seen.add(child)
                    todo.append(child)
        return seen


def load(name: str):
    spec = importlib.util.spec_from_file_location(f"kit_verbs_{name}", Path(__file__).resolve().parent / f"check-{name}.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def database() -> Database:
    env = {k: v for k, v in os.environ.items() if k not in ("MAKELEVEL", "MAKEFLAGS", "MFLAGS", "MAKEOVERRIDES")}
    try:
        proc = subprocess.run([*shlex.split(env.get("KIT_MAKE", "") or "make"), "--no-print-directory", "-pRrq", ":"],
                              capture_output=True, text=True, errors="replace", stdin=subprocess.DEVNULL, env=env)
    except OSError as exc:
        raise Unreadable(f"{TAG} cannot run make: {exc}")
    # `:` is no target, so make ends with "No rule to make target ':'" after the database; any other `***` is a makefile it could not read
    fatal = [line for line in proc.stderr.splitlines() if "***" in line and "No rule to make target ':'" not in line]
    if fatal or "\n# Files" not in proc.stdout:
        raise Unreadable(f"{TAG} make cannot read the makefiles: {fatal[0] if fatal else 'no database'} (run `make -n`)")
    return Database(proc.stdout)


def owner(path: str) -> str:
    """The tool named by the file's first line (`managed by <tool>`): `devkit`, another tool, or "" for the project's own."""
    try:
        first = Path(path.rpartition(":")[0] or path).read_text(encoding="utf-8", errors="replace").split("\n", 1)[0]
    except OSError:
        return ""
    found = HEADER.search(first)
    return found[1] if found else ""


def project_files() -> list[str]:
    return [p for p in ["Makefile", *sorted(str(p) for p in Path("mk").glob("*.mk"))] if Path(p).is_file() and not owner(p)]


def scan(name: str) -> str:
    """`file:line` of a rule with no recipe that names `name` as a target, in the project's own makefiles."""
    pattern = re.compile(rf"^(?:[\w.%/-]+[ \t]+)*{re.escape(name)}(?:[ \t]+[\w.%/-]+)*[ \t]*:(?!=)")
    for path in project_files():
        for number, line in enumerate(Path(path).read_text(encoding="utf-8", errors="replace").splitlines(), 1):
            if pattern.match(line):
                return f"{path}:{number}"
    return ""


def where(db: Database, name: str) -> str:
    rule = db.rules.get(name)
    return (rule.origin if rule else "") or scan(name) or name


def foreign(db: Database, name: str) -> bool:
    rule = db.rules.get(name)
    return bool(rule and rule.origin and owner(rule.origin) not in ("", "devkit"))


QUOTED = re.compile(r"\"[^\"]*\"|'[^']*'")
GAP = re.compile(r"&&|\|\||[;|&\n`(){}$]+")
LEAD = {"env", "time", "exec", "nice", "command", "then", "do", "else", "if", "elif", "while", "until", "!"}


def commands(line: str) -> list[list[str]]:
    """The commands a recipe line runs, as word lists (the command word first): `@`, `-` and `+` marks, `VAR=x` words, quoted text,
    a trailing comment and `$(GO)` (read as go) dealt with; `;`, `&&`, `||`, `|`, `$(` and the like end a command."""
    text = re.sub(r"\$[({](GO|GOFMT)[)}]", lambda m: m[1].lower(), line)
    text = re.sub(r"(^|\s)#.*", "", QUOTED.sub('""', text))
    out = []
    for part in GAP.split(text):
        words = [w.lstrip("@-+") if n == 0 else w for n, w in enumerate(part.split())]
        while words and (words[0] in LEAD or not words[0] or re.match(r"^[A-Za-z_]\w*=\S*$", words[0])):
            words.pop(0)
            words[:1] = [words[0].lstrip("@-+")] if words else []
        if words:
            out.append([os.path.basename(words[0]), *words[1:]])
    return out


def runs(line: str) -> list[str]:
    """Which of RETIRE's `what` a recipe line runs as a command word (a `docker compose up` needs postgres in the line or in its -f file)."""
    found = []
    for words in commands(line):
        args = [w for w in words[1:] if not w.startswith("-")]
        if words[0] == "go" and args[:1] == ["test"]:
            found.append("go test")
        elif words[0] == "gofmt" or (words[0] == "go" and args[:1] == ["fmt"]):
            found.append("gofmt")
        elif words[0] == "go" and args[:1] == ["get"]:
            found.append("go get")
        elif words[0] == "go" and args[:2] == ["mod", "tidy"]:
            found.append("go mod tidy")
        elif (words[0] == "docker" and args[:1] == ["compose"] or words[0] == "docker-compose") and ({"up", "start"} & set(words[1:])):
            files = [words[i + 1] for i, w in enumerate(words[:-1]) if w in ("-f", "--file")] + [w.split("=", 1)[1] for w in words if w.startswith("--file=")]
            texts = [line] + [Path(f).read_text(encoding="utf-8", errors="replace") for f in files if Path(f).is_file()]
            if any("postgres" in t.lower() for t in texts):
                found.append("compose postgres")
    return found


def keep_rows() -> tuple[dict[str, str], list[str]]:
    """config/goal-keep.list: ({goal: reason}, refusals). A row with no reason is refused with its line number and keeps nothing."""
    keep, refused = {}, []
    try:
        lines = Path(KEEP).read_text(encoding="utf-8", errors="replace").splitlines()
    except OSError:
        return keep, refused
    for number, raw in enumerate(lines, 1):
        row = raw.strip()
        if not row or row.startswith("#"):
            continue
        goal, colon, reason = (part.strip() for part in row.partition(":"))
        if goal and reason:
            keep[goal] = reason
        else:
            refused.append(f"{TAG} V1 {KEEP}:{number}: `{row}` " + ("has no reason" if goal and colon else "is no `<goal>: <reason>` row")
                           + "; write why the goal stays, or delete the row")
    return keep, refused


def retire_hits(db: Database) -> list[dict[str, str]]:
    """Each goal of the project (its recipe is in a file the project owns) whose recipe runs what a kit goal of the database runs,
    unless a keep row holds it: {goal, kit, module, at (file:line)}."""
    keep, hits = keep_rows()[0], []
    for goal, rule in sorted(db.rules.items()):
        if not rule.origin or owner(rule.origin) != "" or goal in keep:
            continue
        ran = {what for line in rule.recipe for what in runs(line)}
        for kit, module in dict.fromkeys((k, m) for what, k, m in RETIRE if what in ran and db.defined(k)):
            hits.append({"goal": goal, "kit": kit, "module": module, "at": rule.origin})
    return hits


def v1(db: Database, goals: list[str]) -> list[str]:
    held = lambda name: name in db.rules if name in ("test", "lint", "fmt") else db.defined(name)  # `lint` is bare while LINT_ALL is empty
    out = [f"{TAG} V1 {name}: not defined (the seed Makefile has it)" for name in REQUIRED if not held(name)]
    docker = db.rules.get("check-docker")
    real = bool(docker and [p for p in docker.prereqs if p != "kit-verbs"])
    if not real and (Path("mk/kit-docker.mk").is_file() or "check-docker" in goals):
        out.append(f"{TAG} V1 check-docker: not defined (the docker module is on; the seed has `check-docker: kit-docker-need check-full $(CHECK_DOCKER)`)")
    for name in SYNONYMS:
        if name in db.rules and not foreign(db, name):
            out.append(f"{TAG} V1 {where(db, name)}: {name} is a second name for check or check-full; delete it")
    out += keep_rows()[1] + [f"{TAG} V1 {hit['at']}: retire: {hit['goal']} -> {hit['kit']}" for hit in retire_hits(db)]
    return out


def v2(db: Database) -> list[str]:
    out = []
    for name in SHAPED:
        rule = db.rules.get(name)
        if rule and rule.origin and owner(rule.origin) == "":
            out.append(f"{TAG} V2 {rule.origin}: {name} has a recipe of its own; a verb is extended by a list, never by a recipe")
    check, extra = db.rules.get("check"), db.words("CHECK_EXTRA")
    if check and not any("$" in word for word in extra) and set(check.prereqs) != {"kit-verbs", *extra}:
        diff = sorted(set(check.prereqs) ^ {"kit-verbs", *extra})
        out.append(f"{TAG} V2 {where(db, 'check')}: check must be `check: $(CHECK_EXTRA)` (and kit-verbs); differs in: {' '.join(diff)}")
    full = db.rules.get("check-full")
    if full and ("KIT_TIER", "full") not in full.vars:
        at = where(db, "check-full")
        at = at if ":" in at else scan("check") or "Makefile"  # a Makefile older than the tier lines has no check-full of its own
        out.append(f"{TAG} V2 {at}: no `check-full: KIT_TIER := full` line")
    rule = db.rules.get("e2e")
    if rule and db.defined("e2e") and not foreign(db, "e2e"):
        module = rule.origin and owner(rule.origin) == "devkit" and Path(rule.origin.rpartition(":")[0]).name == "kit-e2e.mk"
        if not module:
            out.append(f"{TAG} V2 {where(db, 'e2e')}: e2e is the e2e module's goal (mk/kit-e2e.mk); rename yours")
    version = (db.words("MAKE_VERSION") or ["0"])[0]
    if re.match(r"(?:[0-3]\.|4\.[0-3](?:\.|$))", version):  # `.WAIT` is a target name below 4.4, never an order mark
        for path in project_files():
            for number, line in enumerate(Path(path).read_text(encoding="utf-8", errors="replace").splitlines(), 1):
                if WAIT.search(line.split("#", 1)[0]):
                    out.append(f"{TAG} V2 {path}:{number}: `.WAIT` needs GNU make 4.4 and this is {version}; list the pair in written order")
    return out


def v3(db: Database, docker: list[str]) -> list[str]:
    out, seen, queue = [], {"check"}, [("check", "check")]
    while queue:
        parent, node = queue.pop(0)
        for child in db.rules[node].prereqs if node in db.rules else []:
            if child in seen:
                continue
            seen.add(child)
            queue.append((node, child))
            kind = ("a verb" if child in VERBS or child in SYNONYMS else "a check-<scope> goal" if child.startswith("check-") else
                    "a docker goal" if child in docker else "")
            if kind:
                out.append(f"{TAG} V3 {where(db, child)}: check reaches {child} through {parent}, {kind}")
    return out


def once(db: Database, root: Path, cs) -> list[str]:
    """V3, one make per target (FB-1199): a check-select target that reaches, or is reached by, another or an item of
    CHECK_EXTRA, CHECK_FULL or CHECK_DOCKER; a recipe that two of a gate's make processes reach (each target of
    check-select is one make, the outer items another)."""
    t = cs.tree(root, cs.map_files(root, []))
    picked = [name for name in cs.plan(t, cs.load_rows(root, []), cs.load_registry(root, [], t.paths))[0] if name in db.rules]
    skip = {"kit-verbs", "kit-select", "kit-docker-need"}
    items = [w for key in ("CHECK_EXTRA", "CHECK_FULL", "CHECK_DOCKER") for w in db.words(key) if w not in picked and w not in skip]
    reach = {name: db.reach(name, skip) for name in [*picked, *items]}
    outer = set(items).union(*(reach[item] for item in items))
    out, said = [], set()

    def owner(x: str) -> str:
        """The gate item `x` is reached from."""
        return next(i for i in items if x in reach[i])

    def refuse(a: str, b: str, how: str, x: str) -> None:
        if (key := tuple(sorted((a, b)) + [x])) not in said:
            said.add(key)
            out.append(f"{TAG} V3 {where(db, a)}: {how} ({where(db, b)}); one make must build {x}")
            out.append(f"next: list both in one group, in CHECK_FULL or one map row, so one make builds {x} once")
    for name in picked:
        for other in [o for o in picked if o != name and o in reach[name]] + [i for i in items if i in reach[name]]:
            refuse(name, other, f"check-select target {name} reaches {other}", other)
        if name in outer:
            refuse(name, owner(name), f"check-select target {name} is reached by {owner(name)}, a gate item", name)
    makes = [(name, reach[name]) for name in picked] + [("", outer)]
    for at, (a, mine) in enumerate(makes):
        for b, theirs in makes[at + 1:]:
            for x in sorted((mine & theirs) - set(picked) - set(items)):
                if x in db.rules and db.rules[x].origin:
                    two = b or owner(x)
                    refuse(a, two, f"{a} and {two} both reach {x}, a target with a recipe", x)
    return out


def v4(root: Path, cs) -> list[str]:
    rows = cs.load_rows(root, [])
    pack = [row for row in rows if row.where.startswith(".devkit/check-map.d/")]
    texts = [cs.read(p) for p in cs.map_files(root, []) if ".devkit" in p.parts]
    sets = set(load("units").directives(texts)[0])
    listing = subprocess.run(["git", "ls-files", "-co", "--exclude-standard", "-z"], capture_output=True, text=True).stdout
    out, said = [], set()
    for path in filter(None, listing.split("\0")):
        first, own = cs.first_row(rows, path), cs.first_row(pack, path)
        if first is None or first in pack or own is None or first.where in said:
            continue
        need = [t for t in own.targets if t in sets]
        if not need or not first.targets or set(need) & set(first.targets):
            continue  # not a stack path, the row is `-`, or it names the stack's target
        said.add(first.where)
        out.append(f"{TAG} V4 {first.where}: `{first.glob}` is read first for {path} and hides {' '.join(need)}; the row names it not")
    return out


def at_key(root: Path, cs, key: str) -> str:
    lines = cs.read(root / ".devkit" / "config.env").splitlines()
    return f".devkit/config.env:{next((n for n, line in enumerate(lines, 1) if line.strip().startswith(f'{key}=')), 0)}"


def v5(root: Path, cs) -> list[str]:
    return [f"{TAG} V5 {at_key(root, cs, key)}: {label} names {name}, a verb of the vocabulary"
            for key, label in LISTS for name in cs.csv_names(root, key) if name in VERBS]


def v6(db: Database, root: Path, cs) -> list[str]:
    out, held = [], db.defined
    docker, rule = db.words("CHECK_DOCKER"), db.rules.get("check-docker")
    if docker and not (rule and [p for p in rule.prereqs if p != "kit-verbs"]):
        out.append(f"{TAG} V6 {db.vars['CHECK_DOCKER'][1] or 'CHECK_DOCKER'}: CHECK_DOCKER is not empty and no check-docker rule runs it")
    for row in cs.load_rows(root, []):
        out += [f"{TAG} V6 {row.where}: names {t}, which no rule defines" for t in row.targets if not held(t)]
    for path in cs.map_files(root, []):
        for number, raw in enumerate(cs.read(path).splitlines(), 1):
            words = raw.split("#", 1)[0].split()
            if words[:1] == ["@every"]:
                at = path.relative_to(root).as_posix() if path.is_relative_to(root) else str(path)
                out += [f"{TAG} V6 {at}:{number}: @every names {t}, which no rule defines" for t in words[1:] if not held(t)]
    for key, label in LISTS:
        out += [f"{TAG} V6 {at_key(root, cs, key)}: {label} names {n}, which no rule defines" for n in cs.csv_names(root, key)
                if "%" not in n and not held(n)]
    return out


def findings(db: Database) -> list[str]:
    """One line per foreign-managed file: the names of the vocabulary's shape it owns are that tool's to change."""
    owned: dict[str, list[str]] = {}
    for name, rule in db.rules.items():
        if rule.origin and foreign(db, name) and (name in VERBS or name in SYNONYMS or FOREIGN_NAME.search(name)):
            owned.setdefault(rule.origin.rpartition(":")[0], []).append(name)
    return [f"{TAG} finding: {file} is managed by {owner(file)}; its {' '.join(sorted(names))} are for that tool's own template to rename, not this repo's"
            for file, names in sorted(owned.items())]


def main() -> int:
    root = Path.cwd()
    if sys.argv[1:] == ["--retire"]:  # what the update menu reads: the one detector, never a copy of its rule
        try:
            print(json.dumps({"retire": retire_hits(database()), "keep": keep_rows()[1]}))
        except Unreadable as stop:
            print(stop, file=sys.stderr)
            return 2
        return 0
    cs = load("select")
    try:
        db = database()
        goals = os.environ.get("KIT_GOALS", "").split()
        docker = ["check-docker", *cs.csv_names(root, "KIT_DOCKER_TARGETS")]
        found = {"V1": v1(db, goals), "V2": v2(db), "V3": v3(db, docker) + once(db, root, cs), "V4": v4(root, cs), "V5": v5(root, cs), "V6": v6(db, root, cs)}
    except (cs.Stop, Unreadable) as stop:
        print("\n".join(stop.lines) if isinstance(stop, cs.Stop) else stop, file=sys.stderr)
        return 2
    for line in findings(db):
        print(line)
    refused = [line for lines in found.values() for line in lines]
    if not refused:
        print(f"{TAG} 6 rules hold")
        return 0
    print("\n".join(refused), file=sys.stderr)
    print("\n".join(NEXT[rule] for rule, lines in found.items() if lines), file=sys.stderr)
    return 1


if __name__ == "__main__":
    sys.exit(main())
