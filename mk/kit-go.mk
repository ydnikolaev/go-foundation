# managed by devkit — do not edit; fix upstream in ~/Developer/projects/devkit
# Go caches live in the MAIN checkout's .cache/ (self-ignoring), shared by every worktree: KIT_MAIN is the parent of
# the git common dir, so a linked worktree resolves to the main checkout, not itself. `:=` on purpose, so
# `make -p` prints the expanded paths. No `$(shell)` at parse time (make 4.4 would expand every exported command-line
# variable for each one): a linked worktree's `.git` is a file `gitdir: <main>/.git/worktrees/<name>`, read with `$(file <)`,
# which needs GNU make >= 4.2 (macOS /usr/bin/make is 3.81: `$(file <)` would expand empty and KIT_MAIN would silently be
# the worktree itself, so the caches would stop being shared). Fail loudly instead.
ifneq ($(filter 3.% 4.0 4.0.% 4.1 4.1.%,$(MAKE_VERSION)),)
$(error mk/kit-go.mk needs GNU make >= 4.2, this is $(MAKE_VERSION); on macOS `brew install make` and run `gmake`)
endif
kit_up = $(patsubst %/,%,$(dir $(patsubst %/,%,$1)))
KIT_GITLINK := $(if $(wildcard .git/.),,$(if $(wildcard .git),$(strip $(subst gitdir:,,$(file <.git)))))
KIT_GITDIR := $(if $(KIT_GITLINK),$(abspath $(if $(filter /%,$(KIT_GITLINK)),,$(CURDIR)/)$(KIT_GITLINK)))
KIT_MAIN := $(if $(findstring /worktrees/,$(KIT_GITDIR)),$(call kit_up,$(call kit_up,$(call kit_up,$(KIT_GITDIR)))),$(CURDIR))

export GOCACHE := $(KIT_MAIN)/.cache/go/build
export GOMODCACHE := $(KIT_MAIN)/.cache/go/mod
export GOTMPDIR := $(KIT_MAIN)/.cache/go/tmp
export GOLANGCI_LINT_CACHE := $(KIT_MAIN)/.cache/golangci
export GOFLAGS := $(strip $(GOFLAGS) -modcacherw)

KIT_CACHE_DIRS := $(GOCACHE) $(GOMODCACHE) $(GOTMPDIR) $(GOLANGCI_LINT_CACHE)
# Create the dirs, and make .cache/ ignore itself so the repo needs no .gitignore edit for it. Done by make's own
# makefile-remaking step, not at parse time: the stamp is an (empty) included makefile whose recipe makes the dirs;
# when it is missing (first run, or cache-trim aged it out of GOTMPDIR) make builds it and restarts once.
KIT_CACHE_STAMP := $(GOTMPDIR)/.kit-cache.mk
-include $(KIT_CACHE_STAMP)
$(KIT_CACHE_STAMP):
	@mkdir -p $(KIT_CACHE_DIRS) && { [ -f "$(KIT_MAIN)/.cache/.gitignore" ] || printf '*\n' >"$(KIT_MAIN)/.cache/.gitignore"; } && : >$@

# The trim stays light; cache-trim-test (its own script test) runs in check-full only (D-7).
CHECK_EXTRA += cache-trim-auto
CHECK_FULL += cache-trim-test

.PHONY: cache-trim cache-trim-auto cache-trim-test cache-status kit-go-test kit-go-lint kit-go-grep kit-go-shape kit-go-baseline

# FB-725 (R-214): grep gates in every `make check`: no //nolint and no time.Sleep in a _test.go (code keeps the explained //nolint:<linter> // <why>, nolintlint polices it). Whole-tree like
# the lint above, for the same reason: a rule that only fires on touched files lets old breaches hide.
CHECK_EXTRA += kit-go-grep

# FB-878/FB-879: the code-shape gate (standards/code-shape.md rules 1 and 5), whole-tree and cheap (a line count, no Go toolchain). Advisory
# until the repo writes config/go-shape.ceiling or sets a KIT_GO_* bound in .devkit/config.env, so an update never turns `make check` red.
CHECK_EXTRA += kit-go-shape

# FB-724 (R-214): the lint baseline, .golangci.yml (managed, govet + errorlint + wrapcheck + nolintlint), in every `make check`.
# Whole-tree on purpose, not selected by changed paths: golangci-lint is cached (GOLANGCI_LINT_CACHE) and a rule that
# only fires on touched files lets old breaches hide. GOLANGCI_LINT names the binary (a test points it at a missing one).
#
# FB-768: golangci-lint takes a host-wide lock (os.TempDir()/golangci-lint.lock) and by default gives up after 5 s with
# "parallel golangci-lint is running", so gates of several repos at once went red. --allow-serial-runners waits on the
# lock instead (retry every 1 s, no deadline); --allow-parallel-runners would drop the lock, so it is not used.
GOLANGCI_LINT ?= golangci-lint
CHECK_EXTRA += kit-go-lint

# Not in CHECK_EXTRA: .devkit/check-map.d/go.map selects it from the changed Go paths (`make check`), and check-full
# runs it once over every module. KIT_CHECK_PATHS (set by the runner) names the paths; empty means the whole tree.
kit-go-test: ## Go tests for the changed packages (KIT_CHECK_PATHS), else every module's ./...
	@bash scripts/kit/go-test.sh

# FB-932 (R-259): with config/go-lint.ceiling (project-owned, written once by kit-go-baseline) a run does not exit on findings: every module
# prints absolute paths, the lines become `<path>\t<linter>\t<line>` and scripts/kit/go-ceiling.sh judges them once, per package and linter.
# Any non-zero exit of the tool is then a failure to run (exit 2), never judged. Without the file every finding is red, as before.
# A build error prints as `<path>:1: : # <pkg>` and the linter `(typecheck)` ends the line after it, so a line with no linter takes the
# first following line that ends in one (else `unknown`).
kit-go-lint: ## golangci-lint v2 baseline (.golangci.yml: govet, errorlint, wrapcheck, nolintlint) in every go.mod dir; config/go-lint.ceiling counts old findings; refuses without the binary
	@mods="$$(git ls-files -co --exclude-standard -- go.mod '*/go.mod' | sort -u)"; \
	if [ -z "$$mods" ]; then echo "kit-go-lint: no go.mod: nothing to lint"; exit 0; fi; \
	bin="$$(command -v "$(GOLANGCI_LINT)" 2>/dev/null)"; \
	if [ -z "$$bin" ]; then \
		echo "kit-go-lint: $(GOLANGCI_LINT) is not installed: not a pass. Install v2: brew install golangci-lint, or go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest" >&2; exit 2; fi; \
	ver="$$("$$bin" --version 2>&1)"; \
	case "$$ver" in *"version 2."*) ;; *) \
		echo "kit-go-lint: $$bin is not golangci-lint v2 (.golangci.yml is version 2; pin: config/versions.toml [template] in devkit): upgrade it" >&2; exit 2 ;; esac; \
	[ -f .golangci.yml ] || { echo "kit-go-lint: .golangci.yml is missing: run devkit update --apply" >&2; exit 2; }; \
	cfg="$(CURDIR)/.golangci.yml"; \
	ceil=""; if [ -f config/go-lint.ceiling ] || [ "$$KIT_GO_CEILING" = write ]; then ceil=1; fi; \
	wflag=""; if [ "$$KIT_GO_CEILING" = write ]; then wflag=--write; fi; \
	top="$$(git rev-parse --show-toplevel 2>/dev/null || pwd)"; found=.artifacts/tmp/go-lint-findings.tsv; \
	if [ -n "$$ceil" ]; then mkdir -p .artifacts/tmp; : > "$$found"; fi; \
	rc=0; \
	for gomod in $$mods; do \
		[ -f "$$gomod" ] || continue; \
		dir="$$(dirname "$$gomod")"; \
		if [ "$$dir" != . ]; then case "/$$dir/" in */testdata/*|*/vendor/*|*/_*|*/.?*) continue ;; esac; fi; \
		echo "kit-go-lint: $$dir"; \
		if [ -z "$$ceil" ]; then \
			(cd "$$dir" && "$$bin" run --allow-serial-runners --config "$$cfg" ./...) || rc=1; \
		else \
			o="$$(cd "$$dir" && "$$bin" run --allow-serial-runners --config "$$cfg" --issues-exit-code=0 --path-mode abs --output.text.print-issued-lines=false ./...)"; r=$$?; \
			if [ "$$r" -ne 0 ]; then echo "kit-go-lint: $$bin exited $$r in $$dir: a tool failure, not judged" >&2; if [ -n "$$o" ]; then printf '%s\n' "$$o" >&2; fi; exit 2; fi; \
			printf '%s\n' "$$o" | awk -v t1="$(CURDIR)" -v t2="$$top" -v t3="$$(pwd -P)" -v t4="$$(pwd)" 'function rel(p,  k, t) { for (k = 1; k <= 4; k++) { t = tp[k]; if (t != "" && index(p, t "/") == 1) return substr(p, length(t) + 2) } return p } function emit() { if (have) printf "%s\t%s\t%s\n", r, (l == "" ? "unknown" : l), r rest; have = 0 } BEGIN { tp[1] = t1; tp[2] = t2; tp[3] = t3; tp[4] = t4 } /^\// { emit(); if (!match($$0, /^[^:]+:[0-9]+/)) next; f = substr($$0, 1, index(substr($$0, 1, RLENGTH), ":") - 1); r = rel(f); rest = substr($$0, length(f) + 1); l = ""; if (match($$0, /\([A-Za-z0-9_-]+\)$$/)) l = substr($$0, RSTART + 1, RLENGTH - 2); have = 1; next } have && l == "" && match($$0, /\([A-Za-z0-9_-]+\)$$/) { l = substr($$0, RSTART + 1, RLENGTH - 2) } END { emit() }' >> "$$found"; \
		fi; \
	done; \
	if [ -n "$$ceil" ]; then \
		KIT_CEILING_TOOL="$$(printf '%s\n' "$$ver" | sed -n 1p)" bash scripts/kit/go-ceiling.sh kit-go-lint config/go-lint.ceiling $$wflag < "$$found"; exit $$?; fi; \
	if [ "$$rc" -ne 0 ]; then echo "kit-go-lint: adopting with old findings? standards/adopt.md#go-baseline" >&2; fi; \
	exit $$rc

kit-go-grep: ## Refuse //nolint and time.Sleep in _test.go files (whole tree); config/go-grep.ceiling counts old ones
	@bash scripts/kit/go-test-grep.sh

kit-go-baseline: ## Record today's Go lint and grep findings in config/go-lint.ceiling and config/go-grep.ceiling; refuses a file that exists
	@for f in config/go-lint.ceiling config/go-grep.ceiling; do \
		if [ -e "$$f" ]; then echo "kit-go-baseline: $$f exists: lower or raise a line by hand" >&2; exit 1; fi; \
	done; \
	$(MAKE) --no-print-directory KIT_GO_CEILING=write kit-go-lint && $(MAKE) --no-print-directory KIT_GO_CEILING=write kit-go-grep && \
	echo "kit-go-baseline: both ceiling files written: commit them, then run make check"

kit-go-shape: ## Code-shape gate: an entry package over 300 code lines, any package over 2000 (advisory until config/go-shape.ceiling exists)
	@bash scripts/kit/go-shape.sh

cache-trim: ## Trim Go caches now (age + size caps)
	@bash scripts/kit/cache-trim.sh --force

cache-trim-auto: ## Trim Go caches at most once per 24h
	@bash scripts/kit/cache-trim.sh --auto

cache-trim-test: ## Test the cache trim script
	@bash scripts/kit/test-cache-trim.sh

cache-status: ## Show Go cache sizes
	@for d in $(KIT_CACHE_DIRS); do \
		printf '%-8s %s\n' "$$(du -sh "$$d" 2>/dev/null | awk '{print $$1}')" "$$d"; \
	done
