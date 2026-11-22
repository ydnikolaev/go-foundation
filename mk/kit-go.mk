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
# DK-061 (placement amended 2026-11-16): GOTMPDIR is outside every work tree and outside /var/folders (Spotlight indexes it):
# <base>/devkit-go/<main checkout path, encoded>, the path with each `_` doubled and then each `/` made `_-` (every `_` starts a
# two-character token, so the code is prefix-free and injective; stable per repo so the test cache keys on it, ends with the
# project name, and has no `%`, which would turn the stamp rule below into a pattern rule). <base> is DEVKIT_TMP_BASE when set, else $(HOME)/Developer/.tmp when $(HOME)/Developer is a directory, else
# $(TMPDIR) (then /tmp), a trailing slash trimmed. scripts/kit/cache-trim.sh computes the same base and leaf in shell.
KIT_TMP_BASE := $(patsubst %/,%,$(or $(DEVKIT_TMP_BASE),$(if $(wildcard $(HOME)/Developer/.),$(HOME)/Developer/.tmp,$(or $(TMPDIR),/tmp))))
KIT_GOTMP_KEY := $(subst /,_-,$(subst _,__,$(KIT_MAIN)))

export GOTMPDIR := $(KIT_TMP_BASE)/devkit-go/$(KIT_GOTMP_KEY)
export GOLANGCI_LINT_CACHE := $(KIT_MAIN)/.cache/golangci
export GOFLAGS := $(strip $(GOFLAGS) -modcacherw)

KIT_CACHE_DIRS := $(GOCACHE) $(GOMODCACHE) $(GOTMPDIR) $(GOLANGCI_LINT_CACHE)
# Create the dirs, and make .cache/ ignore itself so the repo needs no .gitignore edit for it. Done by make's own
# makefile-remaking step, not at parse time: the stamp is an (empty) included makefile whose recipe makes the dirs;
# when it is missing (first run, or cache-trim aged it out of GOTMPDIR) make builds it and restarts once. GOTMPDIR is made 0700.
KIT_CACHE_STAMP := $(GOTMPDIR)/.kit-cache.mk
-include $(KIT_CACHE_STAMP)
$(KIT_CACHE_STAMP):
	@{ [ -d "$(GOTMPDIR)" ] || { mkdir -p "$(GOTMPDIR)" && chmod 0700 "$(GOTMPDIR)"; }; } && mkdir -p $(KIT_CACHE_DIRS) && { [ -f "$(KIT_MAIN)/.cache/.gitignore" ] || printf '*\n' >"$(KIT_MAIN)/.cache/.gitignore"; } && : >$@

# The trim stays light; its own script test lives in devkit's tests/, not in a render.
CHECK_EXTRA += cache-trim-auto

.PHONY: cache-trim cache-trim-auto cache-status kit-go-test kit-go-test-flaky kit-go-lint kit-go-fmt kit-go-grep kit-go-shape kit-go-baseline kit-go-add

# FB-1011: PKG and DIR reach the recipe only through the environment (`$(value)`: make never expands them).
kit-go-add: export GO_ADD_PKG := $(value PKG)
kit-go-add: export GO_ADD_DIR := $(or $(value DIR),.)

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
#
# T-GO: kit-go-lint is a row of .devkit/check-map.d/go.map (`**/*.go kit-go-lint kit-go-test`), not a CHECK_EXTRA item: a run with changed Go
# paths (KIT_CHECK_PATHS, light tier, no config/go-lint.ceiling) lints those packages and checks those files and modules; the full tier, a
# change of .golangci.yml and `make lint` lint every module. It also refuses an unformatted .go file (gofmt -l) and an untidy go.mod
# (`go mod tidy -diff`, offline first; a module missing from the cache is fetched once; a run that still fails with no output is a tool failure, exit 2, never a verdict). `make fmt` runs gofmt -w.
GOLANGCI_LINT ?= golangci-lint
LINT_ALL += kit-go-lint
FMT_ALL += kit-go-fmt

# Empty by default: no package is a DB package. A repo sets its helper's import path(s), space separated; a package
# importing one runs without -short.
KIT_GO_DB_IMPORTS ?=
export KIT_GO_DB_IMPORTS
# FB-1193: the build tags of each Go test tier (comma or space list; the full tier always adds `slow`). Empty = none. A project sets them in its
# own Makefile, after the `-include mk/*.mk` line (`KIT_GO_TEST_TAGS_FULL = integration`), never in the managed .devkit/config.env: a set line
# there conflicts on `devkit update --apply`. `?=` keeps an environment or command-line value; `export` hands the deferred value to go-test.sh.
KIT_GO_TEST_TAGS_LIGHT ?=
KIT_GO_TEST_TAGS_FULL ?=
export KIT_GO_TEST_TAGS_LIGHT
export KIT_GO_TEST_TAGS_FULL
# Runs of each test in the flaky goal.
COUNT ?= 3

# kit-go-test is no CHECK_EXTRA item: .devkit/check-map.d/go.map registers it as a test gate and as the stack of the unit driver
# (`@units kit-go-test scripts/kit/go-test.sh`), so the recipe is the driver: a unit is a package dir; the driver selects the packages
# whose inputs changed (or every one without KIT_CHECK_PATHS), runs the ones its store does not hold green, and writes one row per unit.
# `make check` is the light tier (-short, the light build tags), `make check-full` the full one (no -short, tag `slow`, KIT_GO_TEST_TAGS_FULL
# from the Makefile variable above). `make test TESTS=<pkg> RUN=TestX` runs one package, and fails when TestX never ran. The test cache stays on:
# never -count (a cache off is 3x slower), the flaky goal below is the one place that repeats a test.
# KIT_GO_TEST_FLAGS adds `go test` flags, KIT_GO_TEST_RUNNER a command prefix that runs before `go test` (FB-957); a stored result is reused only under the same flags and runner (FB-1303); the test
# temp dir is made by the script under the same <base> as GOTMPDIR (R-236, R-273), so GOTMPDIR above stays the one every other go command uses.
kit-go-test: ## Go tests through the unit driver: the package dirs whose inputs changed (KIT_CHECK_PATHS), -short in the light tier; KIT_GO_TEST_FLAGS, KIT_GO_TEST_RUNNER
	@python3 scripts/kit/check-units.py go

kit-go-test-flaky: ## Flaky run: the named packages (TESTS=<dir>... or KIT_CHECK_PATHS), no -short, COUNT=<n> (default 3) runs of each test in one call, never their importers; judged by config/go-flaky.ledger
	@KIT_GO_FLAKY_COUNT="$(COUNT)" bash scripts/kit/go-test.sh flaky $(TESTS)

kit-go-fmt: ## gofmt -w every tracked .go file (not testdata, vendor, _x or .x)
	@set -f; files="$$(git ls-files -co --exclude-standard -- '*.go' | awk '!/(^|\/)(testdata|vendor|_[^\/]*|\.[^\/]+)\//')"; \
	present=""; for f in $$files; do [ -f "$$f" ] && present="$$present $$f"; done; \
	if [ -n "$$present" ]; then gofmt -w $$present; fi

# DK-071: golangci-lint compiles every package of the module into the shared GOCACHE, and without -trimpath each compile is keyed on the
# work tree path, so every worktree adds its own copy. A probe of one 40-package module from two paths into one empty GOCACHE grew it 51%
# (149 -> 225 MB); with -trimpath 0.2%. go-test.sh does the same for `go test` (R-270). Findings keep their absolute paths (go-ceiling.sh reads them).
# FB-932 (R-259): with config/go-lint.ceiling (project-owned, written once by kit-go-baseline) a run does not exit on findings: every module
# prints absolute paths, the lines become `<path>\t<linter>\t<line>` and scripts/kit/go-ceiling.sh judges them once, per package and linter.
# Any non-zero exit of the tool is then a failure to run (exit 2), never judged. Without the file every finding is red, as before.
# A build error prints as `<path>:1: : # <pkg>` and the linter `(typecheck)` ends the line after it, so a line with no linter takes the
# first following line that ends in one (else `unknown`).
kit-go-lint: ## gofmt, go mod tidy (offline first; a module missing from the cache is fetched once) and the golangci-lint v2 baseline (.golangci.yml: govet, errorlint, wrapcheck, nolintlint) in every go.mod dir, the changed packages in a light run; config/go-lint.ceiling counts old findings
	@set -f; \
	mods="$$(git ls-files -co --exclude-standard -- go.mod '*/go.mod' | sort -u)"; \
	if [ -z "$$mods" ]; then echo "kit-go-lint: no go.mod: nothing to lint"; exit 0; fi; \
	dirs=""; \
	for gomod in $$mods; do \
		[ -f "$$gomod" ] || continue; \
		dir="$$(dirname "$$gomod")"; \
		if [ "$$dir" != . ]; then case "/$$dir/" in */testdata/*|*/vendor/*|*/_*|*/.?*) continue ;; esac; fi; \
		dirs="$$dirs$${dirs:+ }$$dir"; \
	done; \
	if [ -z "$$dirs" ]; then echo "kit-go-lint: no go.mod: nothing to lint"; exit 0; fi; \
	narrow=""; \
	if [ -n "$$KIT_CHECK_PATHS" ] && [ "$${KIT_TIER:-light}" != full ] && [ ! -f config/go-lint.ceiling ] && [ "$$KIT_GO_CEILING" != write ] \
	   && ! printf '%s\n' $$KIT_CHECK_PATHS | grep -qx '.golangci.yml'; then narrow=1; fi; \
	if [ -n "$$narrow" ]; then \
		targets="$$(printf '%s\n' $$KIT_CHECK_PATHS | awk -v mods="$$dirs" 'BEGIN { n = split(mods, w, " "); for (i = 1; i <= n; i++) own[w[i]] = 1 } \
			!/(\.go|(^|\/)go\.(mod|sum))$$/ || ("/" $$0) ~ /\/(testdata|vendor)\// { next } \
			{ d = $$0; sub(/\/?[^\/]*$$/, "", d); if (d == "") d = "."; o = d; \
			  while (!(o in own)) { if (o == ".") { o = ""; break } if (o ~ /\//) sub(/\/[^\/]*$$/, "", o); else o = "." } \
			  if (o == "") next; \
			  if ($$0 ~ /go\.(mod|sum)$$/) a = "./..."; else if (d == o) a = "."; else a = "./" (o == "." ? d : substr(d, length(o) + 2)); \
			  if (!((o SUBSEP a) in seen)) { seen[o SUBSEP a] = 1; print o "\t" a } }')"; \
		files="$$(printf '%s\n' $$KIT_CHECK_PATHS | awk '/\.go$$/ && !/(^|\/)(testdata|vendor|_[^\/]*|\.[^\/]+)\//')"; \
	else \
		targets="$$(for d in $$dirs; do printf '%s\t./...\n' "$$d"; done)"; \
		files="$$(git ls-files -co --exclude-standard -- '*.go' | awk '!/(^|\/)(testdata|vendor|_[^\/]*|\.[^\/]+)\//')"; \
	fi; \
	if [ -z "$$targets" ]; then echo "kit-go-lint: no Go package among the changed paths"; exit 0; fi; \
	command -v gofmt >/dev/null 2>&1 || { echo "kit-go-lint: gofmt is not on PATH: install Go" >&2; exit 2; }; \
	errf="$$(mktemp "$${TMPDIR:-/tmp}/kit-go-lint.XXXXXX")" || exit 2; trap 'rm -f "$$errf"' EXIT; \
	fail=0; present=""; \
	for f in $$files; do [ -f "$$f" ] && present="$$present $$f"; done; \
	if [ -n "$$present" ]; then \
		bad="$$(gofmt -l $$present 2>&1)"; \
		if [ -n "$$bad" ]; then echo "kit-go-lint: not gofmt-formatted (make fmt):" >&2; printf '%s\n' "$$bad" >&2; fail=1; fi; \
	fi; \
	for dir in $$(printf '%s\n' "$$targets" | cut -f1 | sort -u); do \
		out="$$(cd "$$dir" && GOPROXY=off go mod tidy -diff 2>"$$errf")"; r=$$?; \
		if [ "$$r" -ne 0 ] && [ -z "$$out" ]; then \
			out="$$(cd "$$dir" && go mod tidy -diff 2>"$$errf")"; r=$$?; \
		fi; \
		if [ "$$r" -ne 0 ] && [ -n "$$out" ]; then \
			echo "kit-go-lint: $$dir: go.mod or go.sum is not tidy (go mod tidy):" >&2; printf '%s\n' "$$out" | head -n 20 >&2; fail=1; \
		elif [ "$$r" -ne 0 ]; then \
			echo "kit-go-lint: go mod tidy -diff failed in $$dir (offline, then online; a tool failure, not a verdict):" >&2; head -n 10 "$$errf" >&2; exit 2; \
		fi; \
	done; \
	if [ "$$fail" -ne 0 ]; then exit 1; fi; \
	bin="$$(command -v "$(GOLANGCI_LINT)" 2>/dev/null)"; \
	if [ -z "$$bin" ]; then \
		echo "kit-go-lint: $(GOLANGCI_LINT) is not installed: not a pass. Install v2: brew install golangci-lint, or go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest" >&2; exit 2; fi; \
	ver="$$("$$bin" --version 2>&1)"; \
	case "$$ver" in *"version 2."*) ;; *) \
		echo "kit-go-lint: $$bin is not golangci-lint v2 (.golangci.yml is version 2; pin: config/versions.toml [template] in devkit): upgrade it" >&2; exit 2 ;; esac; \
	[ -f .golangci.yml ] || { echo "kit-go-lint: .golangci.yml is missing: run devkit update --apply" >&2; exit 2; }; \
	cfg="$(CURDIR)/.golangci.yml"; \
	GOFLAGS="$${GOFLAGS:+$$GOFLAGS }-trimpath"; export GOFLAGS; \
	ceil=""; if [ -f config/go-lint.ceiling ] || [ "$$KIT_GO_CEILING" = write ]; then ceil=1; fi; \
	wflag=""; if [ "$$KIT_GO_CEILING" = write ]; then wflag=--write; fi; \
	top="$$(git rev-parse --show-toplevel 2>/dev/null || pwd)"; found=.artifacts/tmp/go-lint-findings.tsv; \
	if [ -n "$$ceil" ]; then mkdir -p .artifacts/tmp; : > "$$found"; fi; \
	rc=0; \
	for dir in $$(printf '%s\n' "$$targets" | cut -f1 | sort -u); do \
		args=""; \
		for a in $$(printf '%s\n' "$$targets" | awk -F'\t' -v d="$$dir" '$$1 == d { print $$2 }'); do \
			if [ "$$a" = ./... ] || [ -d "$$dir/$$a" ]; then args="$$args $$a"; fi; \
		done; \
		[ -n "$$args" ] || continue; \
		echo "kit-go-lint: $$dir"; \
		if [ -z "$$ceil" ]; then \
			(cd "$$dir" && "$$bin" run --allow-serial-runners --config "$$cfg" $$args) || rc=1; \
		else \
			o="$$(cd "$$dir" && "$$bin" run --allow-serial-runners --config "$$cfg" --issues-exit-code=0 --path-mode abs --output.text.print-issued-lines=false $$args)"; r=$$?; \
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

# A dev goal, in no check map: kit-go-lint keeps its GOPROXY=off tidy check and stays the gate. The GOTMPDIR and GOFLAGS are the ones exported above.
# PKG is quoted, so a value with spaces stays one word; a flag such as -u still reaches `go get` as its own argument.
kit-go-add: ## Add a Go dependency: make kit-go-add PKG=<module>[@version] [DIR=<go.mod dir>] (go get, go mod tidy, then the go.mod/go.sum diff)
	@if [ -z "$$GO_ADD_PKG" ]; then echo "kit-go-add: PKG=<module>[@version] is required" >&2; exit 2; fi; \
	dir="$$GO_ADD_DIR"; \
	if [ ! -f "$$dir/go.mod" ]; then echo "kit-go-add: no go.mod in $$dir" >&2; exit 1; fi; \
	command -v go >/dev/null 2>&1 || { echo "kit-go-add: go is not on PATH: install Go" >&2; exit 2; }; \
	cd "$$dir" && go get "$$GO_ADD_PKG" && go mod tidy && { git diff --stat -- go.mod go.sum || true; }

cache-trim: ## Trim Go caches now (age + size caps)
	@bash scripts/kit/cache-trim.sh --force

cache-trim-auto: ## Trim Go caches at most once per 24h
	@bash scripts/kit/cache-trim.sh --auto

cache-status: ## Show Go cache sizes
	@for d in $(KIT_CACHE_DIRS); do \
		printf '%-8s %s\n' "$$(du -sh "$$d" 2>/dev/null | awk '{print $$1}')" "$$d"; \
	done
