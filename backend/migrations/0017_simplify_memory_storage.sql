-- Compatibility cleanup for environments that applied the earlier 0016
-- draft before the beta memory design was simplified.
alter table public.memories
    drop constraint if exists memories_retention_check,
    drop constraint if exists memories_confidence_check,
    drop constraint if exists memories_origin_check,
    drop constraint if exists memories_sensitivity_check,
    drop constraint if exists memories_expiration_check,
    drop constraint if exists memories_embedding_time_check,
    drop constraint if exists memories_superseded_link_check,
    drop constraint if exists memories_no_self_supersession_check,
    drop constraint if exists memories_superseded_by_user_fkey;

do $cleanup_sensitive_memories$
begin
    if exists (
        select 1
        from information_schema.columns
        where table_schema = 'public'
          and table_name = 'memories'
          and column_name = 'sensitivity'
    ) then
        execute $sql$
            update public.memories
            set status = 'forgotten',
                inactive_at = coalesce(inactive_at, statement_timestamp()),
                updated_at = statement_timestamp()
            where status = 'active'
              and sensitivity = 'sensitive'
        $sql$;
    end if;
end;
$cleanup_sensitive_memories$;

drop index if exists public.memories_user_active_expiration_idx;

alter table public.memories
    drop column if exists retention,
    drop column if exists confidence,
    drop column if exists origin,
    drop column if exists sensitivity,
    drop column if exists superseded_by_id,
    drop column if exists embedding,
    drop column if exists embedded_at,
    add column if not exists observed_at timestamptz,
    add column if not exists observed_source_id uuid;

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
set observed_at = coalesce(updated_at, created_at)
where observed_at is null;

alter table public.memories
    alter column observed_at set default now(),
    alter column observed_at set not null,
    add constraint memories_expiration_check
        check (expires_at is null or expires_at > created_at);

comment on column public.memories.observed_at is
    'Trusted source-utterance time used to prevent delayed learning jobs from overwriting newer information.';

comment on column public.memories.observed_source_id is
    'Source utterance used with observed_at for deterministic ordering and replay protection.';
