#!/usr/bin/env bash
# Installed root-owned on the server; invoked by a forced SSH command.
set -euo pipefail
exec 9>/opt/rev-eyes/deploy.lock
flock 9
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
# Input is a tar containing only backend, source_commit and backend.sha256.
cat > "$stage/release.tgz"
python3 - "$stage" <<'PYTHON'
import pathlib, sys, tarfile
stage = pathlib.Path(sys.argv[1])
with tarfile.open(stage / 'release.tgz') as archive:
    members = archive.getmembers()
    assert sorted(m.name for m in members) == ['backend', 'backend.sha256', 'source_commit']
    assert all(m.isfile() and m.size < 200_000_000 for m in members)
    for member in members:
        with (stage / member.name).open('wb') as output:
            output.write(archive.extractfile(member).read())
PYTHON
cd "$stage"
commit=$(cat source_commit)
[[ $commit =~ ^[0-9a-f]{40}$ ]]
sha256sum --check backend.sha256
hash=$(sha256sum backend | cut -d' ' -f1)
cp -p /opt/rev-eyes/backend /opt/rev-eyes/backend.previous
cp -p /opt/rev-eyes/release.json /opt/rev-eyes/release.previous.json
rollback() {
 install -m 0755 /opt/rev-eyes/backend.previous /opt/rev-eyes/backend
 cp -p /opt/rev-eyes/release.previous.json /opt/rev-eyes/release.json
 systemctl restart rev-eyes.service
}
trap 'rollback; exit 1' ERR
install -m 0755 backend /opt/rev-eyes/backend.next
mv /opt/rev-eyes/backend.next /opt/rev-eyes/backend
systemctl restart rev-eyes.service
healthy=false
for attempt in {1..30}; do
 if [[ $(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/health) == 204 ]]; then healthy=true; break; fi
 sleep 1
done
[[ $healthy == true ]]
[[ $(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/workspace/agents) == 401 ]]
systemctl is-active --quiet rev-eyes.service
printf '{"source_commit":"%s","binary_sha256":"%s"}\n' "$commit" "$hash" > /opt/rev-eyes/release.json
trap - ERR
