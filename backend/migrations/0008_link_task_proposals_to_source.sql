alter table public.task_proposals
    add column source_utterance_id uuid not null,

    add constraint task_proposals_source_user_fkey
        foreign key (source_utterance_id, user_id)
        references public.transcript_utterances (id, user_id)
        on delete cascade;

create index task_proposals_source_user_idx
    on public.task_proposals (source_utterance_id, user_id);
