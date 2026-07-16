create table public.sessions (
    id uuid primary key default gen_random_uuid(),
    user_id uuid not null references auth.users (id) on delete cascade,
    status text not null default 'active',
    started_at timestamptz not null default now(),
    last_activity_at timestamptz not null default now(),
    ended_at timestamptz,

    constraint sessions_status_check
        check (status in ('active', 'ended', 'expired')),
    constraint sessions_activity_time_check
        check (last_activity_at >= started_at),
    constraint sessions_end_time_check
        check (ended_at is null or ended_at >= started_at),
    constraint sessions_status_end_time_check
        check (
            (status = 'active' and ended_at is null)
            or (status in ('ended', 'expired') and ended_at is not null)
        ),
    constraint sessions_id_user_id_key unique (id, user_id)
);

create table public.transcript_utterances (
    id uuid primary key default gen_random_uuid(),
    user_id uuid not null references auth.users (id) on delete cascade,
    session_id uuid not null,
    speaker text not null default 'unknown',
    text text not null,
    started_at timestamptz not null,
    ended_at timestamptz not null,
    created_at timestamptz not null default now(),

    constraint transcript_utterances_session_user_fkey
        foreign key (session_id, user_id)
        references public.sessions (id, user_id)
        on delete cascade,
    constraint transcript_utterances_speaker_check
        check (speaker in ('user', 'assistant', 'unknown')),
    constraint transcript_utterances_text_check
        check (length(btrim(text)) > 0),
    constraint transcript_utterances_time_check
        check (ended_at >= started_at)
);

create index sessions_user_activity_idx
    on public.sessions (user_id, last_activity_at desc);

create index sessions_active_activity_idx
    on public.sessions (last_activity_at)
    where status = 'active';

create index transcript_utterances_session_time_idx
    on public.transcript_utterances (session_id, started_at, id);

create index transcript_utterances_user_time_idx
    on public.transcript_utterances (user_id, started_at desc);

alter table public.sessions enable row level security;
alter table public.transcript_utterances enable row level security;

comment on table public.sessions is
    'Application conversation sessions; separate from Supabase Auth sessions and WebSocket connections.';

comment on table public.transcript_utterances is
    'Finalized transcript utterances persisted before routing or memory extraction.';
