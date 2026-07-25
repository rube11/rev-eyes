# Rev Eyes infrastructure

## Scheduler deployment

The scheduler uses two CloudFormation stacks:

- `rev-eyes-scheduler-foundation` owns the fixed runtime roles and the bounded
  CloudFormation execution role. Only the account root session bootstraps it.
- `rev-eyes-scheduler` owns the application resources and contains no IAM
  resources.

Run the one-time foundation bootstrap from the repository root:

```bash
infra/bootstrap-scheduler-foundation.sh SCHEDULER_SECRET_ARN
```

The bootstrap also narrows `RevEyesSchedulerDeploy` to the one application
stack and detaches both `RevEyesSchedulerRegistrarDeploy` and
`RevEyesLightsailDeploy` from `rev-eyes-agent`. Backend code deployments use
SSH and do not require either policy.

After the foundation exists, deploy the scheduler as `rev-eyes-agent`:

```bash
infra/deploy-scheduler.sh deploy \
  https://api.rev-eyes.com \
  SCHEDULER_SECRET_ARN \
  52.11.78.112/32
```

The deploy script creates a named change set, prints every resource change,
and then executes it through the bounded CloudFormation role.

## Backend deployment

Deploy migration 0013 and the backend without copying or printing environment
secrets:

```bash
infra/deploy-backend-code.sh \
  ubuntu@52.11.78.112 \
  ferruben739@gmail.com \
  https://rev-eyes.com,https://www.rev-eyes.com
```

The migration runs in a transient systemd unit that reads the existing
root-owned `/etc/rev-eyes/backend.env` file on the server. The deployment
updates only `BETA_ALLOWED_EMAILS` and `FRONTEND_ORIGIN`; it does not copy or
print the other environment values. It also backs up both the current binary
and environment file before restarting.

`BETA_ALLOWED_EMAILS` is enforced from the signed Supabase access token before
the backend creates a session or WebSocket ticket. Use a comma-separated list
without spaces when adding more testers.

`FRONTEND_ORIGIN` accepts a comma-separated list of exact origins. Keep
`https://www.rev-eyes.com` and add the exact Vercel preview origin when testing
a preview deployment.

For a first deployment through `deploy-lightsail.sh`, the supplied environment
file must contain exactly one `BETA_ALLOWED_EMAILS` entry. See `.env.example`
for the complete backend configuration contract.
