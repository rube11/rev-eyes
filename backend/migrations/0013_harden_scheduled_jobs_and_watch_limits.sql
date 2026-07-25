create table public.watch_active_counts (
    user_id uuid primary key references auth.users (id) on delete cascade,
    active_count smallint not null default 0,

    constraint watches_active_limit_check
        check (active_count between 0 and 5)
);

do $migration$
begin
    if exists (
        select 1
        from public.watches
        where status = 'active'
          and expires_at > statement_timestamp()
        group by user_id
        having count(*) > 5
    ) then
        raise exception
            'cannot install the five-watch limit while a user has more than five active watches';
    end if;
end
$migration$;

update public.watches
set status = 'expired'
where status = 'active'
  and expires_at <= statement_timestamp();

insert into public.watch_active_counts (user_id, active_count)
select user_id, count(*)::smallint
from public.watches
where status = 'active'
group by user_id;

alter table public.watch_active_counts enable row level security;

comment on table public.watch_active_counts is
    'Internal transactional counter that enforces at most five active watches per user.';

create function public.enforce_watch_active_limit()
returns trigger
language plpgsql
set search_path = pg_catalog, public
as $function$
declare
    reserved_count smallint;
begin
    if tg_op = 'DELETE' then
        if old.status = 'active' then
            update public.watch_active_counts
            set active_count = active_count - 1
            where user_id = old.user_id
              and active_count > 0;
        end if;
        return old;
    end if;

    if tg_op = 'UPDATE'
       and old.status = 'active'
       and (
           new.status <> 'active'
           or new.user_id is distinct from old.user_id
       ) then
        update public.watch_active_counts
        set active_count = active_count - 1
        where user_id = old.user_id
          and active_count > 0;
    end if;

    if new.status = 'active'
       and (
           tg_op = 'INSERT'
           or old.status <> 'active'
           or new.user_id is distinct from old.user_id
       ) then
        reserved_count := null;
        insert into public.watch_active_counts as counter (user_id, active_count)
        values (new.user_id, 1)
        on conflict (user_id) do update
        set active_count = counter.active_count + 1
        where counter.active_count < 5
        returning active_count into reserved_count;

        if reserved_count is null then
            raise exception 'a user may have at most five active watches'
                using
                    errcode = '23514',
                    constraint = 'watches_active_limit_check';
        end if;
    end if;

    return new;
end
$function$;

create trigger watches_active_limit_insert
before insert on public.watches
for each row
execute function public.enforce_watch_active_limit();

create trigger watches_active_limit_update
before update of status, user_id on public.watches
for each row
execute function public.enforce_watch_active_limit();

create trigger watches_active_limit_delete
before delete on public.watches
for each row
execute function public.enforce_watch_active_limit();

create table public.scheduled_job_events (
    id uuid primary key,
    kind text not null,
    resource_id uuid not null,
    attempts integer not null default 0,
    next_attempt_at timestamptz not null default now(),
    locked_until timestamptz,
    processed_at timestamptz,
    abandoned_at timestamptz,
    last_error text,
    received_at timestamptz not null default now(),

    constraint scheduled_job_events_kind_check
        check (kind in ('reminder', 'watch')),
    constraint scheduled_job_events_attempts_check
        check (attempts >= 0),
    constraint scheduled_job_events_terminal_check
        check (processed_at is null or abandoned_at is null)
);

create index scheduled_job_events_pending_idx
    on public.scheduled_job_events (next_attempt_at, received_at, id)
    where processed_at is null and abandoned_at is null;

create index scheduled_job_events_terminal_idx
    on public.scheduled_job_events (
        (coalesce(processed_at, abandoned_at))
    )
    where processed_at is not null or abandoned_at is not null;

alter table public.scheduled_job_events enable row level security;

comment on table public.scheduled_job_events is
    'Durable idempotency inbox for due events received from EventBridge.';
