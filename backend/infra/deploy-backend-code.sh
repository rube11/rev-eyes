#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 3 ]]; then
  echo "usage: $0 ubuntu@HOST BETA_ALLOWED_EMAILS FRONTEND_ORIGIN" >&2
  exit 2
fi

host=$1
beta_allowed_emails=$2
frontend_origins=$3
email_pattern='[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}'
if [[ ! "$beta_allowed_emails" =~ ^${email_pattern}(,${email_pattern})*$ ]]; then
  echo "BETA_ALLOWED_EMAILS must be a comma-separated email list without spaces" >&2
  exit 2
fi
origin_pattern='https://[A-Za-z0-9.-]+'
if [[ ! "$frontend_origins" =~ ^${origin_pattern}(,${origin_pattern})*$ ]]; then
  echo "FRONTEND_ORIGIN must be a comma-separated HTTPS origin list without spaces" >&2
  exit 2
fi

migration_name=0014_move_workspace_commands_to_go.sql
migration_source="migrations/$migration_name"
migration_unit="rev-eyes-migrate-0014-$(date +%s)"
backend_binary=$(mktemp)
migration_binary=$(mktemp)
trap 'rm -f "$backend_binary" "$migration_binary"' EXIT

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags='-s -w' -o "$backend_binary" .
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags='-s -w' -o "$migration_binary" ./cmd/migrate

scp \
  "$backend_binary" \
  "$migration_binary" \
  "$migration_source" \
  "$host:/tmp/"

ssh "$host" "
  set -eu

  sudo install -o root -g root -m 0755 /tmp/$(basename "$migration_binary") /opt/rev-eyes/migrate
  sudo install -o root -g root -m 0644 /tmp/$migration_name /opt/rev-eyes/$migration_name
  sudo systemd-run \
    --unit=$migration_unit \
    --wait \
    --pipe \
    --collect \
    --property=Type=oneshot \
    --property=EnvironmentFile=/etc/rev-eyes/backend.env \
    /opt/rev-eyes/migrate /opt/rev-eyes/$migration_name
  if sudo test -x /opt/rev-eyes/backend; then
    sudo cp -p /opt/rev-eyes/backend /opt/rev-eyes/backend.previous
  fi
  sudo cp -p /etc/rev-eyes/backend.env /etc/rev-eyes/backend.env.previous
  sudo install -o root -g root -m 0755 /tmp/$(basename "$backend_binary") /opt/rev-eyes/backend
  sudo sed -i '/^BETA_ALLOWED_EMAILS=/d' /etc/rev-eyes/backend.env
  printf '%s\n' 'BETA_ALLOWED_EMAILS=$beta_allowed_emails' \
    | sudo tee -a /etc/rev-eyes/backend.env >/dev/null
  sudo sed -i '/^FRONTEND_ORIGIN=/d' /etc/rev-eyes/backend.env
  printf '%s\n' 'FRONTEND_ORIGIN=$frontend_origins' \
    | sudo tee -a /etc/rev-eyes/backend.env >/dev/null
  rm \
    /tmp/$(basename "$backend_binary") \
    /tmp/$(basename "$migration_binary") \
    /tmp/$migration_name
  if ! sudo systemctl restart rev-eyes.service ||
    ! sudo systemctl is-active rev-eyes.service ||
    ! curl --fail --silent --show-error --retry 10 --retry-delay 1 --retry-connrefused \
      http://127.0.0.1:8080/health >/dev/null; then
    echo 'backend health check failed; restoring previous binary' >&2
    if sudo test -x /opt/rev-eyes/backend.previous; then
      sudo install \
        -o root \
        -g root \
        -m 0755 \
        /opt/rev-eyes/backend.previous \
        /opt/rev-eyes/backend
      sudo cp -p /etc/rev-eyes/backend.env.previous /etc/rev-eyes/backend.env
      sudo systemctl restart rev-eyes.service
    fi
    exit 1
  fi
"
