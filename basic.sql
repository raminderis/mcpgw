 docker compose exec -it db psql -U cfgmgr -d cfgmgr
 
 
CREATE TABLE mcpgwbasic (
    id SERIAL PRIMARY KEY,
    gateway_name TEXT NOT NULL UNIQUE,
    gateway_svc_port INTEGER NOT NULL UNIQUE,
    created_at TIMESTAMP DEFAULT NOW()
);


CREATE TABLE IF NOT EXISTS users (
  username TEXT PRIMARY KEY,
  password_hash TEXT NOT NULL,
  api_token TEXT NOT NULL UNIQUE,
  expired BOOLEAN NOT NULL DEFAULT FALSE
);

CREATE TABLE policies (
    id UUID PRIMARY KEY,
    policy_id TEXT UNIQUE NOT NULL,
    name TEXT NOT NULL,

    remote_mcp_service TEXT NOT NULL,
    resource_access_request TEXT NOT NULL,
    environment TEXT NOT NULL,

    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    priority INT DEFAULT 100,

    conditions JSONB NOT NULL,

    description TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);


CREATE TABLE registries (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL,
    type        TEXT NOT NULL,
    endpoint    TEXT NOT NULL,
    protocol    TEXT NOT NULL DEFAULT 'http',
    auth_required BOOLEAN NOT NULL DEFAULT false,
    policy_ids  TEXT[] NOT NULL DEFAULT '{}',
    metadata    JSONB NOT NULL DEFAULT '{}',
    status      TEXT NOT NULL DEFAULT 'active',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_registries_name   ON registries (name);
CREATE INDEX idx_registries_type   ON registries (type);
CREATE INDEX idx_registries_status ON registries (status);


CREATE INDEX idx_policies_lookup
    ON policies (remote_mcp_service, resource_access_request, environment)
    WHERE enabled = TRUE;

CREATE INDEX IF NOT EXISTS idx_users_username_token_expired
  ON users (username, api_token, expired);


DROP TABLE mcpgwbasic;

docker compose up -d --force-recreate gwClient