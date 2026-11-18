#!/usr/bin/env python3
# managed by devkit — do not edit; fix upstream in ~/Developer/projects/devkit
"""go-events: filter a `go test -json` stream like plain `go test`, and judge the skip and flaky ledgers (TESTKIT).

    go test -json ... | python3 scripts/kit/go-events.py [--module-path P] [--module-dir D] [--short]
        [--ledger FILE] [--flaky-ledger FILE] [--count N] [--today YYYY-MM-DD] [--skip-pkgs FILE]
        [--results FILE --tier light|full] [--expect NAME,...] [--seen FILE]

Prints on stdout as the events arrive (each print flushed): a package-level output line starting `ok`, `FAIL`, `?` or
`coverage:` (whole); every build-output; a test's buffered output when it fails. A passing test's `=== RUN`, `--- PASS`
and log lines, and a bare `PASS`, print nothing. A line that is not JSON is printed as-is; a JSON event of a shape it does
not know is skipped. At EOF: `go-test: <n> skipped, <k> ledgered`.

`<pkg>` in the ledgers is the import path; with --module-path P and --module-dir D (the module's dir under the git top,
`.` for the root) the P prefix becomes D: `<D>/<rest>`, normalised.

Skip ledger (--ledger FILE, and not --short): rows `<pkg>\\t<test>\\t<reason>`; a skip with no row is red on stderr, once.
Flaky mode (--count N and --flaky-ledger FILE): rows `<pkg>\\t<test>\\t<until YYYY-MM-DD>\\t<reason>`; an unlisted failure,
a row past `until` or more than 14 days ahead, a malformed row and a listed test that ran green are red; a listed failure
inside its date prints and is not red; a package that failed with no failing test (init panic, TestMain os.Exit(1),
timeout) is red. A package that did not build is exit 2 (unmeasured), which wins over exit 1.
--skip-pkgs FILE gets the packages with a test-level skip, sorted.
--results FILE gets one JSON line per package: {"id", "exit", "wall_s", "cached", "tests", "skips", "tier"} (the adapter's unit
result of go-test.sh; `id` is the repo-relative package dir; a package red by the skip ledger has exit 1; `tier` is --tier).
--expect NAME,... (FB-1136): a passing test of that name prints `go-test: pass <name> (<pkg>)`, and --seen FILE gets the names
that passed, one per line, so the caller can fail on a named test that never ran.

Exit 0 clean, 1 any red judgement, 2 a build failure in flaky mode. It does not mirror go test's own exit (the shell takes
that from PIPESTATUS). Stdlib only, python3 >= 3.9."""
from __future__ import annotations

import argparse
import datetime
import json
import posixpath
import re
import sys
from pathlib import Path
from typing import Any, Dict, IO, Iterable, List, Optional, Set, Tuple

PACKAGE_PREFIXES = ("ok", "FAIL", "?", "coverage:")
MAX_DAYS_AHEAD = 14  # check-select's RAISE_DAYS
DATE_RE = re.compile(r"^\d{4}-\d\d-\d\d$")
SKIP_LEDGER = "config/go-skip.ledger"

Key = Tuple[str, str]


def read_rows(path: Optional[str]) -> List[Tuple[int, List[str]]]:
    """The TAB-separated rows of a ledger as (line number, fields); comments, blank lines and a missing file are none."""
    if not path:
        return []
    try:
        text = Path(path).read_text(encoding="utf-8", errors="replace")
    except OSError:
        return []
    rows: List[Tuple[int, List[str]]] = []
    for number, raw in enumerate(text.splitlines(), 1):
        if not raw.strip() or raw.strip().startswith("#"):
            continue
        rows.append((number, [field.strip() for field in raw.split("\t")]))
    return rows


def skip_ledger_keys(path: Optional[str]) -> Set[Key]:
    return {(fields[0], fields[1]) for _, fields in read_rows(path) if len(fields) >= 2 and fields[0] and fields[1]}


class Judge:
    """One pass over the stream: print as events arrive, keep the facts the EOF judgements need."""

    def __init__(self, args: argparse.Namespace, out: IO[str]) -> None:
        self.args = args
        self.out = out
        self.flaky = args.count is not None and bool(args.flaky_ledger)
        self.names: Dict[str, str] = {}
        self.buffers: Dict[Key, List[str]] = {}
        self.skips: Dict[Key, str] = {}
        self.counts: Dict[Key, List[int]] = {}  # (pkg, test) -> [pass, fail]
        self.tested: Set[str] = set()  # raw packages with a test event
        self.pkg_failed: Set[str] = set()  # raw packages with a package-level fail
        self.build_output: Set[str] = set()
        self.build_failed: List[str] = []
        self.family: Dict[str, Set[str]] = {}  # pkg -> test names seen (flaky: parent detection)
        self.expect = {name for name in (args.expect or "").split(",") if name}
        self.seen: Set[str] = set()  # expected names that passed
        self.units: Dict[str, Dict[str, Any]] = {}  # pkg key -> its result row
        self.skip_red: Set[str] = set()  # pkg keys with a skip the ledger does not hold

    def emit(self, text: str) -> None:
        if not text.endswith("\n"):
            text += "\n"
        self.out.write(text)
        self.out.flush()

    def pkg_key(self, name: str) -> str:
        """`<pkg>` of the ledgers: the import path, or its module-relative form under the git top."""
        cached = self.names.get(name)
        if cached is not None:
            return cached
        key = name
        prefix = self.args.module_path
        if prefix and (name == prefix or name.startswith(prefix.rstrip("/") + "/")):
            rest = name[len(prefix):].strip("/")
            key = posixpath.normpath(posixpath.join(self.args.module_dir or ".", rest))
        self.names[name] = key
        return key

    def feed(self, line: str) -> None:
        stripped = line.strip()
        if not stripped.startswith("{"):
            self.emit(line.rstrip("\r\n"))
            return
        try:
            event = json.loads(stripped)
        except ValueError:
            self.emit(line.rstrip("\r\n"))
            return
        if isinstance(event, dict) and isinstance(event.get("Action"), str):
            self.event(event)

    def event(self, event: Dict[str, Any]) -> None:
        action = event["Action"]
        output = event.get("Output")
        output = output if isinstance(output, str) else ""
        if action == "build-output":
            self.build_output.add(import_path(event))
            if output:
                self.emit(output)
            return
        if action == "build-fail":
            self.build_failed.append(import_path(event))
            return
        package = event.get("Package")
        if not isinstance(package, str) or not package:
            return
        test = event.get("Test")
        if not isinstance(test, str) or not test:
            self.package_event(package, action, output, event)
            return
        self.tested.add(package)
        key = (self.pkg_key(package), test)
        if self.flaky:
            self.family.setdefault(key[0], set()).add(test)
        if action == "output":
            self.buffers.setdefault(key, []).append(output)
        elif action == "pass":
            self.counts.setdefault(key, [0, 0])[0] += 1
            self.unit(key[0])["tests"] += 1
            self.buffers.pop(key, None)
            if test in self.expect:
                self.seen.add(test)
                self.emit(f"go-test: pass {test} ({key[0]})")
        elif action == "fail":
            self.counts.setdefault(key, [0, 0])[1] += 1
            self.unit(key[0])["tests"] += 1
            for text in self.buffers.pop(key, []):
                self.emit(text)
        elif action == "skip":
            self.unit(key[0])["skips"] += 1
            buffered = self.buffers.pop(key, [])
            if key not in self.skips:
                self.skips[key] = first_skip_line(buffered)

    def unit(self, key: str) -> Dict[str, Any]:
        """The result row of package `key`, made on first sight."""
        return self.units.setdefault(key, {"id": key, "exit": 0, "wall_s": 0.0, "cached": False, "tests": 0, "skips": 0,
                                           "tier": self.args.tier})

    def package_event(self, package: str, action: str, output: str, event: Dict[str, Any]) -> None:
        row = self.unit(self.pkg_key(package))
        if action == "output":
            if output.startswith(PACKAGE_PREFIXES):
                self.emit(output)
            if output.startswith("ok") and "(cached)" in output:
                row["cached"] = True
        elif action in ("pass", "skip", "fail"):
            elapsed = event.get("Elapsed")
            row["wall_s"] = float(elapsed) if isinstance(elapsed, (int, float)) else 0.0
            if action == "fail":
                row["exit"] = 1
        if action == "fail":
            self.pkg_failed.add(package)
            if event.get("FailedBuild"):
                self.build_failed.append(package)

    def unbuilt(self) -> List[str]:
        """Packages that did not build: a build-fail, a fail with a FailedBuild, or a fail with no test events but a build-output."""
        names = list(self.build_failed)
        for package in sorted(self.pkg_failed):
            if package not in self.tested and package in self.build_output:
                names.append(package)
        return sorted(set(names))

    def skip_verdict(self) -> int:
        """The skip count line; the ledger judgement unless --short. 1 when a skip has no row."""
        known = set() if self.args.short else skip_ledger_keys(self.args.ledger)
        red = 0
        ledgered = 0
        for (package, test), reason in self.skips.items():
            if (package, test) in known:
                ledgered += 1
            elif self.args.ledger and not self.args.short:
                print(f"go-test: skip not in {SKIP_LEDGER}: {package}\t{test}\t{reason}", file=sys.stderr)
                self.skip_red.add(package)
                red = 1
        self.emit(f"go-test: {len(self.skips)} skipped, {ledgered} ledgered")
        return red

    def flaky_verdict(self) -> int:
        """0, 1 (red) or 2 (a build failure, unmeasured)."""
        path = self.args.flaky_ledger
        today = self.args.today or datetime.datetime.now(datetime.timezone.utc).date()
        red = 0
        rows: Dict[Key, datetime.date] = {}
        for number, fields in read_rows(path):
            until = parse_date(fields[2]) if len(fields) >= 4 and fields[0] and fields[1] and fields[3] else None
            if until is None:
                print(f"go-test: flaky: bad row in {path} line {number}: want <pkg>\\t<test>\\t<until YYYY-MM-DD>\\t<reason>", file=sys.stderr)
                red = 1
                continue
            key = (fields[0], fields[1])
            rows[key] = until
            if until < today:
                print(f"go-test: flaky: row past {until.isoformat()}: {key[0]}\t{key[1]}", file=sys.stderr)
                red = 1
            elif (until - today).days > MAX_DAYS_AHEAD:
                print(f"go-test: flaky: until too far (max {MAX_DAYS_AHEAD} days ahead): {key[0]}\t{key[1]}\t{until.isoformat()}", file=sys.stderr)
                red = 1
            else:
                passed, failed = self.counts.get(key, (0, 0))
                if failed:
                    self.emit(f"go-test: flaky: listed failure: {key[0]}\t{key[1]} until {until.isoformat()}")
                elif passed:
                    print(f"go-test: flaky: drained: remove the line: {key[0]}\t{key[1]}", file=sys.stderr)
                    red = 1
        for key, (_, failed) in self.counts.items():
            if not failed or key in rows:
                continue
            prefix = key[1] + "/"
            if any(other.startswith(prefix) for other in self.family.get(key[0], ())):
                continue  # a parent fails with its failing subtest
            print(f"go-test: flaky: unlisted failure: {key[0]}\t{key[1]}", file=sys.stderr)
            red = 1
        unbuilt = self.unbuilt()
        for name in unbuilt:
            print(f"go-test: flaky: build failed: {name}", file=sys.stderr)
        # a package that died with no failing test (init panic, TestMain os.Exit(1), package timeout) is no pass
        failing = {pkg for (pkg, _), (_, failed) in self.counts.items() if failed}
        for package in sorted(self.pkg_failed):
            if package not in unbuilt and self.pkg_key(package) not in failing:
                self.emit(f"go-test: flaky: package {self.pkg_key(package)} failed with no failing test: not a pass")
                red = 1
        return 2 if unbuilt else red


def import_path(event: Dict[str, Any]) -> str:
    """The package of a build event: ImportPath is `pkg [pkg.test]`."""
    return str(event.get("ImportPath") or event.get("Package") or "").split(" ")[0]


def first_skip_line(lines: Iterable[str]) -> str:
    """The first non-empty output line that mentions skip (case-insensitive), stripped; else empty."""
    for chunk in lines:
        for line in chunk.splitlines():
            text = line.strip()
            if text and "skip" in text.lower():
                return text
    return ""


def parse_date(text: str) -> Optional[datetime.date]:
    if not DATE_RE.match(text):
        return None
    try:
        return datetime.date.fromisoformat(text)
    except ValueError:
        return None


def date_arg(text: str) -> datetime.date:
    value = parse_date(text)
    if value is None:
        raise argparse.ArgumentTypeError("want YYYY-MM-DD")
    return value


def count_arg(text: str) -> int:
    value = int(text)
    if value < 1:
        raise argparse.ArgumentTypeError("must be >= 1")
    return value


def parse_args(argv: List[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(prog="go-events.py", description="filter and judge a `go test -json` stream")
    parser.add_argument("--module-path", default="")
    parser.add_argument("--module-dir", default=".")
    parser.add_argument("--short", action="store_true")
    parser.add_argument("--ledger")
    parser.add_argument("--flaky-ledger", dest="flaky_ledger")
    parser.add_argument("--count", type=count_arg)
    parser.add_argument("--today", type=date_arg)
    parser.add_argument("--skip-pkgs", dest="skip_pkgs")
    parser.add_argument("--results")
    parser.add_argument("--tier", default="full", choices=("light", "full"))
    parser.add_argument("--expect")
    parser.add_argument("--seen")
    return parser.parse_args(argv)


def write_file(path: Optional[str], text: str, what: str) -> None:
    if not path:
        return
    try:
        Path(path).write_text(text, encoding="utf-8")
    except OSError as err:
        print(f"warn: go-events: cannot write {what} {path}: {err}", file=sys.stderr)


def run(args: argparse.Namespace, lines: Iterable[str], out: IO[str]) -> int:
    judge = Judge(args, out)
    for line in lines:
        judge.feed(line)
    code = judge.skip_verdict()
    if judge.flaky:
        code = max(code, judge.flaky_verdict())
    write_file(args.skip_pkgs, "".join(f"{name}\n" for name in sorted({package for package, _ in judge.skips})), "--skip-pkgs")
    for key in judge.skip_red:
        judge.unit(key)["exit"] = 1
    write_file(args.results, "".join(json.dumps(judge.units[key]) + "\n" for key in sorted(judge.units)), "--results")
    write_file(args.seen, "".join(f"{name}\n" for name in sorted(judge.seen)), "--seen")
    return code


def main(argv: Optional[List[str]] = None, stdin: Optional[Iterable[str]] = None, stdout: Optional[IO[str]] = None) -> int:
    args = parse_args(sys.argv[1:] if argv is None else argv)
    return run(args, sys.stdin if stdin is None else stdin, sys.stdout if stdout is None else stdout)


if __name__ == "__main__":
    for stream in (sys.stdin, sys.stdout):
        reconfigure = getattr(stream, "reconfigure", None)
        if reconfigure is not None:
            reconfigure(errors="replace")  # a stray byte in the stream is not a reason to crash
    try:
        sys.exit(main())
    except BrokenPipeError:
        sys.exit(1)
