# managed by devkit — do not edit; fix upstream in ~/Developer/projects/devkit
# Docs: ADRs, backlog, rulings, work folders. Formats and lint rules: standards/docs.md. docs.py declares PyYAML in its PEP 723 header, so the goals run under uv (FB-393).
KIT_DOCS := PYTHONDONTWRITEBYTECODE=1 uv run --quiet --no-project --with pyyaml python3 scripts/kit/docs.py

# The rulings and backlog stores are written, indexed and linted by `devkit rec` (KIT_DEVKIT, defined in kit-core.mk).
# Its targets run only once a store index exists (the first `devkit rec` write or `devkit rec index` creates it); a
# project without one is checked by docs.py alone. A missing devkit is an error, never a silent skip.
KIT_DOCS_HAS_INDEX = test -f docs/rulings/rulings.index.yaml -o -f docs/backlog/backlog.index.yaml
KIT_DOCS_NEED_DEVKIT = set -- $(KIT_DEVKIT); command -v "$$1" >/dev/null 2>&1 || { echo "docs: devkit not found (KIT_DEVKIT=$(KIT_DEVKIT)) · next: install devkit, or set KIT_DEVKIT" >&2; exit 1; }

# docs-check is light; docs-test (the tooling's own unit tests, FB-326) runs in check-full only.
CHECK_EXTRA += docs-check
CHECK_FULL += docs-test

.PHONY: docs docs-check docs-test feature-new feature-archive

docs: ## Regenerate the rec indexes (once a store index exists; no commit) and the legacy docs/decisions.md
	@if $(KIT_DOCS_HAS_INDEX); then $(KIT_DOCS_NEED_DEVKIT); $(KIT_DEVKIT) rec index --no-commit || exit 1; fi
	@$(KIT_DOCS) index

# Project lints: a project fragment mk/<name>.mk with `docs-check: <project-lint>` adds a prerequisite here, in any include order.
docs-check: ## Lint ADRs, work folders and cross-refs; with a store index also devkit rec check
	@$(KIT_DOCS) check
	@if $(KIT_DOCS_HAS_INDEX); then $(KIT_DOCS_NEED_DEVKIT); $(KIT_DEVKIT) rec check || exit 1; fi

docs-test: ## Run the docs tooling unit tests
	@PYTHONDONTWRITEBYTECODE=1 uv run --quiet --no-project --with pyyaml python3 -m unittest discover -s scripts/kit -p 'test_*.py'

feature-new: ## Open a work folder: make feature-new SLUG=<kebab-slug>
	@test -n "$(SLUG)" || { echo "feature-new: SLUG=<kebab-slug> is required" >&2; exit 2; }
	@$(KIT_DOCS) feature new "$(SLUG)"

feature-archive: ## Close a work folder: make feature-archive SLUG=<slug> (README outcome stated, phases closed)
	@test -n "$(SLUG)" || { echo "feature-archive: SLUG=<slug> is required" >&2; exit 2; }
	@$(KIT_DOCS) feature archive "$(SLUG)"
