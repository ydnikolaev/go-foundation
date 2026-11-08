# managed by devkit — do not edit; fix upstream in ~/Developer/projects/devkit
# Leak gate: `make check`, `check-full` and `check-docker` fail when a process or a container that the make run started is
# still alive once every other check prerequisite is done (scripts/kit/leak-gate.sh names each survivor, kills nothing).
#
# The root Makefile is a seed (`check: $(CHECK_EXTRA)`), so the gate cannot wrap `check`, and a second `check: .WAIT x`
# line from a fragment runs first (mk/*.mk is read before the seed's own line). The gate is instead one more CHECK_EXTRA
# item whose prerequisites are all the other items, through secondary expansion: they are read after every fragment and
# the project's own lines were parsed. Probed with GNU make 4.4.1: it runs last, serial and under -j.
# `.SECONDEXPANSION:` applies to every rule parsed after this line (later kit fragments, the project's mk/*.mk, the root
# Makefile): a prerequisite that needs a literal `$` must be written `$$$$` in those rules.
#
# No `$(shell)` at parse time (make 4.4 expands every exported command-line variable for each one): make's pid and start
# time are read by the recipe ($$PPID is make: the recipe shell is make's child), never at parse time.
#
# FB-586: the gate runs last, so its recipe also ends the light-run telemetry: after the verdict (its stdout and exit code
# unchanged) `check-select.py --record` writes the one row of a bare `make check`, with make's own wall time. The recorder
# prints nothing, exits 0 and is skipped where the runner is absent.
CHECK_EXTRA += kit-leak-gate

.SECONDEXPANSION:

# What else the goal runs: check-full and check-docker add CHECK_FULL, check-docker adds CHECK_DOCKER (and its need check).
KIT_LEAK_MORE = $(if $(filter check-full check-docker,$(MAKECMDGOALS)),$(CHECK_FULL)) $(if $(filter check-docker,$(MAKECMDGOALS)),kit-docker-need $(CHECK_DOCKER))

.PHONY: kit-leak-gate

kit-leak-gate: export KIT_LEAK_GOALS := $(MAKECMDGOALS)
kit-leak-gate: $$(filter-out kit-leak-gate,$$(CHECK_EXTRA)) $$(KIT_LEAK_MORE)
	@bash scripts/kit/leak-gate.sh "$$PPID" "$$KIT_LEAK_GOALS"; rc=$$?; \
	if [ -f scripts/kit/check-select.py ]; then python3 scripts/kit/check-select.py --record --make-pid "$$PPID" --exit "$$rc" --goals "$$KIT_LEAK_GOALS" || :; fi; \
	exit "$$rc"
