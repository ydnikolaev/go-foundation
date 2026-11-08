# Backlog

Deferred work, one file per item: `GF-NNN-kebab-slug.md`. No loose TODOs, no chat-only promises.
Create with `devkit rec backlog new "Title" [--epic <slug>] [--priority pN] [--origin "..."]`; `devkit rec index`
regenerates the index `backlog.index.yaml` (never edit it by hand) and `make check` lints the store through
`devkit rec check`. Closed items move to `archive/<year>/` with `devkit rec backlog archive`.

```markdown
---
id: GF-001
title: "Short imperative title"
status: open            # open | scheduled | done | dropped
priority: p2            # p1 | p2 | p3
epic: null              # slug of a work folder; required once status is scheduled
origin: "where it came from (ruling, review, session date)"
closes_when: "the observable check that proves it done"
created: 2026-01-31
# closed: 2026-02-14    # required when done or dropped, forbidden while open or scheduled
---

## Problem
## Desired
## Related
```
