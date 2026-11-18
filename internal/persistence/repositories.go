package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	c "caffe/internal/contracts"
)

// querier is satisfied by *sql.DB and *sql.Tx, so reads can run inside a
// read transaction (RunSnapshot) or on their own.
type querier interface {
	QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error)
}

type execer interface {
	ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error)
}

// ---------- events ----------

func insertEvent(ctx context.Context, x execer, e *c.Event) error {
	if e.ID == "" {
		e.ID = uuid.NewString()
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = Now()
	}
	if e.Payload == nil {
		e.Payload = map[string]any{}
	}
	res, err := x.ExecContext(ctx,
		`INSERT INTO events (id, run_id, task_id, agent_id, type, payload, created_at) VALUES (?,?,?,?,?,?,?)`,
		e.ID, e.RunID, e.TaskID, e.AgentID, e.Type, toJSON(e.Payload), ts(e.CreatedAt))
	if err != nil {
		return err
	}
	e.Seq, err = res.LastInsertId()
	return err
}

func (s *Store) AddEvent(ctx context.Context, e *c.Event) error { return insertEvent(ctx, s.db, e) }

// Events are append-only: nothing ever deletes from the events table, so its
// rowid only grows and serves as the cursor (Event.Seq) for incremental
// readers. Deleting events would break that; if retention is ever needed,
// migrate to an explicit AUTOINCREMENT sequence first.
const eventCols = `rowid, id, run_id, task_id, agent_id, type, payload, created_at`

func (s *Store) ListEvents(ctx context.Context, runID string) ([]c.Event, error) {
	return s.queryEvents(ctx, `SELECT `+eventCols+` FROM events WHERE run_id = ? ORDER BY created_at, rowid`, runID)
}

// EventsSince returns up to limit events with Seq > after, in insertion
// order, for one run or, with runID "", for all runs.
func (s *Store) EventsSince(ctx context.Context, after int64, runID string, limit int) ([]c.Event, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("EventsSince: limit must be positive, got %d", limit)
	}
	return s.queryEvents(ctx, `SELECT `+eventCols+` FROM events WHERE rowid > ? AND (? = '' OR run_id = ?) ORDER BY rowid LIMIT ?`,
		after, runID, runID, limit)
}

func (s *Store) queryEvents(ctx context.Context, q string, args ...any) ([]c.Event, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []c.Event
	for rows.Next() {
		var e c.Event
		var payload, created string
		if err := rows.Scan(&e.Seq, &e.ID, &e.RunID, &e.TaskID, &e.AgentID, &e.Type, &payload, &created); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(payload), &e.Payload); err != nil {
			return nil, fmt.Errorf("event %s payload: %w", e.ID, err)
		}
		e.CreatedAt = parseTS(created)
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---------- runs ----------

// CreateRun inserts a run and its creation event.
func (s *Store) CreateRun(ctx context.Context, r *c.Run) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO runs (id, project_path, user_request, status, created_at, started_at, completed_at, heartbeat_at, error, metadata)
			 VALUES (?,?,?,?,?,?,?,?,?,?)`,
			r.ID, r.ProjectPath, r.UserRequest, r.Status, ts(r.CreatedAt), tsPtr(r.StartedAt), tsPtr(r.CompletedAt),
			tsPtr(r.HeartbeatAt), r.Error, toJSON(r.Metadata))
		if err != nil {
			return err
		}
		return insertEvent(ctx, tx, &c.Event{RunID: r.ID, Type: c.EventRunCreated, Payload: map[string]any{"status": r.Status}})
	})
}

// UpdateRun moves a run from one status to another (compare-and-swap) and
// records the transition event in the same transaction.
func (s *Store) UpdateRun(ctx context.Context, r *c.Run, from c.RunStatus) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE runs SET status=?, started_at=?, completed_at=?, heartbeat_at=?, error=?, metadata=? WHERE id=? AND status=?`,
			r.Status, tsPtr(r.StartedAt), tsPtr(r.CompletedAt), tsPtr(r.HeartbeatAt), r.Error, toJSON(r.Metadata), r.ID, from)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("run %s is not %s: %w", r.ID, from, ErrConflict)
		}
		return insertEvent(ctx, tx, &c.Event{RunID: r.ID, Type: c.RunEvent(r.Status),
			Payload: map[string]any{"from": from, "to": r.Status, "error": r.Error}})
	})
}

// Heartbeat marks the run as owned by a live process and returns its current
// status, so the owner notices a cancellation requested by another process.
func (s *Store) Heartbeat(ctx context.Context, runID string) (c.RunStatus, error) {
	var st c.RunStatus
	err := s.db.QueryRowContext(ctx, `UPDATE runs SET heartbeat_at=? WHERE id=? RETURNING status`, ts(Now()), runID).Scan(&st)
	return st, err
}

const runCols = `id, project_path, user_request, status, created_at, started_at, completed_at, heartbeat_at, error, metadata`

func scanRun(row interface{ Scan(...any) error }) (*c.Run, error) {
	var r c.Run
	var created, meta string
	var started, completed, hb sql.NullString
	if err := row.Scan(&r.ID, &r.ProjectPath, &r.UserRequest, &r.Status, &created, &started, &completed, &hb, &r.Error, &meta); err != nil {
		return nil, err
	}
	r.CreatedAt, r.StartedAt, r.CompletedAt, r.HeartbeatAt = parseTS(created), parseTSPtr(started), parseTSPtr(completed), parseTSPtr(hb)
	if err := json.Unmarshal([]byte(meta), &r.Metadata); err != nil {
		return nil, fmt.Errorf("run %s metadata: %w", r.ID, err)
	}
	return &r, nil
}

// GetRun finds a run by id or unambiguous id prefix.
func (s *Store) GetRun(ctx context.Context, idOrPrefix string) (*c.Run, error) {
	return getRun(ctx, s.db, idOrPrefix)
}

func getRun(ctx context.Context, q querier, idOrPrefix string) (*c.Run, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+runCols+` FROM runs WHERE id LIKE ? ESCAPE '\' LIMIT 2`, likePrefix(idOrPrefix))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var found []*c.Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		found = append(found, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	switch len(found) {
	case 0:
		return nil, fmt.Errorf("run %q: %w", idOrPrefix, ErrNotFound)
	case 1:
		return found[0], nil
	}
	return nil, fmt.Errorf("run id prefix %q is ambiguous", idOrPrefix)
}

func likePrefix(p string) string {
	out := make([]byte, 0, len(p)+1)
	for i := 0; i < len(p); i++ {
		if p[i] == '%' || p[i] == '_' || p[i] == '\\' {
			out = append(out, '\\')
		}
		out = append(out, p[i])
	}
	return string(out) + "%"
}

// ListRuns returns runs newest first. activeOnly keeps non-terminal ones.
func (s *Store) ListRuns(ctx context.Context, activeOnly bool) ([]*c.Run, error) {
	q := `SELECT ` + runCols + ` FROM runs`
	if activeOnly {
		q += ` WHERE status NOT IN ('completed','failed','cancelled')`
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*c.Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---------- tasks ----------

// CreateTasks inserts a run's task graph atomically.
func (s *Store) CreateTasks(ctx context.Context, tasks []*c.Task) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		for i, t := range tasks {
			_, err := tx.ExecContext(ctx,
				`INSERT INTO tasks (id, run_id, key, title, description, role, status, acceptance_criteria, max_attempts, created_at, seq)
				 VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
				t.ID, t.RunID, t.Key, t.Title, t.Description, t.Role, t.Status, toJSON(t.AcceptanceCriteria), t.MaxAttempts, ts(t.CreatedAt), i)
			if err != nil {
				return fmt.Errorf("task %s: %w", t.Key, err)
			}
		}
		for _, t := range tasks {
			for _, d := range t.Dependencies {
				if _, err := tx.ExecContext(ctx, `INSERT INTO task_dependencies (task_id, depends_on_id) VALUES (?,?)`, t.ID, d); err != nil {
					return fmt.Errorf("task %s dependency: %w", t.Key, err)
				}
			}
			if err := insertEvent(ctx, tx, &c.Event{RunID: t.RunID, TaskID: t.ID, Type: c.EventTaskCreated,
				Payload: map[string]any{"key": t.Key, "role": t.Role}}); err != nil {
				return err
			}
		}
		return nil
	})
}

// UpdateTask persists a task's mutable fields, but only if its stored status
// is still `from`. Exactly one caller can win a given transition, which is
// what stops two workers from dispatching the same task.
func (s *Store) UpdateTask(ctx context.Context, t *c.Task, from c.TaskStatus, reason string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE tasks SET status=?, assigned_agent_id=?, worktree_path=?, branch_name=?, attempts=?, started_at=?, completed_at=?, error=?
			 WHERE id=? AND status=?`,
			t.Status, t.AssignedAgentID, t.WorktreePath, t.BranchName, t.Attempts, tsPtr(t.StartedAt), tsPtr(t.CompletedAt), t.Error, t.ID, from)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("task %s is not %s: %w", t.Key, from, ErrConflict)
		}
		return insertEvent(ctx, tx, &c.Event{RunID: t.RunID, TaskID: t.ID, AgentID: t.AssignedAgentID, Type: c.TaskEvent(t.Status),
			Payload: map[string]any{"key": t.Key, "from": from, "to": t.Status, "attempts": t.Attempts, "reason": reason}})
	})
}

func (s *Store) ListTasks(ctx context.Context, runID string) ([]*c.Task, error) {
	return listTasks(ctx, s.db, runID)
}

func listTasks(ctx context.Context, q querier, runID string) ([]*c.Task, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT id, run_id, key, title, description, role, status, acceptance_criteria, assigned_agent_id, worktree_path, branch_name,
		        attempts, max_attempts, created_at, started_at, completed_at, error
		 FROM tasks WHERE run_id = ? ORDER BY seq`, runID)
	if err != nil {
		return nil, err
	}
	var out []*c.Task
	byID := map[string]*c.Task{}
	for rows.Next() {
		var t c.Task
		var ac, created string
		var started, completed sql.NullString
		if err := rows.Scan(&t.ID, &t.RunID, &t.Key, &t.Title, &t.Description, &t.Role, &t.Status, &ac, &t.AssignedAgentID,
			&t.WorktreePath, &t.BranchName, &t.Attempts, &t.MaxAttempts, &created, &started, &completed, &t.Error); err != nil {
			rows.Close()
			return nil, err
		}
		if err := json.Unmarshal([]byte(ac), &t.AcceptanceCriteria); err != nil {
			rows.Close()
			return nil, fmt.Errorf("task %s criteria: %w", t.ID, err)
		}
		t.CreatedAt, t.StartedAt, t.CompletedAt = parseTS(created), parseTSPtr(started), parseTSPtr(completed)
		out = append(out, &t)
		byID[t.ID] = &t
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	deps, err := q.QueryContext(ctx,
		`SELECT d.task_id, d.depends_on_id FROM task_dependencies d JOIN tasks t ON t.id = d.depends_on_id
		 WHERE t.run_id = ? ORDER BY t.seq`, runID)
	if err != nil {
		return nil, err
	}
	defer deps.Close()
	for deps.Next() {
		var id, dep string
		if err := deps.Scan(&id, &dep); err != nil {
			return nil, err
		}
		if t := byID[id]; t != nil {
			t.Dependencies = append(t.Dependencies, dep)
		}
	}
	return out, deps.Err()
}

// ---------- agents ----------

// Agent status values written by the engine. Only the transitions into
// running and stopped are evented; "starting" is a placeholder before the
// runtime session exists.
const (
	agentRunning = "running"
	agentStopped = "stopped"
)

// SaveAgent upserts an agent. Moving into running or stopped records
// agent.started / agent.stopped in the same transaction.
func (s *Store) SaveAgent(ctx context.Context, a *c.Agent) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		var prevStatus, prevTask string
		err := tx.QueryRowContext(ctx, `SELECT status, current_task_id FROM agents WHERE id = ?`, a.ID).Scan(&prevStatus, &prevTask)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		_, err = tx.ExecContext(ctx,
			`INSERT INTO agents (id, run_id, name, kind, role, status, workspace_id, pane_id, runtime_session_id, current_task_id, last_heartbeat, metadata)
			 VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
			 ON CONFLICT(id) DO UPDATE SET status=excluded.status, workspace_id=excluded.workspace_id, pane_id=excluded.pane_id,
			   runtime_session_id=excluded.runtime_session_id, current_task_id=excluded.current_task_id,
			   last_heartbeat=excluded.last_heartbeat, metadata=excluded.metadata`,
			a.ID, a.RunID, a.Name, a.Kind, a.Role, a.Status, a.WorkspaceID, a.PaneID, a.RuntimeSessionID, a.CurrentTaskID,
			tsPtr(a.LastHeartbeat), toJSON(a.Metadata))
		if err != nil || a.Status == prevStatus {
			return err
		}
		var typ, taskID string
		switch a.Status {
		case agentRunning:
			typ, taskID = c.EventAgentStarted, a.CurrentTaskID
		case agentStopped:
			typ, taskID = c.EventAgentStopped, prevTask // the task is cleared when the agent stops
		default:
			return nil
		}
		return insertEvent(ctx, tx, &c.Event{RunID: a.RunID, TaskID: taskID, AgentID: a.ID, Type: typ, Payload: map[string]any{
			"name": a.Name, "kind": a.Kind, "role": a.Role, "workspace_id": a.WorkspaceID, "pane_id": a.PaneID}})
	})
}

// ListAgents returns the agents of one run, or of every run when runID is "".
func (s *Store) ListAgents(ctx context.Context, runID string) ([]*c.Agent, error) {
	return listAgents(ctx, s.db, runID)
}

func listAgents(ctx context.Context, q querier, runID string) ([]*c.Agent, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT id, run_id, name, kind, role, status, workspace_id, pane_id, runtime_session_id, current_task_id, last_heartbeat, metadata
		 FROM agents WHERE (? = '' OR run_id = ?) ORDER BY rowid`, runID, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*c.Agent
	for rows.Next() {
		var a c.Agent
		var hb sql.NullString
		var meta string
		if err := rows.Scan(&a.ID, &a.RunID, &a.Name, &a.Kind, &a.Role, &a.Status, &a.WorkspaceID, &a.PaneID, &a.RuntimeSessionID,
			&a.CurrentTaskID, &hb, &meta); err != nil {
			return nil, err
		}
		a.LastHeartbeat = parseTSPtr(hb)
		if err := json.Unmarshal([]byte(meta), &a.Metadata); err != nil {
			return nil, fmt.Errorf("agent %s metadata: %w", a.ID, err)
		}
		out = append(out, &a)
	}
	return out, rows.Err()
}

func (s *Store) GetAgent(ctx context.Context, id string) (*c.Agent, error) {
	all, err := s.ListAgents(ctx, "")
	if err != nil {
		return nil, err
	}
	for _, a := range all { // ponytail: linear scan, add a WHERE id=? query if agent counts get large
		if a.ID == id {
			return a, nil
		}
	}
	return nil, fmt.Errorf("agent %q: %w", id, ErrNotFound)
}

// ---------- executions ----------

// SaveExecution upserts an execution. The first save of an id records
// execution.started in the same transaction. Its end is evented by the
// engine (execution.<status>), which knows the outcome's context.
func (s *Store) SaveExecution(ctx context.Context, e *c.Execution) error {
	var exit any
	if e.ExitCode != nil {
		exit = *e.ExitCode
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		var exists int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM executions WHERE id = ?`, e.ID).Scan(&exists)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		_, err = tx.ExecContext(ctx,
			`INSERT INTO executions (id, task_id, agent_id, attempt, status, prompt, output, started_at, completed_at, exit_code, error)
			 VALUES (?,?,?,?,?,?,?,?,?,?,?)
			 ON CONFLICT(id) DO UPDATE SET agent_id=excluded.agent_id, status=excluded.status, output=excluded.output,
			   completed_at=excluded.completed_at, exit_code=excluded.exit_code, error=excluded.error`,
			e.ID, e.TaskID, e.AgentID, e.Attempt, e.Status, e.Prompt, e.Output, ts(e.StartedAt), tsPtr(e.CompletedAt), exit, e.Error)
		if err != nil || exists == 1 {
			return err
		}
		var runID string
		if err := tx.QueryRowContext(ctx, `SELECT run_id FROM tasks WHERE id = ?`, e.TaskID).Scan(&runID); err != nil {
			return err
		}
		return insertEvent(ctx, tx, &c.Event{RunID: runID, TaskID: e.TaskID, AgentID: e.AgentID, Type: c.EventExecutionStarted,
			Payload: map[string]any{"execution_id": e.ID, "attempt": e.Attempt, "status": e.Status}})
	})
}

// ListExecutions returns a run's executions, oldest first.
func (s *Store) ListExecutions(ctx context.Context, runID string) ([]*c.Execution, error) {
	return listExecutions(ctx, s.db, runID)
}

func listExecutions(ctx context.Context, q querier, runID string) ([]*c.Execution, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT e.id, e.task_id, e.agent_id, e.attempt, e.status, e.prompt, e.output, e.started_at, e.completed_at, e.exit_code, e.error
		 FROM executions e JOIN tasks t ON t.id = e.task_id WHERE t.run_id = ? ORDER BY e.started_at, e.rowid`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*c.Execution
	for rows.Next() {
		var e c.Execution
		var started string
		var completed sql.NullString
		var exit sql.NullInt64
		if err := rows.Scan(&e.ID, &e.TaskID, &e.AgentID, &e.Attempt, &e.Status, &e.Prompt, &e.Output, &started, &completed, &exit, &e.Error); err != nil {
			return nil, err
		}
		e.StartedAt, e.CompletedAt = parseTS(started), parseTSPtr(completed)
		if exit.Valid {
			n := int(exit.Int64)
			e.ExitCode = &n
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}

// ---------- artifacts ----------

func (s *Store) AddArtifact(ctx context.Context, a *c.Artifact) error {
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = Now()
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO artifacts (id, run_id, task_id, type, path, description, created_at) VALUES (?,?,?,?,?,?,?)`,
			a.ID, a.RunID, a.TaskID, a.Type, a.Path, a.Description, ts(a.CreatedAt)); err != nil {
			return err
		}
		return insertEvent(ctx, tx, &c.Event{RunID: a.RunID, TaskID: a.TaskID, Type: c.EventArtifactAdded,
			Payload: map[string]any{"artifact_id": a.ID, "type": a.Type, "path": a.Path, "description": a.Description}})
	})
}

func (s *Store) ListArtifacts(ctx context.Context, runID string) ([]*c.Artifact, error) {
	return listArtifacts(ctx, s.db, runID)
}

func listArtifacts(ctx context.Context, q querier, runID string) ([]*c.Artifact, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT id, run_id, task_id, type, path, description, created_at FROM artifacts WHERE run_id = ? ORDER BY created_at, rowid`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*c.Artifact
	for rows.Next() {
		var a c.Artifact
		var created string
		if err := rows.Scan(&a.ID, &a.RunID, &a.TaskID, &a.Type, &a.Path, &a.Description, &created); err != nil {
			return nil, err
		}
		a.CreatedAt = parseTS(created)
		out = append(out, &a)
	}
	return out, rows.Err()
}

// ---------- recovery ----------

// MarkStaleRunsInterrupted flags runs whose owning process stopped heartbeating
// as interrupted, together with their running executions. It decides nothing
// about the work itself: that is reconciled on resume.
func (s *Store) MarkStaleRunsInterrupted(ctx context.Context, staleAfter time.Duration) ([]string, error) {
	cutoff := ts(Now().Add(-staleAfter))
	var ids []string
	err := s.tx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx,
			`SELECT id FROM runs WHERE status IN ('pending','planning','executing','reviewing','validating')
			 AND COALESCE(heartbeat_at, created_at) < ?`, cutoff)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		for _, id := range ids {
			if _, err := tx.ExecContext(ctx, `UPDATE runs SET status='interrupted' WHERE id=?`, id); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx,
				`UPDATE executions SET status='interrupted' WHERE status='running' AND task_id IN (SELECT id FROM tasks WHERE run_id=?)`, id); err != nil {
				return err
			}
			if err := insertEvent(ctx, tx, &c.Event{RunID: id, Type: c.RunEvent(c.RunInterrupted),
				Payload: map[string]any{"reason": "owning process stopped heartbeating"}}); err != nil {
				return err
			}
		}
		return nil
	})
	return ids, err
}
