# Production pipeline

Pushes to `master` run `.github/workflows/production.yml`. Pull requests run the
same validation and package builds without deployment. Production runs serialize
without cancelling a deployment in progress. The backend deploy starts only after
all validation and both packages succeed. Vercel independently deploys `master`
using its existing Git integration and production configuration.

Download `rev-eyes-beta-<commit>` from the successful Actions run, unzip it, and
install `rev-eyes-beta.ehpk` manually through the Even Hub developer app/device
installation flow. The manifest version remains 0.1.16. CI packaging does not test
physical glasses.

The backend package uses the existing native Moonshine build script on Linux
amd64. The server retains its versioned Moonshine runtime and environment file;
ordinary deployment neither runs database migrations nor changes configuration.
All workflow actions are pinned to commit SHAs.
The root-owned `install-ci-release.sh` takes a release tar on stdin, verifies the
binary checksum (exactly one entry for `backend`), saves the previous binary/metadata, restarts, checks health and
unauthenticated workspace access, and restores the previous release on failure.

The server installer is root-owned at
`/usr/local/sbin/rev-eyes-install-ci-release`. The dedicated `deploy` account
has a root-owned authorized key with `restrict` and a forced installer command;
sudo permits only that installer. The dedicated key is stored in GitHub's
Production environment as `BACKEND_DEPLOY_KEY`; `BACKEND_KNOWN_HOSTS` pins the
host key verified over the existing operator connection. No operator identity
or application credentials are stored in the repository.

The workflow explicitly gates deployment to master pushes/manual runs and uses
read-only repository permissions. Production is restricted to master. Master requires a pull request and passing
`validate` checks, including for administrators; force pushes and deletion are
blocked. Independent approval requires adding a second collaborator; the sole
owner cannot approve their own pull requests.

To roll back manually, restore `/opt/rev-eyes/backend.previous` and
`/opt/rev-eyes/release.previous.json`, then restart `rev-eyes.service`. Automatic
rollback checks the local health endpoint and authentication boundary before
publishing release metadata. Public endpoint checks also run from the CI runner.
Vercel and the backend deploy independently after a master push; backend deployment
is gated by the full validation job, while Vercel retains its native Git behavior.

## Private runner connectivity

The deployment job joins the existing tailnet using `TS_CI_AUTH_KEY` from the
Production environment, then connects to `100.120.108.102:22`. The existing SSH
host key remains pinned via `HostKeyAlias`; the deployment key and forced command
are unchanged. The Tailscale action removes its ephemeral node after the run.

The configured `tag:rev-eyes-ci` may reach only `100.120.108.102` on TCP 22.
The policy preserves access to the four existing non-backend devices, including
backend-to-T3 HTTPS, while member and CI access to the backend is limited to
TCP 22. Tests reject backend port 8080 and preserve backend-to-T3 port 443.
New non-backend devices require an explicit policy update. Tailscale shields-up
is disabled so these rules apply.
The reusable ephemeral auth key created on 2026-11-03 expires after 90 days;
rotate `TS_CI_AUTH_KEY` before 2027-02-01. Public Lightsail firewall rules do
not need changes.

Installer input is limited to 64 MiB compressed and 128 MiB for the binary,
with small fixed bounds for commit/checksum metadata. Upload and validation time
out after 60 seconds; lock acquisition after 30 seconds. Local health requests
have connection and request timeouts. Run `python3 backend/infra/test-ci-release.py`
to check corrupt binaries, wrong checksum targets, traversal, symlinks, oversized
metadata/uploads, and stalled uploads without changing the live service.
