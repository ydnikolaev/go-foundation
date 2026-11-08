# Project-owned: devkit seeded this once and never touches it again. Edit freely.
# Layout (standards/make.md): a thin root; devkit fragments in mk/kit-*.mk (managed), yours in mk/<area>.mk.
.DEFAULT_GOAL := help

CHECK_EXTRA :=    # every tier runs these; devkit fragments append
CHECK_FULL :=     # check-full only: whole-tree suites no path selects
-include mk/*.mk

.PHONY: help check

help: ## List targets
	@grep -hE '^[a-zA-Z0-9_-]+:.*## ' $(MAKEFILE_LIST) | sort | awk -F':.*## ' '{printf "  %-20s %s\n", $$1, $$2}'

check: $(CHECK_EXTRA) ## Light gate: kit gates + what config/check-map selects from the changed paths
check-full: KIT_TIER := full
check-full: $(CHECK_EXTRA) $(CHECK_FULL) ## Full local gate: every mapped target over the whole tree, once
