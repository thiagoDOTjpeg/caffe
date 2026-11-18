CREATE TABLE runs (
    id           TEXT PRIMARY KEY,
    project_path TEXT NOT NULL,
    user_request TEXT NOT NULL,
    status       TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    started_at   TEXT,
    completed_at TEXT,
    heartbeat_at TEXT,
    error        TEXT NOT NULL DEFAULT '',
    metadata     TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX idx_runs_status ON runs(status);

CREATE TABLE tasks (
    id                  TEXT PRIMARY KEY,
    run_id              TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    key                 TEXT NOT NULL,
    title               TEXT NOT NULL,
    description         TEXT NOT NULL,
    role                TEXT NOT NULL,
    status              TEXT NOT NULL,
    acceptance_criteria TEXT NOT NULL DEFAULT '[]',
    assigned_agent_id   TEXT NOT NULL DEFAULT '',
    worktree_path       TEXT NOT NULL DEFAULT '',
    branch_name         TEXT NOT NULL DEFAULT '',
    attempts            INTEGER NOT NULL DEFAULT 0,
    max_attempts        INTEGER NOT NULL DEFAULT 1,
    created_at          TEXT NOT NULL,
    started_at          TEXT,
    completed_at        TEXT,
    error               TEXT NOT NULL DEFAULT '',
    seq                 INTEGER NOT NULL DEFAULT 0,
    UNIQUE (run_id, key)
);
CREATE INDEX idx_tasks_run ON tasks(run_id, seq);

CREATE TABLE task_dependencies (
    task_id       TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    depends_on_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    PRIMARY KEY (task_id, depends_on_id)
);

CREATE TABLE agents (
    id                 TEXT PRIMARY KEY,
    run_id             TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    name               TEXT NOT NULL,
    kind               TEXT NOT NULL,
    role               TEXT NOT NULL,
    status             TEXT NOT NULL,
    workspace_id       TEXT NOT NULL DEFAULT '',
    pane_id            TEXT NOT NULL DEFAULT '',
    runtime_session_id TEXT NOT NULL DEFAULT '',
    current_task_id    TEXT NOT NULL DEFAULT '',
    last_heartbeat     TEXT,
    metadata           TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX idx_agents_run ON agents(run_id);

CREATE TABLE executions (
    id           TEXT PRIMARY KEY,
    task_id      TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    agent_id     TEXT NOT NULL DEFAULT '',
    attempt      INTEGER NOT NULL,
    status       TEXT NOT NULL,
    prompt       TEXT NOT NULL DEFAULT '',
    output       TEXT NOT NULL DEFAULT '',
    started_at   TEXT NOT NULL,
    completed_at TEXT,
    exit_code    INTEGER,
    error        TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_executions_task ON executions(task_id, started_at);

CREATE TABLE artifacts (
    id          TEXT PRIMARY KEY,
    run_id      TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    task_id     TEXT NOT NULL DEFAULT '',
    type        TEXT NOT NULL,
    path        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL
);
CREATE INDEX idx_artifacts_run ON artifacts(run_id, created_at);

CREATE TABLE events (
    id         TEXT PRIMARY KEY,
    run_id     TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    task_id    TEXT NOT NULL DEFAULT '',
    agent_id   TEXT NOT NULL DEFAULT '',
    type       TEXT NOT NULL,
    payload    TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL
);
CREATE INDEX idx_events_run ON events(run_id, created_at);
