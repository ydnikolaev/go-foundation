# Project-owned: this repository is public (R-6). public-scan judges the tree and every ref's history for private repo
# names (derived through gh at run time), host paths, emails and secrets (gitleaks). It needs gh, so it runs before a
# push or a visibility change, never in public CI; its teeth are offline and run in every `make check`.
.PHONY: public-scan public-scan-test

CHECK_EXTRA += public-scan-test

public-scan: ## Private names, host paths, emails, secrets over the tree and --all history (gh; before every push)
	@python3 scripts/public-scan.py scan

public-scan-test: ## public-scan's own teeth: a planted name, path, email and generated secret are refused (offline)
	@python3 scripts/test-public-scan.py
