#!/usr/bin/env python3
# managed by devkit — do not edit; fix upstream in ~/Developer/projects/devkit
"""modules-ts: the TypeScript / JavaScript import-edge lister of the module boundary gate (scripts/kit/modules.py check).

    python3 scripts/kit/modules-ts.py        (run at the repo root; modules.py runs it so)

Prints one line `<file>:<line> <target-file>` (repo-relative, POSIX) per import specifier that resolves to a listed file
of the repo, deduped; third-party packages resolve to nothing and print nothing. The line is the one the statement
(`import`, `export`, `require(`, `import(`) starts on. Stdlib only, python3 >= 3.9.

Rules
  files      the listed `.ts .tsx .mts .cts .js .jsx .mjs .cjs` files: git ls-files --cached --others --exclude-standard
             (an untracked file is read, an ignored one is not); targets are listed files too
  tokenizer  skips `//` and `/* */` comments, quoted strings and template literals (their `${}` too); reads the
             specifier of `import … from 'x'`, `import 'x'`, `export … from 'x'`, `import('x')`, `require('x')` and
             `import x = require('x')`, across line breaks
  relative   `./x`, `../x` resolve from the importing file's directory
  tsconfig   the root `tsconfig.json` `compilerOptions.baseUrl` and `paths` (comments and trailing commas stripped;
             `extends` is not followed): a bare specifier tries `paths` (the longest matching prefix), then `baseUrl`
  workspace  the root `package.json` `workspaces` (a list, or an object's `packages`) and the `packages:` lines of
             `pnpm-workspace.yaml` name workspace dirs (globs such as `packages/*` expand over the listed
             package.json dirs; a `!` line excludes). Each dir's `package.json` `name` maps `<name>` and
             `<name>/<sub>` into that dir. A bare `<name>` resolves through that package.json's `exports` (`"."`, a
             string or its `import` / `default`), then `main`, then `index.*`; `<name>/<sub>` and every relative
             path resolve by extension (`.ts .tsx .mts .cts .js .jsx .mjs .cjs .d.ts`) and `index.*` lookup, and a
             `./x.js` specifier also finds `x.ts` (and likewise for `.jsx .mjs .cjs`)
  other      any other bare specifier (`react`, `node:fs`) is third party; a `?query` suffix is dropped
  red        a root tsconfig.json or package.json that does not parse stops the lister: exit 1, stderr names it

Known gaps: JSX text and regex literals can be misread by the quote-skipping tokenizer (a regex is guessed from the
token before the `/`); `extends` in tsconfig is not followed; `exports` subpath patterns, `imports` (`#x`) and
node_modules links are not read; a package.json that does not parse below the root is skipped.
"""
from __future__ import annotations

import json
import os
import posixpath
import re
import subprocess
import sys

LS_FILES = ("git", "ls-files", "--cached", "--others", "--exclude-standard", "-z")
SOURCE = (".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs")
EXTS = SOURCE + (".d.ts",)
JS_TO_TS = {".js": (".ts", ".tsx"), ".jsx": (".tsx",), ".mjs": (".mts",), ".cjs": (".cts",)}
REGEX_AFTER = {"return", "typeof", "case", "do", "else", "in", "of", "instanceof", "new", "delete", "void", "throw",
               "yield", "await"}
SCAN_LIMIT = 400  # tokens an import/export clause may span before `from`


def fail(message: str) -> None:
    sys.stderr.write(f"modules-ts: {message}\n")
    sys.exit(1)


def listed_files() -> list:
    proc = subprocess.run(list(LS_FILES), capture_output=True)
    if proc.returncode != 0:
        lines = proc.stderr.decode("utf-8", "replace").strip().splitlines()
        fail(f"git ls-files: {lines[0] if lines else 'exit ' + str(proc.returncode)}")
    return [os.fsdecode(item) for item in proc.stdout.split(b"\0") if item]


# ---- the tokenizer ---------------------------------------------------------------------------------------------

class Lexer:
    """Tokens (kind, value, line): `w` a word, `s` a quoted string (its raw text), `p` one punctuation character.
    Comments and template literals produce none."""

    def __init__(self, src: str) -> None:
        self.s = src
        self.i = 0
        self.line = 1

    def comment(self) -> None:
        s = self.s
        if s.startswith("//", self.i):
            end = s.find("\n", self.i)
            self.i = len(s) if end < 0 else end
        else:
            end = s.find("*/", self.i + 2)
            end = len(s) if end < 0 else end + 2
            self.line += s.count("\n", self.i, end)
            self.i = end

    def quoted(self):
        """At a quote: the raw text, or None for a string that is not closed on its line (JSX text, a lone apostrophe)."""
        s, n = self.s, len(self.s)
        quote, j = s[self.i], self.i + 1
        while j < n:
            ch = s[j]
            if ch == "\\":
                if s[j + 1:j + 2] == "\n":
                    self.line += 1
                j += 2
                continue
            if ch == quote or ch == "\n":
                break
            j += 1
        closed = j < n and s[j] == quote
        value = s[self.i + 1:j] if closed else None
        self.i = j + 1 if closed else max(j, self.i + 1)
        return value

    def template(self) -> None:
        s, n = self.s, len(self.s)
        j = self.i + 1
        while j < n:
            ch = s[j]
            if ch == "\\":
                if s[j + 1:j + 2] == "\n":
                    self.line += 1
                j += 2
                continue
            if ch == "`":
                self.i = j + 1
                return
            if ch == "\n":
                self.line += 1
            elif ch == "$" and s[j + 1:j + 2] == "{":
                self.i = j + 2
                self.expr()
                j = self.i
                continue
            j += 1
        self.i = n

    def expr(self) -> None:
        """Skip a template's `${ … }` body (strings, nested templates and comments inside it included)."""
        s, n = self.s, len(self.s)
        depth = 1
        while self.i < n:
            ch = s[self.i]
            if ch == "\n":
                self.line += 1
                self.i += 1
            elif ch == "/" and s[self.i + 1:self.i + 2] in ("/", "*"):
                self.comment()
            elif ch in "'\"":
                self.quoted()
            elif ch == "`":
                self.template()
            else:
                self.i += 1
                if ch == "{":
                    depth += 1
                elif ch == "}":
                    depth -= 1
                    if depth == 0:
                        return

    def regex(self) -> bool:
        """At a `/` that starts a regex literal: skip it and its flags; False when it is not closed on its line."""
        s, n = self.s, len(self.s)
        j, in_class = self.i + 1, False
        while j < n:
            ch = s[j]
            if ch == "\n":
                return False
            if ch == "\\":
                j += 2
                continue
            if ch == "[":
                in_class = True
            elif ch == "]":
                in_class = False
            elif ch == "/" and not in_class:
                j += 1
                while j < n and (s[j].isalnum() or s[j] == "_"):
                    j += 1
                self.i = j
                return True
            j += 1
        return False

    def tokens(self) -> list:
        s, n = self.s, len(self.s)
        out: list = []
        while self.i < n:
            ch = s[self.i]
            if ch == "\n":
                self.line += 1
                self.i += 1
            elif ch.isspace() or ch == "﻿":
                self.i += 1
            elif ch == "/" and s[self.i + 1:self.i + 2] in ("/", "*"):
                self.comment()
            elif ch in "'\"":
                line = self.line
                value = self.quoted()
                if value is not None:
                    out.append(("s", value, line))
            elif ch == "`":
                self.template()
            elif ch.isalnum() or ch in "_$":
                j = self.i + 1
                while j < n and (s[j].isalnum() or s[j] in "_$"):
                    j += 1
                out.append(("w", s[self.i:j], self.line))
                self.i = j
            else:
                if ch == "/" and self.regex_allowed(out) and self.regex():
                    continue
                out.append(("p", ch, self.line))
                self.i += 1
        return out

    @staticmethod
    def regex_allowed(out: list) -> bool:
        if not out:
            return True
        kind, value, _ = out[-1]
        if kind == "p":
            return value not in ")]}"
        return kind == "w" and value in REGEX_AFTER


def specifiers(src: str) -> list:
    """[(line, specifier)] for every import, export-from, dynamic import and require of the source text."""
    t = Lexer(src).tokens()
    n = len(t)

    def at(j: int) -> tuple:
        return t[j][:2] if 0 <= j < n else ("", "")

    def clause(j: int):
        """The specifier after `from`, scanning an import/export clause from token j; None when the clause ends first."""
        for k in range(j, min(j + SCAN_LIMIT, n)):
            kind, value = at(k)
            if kind == "w" and value == "from" and at(k + 1)[0] == "s":
                return t[k + 1][1]
            if kind == "w" or (kind == "p" and value in "{},*"):
                continue
            return None
        return None

    out = []
    for i, (kind, value, line) in enumerate(t):
        if kind != "w" or at(i - 1) == ("p", "."):
            continue
        found = None
        if value == "import":
            if at(i + 1)[0] == "s":
                found = t[i + 1][1]
            elif at(i + 1) == ("p", "("):
                if at(i + 2)[0] == "s" and at(i + 3) in (("p", ")"), ("p", ",")):
                    found = t[i + 2][1]
            else:
                found = clause(i + 1)
        elif value == "export":
            found = clause(i + 1)
        elif value == "require":
            if at(i + 1) == ("p", "(") and at(i + 2)[0] == "s" and at(i + 3) == ("p", ")"):
                found = t[i + 2][1]
        if found:
            out.append((line, found))
    return out


# ---- configuration ---------------------------------------------------------------------------------------------

def jsonc(text: str):
    """json.loads of text with `//` and `/* */` comments and trailing commas removed (both outside strings)."""
    text = text.lstrip("\ufeff")
    out: list = []
    n, i = len(text), 0

    def skip_trivia(j: int) -> int:
        while True:
            while j < n and text[j].isspace():
                j += 1
            if text.startswith("//", j):
                end = text.find("\n", j)
                j = n if end < 0 else end
            elif text.startswith("/*", j):
                end = text.find("*/", j + 2)
                j = n if end < 0 else end + 2
            else:
                return j

    while i < n:
        ch = text[i]
        if ch == '"':
            j = i + 1
            while j < n and text[j] != '"':
                j += 2 if text[j] == "\\" else 1
            out.append(text[i:j + 1])
            i = j + 1
        elif text.startswith("//", i) or text.startswith("/*", i):
            i = skip_trivia(i)
        elif ch == ",":
            after = skip_trivia(i + 1)
            if not (after < n and text[after] in "}]"):
                out.append(",")
            i += 1
        else:
            out.append(ch)
            i += 1
    return json.loads("".join(out))


def read_json(path: str, red: bool):
    try:
        with open(path, encoding="utf-8") as handle:
            return jsonc(handle.read())
    except FileNotFoundError:
        return None
    except (OSError, ValueError) as exc:
        if red:
            fail(f"{path} does not parse: {exc}")
        return None


def glob_re(pattern: str):
    """A workspace glob as a regex over a repo-relative dir: `*` and `?` stay in a segment, `**` crosses segments."""
    text = pattern.strip()
    text = text[2:] if text.startswith("./") else text
    text = text.rstrip("/")
    text = "" if text == "." else text
    out, i = [], 0
    while i < len(text):
        if text.startswith("**/", i):
            out.append("(?:[^/]+/)*")
            i += 3
        elif text.startswith("**", i):
            out.append(".*")
            i += 2
        elif text[i] == "*":
            out.append("[^/]*")
            i += 1
        elif text[i] == "?":
            out.append("[^/]")
            i += 1
        else:
            out.append(re.escape(text[i]))
            i += 1
    return re.compile("".join(out))


def workspace_patterns() -> list:
    found: list = []
    root = read_json("package.json", True)
    if isinstance(root, dict):
        ws = root.get("workspaces")
        if isinstance(ws, dict):
            ws = ws.get("packages")
        if isinstance(ws, list):
            found += [w for w in ws if isinstance(w, str)]
    try:
        with open("pnpm-workspace.yaml", encoding="utf-8") as handle:
            lines = handle.read().splitlines()
    except OSError:
        lines = []
    inside = False
    for raw in lines:
        line = re.sub(r"\s#.*\Z", "", raw).rstrip()
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        if re.match(r"packages\s*:", line):
            inside = True
            rest = line.split(":", 1)[1].strip()
            if rest.startswith("["):
                found += [w.strip().strip("'\"") for w in rest.strip("[]").split(",") if w.strip()]
            continue
        if inside and line[0] in " \t-":
            hit = re.match(r"\s*-\s*(.+)\Z", line)
            if hit:
                found.append(hit.group(1).strip().strip("'\""))
        else:
            inside = False
    return found


class Resolver:
    def __init__(self, files: list) -> None:
        self.files = set(files)
        self.base_url = None
        self.paths: dict = {}
        cfg = read_json("tsconfig.json", True)
        options = cfg.get("compilerOptions") if isinstance(cfg, dict) else None
        if isinstance(options, dict):
            if isinstance(options.get("baseUrl"), str):
                self.base_url = posixpath.normpath(options["baseUrl"])
            raw = options.get("paths")
            if isinstance(raw, dict):
                self.paths = {k: [t for t in v if isinstance(t, str)] for k, v in raw.items() if isinstance(v, list)}
        self.packages: dict = {}  # name -> (dir, package.json data)
        patterns = workspace_patterns()
        plus = [glob_re(p) for p in patterns if not p.startswith("!")]
        minus = [glob_re(p[1:]) for p in patterns if p.startswith("!")]
        for f in sorted(self.files):
            if os.path.basename(f) != "package.json" or "node_modules/" in "/" + f:
                continue
            folder = posixpath.dirname(f)
            if not any(r.fullmatch(folder) for r in plus) or any(r.fullmatch(folder) for r in minus):
                continue
            data = read_json(f, False)
            if isinstance(data, dict) and isinstance(data.get("name"), str):
                self.packages[data["name"]] = (folder, data)
        self.names = sorted(self.packages, key=len, reverse=True)

    # file lookup
    def probe_file(self, path: str):
        path = posixpath.normpath(path)
        if path == ".." or path.startswith("../"):
            return None
        if path in self.files:
            return path
        stem, ext = posixpath.splitext(path)
        for swap in JS_TO_TS.get(ext, ()):
            if stem + swap in self.files:
                return stem + swap
        for e in EXTS:
            if path + e in self.files:
                return path + e
        return None

    def probe_dir(self, path: str):
        for e in EXTS:
            cand = posixpath.normpath(posixpath.join(path, "index" + e))
            if cand in self.files:
                return cand
        return None

    def path(self, path: str):
        return self.probe_file(path) or self.probe_dir(path)

    # entries of a workspace package
    def flatten(self, value) -> list:
        if isinstance(value, str):
            return [value]
        if isinstance(value, list):
            return [x for item in value for x in self.flatten(item)]
        if isinstance(value, dict):
            return [x for key in ("import", "default") if key in value for x in self.flatten(value[key])]
        return []

    def entry(self, folder: str, data: dict):
        cands: list = []
        exports = data.get("exports")
        if isinstance(exports, dict) and any(k.startswith(".") for k in exports):
            cands += self.flatten(exports.get("."))
        elif exports is not None:
            cands += self.flatten(exports)
        if isinstance(data.get("main"), str):
            cands.append(data["main"])
        for cand in cands:
            got = self.path(posixpath.join(folder, cand))
            if got:
                return got
        return self.probe_dir(folder)

    def alias(self, spec: str):
        best, score, picks = None, -1, []
        for pattern, targets in self.paths.items():
            if "*" in pattern:
                pre, post = pattern.split("*", 1)
                if len(spec) >= len(pre) + len(post) and spec.startswith(pre) and spec.endswith(post):
                    mid = spec[len(pre):len(spec) - len(post)]
                    if len(pre) > score:
                        best, score, picks = pattern, len(pre), [t.replace("*", mid, 1) for t in targets]
            elif pattern == spec:
                best, score, picks = pattern, 10 ** 6, list(targets)
        for pick in picks:
            got = self.path(posixpath.join(self.base_url or ".", pick))
            if got:
                return got
        return None

    def bare(self, spec: str):
        got = self.alias(spec)
        if got:
            return got
        if self.base_url is not None:
            got = self.path(posixpath.join(self.base_url, spec))
            if got:
                return got
        for name in self.names:
            if spec == name or spec.startswith(name + "/"):
                folder, data = self.packages[name]
                if spec == name:
                    return self.entry(folder, data)
                return self.path(posixpath.join(folder, spec[len(name) + 1:]))
        return None

    def resolve(self, source: str, spec: str):
        spec = spec.split("?", 1)[0]
        if not spec or spec.startswith("/") or spec.startswith("#") or spec.startswith("node:"):
            return None
        if spec == "." or spec == ".." or spec.startswith("./") or spec.startswith("../"):
            return self.path(posixpath.join(posixpath.dirname(source), spec))
        return self.bare(spec)


def main() -> int:
    files = listed_files()
    resolver = Resolver(files)
    lines = []
    for path in sorted(f for f in files if f.endswith(SOURCE) and os.path.isfile(f)):
        try:
            with open(path, encoding="utf-8", errors="replace") as handle:
                text = handle.read()
        except OSError:
            continue
        seen = set()
        for line, spec in specifiers(text):
            target = resolver.resolve(path, spec)
            if target and target != path and (line, target) not in seen:
                seen.add((line, target))
                lines.append(f"{path}:{line} {target}")
    if lines:
        sys.stdout.write("\n".join(lines) + "\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
