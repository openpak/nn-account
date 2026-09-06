-- nn-account adapter schema (Nintendo-specific; fresh OpenPak schema).
-- Port provenance: fields modeled on Pretendo/account f7b1bc2 PNID/NEX/server
-- models. Console passwords are NOT stored here; authentication delegates to
-- the account core (PRD FR-1).

CREATE TABLE pnids (
    pid                BIGINT PRIMARY KEY CHECK (pid BETWEEN 1000000000 AND 1799999999),
    username           TEXT NOT NULL UNIQUE,
    account_id         UUID NOT NULL,          -- core account (links validated via core)
    access_level       INT  NOT NULL DEFAULT 0 CHECK (access_level IN (0,1,2,3)),
    server_access_level TEXT NOT NULL DEFAULT 'prod',
    mii_name           TEXT NOT NULL DEFAULT 'nnid',
    mii_data           TEXT NOT NULL DEFAULT '', -- binary Mii, base64
    mii_hash           TEXT NOT NULL DEFAULT '',
    country            TEXT NOT NULL DEFAULT 'US',
    language           TEXT NOT NULL DEFAULT 'en',
    region             INT  NOT NULL DEFAULT 1, -- 2=JPN,1=USA,4=EUR,5=GB,8=KOR
    timezone_name      TEXT NOT NULL DEFAULT 'EST5EDT',
    deleted            BOOLEAN NOT NULL DEFAULT FALSE,
    creation_date      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE nex_accounts (
    pid                 BIGINT PRIMARY KEY,    -- NEX pid (distinct identity; may equal owning_pid)
    owning_pid          BIGINT REFERENCES pnids(pid), -- NULL = device-only provisional record (FR-2)
    password            TEXT NOT NULL,         -- NEX-only secret (FR-1: separate domain)
    access_level        INT  NOT NULL DEFAULT 0,
    server_access_level TEXT NOT NULL DEFAULT 'prod',
    friend_code         TEXT NOT NULL DEFAULT '',
    device_type         TEXT NOT NULL DEFAULT 'wiiu' CHECK (device_type IN ('wiiu','3ds')),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_nex_accounts_owning ON nex_accounts(owning_pid);

CREATE TABLE devices (
    fcdcert_hash        TEXT PRIMARY KEY,      -- SHA-256 (base64) of the console cert
    model               TEXT NOT NULL DEFAULT '',  -- ctr/spr/ftr/ktr/red/jan/wiiu
    serial              TEXT NOT NULL DEFAULT '',
    environment         TEXT NOT NULL DEFAULT '',
    mac_hash            TEXT NOT NULL DEFAULT '',
    access_level        INT NOT NULL DEFAULT 0,
    server_access_level TEXT NOT NULL DEFAULT 'prod',
    linked_pids         BIGINT[] NOT NULL DEFAULT '{}'
);

CREATE TABLE servers (
    game_server_id      TEXT NOT NULL,
    access_mode         TEXT NOT NULL DEFAULT 'prod' CHECK (access_mode IN ('prod','test','dev')),
    device              INT NOT NULL DEFAULT 1,  -- core SystemType enum (1=WUP, 2=CTR)
    client_id           TEXT NOT NULL,
    service_name        TEXT NOT NULL DEFAULT '',
    service_type        TEXT NOT NULL DEFAULT '',
    title_ids           TEXT[] NOT NULL DEFAULT '{}',
    ip                  TEXT NOT NULL DEFAULT '',
    ip_list             TEXT[] NOT NULL DEFAULT '{}',
    port                INT NOT NULL DEFAULT 0,
    aes_key             TEXT NOT NULL,
    maintenance_mode    BOOLEAN NOT NULL DEFAULT FALSE,
    health_check_port   INT,
    PRIMARY KEY (game_server_id, access_mode)
);

-- Tokens are stored hashed; raw values exist only in responses.
CREATE TABLE oauth_tokens (
    token_hash  TEXT PRIMARY KEY,
    client_id   TEXT NOT NULL DEFAULT '',
    pid         BIGINT NOT NULL,
    token_type  TEXT NOT NULL CHECK (token_type IN ('oauth_access','oauth_refresh')),
    title_id    BIGINT NOT NULL DEFAULT 0,
    issued_at   TIMESTAMPTZ NOT NULL,
    expires_at  TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_oauth_tokens_pid ON oauth_tokens(pid);

CREATE TABLE nex_tokens (
    token_hash     TEXT PRIMARY KEY,
    game_server_id TEXT NOT NULL,
    pid            BIGINT NOT NULL,
    title_id       BIGINT NOT NULL DEFAULT 0,
    issued_at      TIMESTAMPTZ NOT NULL,
    expires_at     TIMESTAMPTZ NOT NULL
);

CREATE TABLE independent_service_tokens (
    token_hash  TEXT PRIMARY KEY,
    client_id   TEXT NOT NULL,
    title_id    BIGINT NOT NULL,
    pid         BIGINT NOT NULL,
    issued_at   TIMESTAMPTZ NOT NULL,
    expires_at  TIMESTAMPTZ NOT NULL
);

-- Highest processed invalidation event version from the core (events outbox).
CREATE TABLE processed_events (
    id          INT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    version     BIGINT NOT NULL DEFAULT 0,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO processed_events (id, version) VALUES (1, 0);
