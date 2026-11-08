# managed by devkit — do not edit; fix upstream in ~/Developer/projects/devkit
# Queue shim (standards/make.md §2, R-141): GNU make reads this before Makefile. At MAKELEVEL 0 a command-line goal is
# classed by name: a heavy goal with the host queue present, no HEAVY_RUN_SLOT and no -n/-q/-t queues the whole
# invocation once through heavy_run (Makefile is not read here: the inner make reads it inside the hold); light and
# unclassed goals run Makefile at HEAVY_RUN_CORES=2 GOMAXPROCS=2; dev goals run it untouched. A sub-make only reads
# Makefile: it inherits its parent's decision. Sizes (queue_cores, a goal=cores map over it; the largest goal asked
# wins) and the extra heavy names come from .devkit/config.env (needs make >= 4.2); a command-line or environment value
# wins; `KIT_LIGHT_TARGETS` takes goals out of the heavy class (a name also heavy stays heavy). No `$(shell)`. Your rules go in Makefile, never here.
# Without the host queue (no heavy_run.py) it is inert: nothing queued, nothing exported. `kit_conf` is the one values
# file the body reads: .devkit/config.env here, config/ops.env in a copy made by /devkit:apply-ops.
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
kit_dev_names := dev-up dev-down dev-status run serve
kit_light := $(filter-out $(kit_list),check lint fmt vet)
kit_lit := $(filter-out $(filter $(kit_list) $(kit_docker),$(MAKECMDGOALS)),$(filter $(kit_lights),$(MAKECMDGOALS)))
kit_heavy := $(filter-out $(kit_light) $(kit_dev_names) $(kit_lit),$(filter check-% %-check test test-% e2e% %-gate build build-% \
  %-build image image-% %-image $(kit_list) $(kit_docker),$(MAKECMDGOALS)))
kit_dev := $(filter $(kit_dev_names),$(MAKECMDGOALS))
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
kit_outer := $(if $(kit_heavy),$(if $(HEAVY_RUN_SLOT)$(kit_dry),,$(KIT_HEAVY_RUN)))
endif

ifneq ($(kit_outer),)
kit_first := $(firstword $(MAKECMDGOALS))
.PHONY: $(MAKECMDGOALS)
$(kit_first):
	+@python3 $(KIT_HEAVY_RUN) run --label $(if $(word 2,$(MAKECMDGOALS)),multi-$(words $(MAKECMDGOALS)):)$(firstword $(kit_heavy)) --cores $(kit_cores) -- $(MAKE) --no-print-directory $(MAKECMDGOALS)
$(filter-out $(kit_first),$(MAKECMDGOALS)): $(kit_first) ;
else
ifeq ($(MAKELEVEL)$(HEAVY_RUN_SLOT)$(kit_dev)$(if $(KIT_HEAVY_RUN),,x),0)
export HEAVY_RUN_CORES := $(if $(kit_heavy),$(kit_cores),2)
export GOMAXPROCS := $(HEAVY_RUN_CORES)
endif
include Makefile
endif
