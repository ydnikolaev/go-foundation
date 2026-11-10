# managed by devkit — do not edit; fix upstream in ~/Developer/projects/devkit
# Core gate: the devkit-managed files are intact. `copier update` exits 0 even when a same-line local edit of a
# managed file leaves inline conflict markers behind; this gate keeps such a file from being committed green.

# No `$(shell)` at parse time (make 4.4 expands every exported command-line variable for each one): the file list is
# found in the recipe.
CHECK_EXTRA += kit-check
CHECK_EXTRA += kit-harness

.PHONY: kit-check kit-harness devkit-commit

kit-check: ## devkit-managed files carry no conflict markers; no workaround marker names a fix this kit already has
	@bad=$$(grep -lE '^(<<<<<<<|>>>>>>>)( |$$)' $(wildcard mk/kit-*.mk) $(wildcard .claude/rules/kit-*.md) $$(find scripts/kit .devkit -type f 2>/dev/null) /dev/null 2>/dev/null); \
	if [ -n "$$bad" ]; then echo "kit-check: conflict markers in managed files:" >&2; echo "$$bad" >&2; exit 1; fi; \
	if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then echo "kit-check: ok (workaround scan skipped: no git work tree)"; exit 0; fi; \
	git grep -I -n -o -E 'devkit-workaroun[d]: FB-[0-9]+' 2>/dev/null | sed 's/^\(.*\):\([0-9][0-9]*\):[^:]*: \(FB-[0-9]*\)$$/\3 \1:\2/' | \
	awk -v list=.devkit/fixed-feedback 'BEGIN { while ((getline l < list) > 0) if (l ~ /^FB-[0-9]+ [^ ]+[ \t]*$$/) { split(l, a, " "); fixed[a[1]] = a[2] } } \
	{ id = $$1; at = $$0; sub(/^[^ ]+ /, "", at); n++; if (!(id in seen)) { seen[id] = 1; order[++k] = id } \
	  if (id in fixed) { print "kit-check: " id " is fixed in " fixed[id] " (installed); remove the workaround at " at > "/dev/stderr"; red = 1 } } \
	END { if (red) exit 1; if (n) { s = ""; for (i = 1; i <= k; i++) s = s (i > 1 ? " " : "") order[i]; printf "kit-check: ok (%d live workaround(s): %s)\n", n, s } else print "kit-check: ok" }'

# The devkit CLI the kit targets call (`devkit commit`, `devkit rec`, `devkit doctor`); a repo or the CI names another.
KIT_DEVKIT ?= devkit

# The harness check: the project's own CLAUDE.md and rules agree with the platform and the repo (`devkit doctor`'s
# harness.* rows; a warn row exits 0). The rows skip themselves in a repo that is not a devkit repo. A missing devkit is
# an error, never a silent skip (the way kit-docs refuses).
KIT_HARNESS_NEED_DEVKIT = set -- $(KIT_DEVKIT); command -v "$$1" >/dev/null 2>&1 || { echo "harness: devkit not found (KIT_DEVKIT=$(KIT_DEVKIT)) · next: install devkit, or set KIT_DEVKIT" >&2; exit 1; }

kit-harness: ## project instructions agree with the platform's and the repo (harness.* rows; warn rows exit 0)
	@$(KIT_HARNESS_NEED_DEVKIT); \
	$(KIT_DEVKIT) doctor --row harness.conflict --row harness.names --row harness.refs --row harness.restate --row harness.budget --row harness.gates --row harness.a2a

# The project's own steps of `devkit stop` (STOP §3): a project or an `mk/<area>.mk` adds `STOP_STEPS += <target>`; the verb
# runs `make <target>` per word, after the worktrees and before the containers. Word order follows include order, so a step
# must not depend on another. The value reaches the recipe through the environment only.
.PHONY: devkit-stop-steps
devkit-stop-steps: export DEVKIT_STOP_STEPS = $(STOP_STEPS)
devkit-stop-steps: ## Print the project's STOP_STEPS (the targets `devkit stop` runs), one line
	@printf '%s\n' "$$DEVKIT_STOP_STEPS"

# One conventional commit of the named files (`devkit commit`). The values reach the recipe only through the
# environment (never expanded into shell text), so a quote or `;` in M is data; P is split on spaces into paths.
devkit-commit: export DEVKIT_COMMIT_TYPE := $(value TYPE)
devkit-commit: export DEVKIT_COMMIT_SCOPE := $(value SCOPE)
devkit-commit: export DEVKIT_COMMIT_M := $(value M)
devkit-commit: export DEVKIT_COMMIT_P := $(value P)
devkit-commit: export DEVKIT_COMMIT_CLOSES := $(value CLOSES)
devkit-commit: ## Commit exactly the files in P as one conventional commit: make devkit-commit TYPE=fix SCOPE=x M="subject" P="a b" [CLOSES=FB-1]
	@set -f; set --; \
	[ -z "$$DEVKIT_COMMIT_SCOPE" ] || set -- "$$@" --scope="$$DEVKIT_COMMIT_SCOPE"; \
	[ -z "$$DEVKIT_COMMIT_CLOSES" ] || set -- "$$@" --closes="$$DEVKIT_COMMIT_CLOSES"; \
	$(KIT_DEVKIT) commit --type="$$DEVKIT_COMMIT_TYPE" --message="$$DEVKIT_COMMIT_M" "$$@" -- $$DEVKIT_COMMIT_P

# This project's page of the host overview (`devkit overview`, standards/make.md §7): built first when missing or stale,
# then opened. OVERVIEW_FLAGS passes more flags (`--no-open` prints the page path only).
OVERVIEW_FLAGS ?=
.PHONY: devkit-overview
devkit-overview: ## Open this project's page of devkit overview
	$(KIT_DEVKIT) overview --project $(CURDIR) $(OVERVIEW_FLAGS)
