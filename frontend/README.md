# rev-eyes frontend

React, TypeScript, and Vite client for the Even G2 glasses.

## Scripts

- `pnpm dev`
- `pnpm build`
- `pnpm build:beta`
- `pnpm lint`
- `pnpm pack:beta`
- `pnpm preview`

## Beta package

Create `.env.local` from `.env.example` and set the Supabase publishable
configuration. Then run:

```bash
pnpm lint
pnpm pack:beta
```

The beta build uses `https://api.rev-eyes.com` and produces
`rev-eyes-beta.ehpk`. The package network permission includes both the backend
and Supabase origins.

Vercel is configured by `vercel.json` to run the same beta build. Production
and preview deployments require these values in the Vercel project
environment:

```text
VITE_API_BASE_URL=https://api.rev-eyes.com
VITE_SUPABASE_URL
VITE_SUPABASE_PUBLISHABLE_KEY
```
