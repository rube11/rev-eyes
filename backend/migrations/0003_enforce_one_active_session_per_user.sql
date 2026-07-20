create unique index sessions_one_active_per_user_idx
    on public.sessions (user_id)
    where status = 'active';
