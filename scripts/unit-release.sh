#!/usr/bin/env bash
# unit-release: tag one unit after a green full gate (R-2).
#   scripts/unit-release.sh <unit> <X.Y.Z>
# The unit lives in units/<unit>/ and the tag is units/<unit>/vX.Y.Z (the Go rule: the tag prefix is the module dir).
# Refuses: no such unit, a version that is not X.Y.Z, no units/<unit>/releasenotes/<X.Y.Z>.yaml, a dirty tree, an existing
# tag, a red `make check-full`. It never pushes: after `make public-scan` passes, the session pushes the tag (R-7), then `make consumers`.
set -euo pipefail
unit=${1:?usage: unit-release.sh <unit> <X.Y.Z>}
ver=${2:?usage: unit-release.sh <unit> <X.Y.Z>}
dir="units/$unit"
die() { echo "unit-release: $*" >&2; exit 1; }
[[ -f "$dir/unit.json" && -f "$dir/go.mod" ]] || die "$dir is not a unit (no unit.json/go.mod)"
[[ "$ver" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "$ver is not X.Y.Z"
[[ -f "$dir/releasenotes/$ver.yaml" ]] || die "no $dir/releasenotes/$ver.yaml: every release ships adoption notes"
major=${ver%%.*}
if (( major >= 2 )); then
  grep -qE "^module github.com/ydnikolaev/go-foundation/$dir/v$major\$" "$dir/go.mod" \
    || die "v$major needs the module path github.com/ydnikolaev/go-foundation/$dir/v$major"
fi
tag="$dir/v$ver"
[[ -z "$(git status --porcelain)" ]] || die "the tree is dirty; a tag names a commit, commit first"
if git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then die "tag $tag exists"; fi
make check-full || die "make check-full is red; no tag"
git tag -a "$tag" -m "$unit v$ver"
echo "unit-release: tagged $tag at $(git rev-parse --short HEAD); next: make public-scan && git push origin $tag && make consumers (R-7)"
