# managed by devkit — do not edit; fix upstream in ~/Developer/projects/devkit
# Module boundary gate: `make check` refuses an import edge that config/modules.ini does not declare (lint, then the edge
# walk, scripts/kit/modules.py check). A repo with no module declared pays one `modules: none declared` line.
# The name is unclassed (R-141, kit-select's pattern): it runs bare inside the light `check` and never queues; an owner may
# also type `make kit-modules`.
CHECK_EXTRA += kit-modules

.PHONY: kit-modules

kit-modules:
	@python3 scripts/kit/modules.py check
