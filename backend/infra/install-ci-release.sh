#!/usr/bin/env bash
# Installed root-owned on the server; invoked by a forced SSH command.
set -euo pipefail
exec 9>/opt/rev-eyes/deploy.lock
flock -w 30 9
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
# Input is a tar containing only backend, source_commit and backend.sha256.
python3 -c '
import hashlib, pathlib, re, signal, sys, tarfile
signal.alarm(60)
stage = pathlib.Path(sys.argv[1])
limit = 64 * 1024 * 1024
with (stage / "release.tgz").open("wb") as output:
    total = 0
    while chunk := sys.stdin.buffer.read(1024 * 1024):
        total += len(chunk)
        if total > limit:
            raise ValueError("Release exceeds 64 MiB upload limit")
        output.write(chunk)
with tarfile.open(stage / "release.tgz", "r:gz") as archive:
    members = archive.getmembers()
    limits = {"backend": 128 * 1024 * 1024, "backend.sha256": 74, "source_commit": 41}
    if sorted(m.name for m in members) != sorted(limits):
        raise ValueError("Unexpected release members")
    if any(not m.isfile() or not 0 < m.size <= limits[m.name] for m in members):
        raise ValueError("Invalid release member type or size")
    for member in members:
        with (stage / member.name).open("wb") as output:
            output.write(archive.extractfile(member).read())
commit = (stage / "source_commit").read_bytes()
checksum = (stage / "backend.sha256").read_bytes()
if not re.fullmatch(rb"[0-9a-f]{40}\n?", commit):
    raise ValueError("Invalid source commit")
if not re.fullmatch(rb"[0-9a-f]{64}  backend\n", checksum):
    raise ValueError("Expected one SHA-256 entry for backend")
actual = hashlib.sha256((stage / "backend").read_bytes()).hexdigest().encode()
if actual != checksum[:64]:
    raise ValueError("Backend checksum mismatch")
' "$stage"
cd "$stage"
commit=$(cat source_commit)
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
 if [[ $(curl --connect-timeout 1 --max-time 2 -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/health) == 204 ]]; then healthy=true; break; fi
 sleep 1
done
[[ $healthy == true ]]
[[ $(curl --connect-timeout 1 --max-time 2 -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/workspace/agents) == 401 ]]
systemctl is-active --quiet rev-eyes.service
printf '{"source_commit":"%s","binary_sha256":"%s"}\n' "$commit" "$hash" > /opt/rev-eyes/release.json
trap - ERR
