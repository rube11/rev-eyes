alter table public.task_proposals
    alter column schedule set not null,
    add column due_at timestamptz not null,
    add column enqueued_at timestamptz,

    add constraint task_proposals_due_time_check
        check (due_at > created_at),
    add constraint task_proposals_enqueued_check
        check (
            enqueued_at is null
            or (status = 'accepted' and enqueued_at >= due_at)
        );

create index task_proposals_due_idx
    on public.task_proposals (due_at, id)
    where status = 'accepted' and enqueued_at is null;

comment on column public.task_proposals.due_at is
    'Absolute execution time resolved before the user confirms the reminder.';

comment on column public.task_proposals.enqueued_at is
    'Time the reminder was atomically placed in the notification outbox.';
