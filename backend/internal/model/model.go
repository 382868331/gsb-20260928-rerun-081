package model

import "time"

// NodeType enumerates executable node kinds.
type NodeType string

const (
	NodeStart    NodeType = "start"
	NodeTask     NodeType = "task"     // automatic activity
	NodeDecision NodeType = "decision" // condition branch
	NodeWait     NodeType = "wait"     // pause until external input
	NodeEnd      NodeType = "end"
)

// Edge carries an optional condition for decision branches.
// Empty condition means default / unconditional flow.
type Edge struct {
	ID        string `json:"id"`
	From      string `json:"from"`
	To        string `json:"to"`
	Condition string `json:"condition,omitempty"` // expression evaluated against instance data
	Label     string `json:"label,omitempty"`
}

type Node struct {
	ID           string   `json:"id"`
	Type         NodeType `json:"type"`
	Name         string   `json:"name"`
	CompensateTo string   `json:"compensateTo,omitempty"` // node id used to undo this task on failure
	X            int      `json:"x"`
	Y            int      `json:"y"`

	// Collaborative wait configuration (only meaningful for wait nodes).
	// When Participants is non-empty the wait step needs one confirmation
	// from every effective participant before it can complete; partial
	// input never finishes the step early.
	Participants   []string `json:"participants,omitempty"`
	AllowDelegate  bool     `json:"allowDelegate,omitempty"`
	TimeoutSeconds int64    `json:"timeoutSeconds,omitempty"`
	// TimeoutPolicy decides the outcome once the deadline passes:
	// "approve" completes the step, anything else (default) rejects it and
	// the wait fails into compensation.
	TimeoutPolicy string `json:"timeoutPolicy,omitempty"`
}

// Definition is an immutable published version of a process.
type Definition struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Version     int       `json:"version"`
	Nodes       []Node    `json:"nodes"`
	Edges       []Edge    `json:"edges"`
	PublishedAt time.Time `json:"publishedAt"`
}

type InstanceStatus string

const (
	StatusRunning   InstanceStatus = "running"
	StatusWaiting   InstanceStatus = "waiting"
	StatusCompleted InstanceStatus = "completed"
	StatusFailed    InstanceStatus = "failed"
)

type NodeStatus string

const (
	NodeEntered      NodeStatus = "entered"
	NodeSucceeded    NodeStatus = "succeeded"
	NodeFailed       NodeStatus = "failed"
	NodeCompensating NodeStatus = "compensating"
	NodeCompensated  NodeStatus = "compensated"
	NodeCompFailed   NodeStatus = "compensation_failed"
)

// StepRecord is one real engine event. The UI renders arrows only from these records.
type StepRecord struct {
	Seq        int        `json:"seq"`
	NodeID     string     `json:"nodeId"`
	EdgeID     string     `json:"edgeId,omitempty"` // edge actually traversed to enter node
	Action     string     `json:"action"`           // enter / succeed / wait / fail / compensate / complete ...
	Status     NodeStatus `json:"status,omitempty"`
	Detail     string     `json:"detail,omitempty"`
	OccurredAt time.Time  `json:"occurredAt"`
}

type Instance struct {
	ID           string         `json:"id"`
	DefinitionID string         `json:"definitionId"` // pinned definition version
	Version      int            `json:"version"`
	Status       InstanceStatus `json:"status"`
	Data         map[string]any `json:"data"`
	CurrentNode  string         `json:"currentNode,omitempty"`
	WaitNode     string         `json:"waitNode,omitempty"`
	Records      []StepRecord   `json:"records"`
	// ConsumedInputs gives idempotency: same external input key advances only once.
	ConsumedInputs map[string]bool `json:"consumedInputs"`
	StoppedAt      string          `json:"stoppedAt,omitempty"` // precise node where execution stopped (incl. compensation failure)

	// Crash-safe compensation journal. When a task fails the instance enters
	// the compensating phase until every succeeded task is undone (or one
	// compensation itself fails). A restart mid-compensation resumes from this
	// journal instead of re-running business tasks or completed compensations.
	Compensating     bool            `json:"compensating,omitempty"`
	FailedNode       string          `json:"failedNode,omitempty"` // node whose failure triggered compensation
	CompensatedNodes map[string]bool `json:"compensatedNodes,omitempty"`

	// DefinitionHistory audits explicit migrations of waiting instances. Old
	// step records always keep the definition version they happened under; the
	// histories of two versions are never spliced together.
	DefinitionHistory []DefinitionRef `json:"definitionHistory,omitempty"`

	// Wait is the live collaborative-wait state, present only while the
	// instance waits at a multi-party wait node. Persisted with the instance
	// so a restart keeps waiting with every confirmation intact.
	Wait *WaitState `json:"wait,omitempty"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// DefinitionRef records that an instance was pinned to a definition version.
type DefinitionRef struct {
	DefinitionID string    `json:"definitionId"`
	Name         string    `json:"name"`
	Version      int       `json:"version"`
	Since        time.Time `json:"since"`
}

type ExternalInput struct {
	WaitNode string         `json:"waitNode"`
	Key      string         `json:"key"`            // client-chosen idempotency key
	From     string         `json:"from,omitempty"` // participant id for collaborative waits
	Payload  map[string]any `json:"payload"`
}

// PartyInput is one confirmed participant input on a collaborative wait.
// AuthID ties it to the authorization scope that was in effect when the
// input was accepted, so a later decision is traceable to that scope.
type PartyInput struct {
	Key     string         `json:"key"`
	From    string         `json:"from"`
	Payload map[string]any `json:"payload,omitempty"`
	AuthID  string         `json:"authId"`
	At      time.Time      `json:"at"`
}

// WaitAuthorization is one version of the acceptance scope of a
// collaborative wait: who may confirm, and whether delegation is allowed.
// Every scope change appends a new record; old ones are never rewritten.
type WaitAuthorization struct {
	ID            string            `json:"id"`
	Participants  []string          `json:"participants"`
	AllowDelegate bool              `json:"allowDelegate"`
	Delegations   map[string]string `json:"delegations,omitempty"` // participant -> delegate
	Since         time.Time         `json:"since"`
	Reason        string            `json:"reason,omitempty"`
	// PriorInputsKept records whether inputs accepted under earlier
	// authorizations remained valid when this scope took effect.
	PriorInputsKept bool `json:"priorInputsKept"`
}

// WaitState is the durable state of an in-progress collaborative wait.
// It survives save/restart so confirmations are never re-executed.
type WaitState struct {
	AuthID         string                `json:"authId"`
	Received       map[string]PartyInput `json:"received"`       // effective participant -> confirmation
	Authorizations []WaitAuthorization   `json:"authorizations"` // full history, last is current
	Deadline       *time.Time            `json:"deadline,omitempty"`
	Decided        bool                  `json:"decided,omitempty"`
	Decision       string                `json:"decision,omitempty"`       // approved | rejected | timeout_approved | timeout_rejected
	DecisionAuthID string                `json:"decisionAuthId,omitempty"` // scope in effect at decision time
}
