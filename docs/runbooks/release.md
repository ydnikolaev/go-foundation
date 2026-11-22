# Release runbook

How this repo is released. `/devkit:release` reads this file and runs it after the owner's one go-ahead; the format is
in `~/.devkit/current/standards/make.md` §5 and `make docs-check` refuses a file that breaks it. Six steps, in this
order. Each has `run` (commands in backticks, each a `make` target or a `devkit` verb; `none` when the repo has no such
step), `proves` (the command whose success says the step is done) and `on red` (what to do when it fails). `<version>`
is replaced with the version the owner approved. A touch only the owner can make gets an `owner:` line in its step.

A release is named: vX.Y.Z, the next after the last tag.
Notes are written in: English.

## 1. What ships
- run: `devkit release notes <version>`
- proves: `devkit release check <version>`
- on red: fix what the check names in the notes file, run it again

## 2. The gate
- run: none (step 3 runs `make check-release` and refuses on its red)
- proves: none
- on red: none

## 3. The mark
- run: `devkit release <version>`
- proves: `git describe --tags --exact-match HEAD`
- on red: fix the red on the branch, start the release again

## 4. Ship
- run: none
- proves: none
- on red: none

## 5. Verify live
- run: none
- proves: none
- on red: none

## 6. Announce and record
- run: none
- proves: none
- on red: none
