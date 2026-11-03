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
The root-owned `install-ci-release.sh` takes a release tar on stdin, verifies the
binary checksum, saves the previous binary/metadata, restarts, checks health and
unauthenticated workspace access, and restores the previous release on failure.

The server installer is root-owned at
`/usr/local/sbin/rev-eyes-install-ci-release`. The dedicated `deploy` account
has a root-owned authorized key with `restrict` and a forced installer command;
sudo permits only that installer. The dedicated key is stored in GitHub's
Production environment as `BACKEND_DEPLOY_KEY`; `BACKEND_KNOWN_HOSTS` pins the
host key verified over the existing operator connection. No operator identity
or application credentials are stored in the repository.

The workflow explicitly gates deployment to master pushes/manual runs and uses
read-only repository permissions. The existing Production environment has no
branch policy; changing that policy requires Administration permission, which
the setup token lacks. No repository protection settings were changed.

To roll back manually, restore `/opt/rev-eyes/backend.previous` and
`/opt/rev-eyes/release.previous.json`, then restart `rev-eyes.service`. Automatic
rollback checks the local health endpoint and authentication boundary before
publishing release metadata. Public endpoint checks also run from the CI runner.
Vercel and the backend deploy independently after a master push; backend deployment
is gated by the full validation job, while Vercel retains its native Git behavior.
