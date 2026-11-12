#!/usr/bin/env bash
set -euo pipefail
: "${GITHUB_ENV:?}" "${RUNNER_TEMP:?}"
# PRs do not write the main branch's native dependency/compiler caches.
cache_root=/opt/hostedtoolcache/haven-tools
if [[ "${CI_EVENT:-}" == pull_request ]]; then cache_root="$RUNNER_TEMP/haven-tools"; fi
mkdir -p "$cache_root/go/bin" "$cache_root/npm"
{
  printf 'GOMODCACHE=%s/go/mod\nGOCACHE=%s/go/build\nGOBIN=%s/go/bin\n' "$cache_root" "$cache_root" "$cache_root"
} >> "$GITHUB_ENV"
# Exact lockfile + runtime identity. npm performs integrity verification on a miss;
# a hit reuses the expanded immutable installation without another unpack/copy.
key=$( { sha256sum package.json package-lock.json; node --version; uname -sm; } | sha256sum | cut -d' ' -f1)
deps="$cache_root/node/$key"
mkdir -p "$cache_root/node"
exec 9>"$cache_root/node.lock"
flock 9
if [[ ! -f "$deps/ready" ]]; then
  tmp=$(mktemp -d "$cache_root/node/.install.XXXXXX")
  trap 'rm -rf "$tmp"' EXIT
  cp package.json package-lock.json "$tmp/"
  npm ci --prefix "$tmp" --cache "$cache_root/npm" --prefer-offline --ignore-scripts --no-audit --no-fund
  touch "$tmp/ready"
  mv "$tmp" "$deps"
  trap - EXIT
  echo 'npm expanded dependency cache: miss'
else
  echo 'npm expanded dependency cache: hit'
fi
ln -s "$deps/node_modules" node_modules
# Keep three lock identities; the dedicated runner executes one job at a time.
touch "$deps/ready"
python3 - "$cache_root/node" <<'PY'
from pathlib import Path
import shutil,sys
entries=sorted((p for p in Path(sys.argv[1]).iterdir() if (p/'ready').is_file()),key=lambda p:(p/'ready').stat().st_mtime,reverse=True)
for p in entries[3:]: shutil.rmtree(p)
PY
