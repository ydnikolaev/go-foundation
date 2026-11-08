// Package adoption tells a consuming repository what a tool's releases still
// ask of it, and records when the repository has done it.
//
// A tool ships a corpus of release notes inside its binary. Each Change may
// carry an Action: a scope (none, local, or a party scope the tool names), a
// why, an optional runnable detect (exit 0 means satisfied) and an optional
// run. The repository keeps its own Clock — the release it adopted through,
// plus the ids it adopted ahead of a release — in a committed file of the
// tool's choosing. Pending walks the corpus from that clock up to the running
// binary; CheckDetects re-proves every detect before a tool moves the clock;
// Notice is the one-line advisory a tool prints on every verb while something
// is owed, in both directions; ClockFile asks git whether the clock's file is
// committed, because a machine-local clock is lost to every clone.
//
// The binary is per machine and adoption is per repository, so a tool that
// adopts only inside its update verb leaves every other repository on the
// machine unadopted. Asking at use makes the arrival path irrelevant.
//
// Origin: a2ahub internal/notes at b5f12155 (decisions D-043, D-044).
package adoption
