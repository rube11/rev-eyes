create table public.schedule_registrations (
    id uuid primary key default gen_random_uuid(),
    kind text not null,
    resource_id uuid not null,
    schedule_at timestamptz,
    interval_minutes integer,
    end_at timestamptz,
    attempts integer not null default 0,
    next_attempt_at timestamptz not null default now(),
    locked_until timestamptz,
    registered_at timestamptz,
    last_error text,
    created_at timestamptz not null default now(),

    constraint schedule_registrations_kind_check
        check (kind in ('reminder', 'watch')),
    constraint schedule_registrations_attempts_check
        check (attempts >= 0),
    constraint schedule_registrations_shape_check
        check (
            (
                kind = 'reminder'
                and schedule_at is not null
                and interval_minutes is null
                and end_at is null
            )
            or
            (
                kind = 'watch'
                and schedule_at is null
                and interval_minutes between 60 and 1440
                and end_at is not null
            )
        ),
    constraint schedule_registrations_resource_unique
        unique (kind, resource_id)
);

create index schedule_registrations_pending_idx
    on public.schedule_registrations (next_attempt_at, created_at, id)
    where registered_at is null;

alter table public.schedule_registrations enable row level security;

comment on table public.schedule_registrations is
    'Durable outbox for one-time reminder and recurring web-watch schedules.';

update public.watches
set status = 'expired'
where status = 'active'
  and expires_at <= statement_timestamp();

insert into public.schedule_registrations (
    kind,
    resource_id,
    schedule_at
)
select
    'reminder',
    proposal.id,
    proposal.due_at
from public.task_proposals as proposal
where proposal.status = 'accepted'
  and proposal.enqueued_at is null
on conflict (kind, resource_id) do nothing;

insert into public.schedule_registrations (
    kind,
    resource_id,
    interval_minutes,
    end_at
)
select
    'watch',
    watch.id,
    watch.interval_minutes,
    watch.expires_at
from public.watches as watch
where watch.status = 'active'
  and watch.expires_at > statement_timestamp()
on conflict (kind, resource_id) do nothing;
