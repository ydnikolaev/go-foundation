# Work folders

Work that spans more than one session or more than one agent gets a folder under `active/<slug>/` (goal, tracker,
rulings, specs, plans, handoff); closed work moves whole to `archive/<YYYY>/<slug>/`. Open one with
`make feature-new SLUG=<slug>` and close it with `make feature-archive SLUG=<slug>`; the shape and the caps are
in devkit's `standards/structure.md` §3, and `make docs-check` lints them.
