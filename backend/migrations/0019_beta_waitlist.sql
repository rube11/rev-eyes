create table public.beta_waitlist (
  email text primary key check (length(email) <= 254),
  created_at timestamptz not null default now()
);
alter table public.beta_waitlist enable row level security;
revoke all on public.beta_waitlist from anon, authenticated;

create function public.join_beta_waitlist(email_address text, website text default '')
returns void language plpgsql security definer set search_path = '' as $$
declare normalized text := lower(trim(email_address));
begin
  -- Honeypot: automated form fills receive no information and store nothing.
  if coalesce(website, '') <> '' then return; end if;
  if normalized is null or length(normalized) > 254 or normalized !~ '^[^[:space:]@]+@[^[:space:]@]+[.][^[:space:]@]+$' then
    raise exception 'Invalid email address';
  end if;
  insert into public.beta_waitlist(email) values(normalized) on conflict (email) do nothing;
end;
$$;
revoke all on function public.join_beta_waitlist(text, text) from public;
grant execute on function public.join_beta_waitlist(text, text) to anon, authenticated;
