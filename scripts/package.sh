#!/usr/bin/env bash
# Usage: scripts/package.sh GOOS GOARCH VERSION [OUTPUT_DIRECTORY]
set -euo pipefail

if [[ $# -lt 3 || $# -gt 4 ]]; then
  echo "Usage: $0 GOOS GOARCH VERSION [OUTPUT_DIRECTORY]" >&2
  exit 2
fi

target_os=$1
target_arch=$2
release_version=$3
output_dir=${4:-dist}
case "$target_os/$target_arch" in
  linux/amd64|linux/arm64|freebsd/amd64|freebsd/arm64|openbsd/amd64|openbsd/arm64|darwin/amd64|darwin/arm64) ;;
  *) echo "Unsupported target: $target_os/$target_arch" >&2; exit 2 ;;
esac
# Restrict metadata used in filenames and Go linker arguments. Development
# archives use 'dev'; release archives use semantic versions with optional v.
if [[ ${#release_version} -gt 80 ]] || [[ ! "$release_version" =~ ^(dev|v?[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?(\+[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?)$ ]]; then
  echo "Version must be dev or a semantic version (for example v0.1.0)." >&2
  exit 2
fi

repo_root=$(cd "$(dirname "$0")/.." && pwd)
cd "$repo_root"
mkdir -p "$output_dir"
output_dir=$(cd "$output_dir" && pwd)
stage=$(mktemp -d "${TMPDIR:-/tmp}/tuigram-package.XXXXXXXX")
trap 'rm -rf -- "$stage"' EXIT
build_commit=$(git rev-parse --verify HEAD)
build_date=$(git show -s --format=%cI HEAD)
archive_name="tuigram_${release_version}_${target_os}_${target_arch}"
mkdir "$stage/$archive_name"

CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" go build \
  -mod=readonly -trimpath -buildvcs=false \
  -ldflags "-s -w -X main.version=$release_version -X main.commit=$build_commit -X main.buildDate=$build_date" \
  -o "$stage/$archive_name/tuigram" ./cmd/tuigram
cp LICENSE README.md CHANGELOG.md SECURITY.md "$stage/$archive_name/"
cp internal/tgcalls/LICENSE "$stage/$archive_name/LICENSE-gotd-calls"
cp -R docs "$stage/$archive_name/"
COPYFILE_DISABLE=1 tar -czf "$output_dir/$archive_name.tar.gz" -C "$stage" "$archive_name"
echo "$output_dir/$archive_name.tar.gz"
