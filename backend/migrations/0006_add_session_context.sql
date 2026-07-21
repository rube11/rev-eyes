alter table public.sessions
    add column context_summary text,
    add column context_summary_through_id uuid,

    add constraint sessions_context_summary_check
        check (
            (context_summary is null and context_summary_through_id is null)
            or (
                length(btrim(context_summary)) > 0
                and context_summary_through_id is not null
            )
        );

comment on column public.sessions.context_summary is
    'Compacted transcript content preceding context_summary_through_id.';
