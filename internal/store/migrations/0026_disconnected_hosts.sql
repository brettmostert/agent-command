-- The Remote hosts you disconnected (PAN-194): nothing dials them until you
-- connect them again. By name, for config.toml's hosts as for the added ones.
CREATE TABLE remote_hosts_disconnected (
    name            TEXT PRIMARY KEY,
    disconnected_at INTEGER NOT NULL
);
