# managed by devkit — do not edit; fix upstream in ~/Developer/projects/devkit
# Gate tiers: `make check` is the light gate, `check-full` and `check-docker` (the root Makefile's seed) the full ones.
# kit-select runs the change-aware runner: it selects targets from the changed paths through config/check-map, the
# project's config/check-map.d/*.map and the packs' .devkit/check-map.d/*.map (in that order), and refuses an unmapped path once the project has the `check-full: KIT_TIER := full` line.

# No `$(shell)` at parse time (make 4.4 expands every exported command-line variable for each one). Values reach the
# runner through the environment, never expanded into shell text. KIT_TIER is `full` only while check-full or check-docker
# is being built: their target-specific `KIT_TIER := full` is inherited by every prerequisite, kit-select included, and
# `export` hands that value (whatever its origin: the Makefile, the command line, the environment) to the recipe. The
# runner accepts `full` only when make's goals name check-full or check-docker. KIT_CHECK_BASE is read from the
# environment or the command line as given.
CHECK_EXTRA += kit-select
export KIT_TIER

.PHONY: kit-select

kit-select: export KIT_GOALS := $(MAKECMDGOALS)
kit-select: export KIT_MAKE := $(MAKE)
kit-select:
	@KIT_MAKE_PID=$$PPID python3 scripts/kit/check-select.py

# Coverage floors: the selector checks config/coverage-*.yaml and its .d/*.yaml fragments on every run; `coverage-raise` is
# explicit only (it runs no test, and is no heavy goal): after a bare `make check-full` in the checkout lanes merge into, it
# raises the floors from that run's measure and names the files to commit.
.PHONY: coverage-raise

coverage-raise: ## Raise config/coverage-<stack>.yaml from this checkout's last make check-full (never lowers); commit the file
	@python3 scripts/kit/cover-gate.py raise
