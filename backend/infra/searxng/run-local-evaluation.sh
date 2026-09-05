#!/usr/bin/env bash
# Local-only, disposable SearXNG runtime for opt-in web-research tests.
# Installs pinned SearXNG requirements into a temporary venv, never system Python.
set -euo pipefail

if [[ ${RUN_LOCAL_SEARXNG_EVAL:-} != 1 ]]; then
  echo 'Set RUN_LOCAL_SEARXNG_EVAL=1 to download/install the local evaluation runtime.' >&2
  exit 2
fi
port=${1:-8889}
if [[ ! $port =~ ^[0-9]{4,5}$ ]] || ((port < 1024 || port > 65535)); then
  echo 'Choose a local port between 1024 and 65535.' >&2
  exit 2
fi
settings_name=${2:-settings.yml}
case "$settings_name" in
  settings.yml|evaluation-settings.yml|evaluation-yahoo-settings.yml) ;;
  *) echo 'Choose a checked-in SearXNG settings filename for local evaluation.' >&2; exit 2 ;;
esac
settings=$(readlink -f "$(dirname "$0")/$settings_name")
test -f "$settings"
runtime=$(mktemp -d /tmp/rev-eyes-searxng-eval.XXXXXX)
revision=a1144dda3e97668c9d445022b7019c224cd4bb1e
echo "Evaluation runtime: $runtime"
echo "SearXNG revision: $revision"
echo "Evaluation settings: $settings_name"
echo 'After stopping its processes, remove only this exact disposable directory if it remains.'

git init -q "$runtime/src"
git -C "$runtime/src" remote add origin https://github.com/searxng/searxng.git
git -C "$runtime/src" fetch -q --depth=1 origin "$revision"
git -C "$runtime/src" checkout -q --detach FETCH_HEAD
python3 -m venv --without-pip "$runtime/venv"
# Debian's base Python may omit ensurepip; bootstrap pip only inside this venv.
curl --fail --silent --show-error --max-time 30 \
  https://bootstrap.pypa.io/pip/pip.pyz --output "$runtime/pip.pyz"
"$runtime/venv/bin/python" "$runtime/pip.pyz" install --quiet \
  -r "$runtime/src/requirements.txt" 'granian==2.8.2'
"$runtime/venv/bin/python" "$runtime/pip.pyz" show granian | sed -n '1,2p'

unset OPENAI_API_KEY TAVILY_API_KEY
export SEARXNG_SETTINGS_PATH="$settings"
export SEARXNG_CACHE_PATH="$runtime/cache"
export XDG_CACHE_HOME="$runtime/cache"
mkdir -p "$runtime/cache"
cd "$runtime/src"
exec "$runtime/venv/bin/granian" --interface wsgi --host 127.0.0.1 --port "$port" --workers 1 searx.webapp:app
