create table public.notifications (
    id uuid primary key default gen_random_uuid(),
    user_id uuid not null references auth.users (id) on delete cascade,
    text text not null,
    created_at timestamptz not null default now(),
    delivered_at timestamptz,

    constraint notifications_text_check
        check (length(btrim(text)) between 1 and 1000)
);

create index notifications_pending_user_idx
    on public.notifications (user_id, created_at, id)
    where delivered_at is null;

alter table public.notifications enable row level security;

comment on table public.notifications is
    'Durable proactive messages awaiting realtime delivery.';
