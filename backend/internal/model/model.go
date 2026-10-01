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
	Key      string         `json:"key"` // client-chosen idempotency key
	Payload  map[string]any `json:"payload"`
}
