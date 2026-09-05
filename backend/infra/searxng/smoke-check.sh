#!/bin/sh
set -eu

base_url=${SEARXNG_BASE_URL:-http://127.0.0.1:8888}
payload_file=$(mktemp)
trap 'rm -f "$payload_file"' EXIT HUP INT TERM

curl \
  --fail \
  --silent \
  --show-error \
  --retry 20 \
  --retry-delay 1 \
  --retry-connrefused \
  --max-time 5 \
  "$base_url/healthz" \
  >/dev/null

curl \
  --fail \
  --silent \
  --show-error \
  --get \
  --max-time 20 \
  --data-urlencode 'q=Open Source Initiative' \
  --data-urlencode 'format=json' \
  --data-urlencode 'categories=general' \
  --data-urlencode 'language=en' \
  --data-urlencode 'safesearch=2' \
  "$base_url/search" \
  --output "$payload_file"

python3 - "$payload_file" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as response:
    payload = json.load(response)

results = payload.get("results")
if not isinstance(results, list) or not results:
    engines = payload.get("unresponsive_engines", [])
    raise SystemExit(
        "SearXNG smoke query returned no results; "
        f"unresponsive engines: {engines!r}"
    )

if not any(
    isinstance(result, dict)
    and isinstance(result.get("url"), str)
    and result["url"].startswith(("http://", "https://"))
    for result in results
):
    raise SystemExit("SearXNG smoke response contains no usable web URL")
PY
