-- acmd:foreign-keys-off
-- There's no Default group: a session can be in no group (group_id NULL).
-- Changing group_id from NOT NULL means rebuilding sessions (SQLite's own
-- steps), with foreign keys off so dropping the old table cascades nothing.
CREATE TABLE sessions_new (
    id                TEXT PRIMARY KEY,
    name              TEXT NOT NULL DEFAULT '',
    description       TEXT NOT NULL DEFAULT '',
    group_id          TEXT REFERENCES groups (id),
    name_source       TEXT NOT NULL DEFAULT 'auto',
    cwd               TEXT NOT NULL DEFAULT '',
    claude_session_id TEXT NOT NULL DEFAULT '',
    claude_history    TEXT NOT NULL DEFAULT '[]', -- JSON array of previous Claude session ids
    transcript_path   TEXT NOT NULL DEFAULT '',
    status            TEXT NOT NULL DEFAULT '',
    status_detail     TEXT NOT NULL DEFAULT '',
    unseen            INTEGER NOT NULL DEFAULT 0,
    kind              TEXT NOT NULL DEFAULT 'background',
    pid               INTEGER NOT NULL DEFAULT 0,
    created_at        INTEGER NOT NULL,
    last_event_at     INTEGER NOT NULL DEFAULT 0,
    archived_at       INTEGER NOT NULL DEFAULT 0,
    harness_kind      TEXT NOT NULL DEFAULT 'claude',
    remote            INTEGER NOT NULL DEFAULT 0,
    host              TEXT NOT NULL DEFAULT '',
    remote_id         TEXT NOT NULL DEFAULT '',
    host_gone         INTEGER NOT NULL DEFAULT 0,
    job_id            TEXT NOT NULL DEFAULT '',
    set_id            TEXT NOT NULL DEFAULT '',
    set_order         INTEGER NOT NULL DEFAULT 0,
    status_since      INTEGER NOT NULL DEFAULT 0,
    harness_id        TEXT NOT NULL DEFAULT ''
);

INSERT INTO sessions_new (id, name, description, group_id, name_source, cwd, claude_session_id, claude_history,
    transcript_path, status, status_detail, unseen, kind, pid, created_at, last_event_at, archived_at, harness_kind,
    remote, host, remote_id, host_gone, job_id, set_id, set_order, status_since, harness_id)
SELECT id, name, description,
    CASE WHEN group_id IN (SELECT id FROM groups WHERE is_default = 1) OR group_id = '' THEN NULL ELSE group_id END,
    name_source, cwd, claude_session_id, claude_history, transcript_path, status, status_detail, unseen, kind, pid,
    created_at, last_event_at, archived_at, harness_kind, remote, host, remote_id, host_gone, job_id, set_id,
    set_order, status_since, harness_id
FROM sessions;

DROP TABLE sessions;
ALTER TABLE sessions_new RENAME TO sessions;

CREATE INDEX sessions_claude_session_id ON sessions (claude_session_id);
CREATE INDEX sessions_group ON sessions (group_id);
CREATE UNIQUE INDEX sessions_host_remote ON sessions (host, remote_id) WHERE host != '';
CREATE INDEX sessions_set ON sessions (set_id) WHERE set_id != '';

DELETE FROM groups WHERE is_default = 1;
ALTER TABLE groups DROP COLUMN is_default;
