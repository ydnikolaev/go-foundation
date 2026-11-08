# managed by devkit — do not edit; fix upstream in ~/Developer/projects/devkit
# /// script
# requires-python = ">=3.10"
# dependencies = ["pyyaml"]
# ///
"""Tests for docs.py (managed by devkit — do not edit; fix upstream in ~/Developer/projects/devkit). Each test builds a fixture repo in a tempdir and runs the CLI against it."""
import copy
import datetime
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

import yaml

TOOL = Path(__file__).resolve().parent / "docs.py"
BODY = "## Context\n\nc\n\n## Decision\n\nd\n\n## Consequences\n\nq\n"


def adr(id_="ADR-001", **over):
    m = {"id": id_, "title": "T", "status": "accepted", "scope": "project", "date": "2026-10-03"}
    m.update(over)
    m = {k: v for k, v in m.items() if v is not None}
    return "---\n" + yaml.safe_dump(m, sort_keys=False) + "---\n\n" + BODY


def item(id_="TT-001", **over):
    m = {"id": id_, "title": "T", "status": "open", "priority": "p2", "epic": None,
         "origin": "o", "closes_when": "c", "created": "2026-10-03"}
    m.update(over)
    m = {k: v for k, v in m.items() if v != "__drop__"}
    return "---\n" + yaml.safe_dump(m, sort_keys=False) + "---\n\nbody\n"


def phase(id_, wave=1, deps=(), area=None, status="planned", spec=None):
    return {"id": id_, "title": id_.title(), "status": status, "wave": wave, "depends_on": list(deps),
            "area": area if area is not None else [f"src/{id_.lower()}/**"],
            "spec": spec or f"specs/{id_}-x.md"}


def tracker(*phases, **over):
    t = {"epic": "e1", "title": "Epic", "status": "in-progress", "owner": "yura", "started": "2026-10-03",
         "goal": "g", "resume": {"next": "do P-A", "current_wave": 1, "handoff": "none"},
         "phases": list(phases)}
    t.update(over)
    return t


class Base(unittest.TestCase):
    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.root = Path(self._tmp.name)
        self.w(".devkit/config.env", "# test fixture\nKIT_ID_PREFIX=TT\n")
        self.w("docs/rulings.md", "| Id | Date |\n|---|---|\n| R-001 | 2026-10-03 | x |\n")

    def w(self, rel, text):
        p = self.root / rel
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(text, encoding="utf-8")

    def epic(self, slug="e1", *phases, specs=True, archive=None, **over):
        base = f"docs/features/{archive or 'active'}/{slug}"
        t = tracker(*phases, **over)
        t["epic"] = slug
        self.w(f"{base}/tracker.yaml", yaml.safe_dump(t, sort_keys=False))
        if specs:
            for p in phases:
                self.w(f"{base}/{p['spec']}", "spec\n")
        return self.root / base

    def run_tool(self, *args, env=None):
        """Run docs.py in the fixture repo. The inherited environment loses KIT_ID_PREFIX and KIT_DOCS_TRACKERS
        (a repo that exports the switch runs these tests under it); a case that wants one passes it in `env`."""
        base = {k: v for k, v in os.environ.items() if k not in ("KIT_ID_PREFIX", "KIT_DOCS_TRACKERS")}
        base.update(DOCS_ROOT=str(self.root), PYTHONDONTWRITEBYTECODE="1")
        base.update(env or {})
        r = subprocess.run([sys.executable, str(TOOL), *args], capture_output=True, text=True, env=base)
        return r.returncode, r.stdout + r.stderr

    def index(self):
        self.run_tool("index")

    def check(self, env=None):
        self.index()
        return self.run_tool("check", env=env)

    def assertClean(self):
        code, out = self.check()
        self.assertEqual((code, out.strip()), (0, "docs-check: OK"), out)

    def assertProblem(self, text):
        code, out = self.check()
        self.assertEqual(code, 1, out)
        self.assertIn(text, out)


class AdrTests(Base):
    def test_valid_passes(self):
        self.w("docs/decisions/ADR-001-a.md", adr())
        self.assertClean()

    def test_id_must_match_filename(self):
        self.w("docs/decisions/ADR-001-a.md", adr("ADR-002"))
        self.assertProblem("does not match file name")

    def test_duplicate_ids(self):
        self.w("docs/decisions/ADR-001-a.md", adr())
        self.w("docs/decisions/ADR-001-b.md", adr())
        self.assertProblem("duplicate id ADR-001")

    def test_status_vocab(self):
        self.w("docs/decisions/ADR-001-a.md", adr(status="maybe"))
        self.assertProblem("status 'maybe'")

    def test_scope_epic_must_exist(self):
        self.w("docs/decisions/ADR-001-a.md", adr(scope="epic:nope"))
        self.assertProblem("scope epic 'nope' does not exist")

    def test_scope_epic_ok_for_active_and_archive(self):
        self.epic("e1", phase("P-A"))
        self.epic("old", phase("P-A"), archive="archive/2026")
        self.w("docs/decisions/ADR-001-a.md", adr(scope="epic:e1"))
        self.w("docs/decisions/ADR-002-b.md", adr("ADR-002", scope="epic:old"))
        self.assertClean()

    def test_bad_date_and_missing_heading(self):
        self.w("docs/decisions/ADR-001-a.md", adr(date="yesterday").replace("## Decision", "## Decide"))
        code, out = self.check()
        self.assertIn("date must be", out)
        self.assertIn("missing '## Decision'", out)

    def test_supersession_reciprocal_passes(self):
        self.w("docs/decisions/ADR-001-a.md", adr(status="superseded", superseded_by="ADR-002"))
        self.w("docs/decisions/ADR-002-b.md", adr("ADR-002", supersedes=["ADR-001"]))
        self.assertClean()

    def test_supersession_missing_back_pointer(self):
        self.w("docs/decisions/ADR-001-a.md", adr())
        self.w("docs/decisions/ADR-002-b.md", adr("ADR-002", supersedes=["ADR-001"]))
        code, out = self.check()
        self.assertIn("ADR-001.superseded_by != ADR-002", out)
        self.assertIn("status is not 'superseded'", out)

    def test_superseded_by_without_forward_pointer(self):
        self.w("docs/decisions/ADR-001-a.md", adr(status="superseded", superseded_by="ADR-002"))
        self.w("docs/decisions/ADR-002-b.md", adr("ADR-002"))
        self.assertProblem("ADR-002.supersedes lacks ADR-001")

    def test_amends_reciprocity(self):
        self.w("docs/decisions/ADR-001-a.md", adr(amended_by=["ADR-002"]))
        self.w("docs/decisions/ADR-002-b.md", adr("ADR-002", amends=["ADR-001"]))
        self.assertClean()
        self.w("docs/decisions/ADR-001-a.md", adr())
        self.assertProblem("ADR-001.amended_by lacks ADR-002")

    def test_unknown_reference(self):
        self.w("docs/decisions/ADR-001-a.md", adr(amends=["ADR-009"]))
        self.assertProblem("amends unknown ADR-009")

    def test_adr_new_refuses_with_the_devkit_verb(self):
        for args in (("Use Postgres!",), ("X", "--scope", "epic:zzz", "--status", "accepted"), ()):
            code, out = self.run_tool("adr", "new", *args)
            self.assertNotEqual(code, 0, out)
            self.assertIn("next: devkit rec ruling new", out)
        self.assertFalse((self.root / "docs/decisions").exists())
        self.assertFalse((self.root / "docs/decisions.md").exists())

    def test_decisions_index_groups_by_scope(self):
        self.epic("e1", phase("P-A"))
        self.w("docs/decisions/ADR-001-a.md", adr())
        self.w("docs/decisions/ADR-002-b.md", adr("ADR-002", scope="epic:e1", title="Pipe | title"))
        self.index()
        text = (self.root / "docs/decisions.md").read_text()
        self.assertIn("GENERATED by scripts/kit/docs.py index — do not edit", text)
        self.assertIn("## project", text)
        self.assertIn("## epic: e1", text)
        self.assertIn("[ADR-002](decisions/ADR-002-b.md)", text)
        self.assertIn("Pipe \\| title", text)


class BacklogTests(Base):
    """FB-173: the backlog is written and linted by `devkit rec`; docs.py neither lints items nor writes a backlog.yaml."""

    def test_backlog_new_refuses_with_the_devkit_verb(self):
        self.epic("e1", phase("P-A"))
        for args in (("First thing",), ("Second", "--epic", "e1", "--priority", "p1", "--origin", "R-001"), ()):
            code, out = self.run_tool("backlog", "new", *args)
            self.assertNotEqual(code, 0, out)
            self.assertIn("next: devkit rec backlog new", out)
        self.assertFalse((self.root / "docs/backlog").exists())
        self.assertFalse((self.root / "docs/backlog.yaml").exists())

    def test_items_are_not_linted_here(self):
        self.w("docs/backlog/README.md", "# Backlog\n")
        self.w("docs/backlog/TT-001-a.md", item(status="wip", priority="p9", origin="", closes_when="", epic="nope"))
        self.w("docs/backlog/TT-002-b.md", item("TT-002", origin="TODO: later"))
        self.assertClean()  # rec check lints them, once the project has a store index

    def test_no_backlog_yaml_is_written_or_required(self):
        self.w("docs/backlog/TT-001-a.md", item())
        self.assertClean()
        self.assertFalse((self.root / "docs/backlog.yaml").exists())
        self.w("docs/backlog.yaml", "stale: true\n")
        self.assertClean()  # a leftover file is nobody's to check


class TrackerTests(Base):
    def test_valid_passes(self):
        self.epic("e1", phase("P-A"), phase("P-B", 2, ["P-A"]))
        self.assertClean()

    def test_archived_tracker_is_linted(self):
        self.epic("old", phase("P-A"), phase("P-B", 1, ["P-A"]), archive="archive/2026")
        self.assertProblem("must be > wave")

    def test_header_vocab_and_caps(self):
        self.epic("e1", phase("P-A"), status="bogus", goal="x" * 301,
                  resume={"next": "y" * 401, "current_wave": -1, "handoff": "none"})
        code, out = self.check()
        for s in ("status 'bogus'", "goal missing or longer", "resume.next", "current_wave"):
            self.assertIn(s, out)

    def test_epic_must_match_dir(self):
        d = self.epic("e1", phase("P-A"))
        t = yaml.safe_load((d / "tracker.yaml").read_text())
        t["epic"] = "other"
        (d / "tracker.yaml").write_text(yaml.safe_dump(t))
        self.assertProblem("!= directory name 'e1'")

    def test_phase_required_keys_and_vocab(self):
        bad = phase("P-A")
        bad.update(status="nope", wave=0)
        del bad["title"]
        self.epic("e1", bad)
        code, out = self.check()
        for s in ("title missing", "status 'nope'", "wave must be"):
            self.assertIn(s, out)

    def test_phase_id_format(self):
        self.epic("e1", phase("p-lower", spec="specs/s.md"))
        self.assertProblem("must be UPPER-KEBAB")

    def test_duplicate_phase_ids(self):
        self.epic("e1", phase("P-A"), phase("P-A", 2, area=["z/**"]))
        self.assertProblem("duplicate phase id P-A")

    def test_unknown_dependency(self):
        self.epic("e1", phase("P-A", 2, ["P-X"]))
        self.assertProblem("unknown phase P-X")

    def test_cycle(self):
        self.epic("e1", phase("P-A", 1, ["P-B"]), phase("P-B", 2, ["P-A"]))
        self.assertProblem("dependency cycle")

    def test_wave_must_exceed_dependency_wave(self):
        self.epic("e1", phase("P-A"), phase("P-B", 1, ["P-A"]))
        self.assertProblem("P-B: wave 1 must be > wave 1")

    def test_same_wave_area_overlap(self):
        self.epic("e1", phase("P-A", area=["api/**"]), phase("P-B", area=["api/auth/**"]))
        self.assertProblem("overlaps")

    def test_same_wave_disjoint_and_different_wave_overlap_ok(self):
        self.epic("e1", phase("P-A", area=["api/a/**"]), phase("P-B", area=["api/b/**"]),
                  phase("P-C", 2, ["P-A"], area=["api/a/x/**"]))
        self.assertClean()

    def test_finished_phase_holds_no_area_in_its_wave(self):
        for status in ("done", "deferred", "re-scoped"):
            self.epic("e1", phase("P-A", area=["api/**"], status=status), phase("P-B", area=["api/auth/**"]))
            self.assertClean()
        self.epic("e1", phase("P-A", area=["api/**"], status="review"), phase("P-B", area=["api/auth/**"]))
        self.assertProblem("overlaps")  # two live phases stay red

    def test_area_required_unless_deferred(self):
        self.epic("e1", phase("P-A", area=[]))
        self.assertProblem("area must be a non-empty list")
        self.epic("e1", phase("P-A", area=[], status="deferred"))
        self.assertClean()

    def test_spec_must_exist_unless_planned(self):
        d = self.epic("e1", phase("P-A", status="spec-ready"), specs=False)
        self.assertProblem("spec 'specs/P-A-x.md' does not exist")
        self.w("docs/features/active/e1/specs/P-A-x.md", "s\n")
        self.assertClean()

    def test_unreferenced_spec(self):
        self.epic("e1", phase("P-A"))
        self.w("docs/features/active/e1/specs/orphan.md", "s\n")
        self.assertProblem("specs/orphan.md is referenced by 0 phases")

    def test_spec_referenced_twice(self):
        self.epic("e1", phase("P-A", spec="specs/s.md"), phase("P-B", area=["b/**"], spec="specs/s.md"))
        self.assertProblem("specs/s.md is referenced by 2 phases")

    def test_handoff_must_exist(self):
        res = {"next": "n", "current_wave": 1, "handoff": "plans/handoff.md"}
        self.epic("e1", phase("P-A"), resume=res)
        self.assertProblem("resume.handoff 'plans/handoff.md' does not exist")
        self.w("docs/features/active/e1/plans/handoff.md", "h\n")
        self.assertClean()

    def test_line_caps(self):
        d = self.epic("e1", phase("P-A"))
        for name, cap in (("README.md", 100), ("rulings.md", 80), ("plans/handoff.md", 60), ("specs/P-A-x.md", 150)):
            self.w(f"docs/features/active/e1/{name}", "x\n" * cap)
            self.assertClean()
            self.w(f"docs/features/active/e1/{name}", "x\n" * (cap + 1))
            self.assertProblem(f"{cap + 1} lines > {cap}")
            self.w(f"docs/features/active/e1/{name}", "x\n")

    def test_active_epic_without_tracker(self):
        self.w("docs/features/active/e1/README.md", "x\n")
        self.assertProblem("tracker.yaml: missing")


class NextAndResumeTests(Base):
    def test_next_lists_ready_phases_by_wave(self):
        self.epic("e1", phase("P-A", status="done"), phase("P-B", 2, ["P-A"]), phase("P-C", 3, ["P-B"]),
                  phase("P-D", 2, ["P-A"], status="deferred", area=[]), phase("P-E", 2, area=["e/**"]))
        code, out = self.run_tool("tracker", "next")
        self.assertEqual(code, 0, out)
        self.assertIn("e1:", out)
        self.assertIn("wave 2", out)
        self.assertIn("P-B", out)
        self.assertIn("P-E", out)
        for hidden in ("P-A ", "P-C", "P-D"):
            self.assertNotIn(hidden, out)

    def test_next_filters_by_slug_and_unknown_slug(self):
        self.epic("e1", phase("P-A"))
        self.epic("e2", phase("P-Z", area=["z/**"]))
        code, out = self.run_tool("tracker", "next", "e2")
        self.assertIn("P-Z", out)
        self.assertNotIn("P-A", out)
        self.assertEqual(self.run_tool("tracker", "next", "nope")[0], 1)

    def test_resume_prints_pointer(self):
        self.epic("e1", phase("P-A"), resume={"next": "do\n  P-A", "current_wave": 2, "handoff": "plans/handoff.md"})
        code, out = self.run_tool("resume")
        self.assertEqual(code, 0)
        self.assertIn("e1: wave 2 · next: do P-A", out)
        self.assertIn("  handoff: docs/features/active/e1/plans/handoff.md", out)

    def test_resume_skips_the_optional_keys_and_check_passes_without_them(self):
        self.epic("e1", phase("P-A"), resume={"handoff": "none"})
        code, out = self.run_tool("resume")
        self.assertEqual(code, 0)
        self.assertIn("e1\n", out)
        self.assertNotIn("wave ?", out)
        self.assertNotIn("next: ", out)
        self.assertClean()

    def test_resume_empty_when_no_epics(self):
        self.assertEqual(self.run_tool("resume"), (0, ""))

    def test_resume_survives_garbage(self):
        self.w("docs/features/active/bad/tracker.yaml", "{{{ not: yaml: [")
        self.w("docs/features/active/list/tracker.yaml", "- 1\n- 2\n")
        self.w("docs/features/active/weird/tracker.yaml", "resume: 5\nstatus: x\n")
        self.w("docs/features/active/none/README.md", "x")
        code, _ = self.run_tool("resume")
        self.assertEqual(code, 0)


class CrossRefTests(Base):
    def test_unknown_refs_fail(self):
        self.w("docs/note.md", "See ADR-042 and TT-007 and R-099.\n")
        code, out = self.check()
        self.assertEqual(code, 1)
        for ref in ("ADR-042", "TT-007", "R-099"):
            self.assertIn(f"unknown reference {ref}", out)

    def test_known_refs_pass(self):
        self.w("docs/decisions/ADR-001-a.md", adr())
        self.w("docs/backlog/TT-001-a.md", item())
        self.w("docs/note.md", "See ADR-001, TT-001, R-001.\n")
        self.w(".claude/rules/docs.md", "Cite R-001.\n")
        self.assertClean()

    def test_claude_dir_is_scanned(self):
        self.w(".claude/rules/docs.md", "Cite R-002.\n")
        self.assertProblem(".claude/rules/docs.md")

    def test_fenced_code_is_ignored(self):
        self.w("docs/note.md", "```\nADR-042 TT-007 R-099\n```\n~~~md\nR-098\n~~~\n")
        self.assertClean()
        self.w("docs/note.md", "```\nR-099\n```\nafter fence R-098\n")
        self.assertProblem("line 4: unknown reference R-098")

    def test_research_is_ignored(self):
        self.w("docs/research/x.md", "R-099 ADR-042\n")
        self.assertClean()

    def test_ruling_ids_come_from_table_rows_only(self):
        self.w("docs/note.md", "R-001 yes, R-002 no\n")
        self.assertProblem("unknown reference R-002")

    def test_adr_does_not_match_as_r(self):
        self.w("docs/decisions/ADR-001-a.md", adr())
        self.w("docs/note.md", "ADR-001\n")
        self.assertClean()

    def test_archived_backlog_id_resolves(self):
        """FB-173: an archived item is docs/backlog/archive/<year>/<id>-*.md, not a file of docs/backlog/."""
        self.w("docs/note.md", "See TT-004.\n")
        self.assertProblem("unknown reference TT-004")
        self.w("docs/backlog/archive/2026/TT-004-old.md", item("TT-004", status="done", closed="2026-10-04"))
        self.assertClean()
        self.w("docs/note.md", "See TT-004 and TT-005.\n")
        self.assertProblem("unknown reference TT-005")

    def test_ruling_files_resolve_live_and_archived(self):
        """FB-173: with docs/rulings/ present an R-n of any width is a file, live or archived."""
        self.w("docs/rulings/R-7.md", "r\n")
        self.w("docs/rulings/archive/2026/R-12.md", "r\n")
        self.w("docs/rulings/R-100.md", "r\n")
        self.w("docs/note.md", "See R-7, R-12, R-100, and the legacy R-001.\n")
        self.assertClean()
        self.w("docs/note.md", "See R-8.\n")
        self.assertProblem("unknown reference R-8")

    def test_three_digit_ruling_resolves_by_file_or_legacy_row(self):
        self.w("docs/rulings/R-002.md", "r\n")
        self.w("docs/note.md", "See R-001 (legacy row) and R-002 (file).\n")
        self.assertClean()
        self.w("docs/note.md", "See R-003.\n")
        self.assertProblem("unknown reference R-003")

    def test_fb264_zero_padded_ruling_resolves_to_the_unpadded_file_live_and_archived(self):
        """FB-264 (standards/docs.md, R-122): R-001 is R-1.md, with no legacy table to vouch for it."""
        (self.root / "docs/rulings.md").unlink()
        self.w("docs/rulings/R-1.md", "r\n")
        self.w("docs/rulings/archive/2026/R-3.md", "r\n")
        self.w("docs/note.md", "See R-001, R-1, R-003 and R-3.\n")
        self.assertClean()

    def test_fb264_zero_padded_ruling_without_a_file_stays_unknown(self):
        (self.root / "docs/rulings.md").unlink()
        self.w("docs/rulings/R-1.md", "r\n")
        self.w("docs/note.md", "See R-001 and R-002.\n")
        self.assertProblem("unknown reference R-002")
        self.assertNotIn("R-001", self.check()[1])

    def test_two_digit_ruling_is_not_linted_without_a_ruling_store(self):
        """FB-173: a project citing host rulings such as R-89 in prose stays green until it has docs/rulings/."""
        self.w("docs/note.md", "The host ruled R-89 and R-5.\n")
        self.assertClean()
        (self.root / "docs/rulings").mkdir()
        self.assertProblem("unknown reference R-89")

    def test_ruling_file_name_must_be_the_id(self):
        self.w("docs/rulings/R-7-extra.md", "r\n")
        self.w("docs/rulings/R-8.md", "r\n")
        self.w("docs/note.md", "See R-8.\n")
        self.assertClean()
        self.w("docs/note.md", "See R-8 and R-7.\n")
        self.assertProblem("unknown reference R-7")

    def test_foreign_prefix_is_not_linted(self):
        self.w("docs/backlog/ZZ-001-a.md", item("ZZ-001"))
        self.w("docs/note.md", "See ZZ-001.\n")  # ZZ is not this repo's prefix: the pattern does not look for it
        self.assertClean()


class FeatureTests(Base):
    def test_new_lays_the_folder_and_check_passes(self):
        code, out = self.run_tool("feature", "new", "dev-env")
        self.assertEqual(code, 0, out)
        base = self.root / "docs/features/active/dev-env"
        for name in ("README.md", "tracker.yaml", "rulings.md", "specs/.gitkeep", "plans/handoff.md"):
            self.assertTrue((base / name).is_file(), name)
            self.assertIn(f"docs/features/active/dev-env/{name}", out)
        readme = (base / "README.md").read_text()
        for h in ("## Goal", "## Scope", "## Non-scope", "## Phase map"):
            self.assertIn(h, readme)
        t = yaml.safe_load((base / "tracker.yaml").read_text())
        self.assertEqual(t["phases"], [])
        self.assertEqual(t["resume"], {"handoff": "plans/handoff.md"})  # no `next`, no `current_wave`: both optional
        self.assertClean()

    def test_new_epic_is_usable_as_adr_scope(self):
        self.run_tool("feature", "new", "dev-env")
        self.w("docs/decisions/ADR-001-a.md", adr(scope="epic:dev-env"))
        self.assertClean()

    def test_new_refuses_invalid_slug(self):
        for bad in ("Dev-Env", "dev_env", "1st", "dev--env", "dev-", "a b"):
            code, out = self.run_tool("feature", "new", bad)
            self.assertNotEqual(code, 0, bad)
            self.assertIn("kebab-case", out)
        self.assertFalse((self.root / "docs/features").exists())

    def test_new_refuses_duplicate_in_active_and_archive(self):
        self.run_tool("feature", "new", "dev-env")
        code, out = self.run_tool("feature", "new", "dev-env")
        self.assertNotEqual(code, 0)
        self.assertIn("already exists", out)
        self.epic("old", phase("P-A"), archive="archive/2025")
        code, out = self.run_tool("feature", "new", "old")
        self.assertNotEqual(code, 0)
        self.assertIn("already exists", out)

    def test_archive_refused_while_readme_is_a_stub(self):
        self.run_tool("feature", "new", "dev-env")
        code, out = self.run_tool("feature", "archive", "dev-env")
        self.assertNotEqual(code, 0)
        self.assertIn("stub marker", out)
        self.assertTrue((self.root / "docs/features/active/dev-env").is_dir())

    def _filled(self, slug, status):
        self.run_tool("feature", "new", slug)
        base = self.root / "docs/features/active" / slug
        (base / "README.md").write_text("# x\n\n## Goal\n\nShipped.\n")
        t = yaml.safe_load((base / "tracker.yaml").read_text())
        t["phases"] = [phase("P-A", status=status)]
        (base / "tracker.yaml").write_text(yaml.safe_dump(t, sort_keys=False))
        self.w(f"docs/features/active/{slug}/specs/P-A-x.md", "spec\n")

    def test_archive_refused_with_an_open_phase(self):
        self._filled("dev-env", "in-progress")
        code, out = self.run_tool("feature", "archive", "dev-env")
        self.assertNotEqual(code, 0)
        self.assertIn("phase P-A is 'in-progress'", out)
        self.assertTrue((self.root / "docs/features/active/dev-env").is_dir())

    def test_archive_moves_when_closed(self):
        self._filled("dev-env", "done")
        code, out = self.run_tool("feature", "archive", "dev-env")
        self.assertEqual(code, 0, out)
        year = datetime.date.today().year
        self.assertFalse((self.root / "docs/features/active/dev-env").exists())
        self.assertTrue((self.root / f"docs/features/archive/{year}/dev-env/tracker.yaml").is_file())
        self.assertClean()

    def test_archive_unknown_slug(self):
        self.assertNotEqual(self.run_tool("feature", "archive", "nope")[0], 0)

    def test_empty_phases_list_is_valid_but_missing_is_not(self):
        self.run_tool("feature", "new", "dev-env")
        self.assertClean()
        p = self.root / "docs/features/active/dev-env/tracker.yaml"
        t = yaml.safe_load(p.read_text())
        del t["phases"]
        p.write_text(yaml.safe_dump(t, sort_keys=False))
        self.assertProblem("phases must be a list")


class IdPrefixTests(Base):
    def test_prefix_comes_from_config_and_drives_the_pattern(self):
        self.w(".devkit/config.env", "KIT_PREFIX=ZZ\nKIT_ID_PREFIX=QX\n")
        self.w("docs/backlog/QX-001-a.md", item("QX-001"))
        self.w("docs/note.md", "See QX-001.\n")
        self.assertClean()
        self.w("docs/backlog/TT-002-b.md", item("TT-002"))
        self.assertClean()  # a foreign prefix is not a backlog file of this repo
        self.w("docs/note.md", "See QX-001 and QX-002.\n")
        self.assertProblem("unknown reference QX-002")

    def test_missing_prefix_fails_loudly(self):
        (self.root / ".devkit/config.env").unlink()
        code, out = self.run_tool("check")
        self.assertNotEqual(code, 0)
        self.assertIn("KIT_ID_PREFIX", out)


class TrackersSwitchTests(Base):
    """FB-195: KIT_DOCS_TRACKERS=off (environment only) skips lint_trackers whole; the size caps always run."""

    OFF = {"KIT_DOCS_TRACKERS": "off"}

    def broken(self):
        return self.epic("e1", phase("P-A", 1), phase("P-B", 1, ["P-A"]))  # wave 1 must be > wave 1

    def test_off_skips_a_broken_tracker(self):
        self.broken()
        code, out = self.check(env=self.OFF)
        self.assertEqual((code, out.strip()), (0, "docs-check: OK"), out)

    def test_off_skips_a_missing_tracker(self):
        self.w("docs/features/active/e1/README.md", "x\n")
        code, out = self.check(env=self.OFF)
        self.assertEqual((code, out.strip()), (0, "docs-check: OK"), out)
        self.assertProblem("tracker.yaml: missing")  # the same repo without the switch

    def test_off_keeps_the_line_caps(self):
        d = self.broken()
        self.w("docs/features/active/e1/specs/P-A-x.md", "x\n" * 151)
        code, out = self.check(env=self.OFF)
        self.assertEqual(code, 1, out)
        self.assertIn("specs/P-A-x.md: 151 lines > 150", out)
        self.assertNotIn("must be > wave", out)
        self.w("docs/features/active/e1/README.md", "x\n" * 101)  # a folder with no tracker still has caps
        self.assertIn("README.md: 101 lines > 100", self.check(env=self.OFF)[1])

    def test_off_keeps_the_other_lints(self):
        self.broken()
        self.w("docs/decisions/ADR-001-a.md", adr(status="maybe"))
        code, out = self.check(env=self.OFF)
        self.assertEqual(code, 1, out)
        self.assertIn("status 'maybe'", out)

    def test_unset_reports_the_broken_tracker(self):
        self.broken()
        code, out = self.check()
        self.assertEqual(code, 1, out)
        self.assertIn("must be > wave", out)

    def test_any_other_value_refuses(self):
        self.broken()
        for bad in ("on", "OFF", "1", "", "false"):
            code, out = self.check(env={"KIT_DOCS_TRACKERS": bad})
            self.assertNotEqual(code, 0, bad)
            self.assertIn("KIT_DOCS_TRACKERS", out)
            self.assertNotIn("docs-check: OK", out)

    def test_the_parent_environment_is_scrubbed(self):
        """A run under KIT_DOCS_TRACKERS=off (a repo that opts out) must still see a broken tracker in these cases."""
        self.broken()
        before = os.environ.get("KIT_DOCS_TRACKERS")
        with mock.patch.dict(os.environ, {"KIT_DOCS_TRACKERS": "off"}):
            code, out = self.check()
        self.assertEqual(code, 1, out)
        self.assertIn("must be > wave", out)
        self.assertEqual(os.environ.get("KIT_DOCS_TRACKERS"), before)  # patch.dict restored the parent environment


class DormantTests(Base):
    def test_repo_without_features_or_docs_dirs_passes(self):
        (self.root / "docs/rulings.md").unlink()
        self.assertClean()


class IndexTests(Base):
    def test_idempotent(self):
        self.w("docs/decisions/ADR-001-a.md", adr())
        self.w("docs/backlog/TT-001-a.md", item())
        self.run_tool("index")
        before = {n: (self.root / n).read_text() for n in ("docs/decisions.md",)}
        code, out = self.run_tool("index")
        self.assertIn("up to date", out)
        after = {n: (self.root / n).read_text() for n in before}
        self.assertEqual(before, after)
        self.assertFalse((self.root / "docs/backlog.yaml").exists())

    def test_without_adr_files_nothing_is_written_or_checked(self):
        """FB-173: decisions.md is the legacy render of ADR files; no ADR file, no generated file."""
        self.w("docs/backlog/TT-001-a.md", item())
        code, out = self.run_tool("index")
        self.assertEqual(code, 0, out)
        self.assertIn("up to date", out)
        self.assertFalse((self.root / "docs/decisions.md").exists())
        self.assertFalse((self.root / "docs/backlog.yaml").exists())
        self.assertEqual(self.run_tool("check"), (0, "docs-check: OK\n"))
        self.w("docs/decisions/README.md", "# ADRs\n")
        self.assertEqual(self.run_tool("check"), (0, "docs-check: OK\n"))  # a README is not an ADR file

    def test_a_hand_written_decisions_md_is_left_alone_without_adr_files(self):
        self.w("docs/decisions.md", "# Decisions\n\nhand edited\n")
        self.run_tool("index")
        self.assertEqual((self.root / "docs/decisions.md").read_text(), "# Decisions\n\nhand edited\n")
        self.assertEqual(self.run_tool("check"), (0, "docs-check: OK\n"))

    def test_missing_decisions_md_is_red_once_adr_files_exist(self):
        self.w("docs/decisions/ADR-001-a.md", adr())
        code, out = self.run_tool("check")
        self.assertEqual(code, 1)
        self.assertIn("docs/decisions.md: generated file is missing or stale", out)

    def test_check_flags_stale_index(self):
        self.w("docs/decisions/ADR-001-a.md", adr())
        self.index()
        self.w("docs/decisions/ADR-002-b.md", adr("ADR-002"))
        code, out = self.run_tool("check")
        self.assertEqual(code, 1)
        self.assertIn("docs/decisions.md: generated file is missing or stale", out)

    def test_quiet_suppresses_ok(self):
        self.index()
        self.assertEqual(self.run_tool("check", "--quiet"), (0, ""))
        self.assertEqual(self.run_tool("check")[1].strip(), "docs-check: OK")


if __name__ == "__main__":
    unittest.main()
