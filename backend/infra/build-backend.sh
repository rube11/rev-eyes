#!/usr/bin/env bash
set -euo pipefail
if [[ $# -ne 1 ]]; then echo 'usage: infra/build-backend.sh OUTPUT_BINARY' >&2; exit 2; fi
if [[ -z ${MOONSHINE_NATIVE_DIR:-} ]]; then
 CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o "$1" .
else
 if [[ $(uname -s) != Linux || $(uname -m) != x86_64 ]]; then echo 'Native build requires Linux x86_64' >&2; exit 2; fi
 runtime_dir=$(cd "$MOONSHINE_NATIVE_DIR" && pwd)
 test -f "$runtime_dir/lib/libmoonshine.so"
 test -f "$runtime_dir/lib/libonnxruntime.so.1"
 CGO_ENABLED=1 GOOS=linux GOARCH=amd64 \
 CGO_LDFLAGS="-L$runtime_dir/lib -Wl,-rpath,/opt/rev-eyes/moonshine/v0.1.5/lib" \
 go build -tags moonshine -trimpath -ldflags='-s -w' -o "$1" .
fi
