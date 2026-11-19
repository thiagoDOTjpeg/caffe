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

	RolePlanner Role = "planner"
)

var TaskRoles = []Role{RoleResearcher, RoleDeveloper, RoleReviewer, RoleValidator}

func (r Role) ValidForTask() bool {
	for _, x := range TaskRoles {
		if r == x {
			return true
		}
	}
	return false
}

func (r Role) ReadOnly() bool { return r == RoleResearcher || r == RoleReviewer || r == RolePlanner }

type ExecutionStatus string

const (
	ExecRunning     ExecutionStatus = "running"
	ExecCompleted   ExecutionStatus = "completed"
	ExecFailed      ExecutionStatus = "failed"
	ExecCancelled   ExecutionStatus = "cancelled"
	ExecInterrupted ExecutionStatus = "interrupted"
	ExecUnknown     ExecutionStatus = "unknown"
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
	HeartbeatAt *time.Time
	Error       string
	Metadata    map[string]string
}

type Task struct {
	ID                 string
	RunID              string
	Key                string
	Title              string
	Description        string
	Role               Role
	Status             TaskStatus
	Dependencies       []string
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
	Name             string
	Kind             string
	Role             Role
	Status           string
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
	AgentID     string
	Attempt     int
	Status      ExecutionStatus
	Prompt      string
	Output      string
	StartedAt   time.Time
	CompletedAt *time.Time
	ExitCode    *int
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
	Seq       int64
	ID        string
	RunID     string
	TaskID    string
	AgentID   string
	Type      string
	Payload   map[string]any
	CreatedAt time.Time
}
