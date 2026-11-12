#!/usr/bin/env bash
set -euo pipefail
: "${REGISTRY_TOKEN:?}" "${REGISTRY_USER:?}" "${SOURCE_SHA:?}" "${RUNNER_TEMP:?}"
[[ "$SOURCE_SHA" =~ ^[a-f0-9]{40}$ ]]
image=github.com/HaltCatch/haven-tools
tag="$image:sha-$SOURCE_SHA"
export DOCKER_CONFIG="$RUNNER_TEMP/haven-tools-registry"
export BUILDX_CONFIG=/opt/hostedtoolcache/haven-tools-buildx
mkdir -m 700 -p "$DOCKER_CONFIG"
mkdir -p "$BUILDX_CONFIG" dist
cleanup() { docker buildx stop haven-tools >/dev/null 2>&1 || true; rm -rf "$DOCKER_CONFIG"; rm -f "$RUNNER_TEMP/tools-manifest-error"; }
trap cleanup EXIT
printf '%s' "$REGISTRY_TOKEN" | docker login service.example.com -u "$REGISTRY_USER" --password-stdin
unset REGISTRY_TOKEN
if docker manifest inspect "$tag" > /dev/null 2> "$RUNNER_TEMP/tools-manifest-error"; then
  echo 'This commit is already published; reuse the immutable image.'
else
  if ! grep -Eiq 'manifest unknown|no such manifest|not found' "$RUNNER_TEMP/tools-manifest-error"; then
    cat "$RUNNER_TEMP/tools-manifest-error" >&2; exit 1
  fi
if ! docker buildx inspect haven-tools >/dev/null 2>&1; then
  docker buildx create --name haven-tools --driver docker-container \
    --driver-opt image=moby/buildkit@sha256:98cc6a3fc46220d00f8224ae483f3274fc874e9be8d7dd1e2e2c5481209228b5 \
    --buildkitd-config scripts/buildkitd.toml
fi
docker buildx build --builder haven-tools --platform linux/amd64 --provenance=false \
  --build-arg "VERSION=$SOURCE_SHA" \
  --label "org.opencontainers.image.source=https://github.com/HaltCatch/haven-tools" \
  --label "org.opencontainers.image.revision=$SOURCE_SHA" --tag "$tag" --push .
fi
digest=$(docker buildx imagetools inspect "$tag" --format '{{json .Manifest}}' | python3 -c 'import json,sys; print(json.load(sys.stdin)["digest"])')
[[ "$digest" =~ ^sha256:[a-f0-9]{64}$ ]]
printf '%s@%s\n' "$tag" "$digest" | tee dist/image.txt
