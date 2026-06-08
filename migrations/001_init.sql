PRAGMA journal_mode=WAL;
PRAGMA synchronous=NORMAL;
PRAGMA busy_timeout=5000;

CREATE TABLE IF NOT EXISTS events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    delivery_id TEXT NOT NULL UNIQUE,
    github_event TEXT NOT NULL,
    repository_full_name TEXT,
    installation_id INTEGER,
    payload_json TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    retry_count INTEGER NOT NULL DEFAULT 0,
    next_retry_at DATETIME,
    last_error TEXT,
    received_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    acked_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_events_status ON events(status);
CREATE INDEX IF NOT EXISTS idx_events_delivery_id ON events(delivery_id);
CREATE INDEX IF NOT EXISTS idx_events_next_retry ON events(next_retry_at);

CREATE TABLE IF NOT EXISTS agents (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    agent_id TEXT NOT NULL UNIQUE,
    connected INTEGER NOT NULL DEFAULT 0,
    last_seen_at DATETIME,
    last_ack_event_id INTEGER,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS event_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id INTEGER,
    level TEXT NOT NULL,
    message TEXT NOT NULL,
    created_at DATETIME NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_event_logs_event_id ON event_logs(event_id);
CREATE INDEX IF NOT EXISTS idx_event_logs_created_at ON event_logs(created_at);
