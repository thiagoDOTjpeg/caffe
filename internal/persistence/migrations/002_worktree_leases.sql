CREATE TABLE worktree_leases (
    path         TEXT PRIMARY KEY,
    holder_kind  TEXT NOT NULL,
    holder_id    TEXT NOT NULL,
    run_id       TEXT NOT NULL DEFAULT '',
    task_id      TEXT NOT NULL DEFAULT '',
    acquired_at  TEXT NOT NULL,
    heartbeat_at TEXT NOT NULL
);
