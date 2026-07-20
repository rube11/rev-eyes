alter table public.transcript_utterances
    add constraint transcript_utterances_id_user_id_key
    unique (id, user_id);

create table public.memories (
    id uuid primary key default gen_random_uuid(),
    user_id uuid not null
        references auth.users (id)
        on delete cascade,
    text text not null,
    created_at timestamptz not null default now(),

    constraint memories_text_check
        check (length(btrim(text)) > 0),
    constraint memories_id_user_id_key
        unique (id, user_id)
);

create table public.memory_sources (
    user_id uuid not null,
    memory_id uuid not null,
    utterance_id uuid not null,

    primary key (memory_id, utterance_id),

    constraint memory_sources_memory_user_fkey
        foreign key (memory_id, user_id)
        references public.memories (id, user_id)
        on delete cascade,

    constraint memory_sources_utterance_user_fkey
        foreign key (utterance_id, user_id)
        references public.transcript_utterances (id, user_id)
        on delete cascade
);

create index memories_user_created_idx
    on public.memories (user_id, created_at desc);

create index memory_sources_utterance_user_idx
    on public.memory_sources (utterance_id, user_id);

alter table public.memories enable row level security;
alter table public.memory_sources enable row level security;
