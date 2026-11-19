-- Ownership of a task worktree across processes (docs/studio/architecture.md
-- §9.2): an agent while it writes, or a human editing in Studio.
CREATE TABLE worktree_leases (
    path         TEXT PRIMARY KEY,
    holder_kind  TEXT NOT NULL,      -- 'agent' | 'human'
    holder_id    TEXT NOT NULL,      -- execution id | studio instance id
    run_id       TEXT NOT NULL DEFAULT '',
    task_id      TEXT NOT NULL DEFAULT '',
    acquired_at  TEXT NOT NULL,
    heartbeat_at TEXT NOT NULL
);
