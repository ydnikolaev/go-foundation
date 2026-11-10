# managed by devkit — do not edit; fix upstream in ~/Developer/projects/devkit
# The foundation module: the unit gate of a shared-unit library repo (standards/foundation.md). The logic is devkit's
# `foundation` verbs, so no script is copied here. `kit-foundation-check` runs in every `make check` (the unit rules, then
# UNITS.md must equal the catalogue of the tree); `kit-foundation-lint` runs in `make check-full`. A release is
# `devkit foundation release <id> X.Y.Z`, never a make target. Each unit's tests run through the Go module's kit-go-test.
KIT_FOUNDATION_NEED_DEVKIT = set -- $(KIT_DEVKIT); command -v "$$1" >/dev/null 2>&1 || { echo "foundation: devkit not found (KIT_DEVKIT=$(KIT_DEVKIT)) · next: install devkit, or set KIT_DEVKIT" >&2; exit 1; }

CHECK_EXTRA += kit-foundation-check
CHECK_FULL += kit-foundation-lint

.PHONY: kit-foundation-check kit-foundation-catalogue kit-foundation-lint

kit-foundation-check: ## The unit gate: every unit rule holds, and UNITS.md equals the catalogue of the tree
	@$(KIT_FOUNDATION_NEED_DEVKIT); \
	$(KIT_DEVKIT) foundation check --root "$(CURDIR)" || exit $$?; \
	tmp="$$(mktemp)"; trap 'rm -f "$$tmp"' EXIT; \
	$(KIT_DEVKIT) foundation catalog --markdown --root "$(CURDIR)" >"$$tmp" || exit $$?; \
	if ! cmp -s "$$tmp" UNITS.md; then \
		echo "kit-foundation-check: UNITS.md differs from the catalogue of units/ · next: make kit-foundation-catalogue, then commit UNITS.md" >&2; exit 1; fi

kit-foundation-catalogue: ## Write UNITS.md from the catalogue of units/ (commit it)
	@$(KIT_FOUNDATION_NEED_DEVKIT); \
	tmp="$$(mktemp)"; trap 'rm -f "$$tmp"' EXIT; \
	$(KIT_DEVKIT) foundation catalog --markdown --root "$(CURDIR)" >"$$tmp" || exit $$?; \
	cat "$$tmp" >UNITS.md

kit-foundation-lint: ## The binding's lint in every unit: gofmt, vet and the pinned golangci-lint
	@$(KIT_FOUNDATION_NEED_DEVKIT); \
	$(KIT_DEVKIT) foundation lint --root "$(CURDIR)"
