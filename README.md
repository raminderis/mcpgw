# MCPGW

MCPGW is a dynamic Model Context Protocol gateway that routes MCP traffic, persists gateway runtime configuration in PostgreSQL, and applies port changes without manual edits to gateway code.

The current system is built as a set of small Go services:
- UI service for operator input
- cfgmgr for configuration persistence and change orchestration
- cfgUpdater for file-based runtime mutation and controlled restart trigger
- gwsvc for MCP traffic handling and forwarding to downstream MCP backends

## Why This Exists

Most early MCP gateways hardcode connection and bind settings. MCPGW moves runtime settings into control-plane flows so operators can change gateway behavior through API/UI actions.

Current implemented capability:
- Dynamic gateway listen port changes from UI input
- Persisted port state in PostgreSQL
- Controlled restart path that targets the old runtime port
- Config file mutation as the handoff mechanism to gwsvc

## Repository Layout

- `ui/main.go`: operator-facing configuration UI (Gin)
- `cfgmgr/cmd/cfgmgr/main.go`: cfgmgr HTTP server bootstrap
- `cfgmgr/internal/cfgmgr/cfgmgr.go`: request handler and orchestration logic
- `cfgmgr/internal/cfgmgr/cfgmgr_data.go`: PostgreSQL persistence and cfgUpdater trigger
- `gwsvc/cmd/cfgUpdater/main.go`: config writer and gwsvc restart trigger
- `gwsvc/cmd/gwsvc/main.go`: MCP gateway runtime, reload and shutdown endpoints
- `gwsvc/cmd/gwClient/main.go`: local test client for MCP calls

## Architecture

### Data Plane

- `gwsvc` exposes `/mcp`.
- For `tools/list`, `gwsvc` forwards to downstream Neo4j MCP endpoint and returns merged response behavior currently implemented in code path.
- Other MCP methods are handled by Go MCP SDK streamable HTTP handler.

### Control Plane

1. Operator submits form in UI (`POST /configure`).
2. UI parses `listenAddress` into an integer port and calls cfgmgr `POST /addgwconfig`.
3. cfgmgr reads existing port from DB, writes new port, and if changed, calls cfgUpdater with old and new port values.
4. cfgUpdater updates shared config file (`gwsvc-config.json`) with the new listen address.
5. cfgUpdater calls `gwsvc /shutdown` on the old port.
6. Orchestrator restarts `gwsvc`; new process binds using updated config.

This old-port shutdown targeting avoids a common race where shutdown is sent to a not-yet-bound new port.

## API Contracts

### UI

- `GET /`
- `POST /configure` (form fields)

UI sends JSON to cfgmgr:

```json
{
  "mcpgwname": "somegw",
  "port": 9095
}
```

### cfgmgr

- `POST /addgwconfig`

Behavior:
- Validates port in range `1..65535`
- Reads old port for gateway from `mcpgwbasic`
- Upserts new port
- If changed, invokes cfgUpdater

cfgmgr to cfgUpdater payload:

```json
{
  "oldGwsvcListenPort": 9090,
  "newGwsvcListenPort": 9095
}
```

### cfgUpdater

- `GET /health`
- `POST /update-config`

Accepted payloads:
- Preferred: `oldGwsvcListenPort` + `newGwsvcListenPort`
- Backward-compatible: `gwsvcListenPort` (legacy path)

### gwsvc

- `POST|GET /mcp`
- `POST|GET /reload`
- `POST|GET /shutdown`

`/shutdown` returns immediately and performs graceful server shutdown in a goroutine.

## Data Model

Table used by cfgmgr:
- `mcpgwbasic`
  - `gateway_name` (unique logical key)
  - `gateway_svc_port` (integer)

Upsert semantics ensure latest port wins per gateway name.

## Runtime Configuration Resolution

### gwsvc

`gwsvc` loads config from `GWSVC_CONFIG_FILE` (fallback `./gwsvc-config.json`).

Effective values:
- `GwsvcListenAddr`: from config, fallback `:9090`
- `Neo4jMCPURL`: from config, fallback env `NEO4J_MCP_ENDPOINT`, fallback default `http://localhost:9094/mcp`

### cfgUpdater

- Listens on `CFG_UPDATER_PORT` (default `:9091`)
- Writes config atomically by creating `*.tmp` and renaming

## Operational Notes

- Current `cfgmgr` server binds to `127.0.0.1:8091`; this is intentional for local-host control-plane usage.
- UI container uses `CFGMGR_URL` to reach cfgmgr.
- `gwsvc` service-to-service addressing uses container DNS names (example: `gwsvc`, `neo4j-mcp`).
- Secrets and local orchestration settings should not be committed.

## Local Development

### Prerequisites

- Go `1.26.x`
- PostgreSQL `16+`
- Optional: Docker and Docker Compose for multi-service runs

### Run cfgmgr

1. Set cfgmgr env vars in `cfgmgr/.env`.
2. Start:

```bash
go run ./cfgmgr/cmd/cfgmgr
```

### Run gwsvc

```bash
go run ./gwsvc/cmd/gwsvc/main.go
```

### Run cfgUpdater

```bash
go run ./gwsvc/cmd/cfgUpdater/main.go
```

### Run UI

```bash
go run ./ui/main.go
```

## Known Gaps and Next Hardening Steps

- Add structured logs and correlation IDs across UI -> cfgmgr -> cfgUpdater -> gwsvc.
- Add integration smoke tests for old-port to new-port transition.
- Add authentication and authorization on control-plane endpoints.
- Add retries/backoff policy for shutdown trigger call.
- Add a public `docker-compose.example.yml` without sensitive values.

## License

Repository currently does not define a top-level license file. Add one before broad external adoption.
