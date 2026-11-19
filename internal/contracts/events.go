package contracts

const (
	EventRunCreated  = "run.created"
	EventTaskCreated = "task.created"

	EventAgentStarted     = "agent.started"
	EventAgentStopped     = "agent.stopped"
	EventExecutionStarted = "execution.started"
	EventArtifactAdded    = "artifact.added"

	EventAgentBlocked      = "agent.blocked"
	EventWorktreeCreated   = "worktree.created"
	EventWorktreeRemoved   = "worktree.removed"
	EventDiffCollected     = "diff.collected"
	EventValidationCommand = "validation.command"
	EventWarning           = "warning"
)

func RunEvent(s RunStatus) string { return "run." + string(s) }

func TaskEvent(s TaskStatus) string { return "task." + string(s) }

func ExecutionEvent(s ExecutionStatus) string { return "execution." + string(s) }
