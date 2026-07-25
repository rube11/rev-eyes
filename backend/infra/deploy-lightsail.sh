#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 3 ]]; then
  echo "usage: $0 ubuntu@HOST ENV_FILE SCHEDULE_REGISTRAR_URL" >&2
  exit 2
fi

host=$1
env_file=$2
schedule_registrar_url=$3
binary=$(mktemp)
trap 'rm -f "$binary"' EXIT

if [[ ! -r "$env_file" ]]; then
  echo "environment file is not readable: $env_file" >&2
  exit 2
fi
mapfile -t beta_allowlist < <(sed -n 's/^BETA_ALLOWED_EMAILS=//p' "$env_file")
email_pattern='[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}'
if [[ ${#beta_allowlist[@]} -ne 1 ]] ||
  [[ ! "${beta_allowlist[0]}" =~ ^${email_pattern}(,${email_pattern})*$ ]]; then
  echo "environment file must contain one valid BETA_ALLOWED_EMAILS entry" >&2
  exit 2
fi
if [[ "$schedule_registrar_url" != https://* || "$schedule_registrar_url" == */ ]]; then
  echo "schedule registrar URL must use HTTPS and must not end with a slash" >&2
  exit 2
fi

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags='-s -w' -o "$binary" .

scp "$binary" "$host:/tmp/rev-eyes-backend"
ssh "$host" \
  'sudo install -o root -g root -m 0755 /tmp/rev-eyes-backend /opt/rev-eyes/backend && rm /tmp/rev-eyes-backend'

{
  sed '/^SCHEDULE_REGISTRAR_URL=/d' "$env_file"
  printf 'SCHEDULE_REGISTRAR_URL=%s\n' "$schedule_registrar_url"
} | ssh "$host" \
  'umask 077; temporary=$(mktemp); cat > "$temporary"; sudo install -o root -g root -m 0600 "$temporary" /etc/rev-eyes/backend.env; rm "$temporary"'

ssh "$host" '
  sudo systemctl restart rev-eyes.service
  sudo systemctl is-active rev-eyes.service
  curl --fail --silent --show-error --retry 10 --retry-delay 1 --retry-connrefused \
    http://127.0.0.1:8080/health >/dev/null
'
