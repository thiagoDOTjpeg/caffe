CREATE TABLE integration_decisions (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id     TEXT NOT NULL,
    task_id    TEXT NOT NULL,
    decision   TEXT NOT NULL,
    note       TEXT NOT NULL DEFAULT '',
    author     TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);
CREATE INDEX idx_integration_decisions_run ON integration_decisions(run_id, id);
