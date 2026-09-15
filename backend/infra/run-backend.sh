#!/usr/bin/env bash
set -euo pipefail
# Run from backend/ after loading the normal backend environment.
if [[ ${SERVER_MOONSHINE_ENABLED:-true} == false ]]; then
  exec go run .
fi
runtime_dir=${MOONSHINE_NATIVE_DIR:-/tmp/rev-eyes-moonshine}
if [[ ! -f "$runtime_dir/model/streaming_config.json" ]]; then
  bash infra/prepare-moonshine.sh "$runtime_dir"
fi
runtime_dir=$(cd "$runtime_dir" && pwd)
export MOONSHINE_MODEL_DIR="$runtime_dir/model"
export LD_LIBRARY_PATH="$runtime_dir/lib${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
export CGO_ENABLED=1
export CGO_LDFLAGS="-L$runtime_dir/lib -Wl,-rpath,$runtime_dir/lib"
exec go run -tags moonshine .
