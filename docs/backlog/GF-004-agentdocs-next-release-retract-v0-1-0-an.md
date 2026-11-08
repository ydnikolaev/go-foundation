---
id: GF-004
title: 'agentdocs next release: retract v0.1.0 and export a root-escape sentinel'
status: open
type: work
tier: 2
priority: p3
origin: agentdocs v0.2.0 close, 2026-11-08
created: 2026-11-08
---
v0.1.0 is a public tag, cached by the module proxy, and it follows symlinks out of the repository. Go's canonical fix is `retract v0.1.0 // follows symlinks out of the root` in units/agentdocs/go.mod, shipped with the next tag. Nobody imports 0.1.0, and @latest resolves to v0.2.0, so this waits for the next real release rather than cutting one.

In the same release, export an `ErrEscapesRoot` so callers can match an escape with errors.Is. Today they get an *Error worded either by os ("path escapes from parent") or by the unit.
