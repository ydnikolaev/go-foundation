# managed by devkit — do not edit; fix upstream in ~/Developer/projects/devkit
# Ports: config/ports.env is the one place a host port is written (standards/ports.md). Read at parse time and
# exported, so every recipe and `docker compose` sees the same values. KEY=VALUE lines only, no trailing comments.
# The names to export are read from the file itself, so it does not matter who included it first (a project mk/*.mk that
# sorts before this one may `include config/ports.env`). A name is the part before `=` on a line, and it holds PORT. No
# `$(shell)` at parse time (make 4.4 expands every exported command-line variable for each one): `$(file <)` reads the
# file, which needs GNU make >= 4.2 (macOS /usr/bin/make is 3.81: use `brew install make`, then `gmake`).
ifneq ($(filter 3.% 4.0 4.0.% 4.1 4.1.%,$(MAKE_VERSION)),)
$(error mk/kit-ports.mk needs GNU make >= 4.2, this is $(MAKE_VERSION); on macOS `brew install make` and run `gmake`)
endif
define KIT_NL


endef
# one word per line (blanks inside a line become @@), then the name before `=` of each line
KIT_PORT_LINES := $(subst $(KIT_NL), ,$(subst $() ,@@,$(file <config/ports.env)))
# a leading `export ` is dropped before the name is taken (`export FOO_PORT=1` names FOO_PORT)
KIT_PORT_NAMES := $(foreach l,$(KIT_PORT_LINES),$(if $(findstring =,$l),$(subst @@,,$(subst :,,$(subst ?,,$(subst +,,$(patsubst export@@%,%,$(firstword $(subst =, ,$l)))))))))
KIT_HASH := \#
KIT_PORT_VARS := $(sort $(foreach n,$(KIT_PORT_NAMES),$(if $(findstring PORT,$n),$(if $(findstring $(KIT_HASH),$n),,$n))))
-include config/ports.env
ifneq ($(strip $(KIT_PORT_VARS)),)
export $(KIT_PORT_VARS)
endif

# The host port registry tool. A PEP 723 script (pyyaml): it runs through `uv run --script` with devkit's uv cache
# (bin/devkit's shebang), never a bare python3 (FB-653); uv absent => the recipe fails naming uv. Absent (a repo cloned to another machine) => a loud skip, never a failure.
KIT_PORTS_TOOL ?= $(HOME)/Developer/_ops/scripts/ports.py
# Where uv is looked for when it is not on PATH (`env -i zsh -c` has no /opt/homebrew/bin).
KIT_UV_DIRS ?= /opt/homebrew/bin /usr/local/bin $(HOME)/.local/bin
# devkit's own uv cache (the one `bin/devkit`'s shebang sets), never a second one.
KIT_UV_CACHE ?= $(HOME)/.devkit/cache/uv

CHECK_EXTRA += ports-check

.PHONY: ports-check

# No port declared (KIT_PORT_VARS empty): nothing to verify, so the registry tool is not called. The compose scan runs in
# both branches: an empty ports.env with a literal host port in compose is the worst case (scripts/kit/ports-scan.sh).
ports-check: ## Declared ports sit inside this repo's registry block; no literal host port in compose
ifeq ($(strip $(KIT_PORT_VARS)),)
	@echo "ports-check: no ports declared in config/ports.env; nothing to verify"
	@bash scripts/kit/ports-scan.sh
else
	@if [ -f "$(KIT_PORTS_TOOL)" ]; then \
		uv=$$(command -v uv || for d in $(KIT_UV_DIRS); do [ -x "$$d/uv" ] && { echo "$$d/uv"; break; }; done); \
		[ -n "$$uv" ] || { echo "ports-check: uv not found (PATH, $(KIT_UV_DIRS)); $(KIT_PORTS_TOOL) runs through uv run --script" >&2; exit 1; }; \
		env -u VIRTUAL_ENV -u UV_PROJECT_ENVIRONMENT -u PYTHONPATH -u PYTHONHOME UV_CACHE_DIR="$(KIT_UV_CACHE)" "$$uv" run --quiet --script "$(KIT_PORTS_TOOL)" verify .; \
	else echo "ports-check: SKIPPED - $(KIT_PORTS_TOOL) not found (no host port registry on this machine); ports NOT verified"; fi
	@bash scripts/kit/ports-scan.sh
endif
