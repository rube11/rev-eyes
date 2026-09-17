#!/usr/bin/env bash
set -euo pipefail
# Run from backend/. This prepares local artifacts; it does not deploy.
if [[ $# -ne 1 ]]; then echo 'usage: infra/prepare-moonshine.sh OUTPUT_DIRECTORY' >&2; exit 2; fi
if [[ $(uname -s) != Linux || $(uname -m) != x86_64 ]]; then echo 'Pinned bundle requires Linux x86_64' >&2; exit 2; fi
mkdir -p "$1"
runtime_dir=$(cd "$1" && pwd)
archive=$(mktemp)
trap 'rm -f "$archive"' EXIT
curl --fail --location --silent --show-error \
 https://github.com/moonshine-ai/moonshine/releases/download/v0.1.5/moonshine-voice-linux-x86_64.tar.gz \
 --output "$archive"
printf '%s  %s\n' 9c3a87fea93ff2ad957938868f95a0a366dce9ff8ad86bde6cdcf5a4cadb51df "$archive" | sha256sum --check --status
tar -xzf "$archive" -C "$runtime_dir" --strip-components=1
CGO_ENABLED=1 CGO_LDFLAGS="-L$runtime_dir/lib -Wl,-rpath,$runtime_dir/lib" \
 go run -tags moonshine ./cmd/moonshine-setup "$runtime_dir/model"
printf 'Prepared Moonshine v0.1.5 in %s\n' "$runtime_dir"
