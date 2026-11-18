// Package contracts holds the domain entities shared by every module.
package contracts

import "time"

type RunStatus string

const (
	RunPending     RunStatus = "pending"
	RunPlanning    RunStatus = "planning"
	RunExecuting   RunStatus = "executing"
	RunReviewing   RunStatus = "reviewing"
	RunValidating  RunStatus = "validating"
	RunCompleted   RunStatus = "completed"
	RunFailed      RunStatus = "failed"
	RunCancelled   RunStatus = "cancelled"
	RunInterrupted RunStatus = "interrupted"
)

// Terminal reports whether no orchestrator process should own the run any more.
// Interrupted is not terminal: it can be resumed.
func (s RunStatus) Terminal() bool {
	return s == RunCompleted || s == RunFailed || s == RunCancelled
}

type TaskStatus string

const (
	TaskPending   TaskStatus = "pending"
	TaskReady     TaskStatus = "ready"
	TaskAssigned  TaskStatus = "assigned"
	TaskRunning   TaskStatus = "running"
	TaskBlocked   TaskStatus = "blocked"
	TaskReviewing TaskStatus = "reviewing"
	TaskCompleted TaskStatus = "completed"
	TaskFailed    TaskStatus = "failed"
	TaskCancelled TaskStatus = "cancelled"
)

type Role string

const (
	RoleResearcher Role = "researcher"
	RoleDeveloper  Role = "developer"
	RoleReviewer   Role = "reviewer"
	RoleValidator  Role = "validator"
	// RolePlanner is not a task role; it only selects the agent that plans.
	RolePlanner Role = "planner"
)

// TaskRoles are the roles a plan may assign to a task.
var TaskRoles = []Role{RoleResearcher, RoleDeveloper, RoleReviewer, RoleValidator}

func (r Role) ValidForTask() bool {
	for _, x := range TaskRoles {
		if r == x {
			return true
		}
	}
	return false
}

// ReadOnly roles must not modify project files.
func (r Role) ReadOnly() bool { return r == RoleResearcher || r == RoleReviewer || r == RolePlanner }

type ExecutionStatus string

const (
	ExecRunning     ExecutionStatus = "running"
	ExecCompleted   ExecutionStatus = "completed"
	ExecFailed      ExecutionStatus = "failed"
	ExecCancelled   ExecutionStatus = "cancelled"
	ExecInterrupted ExecutionStatus = "interrupted" // orchestrator died; outcome not yet reconciled
	ExecUnknown     ExecutionStatus = "unknown"     // reconciled: agent gone, no report — outcome unknowable
)

type ArtifactType string

const (
	ArtifactReport     ArtifactType = "report"
	ArtifactDiff       ArtifactType = "diff"
	ArtifactTestResult ArtifactType = "test-result"
	ArtifactLog        ArtifactType = "log"
	ArtifactPatch      ArtifactType = "patch"
)

type Run struct {
	ID          string
	ProjectPath string
	UserRequest string
	Status      RunStatus
	CreatedAt   time.Time
	StartedAt   *time.Time
	CompletedAt *time.Time
	HeartbeatAt *time.Time // liveness of the owning orchestrator process
	Error       string
	Metadata    map[string]string
}

type Task struct {
	ID                 string
	RunID              string
	Key                string // plan-level id, unique within the run; safe for branch and path names
	Title              string
	Description        string
	Role               Role
	Status             TaskStatus
	Dependencies       []string // task IDs
	AcceptanceCriteria []string
	AssignedAgentID    string
	WorktreePath       string
	BranchName         string
	Attempts           int
	MaxAttempts        int
	CreatedAt          time.Time
	StartedAt          *time.Time
	CompletedAt        *time.Time
	Error              string
}

type Agent struct {
	ID               string
	RunID            string
	Name             string // runtime target name, unique among live agents
	Kind             string
	Role             Role
	Status           string // last runtime status observed
	WorkspaceID      string
	PaneID           string
	RuntimeSessionID string
	CurrentTaskID    string
	LastHeartbeat    *time.Time
	Metadata         map[string]string
}

type Execution struct {
	ID          string
	TaskID      string
	AgentID     string // empty when the orchestrator ran a command itself
	Attempt     int
	Status      ExecutionStatus
	Prompt      string // prompt file path, or the command line for validation commands
	Output      string // redacted tail; the full output is an artifact
	StartedAt   time.Time
	CompletedAt *time.Time
	ExitCode    *int // only for commands the orchestrator executed itself
	Error       string
}

type Artifact struct {
	ID          string
	RunID       string
	TaskID      string
	Type        ArtifactType
	Path        string
	Description string
	CreatedAt   time.Time
}

type Event struct {
	// Seq is the event's position in the append-only log. It strictly grows
	// with insertion order across all runs and processes; readers use it as
	// a cursor. Zero until the event is stored.
	Seq       int64
	ID        string
	RunID     string
	TaskID    string
	AgentID   string
	Type      string
	Payload   map[string]any
	CreatedAt time.Time
}
