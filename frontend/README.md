# rev-eyes frontend

React, TypeScript, and Vite client for the Even G2 glasses.

## Source layout

- `src/app`: application startup and coordination.
- `src/features/auth`: sign-in UI and mobile keyboard handling.
- `src/features/workspace`: conversations, memories, tasks, and watches.
- `src/even`: glasses gestures, rendering, microphone, and realtime transport.
- `src/shared/api`: backend requests and Supabase session persistence.
- `tests`: offline checks for audio lifecycle, realtime messages, and storage.

The active audio path is tap-to-talk with Deepgram speech endpointing. Retired
Moonshine components and unused conversation/tip scaffolds are not part of it.

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
