#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: $0 ubuntu@HOST SEARXNG_IMAGE" >&2
  echo "SEARXNG_IMAGE must be an explicit version tag or sha256 digest, never latest." >&2
  exit 2
fi

host=$1
searxng_image=$2
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)

image_pattern='^(docker\.io|ghcr\.io)/searxng/searxng:([A-Za-z0-9][A-Za-z0-9._-]{0,127})$|^(docker\.io|ghcr\.io)/searxng/searxng@sha256:([A-Fa-f0-9]{64})$'
if [[ ! "$searxng_image" =~ $image_pattern ]] || [[ "$searxng_image" == *:latest ]]; then
  echo "SEARXNG_IMAGE must name the official image with a non-latest tag or sha256 digest" >&2
  exit 2
fi

for source_file in settings.yml searxng.service smoke-check.sh; do
  if [[ ! -r "$script_dir/searxng/$source_file" ]]; then
    echo "missing deployment asset: $script_dir/searxng/$source_file" >&2
    exit 2
  fi
done

remote_stage=$(ssh "$host" 'mktemp -d /tmp/rev-eyes-searxng.XXXXXX')
if [[ ! "$remote_stage" =~ ^/tmp/rev-eyes-searxng\.[A-Za-z0-9]+$ ]]; then
  echo "remote host returned an unsafe staging path" >&2
  exit 1
fi

cleanup() {
  ssh "$host" "rm -rf -- '$remote_stage'" >/dev/null 2>&1 || true
}
trap cleanup EXIT

scp \
  "$script_dir/searxng/settings.yml" \
  "$script_dir/searxng/searxng.service" \
  "$script_dir/searxng/smoke-check.sh" \
  "$host:$remote_stage/"

ssh "$host" bash -s -- "$searxng_image" "$remote_stage" <<'REMOTE'
set -Eeuo pipefail

searxng_image=$1
stage=$2
backup=$(mktemp -d /tmp/rev-eyes-searxng-backup.XXXXXX)
if [[ ! "$backup" =~ ^/tmp/rev-eyes-searxng-backup\.[A-Za-z0-9]+$ ]]; then
  echo "remote host returned an unsafe backup path" >&2
  exit 1
fi
had_settings=0
had_unit=0
had_smoke=0
had_image=0
had_runtime=0
was_active=0
was_enabled=0

if sudo test -f /etc/searxng/settings.yml; then
  had_settings=1
  sudo cp -p /etc/searxng/settings.yml "$backup/settings.yml"
fi
if sudo test -f /etc/systemd/system/rev-eyes-searxng.service; then
  had_unit=1
  sudo cp -p /etc/systemd/system/rev-eyes-searxng.service "$backup/searxng.service"
fi
if sudo test -f /usr/local/lib/rev-eyes/searxng-smoke-check; then
  had_smoke=1
  sudo cp -p /usr/local/lib/rev-eyes/searxng-smoke-check "$backup/smoke-check.sh"
fi
if sudo test -f /etc/searxng/image.env; then
  had_image=1
  sudo cp -p /etc/searxng/image.env "$backup/image.env"
fi
if sudo test -f /etc/searxng/runtime.env; then
  had_runtime=1
fi
if sudo systemctl is-active --quiet rev-eyes-searxng.service; then
  was_active=1
fi
if sudo systemctl is-enabled --quiet rev-eyes-searxng.service; then
  was_enabled=1
fi

restore_file() {
  existed=$1
  backup_file=$2
  target=$3
  mode=$4
  if [[ "$existed" -eq 1 ]]; then
    sudo install -o root -g root -m "$mode" "$backup_file" "$target"
  else
    sudo rm -f -- "$target"
  fi
}

rollback() {
  status=$?
  trap - ERR
  set +e
  echo "SearXNG deployment failed; restoring the previous service configuration" >&2
  sudo systemctl stop rev-eyes-searxng.service >/dev/null 2>&1
  restore_file "$had_settings" "$backup/settings.yml" /etc/searxng/settings.yml 0644
  restore_file "$had_unit" "$backup/searxng.service" /etc/systemd/system/rev-eyes-searxng.service 0644
  restore_file "$had_smoke" "$backup/smoke-check.sh" /usr/local/lib/rev-eyes/searxng-smoke-check 0755
  restore_file "$had_image" "$backup/image.env" /etc/searxng/image.env 0644
  if [[ "$had_runtime" -eq 0 ]]; then
    sudo rm -f -- /etc/searxng/runtime.env
  fi
  sudo systemctl daemon-reload
  if [[ "$had_unit" -eq 1 && "$was_enabled" -eq 1 ]]; then
    sudo systemctl enable rev-eyes-searxng.service >/dev/null 2>&1
  else
    sudo systemctl disable rev-eyes-searxng.service >/dev/null 2>&1
  fi
  if [[ "$had_unit" -eq 1 && "$was_active" -eq 1 ]]; then
    sudo systemctl start rev-eyes-searxng.service
  fi
  sudo rm -rf -- "$backup"
  exit "$status"
}
trap rollback ERR

missing_runtime=0
for runtime_command in curl docker openssl python3; do
  if ! command -v "$runtime_command" >/dev/null 2>&1; then
    missing_runtime=1
  fi
done
if [[ "$missing_runtime" -eq 1 ]]; then
  sudo env DEBIAN_FRONTEND=noninteractive apt-get update
  sudo env DEBIAN_FRONTEND=noninteractive apt-get install -y curl docker.io openssl python3
fi

sudo systemctl enable --now docker.service
sudo docker pull "$searxng_image"

sudo install -d -o root -g root -m 0755 /etc/searxng
sudo install -d -o root -g root -m 0755 /usr/local/lib/rev-eyes
if ! sudo test -s /etc/searxng/runtime.env; then
  sudo sh -c 'umask 077; printf "SEARXNG_SECRET=%s\n" "$(openssl rand -hex 32)" > /etc/searxng/runtime.env'
fi
sudo install -o root -g root -m 0644 "$stage/settings.yml" /etc/searxng/settings.yml
sudo install -o root -g root -m 0644 "$stage/searxng.service" /etc/systemd/system/rev-eyes-searxng.service
sudo install -o root -g root -m 0755 "$stage/smoke-check.sh" /usr/local/lib/rev-eyes/searxng-smoke-check
printf 'SEARXNG_IMAGE=%s\n' "$searxng_image" \
  | sudo tee /etc/searxng/image.env >/dev/null
sudo chown root:root /etc/searxng/image.env
sudo chmod 0644 /etc/searxng/image.env

sudo systemctl daemon-reload
sudo systemctl enable rev-eyes-searxng.service
sudo systemctl restart rev-eyes-searxng.service
sudo /usr/local/lib/rev-eyes/searxng-smoke-check

trap - ERR
sudo rm -rf -- "$backup"
echo "SearXNG is healthy at http://127.0.0.1:8888 using $searxng_image"
REMOTE
