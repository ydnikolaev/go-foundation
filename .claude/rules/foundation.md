---
paths:
  - "units/**"
  - "UNITS.md"
  - "config/foundation.toml"
  - "go.work"
  - "LICENSE"
---
# Foundation repo (this project's own; edit freely, devkit never overwrites it)
This repo holds shared units that other projects take. The rules are `standards/foundation.md` (read it, sections 1 to 5, before a unit change); `devkit foundation check` enforces them in `make check`.
- A unit is `units/<id>/` with one `unit.json`, one module and its own tests. The id never changes; the root holds tooling only.
- A new unit: `devkit foundation take <project> <dir>@<sha> --as <id>` when a consumer gave the code (`devkit foundation give`), else the section 1 checklist by hand.
- A release is `devkit foundation release <id> X.Y.Z` and nothing else: it runs the gates and cuts the tag. Run `make kit-foundation-catalogue` only if a unit summary changed, then commit `UNITS.md`. Who pushes the tag and how is `standards/foundation.md` section 4; read it when you need it, never copy it here.
- The repo is public: no project name, no host path and no secret in any file, and the public-scan gate is green before a tag is cut.
- A fix a consumer needs arrives as a failing test against the unit (`devkit foundation give --fix`); the unit's own session writes the change.
