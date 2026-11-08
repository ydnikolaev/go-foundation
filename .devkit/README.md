<!-- managed by devkit — do not edit; fix upstream in ~/Developer/projects/devkit -->
# .devkit

Everything here is delivered by devkit and overwritten on `copier update`: do not edit it. Same for
`mk/kit-*.mk`, `scripts/kit/` and `.copier-answers.devkit.yml`. A defect or an improvement goes into
`devkit feedback "<what happened>" --want "<the change>"`; the fix is made upstream (`~/Developer/projects/devkit`) and arrives with the next update.
A defect that blocks you: a project extension point if one fits, else patch the managed copy, write
`devkit-workaround: FB-n` beside the change, and file it `--blocking --workaround <path>` (detail: devkit
`standards/defects.md`). `make kit-check` fails once the installed kit lists FB-n in `fixed-feedback`: remove the workaround.

`config.env` carries this project's answers (project slug, variable prefix, backlog id prefix, port block) for the
scripts in `scripts/kit/` and for `devkit rec backlog new`, which takes the id prefix from it. Change an answer with `uvx copier update --answers-file .copier-answers.devkit.yml --vcs-ref <tag> --data key=value`
(`<tag>` from `git -C ~/.devkit/current describe --tags`), not by hand.
`fixed-feedback` is the list of feedback ids devkit has fixed (`FB-n vX.Y.Z` per line); `kit-check` and `devkit update` read it.
