#!/usr/bin/env bash
set -euo pipefail

binary="${1:-./bin/tuigram}"
temp_root="$(mktemp -d)"
trap 'rm -rf "$temp_root"' EXIT
# macOS /var and /tmp are symlinks; private storage deliberately uses real paths.
temp_root="$(cd "$temp_root" && pwd -P)"
chmod 700 "$temp_root"
export XDG_CONFIG_HOME="$temp_root/config"
export XDG_STATE_HOME="$temp_root/state"
export XDG_CACHE_HOME="$temp_root/cache"
unset TUIGRAM_API_ID TUIGRAM_API_HASH TUIGRAM_SESSION_PASSPHRASE

"$binary" --help > "$temp_root/help" 2>&1
"$binary" --version > "$temp_root/version"
"$binary" --demo --snapshot > "$temp_root/snapshot"
grep -q 'Saved Messages' "$temp_root/snapshot"
grep -q 'Welcome to tuigram' "$temp_root/snapshot"
test ! -e "$XDG_CONFIG_HOME"
test ! -e "$XDG_STATE_HOME"
test ! -e "$XDG_CACHE_HOME"
"$binary" config init
"$binary" cache stats
"$binary" cache clear
if "$binary" --snapshot > "$temp_root/error" 2>&1; then
  echo 'Expected --snapshot without --demo to fail' >&2
  exit 1
fi
echo 'CLI smoke checks passed.'
