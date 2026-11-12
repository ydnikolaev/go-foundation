# managed by devkit — do not edit; fix upstream in ~/Developer/projects/devkit
# The foundation module: the unit gate of a shared-unit library repo (standards/foundation.md). The logic is devkit's
# `foundation` verbs, so no script is copied here. `kit-foundation-check` runs in every `make check` (the unit rules, then
# UNITS.md must equal the catalogue of the tree); `kit-foundation-lint` runs in `make check-full`. A release is
# `devkit foundation release <id> X.Y.Z`, never a make target. Each unit's tests run through its stack's test gate (go:
# the Go module's kit-go-test; ts: kit-foundation-test, `npm test --workspaces --if-present`).
# A ts repo (config/foundation.toml says `stack = "ts"`) is an npm workspace: the targets below are a silent no-op in a go
# repo. The first lockfile is the repo's to make: once the first unit lands, run `npm install` at the root and commit
# package-lock.json; with no lockfile the ts targets do nothing (an empty repo stays offline), with one they run `npm ci`.
KIT_FOUNDATION_TS = grep -qx 'stack = "ts"' config/foundation.toml 2>/dev/null
KIT_FOUNDATION_NEED_DEVKIT = set -- $(KIT_DEVKIT); command -v "$$1" >/dev/null 2>&1 || { echo "foundation: devkit not found (KIT_DEVKIT=$(KIT_DEVKIT)) · next: install devkit, or set KIT_DEVKIT" >&2; exit 1; }

CHECK_EXTRA += kit-foundation-check kit-foundation-test
CHECK_FULL += kit-foundation-lint

.PHONY: kit-foundation-deps kit-foundation-check kit-foundation-catalogue kit-foundation-lint kit-foundation-test

kit-foundation-deps: ## ts: npm ci when package-lock.json is newer than node_modules (no lockfile: nothing; go: nothing)
	@$(KIT_FOUNDATION_TS) || exit 0; \
	[ -f package-lock.json ] || exit 0; \
	command -v npm >/dev/null 2>&1 || { echo "foundation: npm not found · next: install Node and npm" >&2; exit 1; }; \
	if [ ! -f node_modules/.package-lock.json ] || [ -n "$$(find package-lock.json -newer node_modules/.package-lock.json)" ]; then npm ci; fi

kit-foundation-test: kit-foundation-deps ## ts: the unit tests, npm test --workspaces --if-present (no lockfile or no unit: nothing)
	@$(KIT_FOUNDATION_TS) || exit 0; \
	[ -f package-lock.json ] || exit 0; \
	set -- units/*/package.json; [ -f "$$1" ] || exit 0; \
	npm test --workspaces --if-present

kit-foundation-check: kit-foundation-deps ## The unit gate: every unit rule holds, and UNITS.md equals the catalogue of the tree
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

kit-foundation-lint: kit-foundation-deps ## The binding's lint in every unit (go: gofmt, vet and the pinned golangci-lint; ts: npm run lint)
	@$(KIT_FOUNDATION_NEED_DEVKIT); \
	$(KIT_DEVKIT) foundation lint --root "$(CURDIR)"
