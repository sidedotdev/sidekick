CREATE TABLE IF NOT EXISTS remote_devices (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    token_hash TEXT NOT NULL,
    created DATETIME NOT NULL,
    last_used DATETIME NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_remote_devices_token_hash ON remote_devices (token_hash);