 docker compose exec -it db psql -U cfgmgr -d cfgmgr
 
 
CREATE TABLE mcpgwbasic (
    id SERIAL PRIMARY KEY,
    gateway_name TEXT NOT NULL UNIQUE,
    gateway_svc_port INTEGER NOT NULL UNIQUE,
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS users (
  username TEXT PRIMARY KEY,
  password TEXT NOT NULL,
  api_token TEXT NOT NULL UNIQUE,
  expired BOOLEAN NOT NULL DEFAULT FALSE
);


DROP TABLE mcpgwbasic;

docker compose up -d --force-recreate gwClient