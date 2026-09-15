#!/usr/bin/env bash
set -euo pipefail
script_dir=$(cd "$(dirname "$0")" && pwd)
fixture=$(mktemp -d)
trap 'rm -rf -- "$fixture"' EXIT
mkdir -p "$fixture/source/lib" "$fixture/source/model"
printf library > "$fixture/source/lib/libmoonshine.so"
printf runtime > "$fixture/source/lib/libonnxruntime.so.1"
printf '{}' > "$fixture/source/model/streaming_config.json"
tar -czf "$fixture/runtime.tgz" -C "$fixture/source" lib model
bash "$script_dir/install-moonshine-runtime.sh" "$fixture/runtime.tgz" "$fixture/installed"
test "$(stat -c %a "$fixture/installed")" = 755
inode=$(stat -c %i "$fixture/installed/lib/libmoonshine.so")
bash "$script_dir/install-moonshine-runtime.sh" "$fixture/runtime.tgz" "$fixture/installed"
test "$inode" = "$(stat -c %i "$fixture/installed/lib/libmoonshine.so")"
printf changed > "$fixture/source/lib/libmoonshine.so"
tar -czf "$fixture/runtime.tgz" -C "$fixture/source" lib model
if bash "$script_dir/install-moonshine-runtime.sh" "$fixture/runtime.tgz" "$fixture/installed" 2>/dev/null; then
  echo 'Unexpectedly replaced an installed runtime' >&2
  exit 1
fi
test "$(cat "$fixture/installed/lib/libmoonshine.so")" = library
echo 'Native runtime installation and immutable redeployment checks passed.'
