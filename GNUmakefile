# managed by devkit — do not edit; fix upstream in ~/Developer/projects/devkit
# Queue shim (standards/make.md §2, R-141): GNU make reads this before Makefile. At MAKELEVEL 0 a command-line goal is
# classed by name: a heavy goal with the host queue present, no HEAVY_RUN_SLOT and no -n/-q/-t queues the whole
# invocation once through heavy_run (Makefile is not read here: the inner make reads it inside the hold); a light or
# unclassed goal (with a host queue, no slot, no -n/-q/-t, no dev goal) goes through `heavy_run run --no-wait --cores 2`: a
# holder at once, never waited, so the queue counts every parallel close (DK-063; the project is heavy_run's own default,
# as for a heavy goal) and runs at HEAVY_RUN_CORES=2 GOMAXPROCS=2; dev goals run Makefile untouched. A sub-make only reads
# Makefile: it inherits its parent's decision. Sizes (queue_cores, a goal=cores map over it; the largest goal asked
# wins) and the extra heavy names come from .devkit/config.env (needs make >= 4.2); a command-line or environment value
# wins; `KIT_LIGHT_TARGETS` takes goals out of the heavy class (a name also heavy stays heavy). Dev goals are dev-up dev-down dev-status
# run serve, the patterns dev-% %-run serve-% run-% watch% and the names of `KIT_DEV_TARGETS` (read as KIT_LIGHT_TARGETS is; a name
# also heavy stays heavy): they never take a slot (FB-1087). No `$(shell)`. Your rules go in Makefile, never here.
# A project's own docker goals are the `docker_targets` answer, never a Makefile variable: this shim classes before Makefile is read (FB-733).
# Without the host queue (no heavy_run.py) it is inert: nothing queued, nothing exported. `kit_conf` is the one values
# file the body reads: .devkit/config.env here, config/ops.env in a copy made by /devkit:apply-ops.
# After Makefile is read, the branch that reads it adds the vocabulary's prerequisite-only lines (standards/make.md §1): `kit-verbs`
# (scripts/kit/check-verbs.py) runs before check, check-full and (docker module on: mk/kit-docker.mk) check-docker whatever recipe the project gave them, and test, lint
# and fmt exist in every repo (`test` runs the registered test targets, `lint` and `fmt` the project's LINT_ALL and FMT_ALL).
# The first make inside a hold of a gate goal (check, check-full, check-docker, sweep) on make >= 4.4 runs kit-verbs (and,
# for check-docker, kit-docker-need) first, then the goals at the grant (`-j$(HEAVY_RUN_CORES) -O -k`; KIT_JOBS marks that
# make, whose kit-verbs already ran); the hold's recipe drops a typed -j and jobserver, so the grant wins.
# Order-only lines (only when a gate goal is asked: a goal typed by hand keeps its order) keep the seed's order: kit-verbs first, then (check-docker) kit-docker-need, the full gate's items,
# CHECK_DOCKER (each item after them but the leak gate, which waits for CHECK_DOCKER: `| check-full` would be a cycle).
# Those lines are read only in a kit render (`.devkit/config.env` exists): a copy made by /devkit:apply-ops has no kit scripts, so no vocabulary.
kit_conf := .devkit/config.env
ifeq ($(MAKELEVEL),0)
kit_comma := ,
ifneq ($(filter 0.% 1.% 2.% 3.% 4.0 4.0.% 4.1 4.1.%,$(MAKE_VERSION)),)
$(warning GNU make $(MAKE_VERSION) is below 4.2: $(kit_conf) is not read, the queue defaults apply)
else
kit_env := $(if $(wildcard $(kit_conf)),$(file <$(kit_conf)))
endif
kit_val = $(patsubst $(1)=%,%,$(filter $(1)=%,$(kit_env)))
kit_cores := $(or $(KIT_QUEUE_CORES),$(call kit_val,KIT_QUEUE_CORES),6)
kit_cores_docker := $(or $(KIT_QUEUE_CORES_DOCKER),$(call kit_val,KIT_QUEUE_CORES_DOCKER),8)
kit_list := $(subst $(kit_comma), ,$(or $(KIT_HEAVY_TARGETS),$(call kit_val,KIT_HEAVY_TARGETS)))
kit_lights := $(subst $(kit_comma), ,$(or $(KIT_LIGHT_TARGETS),$(call kit_val,KIT_LIGHT_TARGETS)))
kit_docker := check-docker $(subst $(kit_comma), ,$(or $(KIT_DOCKER_TARGETS),$(call kit_val,KIT_DOCKER_TARGETS)))
kit_dev_names := dev-up dev-down dev-status run serve dev-% %-run serve-% run-% watch%
kit_devs := $(subst $(kit_comma), ,$(or $(KIT_DEV_TARGETS),$(call kit_val,KIT_DEV_TARGETS)))
kit_light := $(filter-out $(kit_list),check lint fmt vet)
kit_lit := $(filter-out $(filter $(kit_list) $(kit_docker),$(MAKECMDGOALS)),$(filter $(kit_lights),$(MAKECMDGOALS)))
kit_dev := $(filter-out $(filter $(kit_list) $(kit_docker),$(MAKECMDGOALS)),$(filter $(kit_dev_names) $(kit_devs),$(MAKECMDGOALS)))
kit_heavy := $(filter-out $(kit_light) $(kit_dev) $(kit_lit),$(filter check-% %-check test test-% e2e% %-gate build build-% \
  %-build image image-% %-image sweep $(kit_list) $(kit_docker),$(MAKECMDGOALS)))
ifneq ($(kit_dev),)
ifneq ($(kit_heavy),)
$(error $(firstword $(kit_dev)) is a dev goal and $(firstword $(kit_heavy)) is heavy: run them apart)
endif
endif
kit_nums := 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30 31 32
kit_max = $(if $(filter $(2),$(wordlist 1,$(1),$(kit_nums))),$(1),$(2))
kit_maxof = $(if $(word 2,$(1)),$(call kit_maxof,$(call kit_max,$(word 1,$(1)),$(word 2,$(1))) $(wordlist 3,$(words $(1)),$(1))),$(1))
kit_map := $(subst $(kit_comma), ,$(or $(KIT_QUEUE_CORES_MAP),$(call kit_val,KIT_QUEUE_CORES_MAP)))
kit_goal_cores = $(or $(if $(KIT_QUEUE_CORES),,$(firstword $(patsubst $(1)=%,%,$(filter $(1)=%,$(kit_map))))),$(if $(filter $(1),$(kit_docker)),$(call kit_max,$(kit_cores),$(kit_cores_docker)),$(kit_cores)))
ifneq ($(kit_heavy),)
kit_cores := $(call kit_maxof,$(foreach g,$(kit_heavy),$(call kit_goal_cores,$(g))))
endif
kit_dry := $(findstring n,$(firstword -$(MAKEFLAGS)))$(findstring q,$(firstword -$(MAKEFLAGS)))$(findstring t,$(firstword -$(MAKEFLAGS)))
KIT_HEAVY_RUN := $(wildcard $(or $(DEVKIT_HOST),$(HOME)/Developer/_ops)/scripts/heavy_run.py)
kit_outer := $(if $(HEAVY_RUN_SLOT)$(kit_dry),,$(if $(kit_heavy),$(KIT_HEAVY_RUN),$(if $(kit_dev)$(if $(MAKECMDGOALS),,x),,$(KIT_HEAVY_RUN))))
kit_wait := $(if $(kit_heavy),,--no-wait )
kit_slot_cores := $(if $(kit_heavy),$(kit_cores),2)
endif

ifneq ($(kit_outer),)
kit_first := $(firstword $(MAKECMDGOALS))
.PHONY: $(MAKECMDGOALS)
# The hold's make gets the flags without -j and the jobserver and the command-line variables (the text after ` -- `) as the shell
# holds them in MAKEFLAGS: make never expands or quotes them here (FB-1242: a `'` broke the line, a `-jx` inside a value was dropped).
kit_unjob = m=" $$MAKEFLAGS"; f=$${m%% -- *}; t=$${m\#"$$f"}; n=; set -f; for w in $$f; do case $$w in -j*|--jobserver-auth=*) ;; *) n="$${n:+$$n }$$w";; esac; done; set +f
$(kit_first):
	+@$(kit_unjob); MAKEFLAGS="$$n$$t" python3 $(KIT_HEAVY_RUN) run $(kit_wait)--label $(if $(word 2,$(MAKECMDGOALS)),multi-$(words $(MAKECMDGOALS)):)$(firstword $(kit_heavy) $(MAKECMDGOALS)) --cores $(kit_slot_cores) -- $(MAKE) --no-print-directory $(MAKECMDGOALS)
$(filter-out $(kit_first),$(MAKECMDGOALS)): $(kit_first) ;
else
ifeq ($(MAKELEVEL)$(HEAVY_RUN_SLOT)$(kit_dev)$(if $(KIT_HEAVY_RUN),,x),0)
export HEAVY_RUN_CORES := $(if $(kit_heavy),$(kit_cores),2)
export GOMAXPROCS := $(HEAVY_RUN_CORES)
endif
kit_pre44 := $(filter 0.% 1.% 2.% 3.% 4.0 4.0.% 4.1 4.1.% 4.2 4.2.% 4.3 4.3.%,$(MAKE_VERSION))
kit_grant := $(if $(HEAVY_RUN_SLOT),$(if $(HEAVY_RUN_CORES),$(if $(KIT_JOBS)$(kit_pre44),,$(filter check check-full check-docker sweep,$(MAKECMDGOALS)))))
ifneq ($(if $(kit_grant),$(wildcard .devkit/config.env)),)
# The grant in two phases (LR-8): the preflights one make each, the first red ends the run before any other target starts
# (a `.WAIT` group's members have no order-only line), then the gate at the grant; a typed -jN at level 0 stays its own.
kit_first := $(firstword $(MAKECMDGOALS))
.PHONY: $(MAKECMDGOALS)
$(kit_first):
	+@$(MAKE) --no-print-directory kit-verbs KIT_GOALS='$(MAKECMDGOALS)'$(if $(wildcard mk/kit-docker.mk),$(if $(filter check-docker,$(MAKECMDGOALS)), && $(MAKE) --no-print-directory kit-docker-need)) && KIT_JOBS=$(HEAVY_RUN_CORES) $(MAKE) --no-print-directory $(if $(filter --jobserver-auth=%,$(MAKEFLAGS)),,-j$(HEAVY_RUN_CORES)) -O -k $(MAKECMDGOALS)
$(filter-out $(kit_first),$(MAKECMDGOALS)): $(kit_first) ;
else
ifneq ($(kit_grant),)
MAKEFLAGS += -j$(HEAVY_RUN_CORES) -O -k
export KIT_JOBS := $(HEAVY_RUN_CORES)
endif
include Makefile
ifneq ($(wildcard .devkit/config.env),)
.PHONY: kit-verbs test lint fmt
kit-verbs: export KIT_GOALS := $(MAKECMDGOALS)
kit-verbs: export KIT_MAKE := $(MAKE)
kit-verbs: ; @$(if $(KIT_JOBS),:,python3 scripts/kit/check-verbs.py)
check check-full $(if $(wildcard mk/kit-docker.mk),check-docker): kit-verbs
ifneq ($(filter check check-full check-docker sweep,$(MAKECMDGOALS)),)
$(filter-out .WAIT,$(CHECK_EXTRA) $(CHECK_FULL) $(CHECK_DOCKER)): | kit-verbs
ifneq ($(if $(wildcard mk/kit-docker.mk),$(filter check-docker,$(MAKECMDGOALS))),)
$(filter-out .WAIT,$(CHECK_EXTRA) $(CHECK_FULL)): | kit-docker-need
$(filter-out .WAIT,$(CHECK_DOCKER)): | $(filter-out .WAIT kit-leak-gate,$(CHECK_EXTRA) $(CHECK_FULL))
endif
endif
test: kit-test
lint: $(LINT_ALL)
fmt: $(FMT_ALL)
endif
endif
endif
