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

.PHONY: cache-trim cache-trim-auto cache-trim-test cache-status kit-go-test kit-go-lint kit-go-grep

# FB-725 (R-214): grep gates in every `make check`: no //nolint and no time.Sleep in a _test.go (code keeps the explained //nolint:<linter> // <why>, nolintlint polices it). Whole-tree like
# the lint above, for the same reason: a rule that only fires on touched files lets old breaches hide.
CHECK_EXTRA += kit-go-grep

# FB-724 (R-214): the lint baseline, .golangci.yml (managed, errorlint + wrapcheck + nolintlint), in every `make check`.
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

kit-go-lint: ## golangci-lint v2 baseline (.golangci.yml: errorlint, wrapcheck, nolintlint) in every go.mod dir; refuses without the binary
	@mods="$$(git ls-files -co --exclude-standard -- go.mod '*/go.mod' | sort -u)"; \
	if [ -z "$$mods" ]; then echo "kit-go-lint: no go.mod: nothing to lint"; exit 0; fi; \
	bin="$$(command -v "$(GOLANGCI_LINT)" 2>/dev/null)"; \
	if [ -z "$$bin" ]; then \
		echo "kit-go-lint: $(GOLANGCI_LINT) is not installed: not a pass. Install v2: brew install golangci-lint, or go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest" >&2; exit 2; fi; \
	case "$$("$$bin" --version 2>&1)" in *"version 2."*) ;; *) \
		echo "kit-go-lint: $$bin is not golangci-lint v2 (.golangci.yml is version 2; pin: config/versions.toml [template] in devkit): upgrade it" >&2; exit 2 ;; esac; \
	[ -f .golangci.yml ] || { echo "kit-go-lint: .golangci.yml is missing: run devkit update --apply" >&2; exit 2; }; \
	cfg="$(CURDIR)/.golangci.yml"; \
	rc=0; \
	for gomod in $$mods; do \
		[ -f "$$gomod" ] || continue; \
		dir="$$(dirname "$$gomod")"; \
		if [ "$$dir" != . ]; then case "/$$dir/" in */testdata/*|*/vendor/*|*/_*|*/.?*) continue ;; esac; fi; \
		echo "kit-go-lint: $$dir"; \
		(cd "$$dir" && "$$bin" run --allow-serial-runners --config "$$cfg" ./...) || rc=1; \
	done; \
	exit $$rc

kit-go-grep: ## Refuse //nolint and time.Sleep in _test.go files (whole tree)
	@bash scripts/kit/go-test-grep.sh

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
