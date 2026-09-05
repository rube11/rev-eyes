-- The profile is a projection of memories, never a second authored document.
alter table public.memories
    add column profile_layer text not null default 'detail'
        check (profile_layer in ('core', 'recent', 'detail')),
    add column profile_override text
        check (profile_override in ('core', 'detail'));

-- Only promote known canonical families. Unclassified legacy cards remain
-- searchable; do not reinterpret old event text as a current situation.
update public.memories
set profile_layer = case
    when expires_at is not null then 'recent'
    when memory_key like 'profile.role.%'
      or memory_key like 'profile.nutrition.%'
      or memory_key like 'profile.relationship.%'
      or (memory_key is not null and kind in ('goal', 'instruction')) then 'core'
    else 'detail'
end
where status = 'active';

create index memories_profile_user_idx on public.memories(user_id, profile_layer)
where status = 'active';
