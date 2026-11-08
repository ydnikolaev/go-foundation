package agentdocs_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ydnikolaev/go-foundation/units/agentdocs"
)

// installedBy installs docTree into a fresh target stamped by tl and returns
// the target.
func installedBy(t *testing.T, tl agentdocs.Tool) string {
	t.Helper()
	target := filepath.Join(t.TempDir(), "skill")
	mustInstall(t, target, tl)
	return target
}

func assertStatus(t *testing.T, got agentdocs.InstallStatus, state agentdocs.State, version string, sev agentdocs.Severity) {
	t.Helper()
	if got.State != state || got.Version != version {
		t.Errorf("Installed = {%v %q}, want {%v %q}", got.State, got.Version, state, version)
	}
	if s := got.Severity(); s != sev {
		t.Errorf("Severity() = %v, want %v (state %v)", s, sev, got.State)
	}
}

// Ported: cmd_doctor_test.go TestDoctorCheckSkillManualCurrent_NoInstall.
// None is OK: a tool whose install is an explicit verb must not warn on a
// repository that never ran it (spec 01 "Installed").
func TestInstalledNoneIsOK(t *testing.T) {
	t.Parallel()
	t.Run("absent", func(t *testing.T) {
		t.Parallel()
		got := installed(filepath.Join(t.TempDir(), "never-installed"), tool)
		assertStatus(t, got, agentdocs.StateNone, "", agentdocs.SeverityOK)
	})
	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		got := installed(t.TempDir(), tool)
		assertStatus(t, got, agentdocs.StateNone, "", agentdocs.SeverityOK)
	})
}

// Ported: TestDoctorCheckSkillManualCurrent_OlderManual_Advisory and
// TestDoctorCheckSkillManualCurrent_HonestLag: an older copy is Behind, Warn.
func TestInstalledBehindIsWarn(t *testing.T) {
	t.Parallel()
	target := installedBy(t, withTool(func(t *agentdocs.Tool) { t.Version, t.Comparable = "1.3.0", "1.3.0" }))
	assertStatus(t, installed(target, tool), agentdocs.StateBehind, "1.3.0", agentdocs.SeverityWarn)
}

// Ported: TestDoctorCheckSkillManualCurrent_StampNewerThanBinary: a copy
// newer than the binary is its own state, never Current; Warn.
func TestInstalledAheadIsWarn(t *testing.T) {
	t.Parallel()
	target := installedBy(t, withTool(func(t *agentdocs.Tool) { t.Version, t.Comparable = "1.5.0", "1.5.0" }))
	assertStatus(t, installed(target, tool), agentdocs.StateAhead, "1.5.0", agentdocs.SeverityWarn)
}

// Ported: TestDoctorCheckSkillManualCurrent_UpToDate_Clean (stamp half; the
// tree walk is not part of this API) and
// TestDoctorSkillManualVersionRegexpRoundTripsSkillProvenance: what Install
// stamps, Installed reads back as Current.
func TestInstalledRoundTripIsCurrent(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"1.4.0", "0.23.0", "0.0.1", "v2.0.0-rc.1"} {
		tl := withTool(func(t *agentdocs.Tool) { t.Version, t.Comparable = v+" (local, abc)", v })
		target := installedBy(t, tl)
		assertStatus(t, installed(target, tl), agentdocs.StateCurrent, v, agentdocs.SeverityOK)
	}
}

// Installed compares only Comparable. Version is display only: a different
// display string with the same Comparable is Current, and the same display
// string with a different Comparable is not.
func TestInstalledComparesOnlyComparable(t *testing.T) {
	t.Parallel()
	t.Run("display differs, comparable equal", func(t *testing.T) {
		t.Parallel()
		target := installedBy(t, withTool(func(t *agentdocs.Tool) { t.Version = "1.4.0 (local, ffffff)" }))
		assertStatus(t, installed(target, tool), agentdocs.StateCurrent, "1.4.0", agentdocs.SeverityOK)
	})
	t.Run("display equal, comparable differs", func(t *testing.T) {
		t.Parallel()
		target := installedBy(t, withTool(func(t *agentdocs.Tool) { t.Comparable = "1.3.0" }))
		assertStatus(t, installed(target, tool), agentdocs.StateBehind, "1.3.0", agentdocs.SeverityWarn)
	})
	t.Run("display looks like a version, comparable empty", func(t *testing.T) {
		t.Parallel()
		target := installedBy(t, withTool(func(t *agentdocs.Tool) { t.Version, t.Comparable = "1.4.0", "" }))
		assertStatus(t, installed(target, tool), agentdocs.StateDev, "", agentdocs.SeverityWarn)
	})
}

// Ported: TestDoctorSkillManualNamesADevBuildRatherThanSayingVersionUnknown
// and TestDoctorCheckSkillManualCurrent_Dev (stamp half). An empty Comparable
// on EITHER side is Dev, and Dev is Warn.
func TestInstalledEmptyComparableOnEitherSideIsDev(t *testing.T) {
	t.Parallel()
	dev := withTool(func(t *agentdocs.Tool) { t.Version, t.Comparable = "dev", "" })
	t.Run("stamp side", func(t *testing.T) {
		t.Parallel()
		assertStatus(t, installed(installedBy(t, dev), tool), agentdocs.StateDev, "", agentdocs.SeverityWarn)
	})
	t.Run("binary side", func(t *testing.T) {
		t.Parallel()
		assertStatus(t, installed(installedBy(t, tool), dev), agentdocs.StateDev, "1.4.0", agentdocs.SeverityWarn)
	})
	t.Run("both", func(t *testing.T) {
		t.Parallel()
		assertStatus(t, installed(installedBy(t, dev), dev), agentdocs.StateDev, "", agentdocs.SeverityWarn)
	})
}

// A Comparable that is not a version (on either side) cannot be compared, so
// it is Dev rather than a guessed order.
func TestInstalledUncomparableIsDev(t *testing.T) {
	t.Parallel()
	odd := withTool(func(t *agentdocs.Tool) { t.Comparable = "nightly" })
	assertStatus(t, installed(installedBy(t, odd), tool), agentdocs.StateDev, "nightly", agentdocs.SeverityWarn)
	assertStatus(t, installed(installedBy(t, tool), odd), agentdocs.StateDev, "1.4.0", agentdocs.SeverityWarn)
}

// Ported: TestDoctorCheckSkillManualCurrent_UnparseableProvenance_VersionUnknown.
// The marker is present (owned) but the stamp records no comparable line:
// there is nothing to compare, so Dev.
func TestInstalledOwnedWithoutComparableIsDev(t *testing.T) {
	t.Parallel()
	target := t.TempDir()
	mustWrite(t, filepath.Join(target, agentdocs.ProvenanceFile), "<!-- mytool-skill-install -->\nnot a real provenance file\n")
	assertStatus(t, installed(target, tool), agentdocs.StateDev, "", agentdocs.SeverityWarn)
}

// A legacy a2a stamp (every a2a install before its switch, written by a2a's
// own skillProvenance) carries the marker and no comparable line: owned, so
// Dev and Warn, healed by the next install. There is no legacy parser.
func TestInstalledLegacyA2aStampIsDev(t *testing.T) {
	t.Parallel()
	target := t.TempDir()
	legacy := "<!-- a2ahub-skill-install -->\n" +
		"# a2ahub skill — installed artifact\n\n" +
		"Written by `a2a skill install` / `a2a init` (a2a 0.23.0).\n" +
		"Source: github.com/ydnikolaev/a2ahub — skill/a2ahub/.\n\n" +
		"**Do not hand-edit** — refresh with `a2a skill install`.\n"
	mustWrite(t, filepath.Join(target, agentdocs.ProvenanceFile), legacy)
	mustWrite(t, filepath.Join(target, "SKILL.md"), "x\n")
	a2a := withTool(func(t *agentdocs.Tool) { t.ID, t.Name, t.Comparable = "a2ahub", "a2a", "0.23.0" })
	assertStatus(t, installed(target, a2a), agentdocs.StateDev, "", agentdocs.SeverityWarn)
}

// Installed with an empty ID owns nothing: Foreign, not judged.
func TestInstalledEmptyIDOwnsNothing(t *testing.T) {
	t.Parallel()
	target := installedBy(t, tool)
	assertStatus(t, installed(target, withTool(func(t *agentdocs.Tool) { t.ID = "" })), agentdocs.StateForeign, "", agentdocs.SeverityOK)
	// Not even a stamp whose marker has an empty namespace.
	blank := t.TempDir()
	mustWrite(t, filepath.Join(blank, agentdocs.ProvenanceFile), "<!-- -skill-install -->\n<!-- comparable: 1.4.0 -->\n")
	assertStatus(t, installed(blank, withTool(func(t *agentdocs.Tool) { t.ID = "" })), agentdocs.StateForeign, "", agentdocs.SeverityOK)
}

// The comparable line is read only from the leading comment block: a
// "comparable" line in the prose below it is not a stamp.
func TestInstalledReadsComparableOnlyFromTheHead(t *testing.T) {
	t.Parallel()
	target := t.TempDir()
	mustWrite(t, filepath.Join(target, agentdocs.ProvenanceFile),
		"<!-- mytool-skill-install -->\n# heading\n<!-- comparable: 1.4.0 -->\n")
	assertStatus(t, installed(target, tool), agentdocs.StateDev, "", agentdocs.SeverityWarn)
}

// Ported: TestDoctorCheckSkillManualCurrent_ForeignInstall_NotJudged. A
// non-empty directory without the tool's marker is someone else's, and is
// not judged: Foreign, OK.
func TestInstalledForeignIsNotJudged(t *testing.T) {
	t.Parallel()
	t.Run("provenance without the marker", func(t *testing.T) {
		t.Parallel()
		target := t.TempDir()
		mustWrite(t, filepath.Join(target, agentdocs.ProvenanceFile), "not a real provenance file\n<!-- comparable: 1.3.0 -->\n")
		assertStatus(t, installed(target, tool), agentdocs.StateForeign, "", agentdocs.SeverityOK)
	})
	t.Run("no provenance at all", func(t *testing.T) {
		t.Parallel()
		target := t.TempDir()
		mustWrite(t, filepath.Join(target, "notes.md"), "mine\n")
		assertStatus(t, installed(target, tool), agentdocs.StateForeign, "", agentdocs.SeverityOK)
	})
	t.Run("another tool's install", func(t *testing.T) {
		t.Parallel()
		target := installedBy(t, withTool(func(t *agentdocs.Tool) { t.ID, t.Comparable = "othertool", "0.1.0" }))
		assertStatus(t, installed(target, tool), agentdocs.StateForeign, "", agentdocs.SeverityOK)
	})
}

// The severity mapping, all six states.
func TestSeverityMapping(t *testing.T) {
	t.Parallel()
	for state, want := range map[agentdocs.State]agentdocs.Severity{
		agentdocs.StateNone:    agentdocs.SeverityOK,
		agentdocs.StateForeign: agentdocs.SeverityOK,
		agentdocs.StateCurrent: agentdocs.SeverityOK,
		agentdocs.StateDev:     agentdocs.SeverityWarn,
		agentdocs.StateBehind:  agentdocs.SeverityWarn,
		agentdocs.StateAhead:   agentdocs.SeverityWarn,
	} {
		if got := (agentdocs.InstallStatus{State: state}).Severity(); got != want {
			t.Errorf("%v.Severity() = %v, want %v", state, got, want)
		}
	}
}

func TestStateString(t *testing.T) {
	t.Parallel()
	for state, want := range map[agentdocs.State]string{
		agentdocs.StateNone: "none", agentdocs.StateForeign: "foreign", agentdocs.StateDev: "dev",
		agentdocs.StateBehind: "behind", agentdocs.StateCurrent: "current", agentdocs.StateAhead: "ahead",
		agentdocs.State(99): "State(99)",
	} {
		if got := state.String(); got != want {
			t.Errorf("State(%d).String() = %q, want %q", int(state), got, want)
		}
	}
}

// Installed never writes: the target is byte-identical after any judgement.
func TestInstalledIsReadOnly(t *testing.T) {
	t.Parallel()
	target := installedBy(t, tool)
	before, _ := os.ReadFile(filepath.Join(target, agentdocs.ProvenanceFile))
	_ = installed(target, withTool(func(t *agentdocs.Tool) { t.Comparable = "9.9.9" }))
	after, _ := os.ReadFile(filepath.Join(target, agentdocs.ProvenanceFile))
	if string(before) != string(after) {
		t.Fatal("Installed modified PROVENANCE.md")
	}
}
