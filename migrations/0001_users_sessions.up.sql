create table users (
    id text primary key,
    email text unique,
    display_name text not null,
    created_at timestamptz not null,
    updated_at timestamptz not null
);

create table sessions (
    id text primary key,
    owner_id text not null references users(id),
    title text not null,
    provider_name text,
    session_prompt text,
    max_steps integer,
    created_at timestamptz not null,
    updated_at timestamptz not null,
    archived_at timestamptz
);

create index sessions_owner_updated_idx
    on sessions(owner_id, updated_at desc);

insert into users (id, email, display_name, created_at, updated_at)
values ('local', null, 'Local User', now(), now());
