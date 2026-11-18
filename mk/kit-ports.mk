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

# The host port registry tool. A PEP 723 script (pyyaml): it runs through `uv run --no-project --python <rp> --with <dep>
# python <tool>` (KIT_UV_ARGS below) with devkit's uv cache (bin/devkit's shebang), never a bare python3 (FB-653); uv absent
# => the recipe fails naming uv. Absent (a repo cloned to another machine) => a loud skip, never a failure.
KIT_PORTS_TOOL ?= $(HOME)/Developer/_ops/scripts/ports.py
# Where uv is looked for when it is not on PATH (`env -i zsh -c` has no /opt/homebrew/bin).
KIT_UV_DIRS ?= /opt/homebrew/bin /usr/local/bin $(HOME)/.local/bin
# devkit's own uv cache (the one `bin/devkit`'s shebang sets), never a second one.
KIT_UV_CACHE ?= $(HOME)/.devkit/cache/uv
# The uv arguments that run a host tool by its dependencies, not by its path (R-202): a script run by path keeps one
# environment per absolute script path, so every temp copy of a tool adds one to the cache; `--no-project --python
# <rp> --with <dep>` keys it by the dependency set. Shell text for a recipe (a recursive variable, expanded when the recipe
# runs, never at parse time): a command substitution that reads the PEP 723 header of the tool in $$t and prints
# `--python <requires-python> [--with <dep>]...` (requires-python as given, >=3.11 when absent; one --with per entry of
# `dependencies`, an inline or a multi-line array; `[]` or no header: none). A space inside an entry is dropped. Used as
# `t=<tool>; ua=$(KIT_UV_ARGS); uv run --quiet --no-project $$ua python "$$t" ...` (awk only: no host python3).
KIT_UV_ARGS ?= $$(awk ' \
/^\# \/\/\/ script[ \t]*$$/ && !done { inb=1; next } \
inb && /^\# \/\/\/[ \t]*$$/ { inb=0; done=1; next } \
inb { l=$$0; sub(/^\#[ \t]?/, "", l); blk=blk l "\n" } \
END { rp=">=3.11"; s=""; sd=0; n=split(blk, ln, "\n"); \
  for (i=1; i<=n; i++) { \
    if (ln[i] ~ /^requires-python[ \t]*=/) { v=ln[i]; sub(/^[^=]*=[ \t]*/, "", v); q=substr(v, 1, 1); \
      if (q=="\"" || q=="\047") { v=substr(v, 2); k=index(v, q); if (k>0) rp=substr(v, 1, k-1) } } \
    if (!sd && ln[i] ~ /^dependencies[ \t]*=/) { sd=1; s=ln[i]; sub(/^[^=]*=/, "", s); for (j=i+1; j<=n; j++) s=s "\n" ln[j] } \
  } \
  out="--python " rp; st=0; q=""; cm=0; cur=""; m=length(s); \
  for (c=1; c<=m; c++) { ch=substr(s, c, 1); \
    if (q!="") { if (ch==q) { q=""; gsub(/[ \t]/, "", cur); if (cur!="") out=out " --with " cur; cur="" } else cur=cur ch; continue } \
    if (cm) { if (ch=="\n") cm=0; continue } \
    if (ch=="\#") { cm=1; continue } \
    if (ch=="\"" || ch=="\047") { if (st) q=ch; continue } \
    if (ch=="[") { st=1; continue } \
    if (ch=="]" && st) break } \
  print out }' "$$t")

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
		t="$(KIT_PORTS_TOOL)"; uv=$$(command -v uv || for d in $(KIT_UV_DIRS); do [ -x "$$d/uv" ] && { echo "$$d/uv"; break; }; done); \
		[ -n "$$uv" ] || { echo "ports-check: uv not found (PATH, $(KIT_UV_DIRS)); $(KIT_PORTS_TOOL) runs through uv run" >&2; exit 1; }; \
		ua=$(KIT_UV_ARGS); set -f; \
		env -u VIRTUAL_ENV -u UV_PROJECT_ENVIRONMENT -u PYTHONPATH -u PYTHONHOME UV_CACHE_DIR="$(KIT_UV_CACHE)" "$$uv" run --quiet --no-project $$ua python "$$t" verify .; \
	else echo "ports-check: SKIPPED - $(KIT_PORTS_TOOL) not found (no host port registry on this machine); ports NOT verified"; fi
	@bash scripts/kit/ports-scan.sh
endif
