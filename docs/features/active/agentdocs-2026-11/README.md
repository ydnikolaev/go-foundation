# agentdocs-2026-11

## Goal

A tool's agent docs ship inside its binary and reach an agent in any repository the tool serves,
offline, by one shared mechanism taken from a2a: `units/agentdocs` v0.1.0 is tagged, and installing
a2a's own tree through the unit yields the same files a2a writes today.

## Scope

- The unit API in [specs/01-api.md](specs/01-api.md): manifest and topics, install with a provenance
  marker, the installed-copy drift status, agent-surface links, the context-file pointer, and a
  generated-file staleness check.
- A parity test against a2ahub's tree and behaviour, as `adoption` had.
- Adoption notes `units/agentdocs/releasenotes/0.1.0.yaml`, README in the unit shape.

## Non-scope

- Any tool's content, CLI wiring (cobra or other), or doctor wording.
- A reference generator: a cobra tool uses `cobra/doc`; a2a keeps its own catalog.
- Switching a2a to the unit (its own lane later), and each consumer's own side.

## Phase map

- P-PORT: once R-8 is ruled, port test-first with parity against a2ahub, release v0.1.0
  (unit-release.sh, public-scan, tag per R-7), hand the tag to the first consumer.
