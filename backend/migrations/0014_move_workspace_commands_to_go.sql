alter table public.schedule_registrations
add column operation text not null default 'register';

alter table public.schedule_registrations
add constraint schedule_registrations_operation_check
check (operation in ('register', 'cancel'));

alter table public.schedule_registrations
drop constraint schedule_registrations_shape_check;

alter table public.schedule_registrations
add constraint schedule_registrations_shape_check
check (
    (
        operation = 'register'
        and (
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
        )
    )
    or
    (
        operation = 'cancel'
        and schedule_at is null
        and interval_minutes is null
        and end_at is null
    )
);

comment on table public.schedule_registrations is
    'Durable outbox for registering and cancelling reminder and web-watch schedules.';
