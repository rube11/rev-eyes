# Rev Eyes beta deployment

Current beta access is restricted by the backend to:

```text
ferruben739@gmail.com
```

The allowlist is checked against the email claim in a signed Supabase access
token before a session or WebSocket ticket is created. Frontend checks are not
trusted for access control.

## 1. Prepare Supabase Auth

1. In Supabase, open **Authentication > Users**.
2. Confirm `ferruben739@gmail.com` exists and can sign in, or use **Add user >
   Send invitation**.
3. Disable **Allow new users to sign up**.
4. Keep anonymous sign-ins disabled.
5. Confirm the production and preview redirect URLs include the Vercel domains
   used for testing.

References:

- https://supabase.com/docs/guides/auth/users
- https://supabase.com/docs/guides/auth/general-configuration

## 2. Validate locally

From `backend/`:

```bash
go test ./...
go vet ./...
bash -n infra/*.sh
```

From `frontend/`:

```bash
pnpm lint
pnpm pack:beta
```

`pnpm pack:beta` builds against `https://api.rev-eyes.com` and creates the
ignored artifact `frontend/rev-eyes-beta.ehpk`.

## 3. Deploy the backend

Authenticate to AWS before validating or updating the scheduler stacks:

```bash
aws login
aws sts get-caller-identity --region us-west-2
```

Deploy the current database migration and backend:

```bash
cd backend
infra/deploy-backend-code.sh \
  ubuntu@52.11.78.112 \
  ferruben739@gmail.com \
  https://rev-eyes.com,https://www.rev-eyes.com
```

The script updates only `BETA_ALLOWED_EMAILS` and `FRONTEND_ORIGIN` in the
existing root-owned environment file. It retains the previous binary and
environment file and restores both automatically if the service or local
health check fails.

Public smoke checks:

```bash
curl --fail --silent --show-error https://api.rev-eyes.com/health
curl --silent --show-error \
  --request POST \
  --output /dev/null \
  --write-out '%{http_code}\n' \
  https://api.rev-eyes.com/internal/scheduler/run
```

The health endpoint should return HTTP 204. The unauthenticated scheduler probe
should return HTTP 401.

Confirm both production frontend origins pass CORS:

```bash
for origin in https://rev-eyes.com https://www.rev-eyes.com; do
  curl --silent --show-error \
    --request OPTIONS \
    --header "Origin: $origin" \
    --header 'Access-Control-Request-Method: POST' \
    --output /dev/null \
    --write-out "$origin %{http_code}\n" \
    https://api.rev-eyes.com/auth/ws-ticket
done
```

Both origins should return HTTP 204.

## 4. Deploy the frontend

The Vercel project must define these values for Production and the beta Preview
environment:

```text
VITE_API_BASE_URL=https://api.rev-eyes.com
VITE_SUPABASE_URL
VITE_SUPABASE_PUBLISHABLE_KEY
```

`frontend/vercel.json` selects `pnpm build:beta`, and `.env.beta` pins the
backend to `https://api.rev-eyes.com`. Deploy a Preview first, verify the
allowed account end to end, then promote the same commit to Production.

Before testing a Preview, add its exact HTTPS origin to the backend
`FRONTEND_ORIGIN` comma-separated list and restart the backend. Keep
`https://www.rev-eyes.com` in the list so production remains available.

Vercel environment variable changes apply only to new deployments:

- https://vercel.com/docs/environment-variables

## 5. Beta acceptance checks

- `ferruben739@gmail.com` can sign in and obtain a WebSocket ticket.
- Any other valid Supabase account receives the same HTTP 401 response as an
  invalid token.
- The glasses connect, stream audio, render a response, and reconnect after an
  interrupted network connection.
- Reminder confirmation creates one scheduled reminder.
- Watch confirmation creates one scheduled watch and respects the active-watch
  limit.
- Backend and scheduler logs contain no access tokens, API keys, passwords, or
  user transcripts.
