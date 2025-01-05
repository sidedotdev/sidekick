--- Create provider_keys table
CREATE TABLE IF NOT EXISTS provider_keys (
    id TEXT PRIMARY KEY,
    nickname TEXT,
    provider_type TEXT NOT NULL,
    secret_manager_type TEXT NOT NULL,
    secret_name TEXT NOT NULL,
    created DATETIME NOT NULL,
    updated DATETIME NOT NULL
);