# managed by devkit — do not edit; fix upstream in ~/Developer/projects/devkit
# Gate tiers: `make check` is the light gate, `check-full` and `check-docker` (the root Makefile's seed) the full ones.
# kit-select runs each map target (config/check-map, the project's config/check-map.d/*.map, the packs'
# .devkit/check-map.d/*.map, in that order) whose input digest has no green row in <git common dir>/kit/runs.jsonl, and
# refuses an unmapped path once the project has the `check-full: KIT_TIER := full` line. `sweep` is check-full with that
# store ignored.

# No `$(shell)` at parse time (make 4.4 expands every exported command-line variable for each one). Values reach the
# runner through the environment, never expanded into shell text. KIT_TIER is `full` only while check-full or check-docker
# is being built: their target-specific `KIT_TIER := full` is inherited by every prerequisite, kit-select included, and
# `export` hands that value (whatever its origin: the Makefile, the command line, the environment) to the recipe. The
# runner accepts `full` and `sweep` only when make's goals name check-full or check-docker. Old callers (KIT_REUSE set, or
# KIT_CHECK_BASE non-empty) get `make check-full` for a light run.
CHECK_EXTRA += kit-select
export KIT_TIER

.PHONY: kit-select

kit-select: export KIT_GOALS := $(MAKECMDGOALS)
kit-select: export KIT_MAKE := $(MAKE)
kit-select:
	@KIT_MAKE_PID=$$PPID python3 scripts/kit/check-select.py

.PHONY: sweep

sweep: ## check-full with the store ignored: every target runs
	+@$(MAKE) --no-print-directory check-full KIT_TIER=sweep

# `make test` (the managed GNUmakefile: `test: kit-test`): every registered test target (an `@test-gate` line of a check map)
# outside CHECK_DOCKER, tier full, reused from the store like `check-only`; no lint. `make test TESTS="<unit> ..."` runs exactly
# those units, each through the driver of the stack that lists it (scripts/kit/check-units.py --tests), at full, whatever the store
# holds; RUN=<expr> narrows the run inside each unit and a filtered run writes no row. TESTS and RUN reach the recipe through the
# environment, never expanded into shell text. `check-only T="<target> ..."` runs exactly these map targets through the store: `reused <target> green <ts>`, or
# run and record. T is read from the environment, never expanded into shell text. Both are heavy by their names, so the queue
# holds them (`check-%`, `test`).
.PHONY: kit-test check-only

kit-test: ## tests only, no lint: every registered test target outside CHECK_DOCKER, store reused; TESTS="<unit> ..." runs those units
	@if [ -n "$$TESTS" ]; then set -f; exec python3 scripts/kit/check-units.py --tests $${RUN:+--filter "$$RUN"} $$TESTS; fi; \
	gates=$$(cat config/check-map config/check-map.d/*.map .devkit/check-map.d/*.map 2>/dev/null | awk '$$1 == "@test-gate" { for (i = 2; i <= NF && $$i !~ /^#/; i++) print $$i }'); \
	todo=; for g in $$gates; do case " $(CHECK_DOCKER) " in *" $$g "*) ;; *) todo="$$todo $$g";; esac; done; \
	[ -n "$$todo" ] || { echo "kit-test: no test target is registered (an @test-gate line of a check map)"; exit 0; }; \
	$(MAKE) --no-print-directory check-only T="$$todo"

check-only: ## run exactly these map targets through the store: make check-only T="<target> ..."
	@[ -n "$$T" ] || { echo "check-only: name the targets: make check-only T=\"<target> ...\"" >&2; exit 2; }; \
	set -f; KIT_MAKE="$(MAKE)" KIT_MAKE_PID=$$PPID python3 scripts/kit/check-select.py --only $$T

# Coverage floors: the selector checks config/coverage-*.yaml and its .d/*.yaml fragments on every run; `coverage-raise` is
# explicit only (it runs no test, and is no heavy goal): after a bare `make check-full` in the checkout lanes merge into, it
# raises the floors from that run's measure and names the files to commit.
.PHONY: coverage-raise

coverage-raise: ## Raise config/coverage-<stack>.yaml from this checkout's last make check-full (never lowers); commit the file
	@python3 scripts/kit/cover-gate.py raise
