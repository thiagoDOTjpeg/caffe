package contracts

// Event types. The names are part of the persisted contract: consumers
// (the CLI, Caffè Studio) filter on them, so a name is never changed or
// reused. Payload keys per type are documented in docs/architecture.md.
const (
	EventRunCreated  = "run.created"
	EventTaskCreated = "task.created"

	// Written by persistence in the transaction of the row they describe.
	EventAgentStarted     = "agent.started"
	EventAgentStopped     = "agent.stopped"
	EventExecutionStarted = "execution.started"
	EventArtifactAdded    = "artifact.added"

	// Written by the engine.
	EventAgentBlocked      = "agent.blocked"
	EventWorktreeCreated   = "worktree.created"
	EventWorktreeRemoved   = "worktree.removed"
	EventDiffCollected     = "diff.collected"
	EventValidationCommand = "validation.command"
	EventWarning           = "warning"
)

// RunEvent is the event type of a run entering a status ("run.<status>").
func RunEvent(s RunStatus) string { return "run." + string(s) }

// TaskEvent is the event type of a task entering a status ("task.<status>").
func TaskEvent(s TaskStatus) string { return "task." + string(s) }

// ExecutionEvent is the event type of an execution ending with a status
// ("execution.<status>"). Its start is EventExecutionStarted.
func ExecutionEvent(s ExecutionStatus) string { return "execution." + string(s) }
