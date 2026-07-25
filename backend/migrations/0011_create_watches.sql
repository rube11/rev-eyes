create table public.watches (
    id uuid primary key default gen_random_uuid(),
    user_id uuid not null references auth.users (id) on delete cascade,
    session_id uuid not null,
    source_utterance_id uuid not null,
    query text not null,
    condition text not null,
    interval_minutes integer not null,
    expires_at timestamptz not null,
    status text not null default 'proposed',
    created_at timestamptz not null default now(),
    resolved_at timestamptz,
    next_check_at timestamptz,
    last_checked_at timestamptz,
    seen_urls text[] not null default '{}',

    constraint watches_session_user_fkey
        foreign key (session_id, user_id)
        references public.sessions (id, user_id)
        on delete cascade,
    constraint watches_source_user_fkey
        foreign key (source_utterance_id, user_id)
        references public.transcript_utterances (id, user_id)
        on delete cascade,
    constraint watches_query_check
        check (length(btrim(query)) between 1 and 400),
    constraint watches_condition_check
        check (length(btrim(condition)) between 1 and 200),
    constraint watches_interval_check
        check (interval_minutes between 60 and 1440),
    constraint watches_expiry_check
        check (expires_at > created_at),
    constraint watches_status_check
        check (status in ('proposed', 'active', 'rejected', 'expired')),
    constraint watches_resolution_check
        check (
            (status = 'proposed' and resolved_at is null and next_check_at is null)
            or (status = 'rejected' and resolved_at is not null and next_check_at is null)
            or (status in ('active', 'expired') and resolved_at is not null and next_check_at is not null)
        )
);

create unique index watches_one_pending_per_session_idx
    on public.watches (session_id)
    where status = 'proposed';

create index watches_due_idx
    on public.watches (next_check_at, id)
    where status = 'active';

alter table public.watches enable row level security;

comment on table public.watches is
    'User-confirmed background watches for public web updates.';
