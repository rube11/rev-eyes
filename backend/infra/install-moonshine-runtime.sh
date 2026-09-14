#!/usr/bin/env bash
set -euo pipefail
# Called on the deployment host. Never modify files mapped by a running backend.
if [[ $# -ne 2 ]]; then
  echo 'usage: install-moonshine-runtime.sh ARCHIVE VERSION_DIRECTORY' >&2
  exit 2
fi
archive=$1
destination=$2
mkdir -p "$(dirname "$destination")"
staging=$(mktemp -d "${destination}.staging.XXXXXX")
trap 'rm -rf -- "$staging"' EXIT
tar -xzf "$archive" -C "$staging"
test -r "$staging/lib/libmoonshine.so"
test -r "$staging/lib/libonnxruntime.so.1"
test -r "$staging/model/streaming_config.json"
chmod 0755 "$staging"
if [[ -e $destination ]]; then
  if ! diff -qr "$staging" "$destination" >/dev/null; then
    echo 'Installed runtime differs; use a new version directory instead of overwriting it.' >&2
    exit 1
  fi
else
  mv -T "$staging" "$destination"
fi
