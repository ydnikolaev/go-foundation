# adoption

What a tool's releases still ask of the repository that uses the tool, and the record that it was done.
The binary is per machine and adoption is per repository, so a tool that adopts only inside its update verb leaves every
other repository on the machine unadopted. This unit lets every verb ask at use.

## Install

```sh
go get github.com/ydnikolaev/go-foundation/units/adoption@v0.2.0
```

go-foundation is public (R-6): no GOPRIVATE, no token, no vendoring.

## Wire

1. Embed your corpus: one YAML per release (`version`, `released`: a date or `unreleased`, `changes[]` with `action`).
2. Store the `adoption.Clock` (`adapted_through`, `adapted_ahead`) in a file the repository COMMITS.
3. Read verb: `adoption.Pending(all, standing, clock, binaryVersion, policy)`; sort items by `Group`, then `ImpactOrder`.
   A change whose `Actor` is `adoption.ActorOwner` needs a person (a credential, a setting, a decision): show it as
   a hand-over to the owner, never as a step the session runs. An empty `Actor` is an agent step.
4. Record verb: refuse while `adoption.CheckDetects(ctx, proj.Items, nil)` is non-nil; then set `Through` to the binary
   version and append `proj.AuthoredAhead` to `Ahead`.
5. After every successful verb: print `adoption.Notice(...)` on stderr when it is not empty. Never stdout, never the exit code.
6. Doctor: report `adoption.ClockFile(...).Severity()`. `SeverityWarn` is an untracked clock in a repository with no
   commit yet (owed at the first commit); `SeverityFail` is an ignored clock (quote `IgnoredBy`) or an untracked one
   once the repository has commits.

## Configure

- `Policy.PartyScopes`: the scope values that oblige another party (a2a: `space`). Unnamed scopes oblige nothing.
- `Verbs`: your binary name and your read and record commands, as the notice prints them.
- `Load(fsys, pattern)` and `LoadStanding(fsys, path)`: where your corpus lives.

## Extension points

- `DetectRunner`: replace the shell runner (tests, a sandbox).
- Schema validation is yours: `Release.Raw` keeps the exact bytes for your validator.

## Do not

- Do not infer a baseline for a never-recorded clock: a guessed baseline skips work it wrongly believes done.
- Do not write a detect that exits 0 while work is pending (a dry-run is not a detect).
- Do not keep the clock in a gitignored file.
- Do not copy this package. Extend it here; a copy carries `go-foundation-copy: adoption@<sha>` and a removal row.
