create table public.task_proposals (
    id uuid primary key default gen_random_uuid(),
    user_id uuid not null references auth.users (id) on delete cascade,
    session_id uuid not null,
    kind text not null,
    title text not null,
    schedule text,
    status text not null default 'proposed',
    created_at timestamptz not null default now(),
    resolved_at timestamptz,

    constraint task_proposals_session_user_fkey
        foreign key (session_id, user_id)
        references public.sessions (id, user_id)
        on delete cascade,
    constraint task_proposals_kind_check
        check (kind = 'reminder'),
    constraint task_proposals_title_check
        check (length(btrim(title)) between 1 and 120),
    constraint task_proposals_schedule_check
        check (
            schedule is null
            or length(btrim(schedule)) between 1 and 120
        ),
    constraint task_proposals_status_check
        check (status in ('proposed', 'accepted', 'rejected')),
    constraint task_proposals_resolution_check
        check (
            (status = 'proposed' and resolved_at is null)
            or (status in ('accepted', 'rejected') and resolved_at is not null)
        )
);

create unique index task_proposals_one_pending_per_session_idx
    on public.task_proposals (session_id)
    where status = 'proposed';

alter table public.task_proposals enable row level security;

comment on table public.task_proposals is
    'Reminder proposals awaiting or recording explicit user confirmation.';
