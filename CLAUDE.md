# CLAUDE.md: index of this repo

A map, not a manual. Project-owned: devkit seeded it once and never touches it again. Keep it under 3,000 B and
index here every project rule, skill, agent and make target you add, one line each.

## What this repo is

Shared Go code for every ydnikolaev project: primitives and whole modules, one Go module per root directory (a
"unit"), each under `units/<unit>/` and tagged `units/<unit>/vX.Y.Z`. Consumers import by tag; nobody copies. Go only; a unit can pair with a unit
in another stack's foundation repo by id. Catalogue: `UNITS.md`. Public (R-6): nothing here names a private repo.

## Where things are

| Path | What |
|---|---|
| `Makefile` | thin root: `make help` lists targets, `make check` is the light gate, `make check-full` the full one |
| `mk/` | `kit-*.mk` fragments are devkit's (managed, never edited here); yours go in `mk/<area>.mk` |
| `config/ports.env` | every host port of this repo, inside its block |
| `config/dev.env` | committed non-secret local values (no secrets) |
| `.devkit/config.env` | devkit answers for this repo (managed) |
| `.claude/settings.json` | this repo's allow rows and hooks |
| `docs/devkit-feedback.md` | legacy seed; new defect reports go to `devkit feedback` |
| `units/<unit>/` | one unit: `go.mod`, `unit.json`, `README.md`, `releasenotes/`; the root holds tooling only |
| `config/foundation.toml` | module root, `floor` (the highest `go` line a unit may declare), `newest`, `golangci` |
| `config/public-allowlist.txt` | the one place an exception to `make public-scan` lives |
| `docs/rulings/` | layout, versioning, pairing, floor, registry, visibility |

## Gates

`make check` is the light gate, any time: every devkit gate plus the targets `config/check-map` selects from your changed
paths. `make check-full` is the full gate, once at a hand-off or release: every mapped target over the whole tree plus
`CHECK_FULL`. A gate you append to `CHECK_EXTRA` in a fragment runs in both; a gate in neither is not a gate.

## Project rules, skills, agents

Shared rules and agents come from devkit through `~/.claude`. This repo adds only its own: a rule in
`.claude/rules/*.md`, a skill in `.claude/skills/<name>/SKILL.md`, an agent in `.claude/agents/<name>.md`. Never
name one `kit-*` or after a devkit one; additive only. List each below.

- `.claude/rules/units.md`: adding, releasing and consuming a unit
- `.claude/rules/foundation.md`: the foundation module's rule
- `make kit-foundation-check`: the unit gate, `UNITS.md` equal to the catalogue (in `make check`)
- `make kit-foundation-catalogue`: regenerate `UNITS.md`
- `make public-scan`: private names, host paths, emails, secrets over tree and history (gh; in `make check`)
- `make public-scan-test`: its teeth, offline (in `make check`)
- `make kit-foundation-lint`: gofmt, vet, golangci-lint per unit (check-full)
- `devkit foundation release <unit> <version>`: tag a unit after a green gate
