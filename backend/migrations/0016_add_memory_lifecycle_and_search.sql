alter table public.memories
    add column memory_key text,
    add column observed_at timestamptz not null default now(),
    add column observed_source_id uuid,
    add column expires_at timestamptz,
    add column search_document tsvector
        generated always as (
            to_tsvector(
                'english'::regconfig,
                title || ' ' || summary || ' ' || coalesce(memory_key, '')
            )
        ) stored,

    add constraint memories_memory_key_check
        check (
            memory_key is null
            or (
                length(memory_key) between 1 and 160
                and memory_key ~ '^[a-z0-9]+([._-][a-z0-9]+)*$'
            )
        ),
    add constraint memories_expiration_check
        check (expires_at is null or expires_at > created_at);

update public.memories as memory
set observed_at = latest.started_at,
    observed_source_id = latest.utterance_id
from (
    select distinct on (source.memory_id)
        source.memory_id,
        utterance.id as utterance_id,
        utterance.started_at
    from public.memory_sources as source
    join public.transcript_utterances as utterance
      on utterance.id = source.utterance_id
     and utterance.user_id = source.user_id
    order by source.memory_id, utterance.started_at desc, utterance.id desc
) as latest
where memory.id = latest.memory_id;

update public.memories
set observed_at = updated_at
where observed_source_id is null;

create unique index memories_one_active_key_per_user_idx
    on public.memories (user_id, memory_key)
    where status = 'active' and memory_key is not null;

create index memories_active_search_document_idx
    on public.memories using gin (search_document)
    where status = 'active';

comment on column public.memories.memory_key is
    'Stable identity used to update one active atomic memory as the user provides newer information.';

comment on column public.memories.observed_at is
    'Trusted source-utterance time used to prevent delayed learning jobs from overwriting newer information.';

comment on column public.memories.observed_source_id is
    'Source utterance used with observed_at for deterministic ordering and replay protection.';

comment on column public.memories.expires_at is
    'Null for durable memory; temporary context is ignored after this time.';
