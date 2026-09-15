#!/usr/bin/env bash
set -euo pipefail
if [[ $# -ne 1 ]]; then
  echo "usage: $0 ubuntu@HOST" >&2
  exit 2
fi
host=$1
scp moonshine/worker.py moonshine/requirements.txt "$host:/tmp/"
ssh "$host" 'set -eu
  sudo apt-get update -qq
  sudo apt-get install -y -qq python3-venv
  sudo install -d -m 0755 /opt/rev-eyes/moonshine
  sudo install -m 0644 /tmp/worker.py /opt/rev-eyes/moonshine/worker.py
  sudo install -m 0644 /tmp/requirements.txt /opt/rev-eyes/moonshine/requirements.txt
  sudo python3 -m venv /opt/rev-eyes/moonshine/venv
  sudo /opt/rev-eyes/moonshine/venv/bin/pip install --disable-pip-version-check -r /opt/rev-eyes/moonshine/requirements.txt
  sudo /opt/rev-eyes/moonshine/venv/bin/python -c '\''from pathlib import Path; from moonshine_voice.download import get_model_for_language; from moonshine_voice.moonshine_api import ModelArch; p,_=get_model_for_language("en",ModelArch.TINY_STREAMING,cache_root=Path("/opt/rev-eyes/moonshine/models")); Path("/opt/rev-eyes/moonshine/model-path").write_text(p)'\''
  model_path=$(cat /opt/rev-eyes/moonshine/model-path)
  sudo chmod -R a+rX /opt/rev-eyes/moonshine
  sudo install -d /etc/systemd/system/rev-eyes.service.d
  printf "[Service]\nEnvironment=MOONSHINE_PYTHON=/opt/rev-eyes/moonshine/venv/bin/python\nEnvironment=MOONSHINE_WORKER=/opt/rev-eyes/moonshine/worker.py\nEnvironment=MOONSHINE_MODEL_PATH=%s\nEnvironment=OMP_NUM_THREADS=1\n" "$model_path" | sudo tee /etc/systemd/system/rev-eyes.service.d/moonshine.conf >/dev/null
  sudo systemctl daemon-reload
  rm /tmp/worker.py /tmp/requirements.txt
'
