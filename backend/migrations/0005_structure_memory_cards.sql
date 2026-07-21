alter table public.memories
    rename column text to summary;

alter table public.memories
    rename constraint memories_text_check to memories_summary_check;

alter table public.memories
    add column topics text[] not null default array['other']::text[],
    add column kind text not null default 'fact',
    add column title text,
    add column details jsonb not null default '[]'::jsonb,
    add column entities jsonb not null default '[]'::jsonb,
    add column status text not null default 'active',
    add column updated_at timestamptz not null default now(),
    add column inactive_at timestamptz;

update public.memories
set title = left(btrim(summary), 120)
where title is null;

alter table public.memories
    alter column topics drop default,
    alter column kind drop default,
    alter column title set not null,

    add constraint memories_topics_check
        check (
            cardinality(topics) between 1 and 3
            and topics <@ array[
                'work',
                'personal',
                'friends',
                'family',
                'relationships',
                'health',
                'preferences',
                'goals',
                'places',
                'other'
            ]::text[]
        ),
    add constraint memories_kind_check
        check (kind in (
            'fact',
            'preference',
            'relationship',
            'event',
            'goal',
            'instruction'
        )),
    add constraint memories_title_check
        check (length(btrim(title)) between 1 and 120),
    add constraint memories_summary_length_check
        check (length(summary) <= 500),
    add constraint memories_details_check
        check (jsonb_typeof(details) = 'array'),
    add constraint memories_entities_check
        check (jsonb_typeof(entities) = 'array'),
    add constraint memories_status_check
        check (status in ('active', 'superseded', 'forgotten')),
    add constraint memories_inactive_time_check
        check ((status = 'active') = (inactive_at is null));

create index memories_user_active_kind_idx
    on public.memories (user_id, kind, updated_at desc)
    where status = 'active';

create index memories_topics_idx
    on public.memories using gin (topics);

create index memories_entities_idx
    on public.memories using gin (entities);
