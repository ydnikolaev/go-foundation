# Project-owned: the unit gate (R-1..R-5). scripts/units.py is the one reader of the units.
.PHONY: units-check units-catalogue units-test units-lint consumers

CHECK_EXTRA += units-check units-test
CHECK_FULL += units-lint     # a release runs check-full (scripts/unit-release.sh), so every tag is linted

units-check: ## Every unit: unit.json, module path, go floor, go.work, README headings, release notes
	@python3 scripts/units.py check

units-catalogue: ## Write UNITS.md from every units/<id>/unit.json (to $KIT_COMPOSE_OUT when set)
	@python3 scripts/units.py catalogue

units-test: ## The unit and registry gates' own teeth: each rule refuses the wrong thing (offline)
	@python3 scripts/test-units.py && python3 scripts/test-consumers.py

units-lint: ## gofmt, go vet and golangci-lint in every unit (each unit is its own module)
	@set -e; for d in $$(python3 scripts/units.py list | cut -d' ' -f1); do \
	  bad=$$(cd units/$$d && gofmt -l .); [ -z "$$bad" ] || { echo "units-lint: units/$$d: gofmt: $$bad" >&2; exit 1; }; \
	  (cd units/$$d && go vet ./... && golangci-lint run ./...); done; echo "units-lint: ok"

consumers: ## Regenerate the host registry's consumer table via gh (network; never in make check; R-5)
	@python3 scripts/consumers.py generate
