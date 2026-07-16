create index transcript_utterances_session_user_idx
    on public.transcript_utterances (session_id, user_id);
