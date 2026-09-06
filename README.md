# rev/eyes

rev/eyes is a conversational assistant for Even G2 glasses and the web. It
turns short spoken or typed requests into useful context, persistent memories,
reminders, and ongoing watches while keeping the workspace synchronized across
the glasses and browser.

The project is currently in private beta.

## What it does

- Streams tap-to-talk audio from the glasses and transcribes it with Deepgram.
- Routes conversations through OpenAI with relevant user and session context.
- Builds a bounded memory profile from conversations and supports explicit
  remembering, correcting, reviewing, and forgetting.
- Creates reminders and watches through a confirmation flow before scheduling
  work.
- Brings conversations, memories, reminders, watches, and assistant status into
  one responsive web workspace.
- Reconnects interrupted glasses sessions and preserves conversation history.

## How it fits together

```text
Even G2 glasses / React workspace
               |
       HTTP + WebSocket
               |
          Go backend
        /      |       \
 Deepgram   OpenAI    PostgreSQL
                         |
                  AWS event scheduler
```

The frontend owns the glasses interaction model and browser workspace. The Go
backend authenticates Supabase sessions, manages realtime conversations,
coordinates transcription and assistant responses, persists application state,
and dispatches scheduled reminders and watches.

## Stack

- React 19, TypeScript, and Vite
- Even Hub SDK for the Even G2 client
- Go 1.22
- PostgreSQL and Supabase Auth
- OpenAI Responses API
- Deepgram streaming speech-to-text
- Tavily web search
- AWS CloudFormation and EventBridge Scheduler

## Repository layout

```text
.
├── frontend/          React workspace and Even G2 client
├── backend/           Go API, realtime server, migrations, and infrastructure
│   ├── cmd/migrate/   Database migration runner
│   ├── internal/      Application packages
│   ├── migrations/    Ordered PostgreSQL migrations
│   └── infra/         AWS deployment templates and scripts
└── run                Local backend/frontend launcher
```

## Local development

### Prerequisites

- Go 1.22 or newer
- Node.js and pnpm
- A PostgreSQL database
- Supabase project configuration
- OpenAI, Deepgram, and Tavily API credentials
- An HTTPS scheduler registrar endpoint

### Configure the backend

```bash
cd backend
cp .env.example .local.env
```

Fill in `.local.env`, then apply the migrations in order:

```bash
set -a
source .local.env
set +a
for migration in migrations/*.sql; do
  go run ./cmd/migrate "$migration"
done
```

### Configure the frontend

```bash
cd frontend
cp .env.example .env.local
pnpm install
```

Set the Supabase publishable configuration and a backend URL reachable by the
client.

### Start the app

From the repository root, launch both services:

```bash
./run
```

The launcher reads `backend/.local.env`. You can also start each service in its
own terminal with `go run .` from `backend/` and `pnpm dev` from `frontend/`.

## Validation

Run the offline backend checks:

```bash
cd backend
go test ./...
go vet ./...
```

Run the frontend checks:

```bash
cd frontend
pnpm test
pnpm lint
pnpm build
```

Live model and database integration tests are opt-in. A normal offline test run
does not verify provider credentials or deployed behavior.

## More documentation

- [Frontend development and packaging](frontend/README.md)
- [Backend package architecture](backend/internal/README.md)
- [AWS infrastructure and deployment](backend/infra/README.md)

Local environment files, logs, compiled binaries, and Even Hub packages are
ignored by Git. Keep credentials in the local environment files and never
commit them.
