---
id: GF-003
title: Run make public-scan as a pre-push gate, not a habit
status: open
type: work
tier: 1
priority: p1
origin: '2026-11-08: four agentdocs doc commits naming a private repo reached public main; R-6 required the scan, nothing enforced it'
created: 2026-11-08
---
R-6 says `make public-scan` runs before every push. On 2026-11-08 a push of four doc commits ran without it, and a private repo name reached public `main` in docs and commit messages. The fix was a rebuild from the last clean commit plus a force-push by the owner. GitHub may keep the old SHAs reachable by URL; that is unverified.

Shape: a versioned pre-push hook through devkit's hook seam, or a `make push` wrapper that the README names as the only push path. It must refuse when the scan is red, and when it cannot run (exit 2, gh missing). The teeth: a fixture repo whose history names a denylisted word must be refused.
