# Project Memory

## Tech Stack
- Go 1.23+, pure Go SQLite (modernc.org/sqlite, CGO_ENABLED=0)
- net/http (no framework), gorilla/websocket
- Docker scratch base image for minimal size

## Key Design Decisions
- Use modernc.org/sqlite instead of mattn/go-sqlite3 for CGO-free builds
- DB connection pool: max_open=2, max_idle=1 for low-resource VPS
- Event delivery batch size: configurable, default 10
- Exponential backoff for retries: initial 5s, max 300s, max 10 retries
- Docker image: registry.nkit.top/openclaw/github-webhook-relay

## Project Structure
- cmd/server/main.go: HTTP server setup, route registration, graceful shutdown
- internal/config: env var based configuration
- internal/storage: SQLite operations (events, agents, logs)
- internal/github: webhook handler with HMAC-SHA256 verification
- internal/websocket: WebSocket handler with auth, heartbeat, event delivery
- internal/relay: core relay logic (dispatch, ACK, retry)
- internal/admin: health/status/events/retry endpoints
- migrations/: SQL migrations
