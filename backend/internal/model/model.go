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
	// Collab configures a wait node as a multi-party collaborative step.
	// Nil/omitted means the classic single external-input wait.
	Collab *CollabSpec `json:"collab,omitempty"`
	X      int         `json:"x"`
	Y      int         `json:"y"`
}

// TimeoutPolicy decides a collaborative wait when its deadline is reached.
type TimeoutPolicy string

const (
	// TimeoutReject: decision is rejected unless enough approvals arrived.
	TimeoutReject TimeoutPolicy = "reject"
	// TimeoutApprove: decision is approved automatically on deadline.
	TimeoutApprove TimeoutPolicy = "approve"
	// TimeoutMajority: side with the majority of votes received by deadline;
	// ties reject.
	TimeoutMajority TimeoutPolicy = "majority"
)

// CollabSpec declares who must take part in a collaborative wait step,
// whether delegation is allowed, and how the deadline is resolved.
type CollabSpec struct {
	Participants []string `json:"participants"`
	Delegatable  bool     `json:"delegatable"`
	// DeadlineSeconds is measured from the moment the instance enters the
	// wait node. Zero means no deadline (the step can only finish by votes).
	DeadlineSeconds int           `json:"deadlineSeconds,omitempty"`
	TimeoutPolicy   TimeoutPolicy `json:"timeoutPolicy,omitempty"`
	// ApproveThreshold is how many approvals are required to approve.
	// 0 means every participant then in scope must approve (unanimous).
	ApproveThreshold int `json:"approveThreshold,omitempty"`
}

// VoteKind is one participant's stance.
type VoteKind string

const (
	VoteApprove VoteKind = "approve"
	VoteReject  VoteKind = "reject"
)

// CollabVote is one effective or invalidated party input.
type CollabVote struct {
	Key           string    `json:"key"` // idempotency key
	Voter         string    `json:"voter"`
	OnBehalfOf    string    `json:"onBehalfOf,omitempty"` // original participant when cast by a delegate
	Kind          VoteKind  `json:"kind"`
	ViaGrantID    string    `json:"viaGrantId,omitempty"` // grant under which a delegate voted
	Valid         bool      `json:"valid"`
	InvalidReason string    `json:"invalidReason,omitempty"`
	At            time.Time `json:"at"`
}

// GrantAction mutates the set of parties allowed to act.
type GrantAction string

const (
	GrantDelegate    GrantAction = "delegate" // participant adds a delegate
	GrantRevoke      GrantAction = "revoke"   // participant withdraws a delegation
	GrantScopeAdd    GrantAction = "scope_add"
	GrantScopeRemove GrantAction = "scope_remove"
)

// GrantRecord is an append-only authorization-history entry. Old and new
// records are both kept forever so any later decision can be traced to the
// party scope that was effective at that time.
type GrantRecord struct {
	Seq    int         `json:"seq"`
	Action GrantAction `json:"action"`
	Actor  string      `json:"actor"`           // participant/owner performing the change
	Party  string      `json:"party,omitempty"` // delegate or added/removed participant
	// KeepPriorInputs says whether inputs given before this change remain
	// valid. Revocation/scope removal records it explicitly.
	KeepPriorInputs bool      `json:"keepPriorInputs"`
	Note            string    `json:"note,omitempty"`
	At              time.Time `json:"at"`
}

// CollabDecision is the single definitive outcome of a collaborative wait.
type CollabDecision struct {
	Outcome    string `json:"outcome"` // approved / rejected / timeout_approved / timeout_rejected
	Reason     string `json:"reason"`
	Approvals  int    `json:"approvals"`
	Rejections int    `json:"rejections"`
	Threshold  int    `json:"threshold"`
	// ScopeSnapshot is the exact set of participants whose votes were counted,
	// i.e. the scope effective when the decision was made.
	ScopeSnapshot []string  `json:"scopeSnapshot"`
	At            time.Time `json:"at"`
}

// CollabState is the persisted runtime state of one collaborative wait.
type CollabState struct {
	WaitNode string `json:"waitNode"`
	// Scope is the currently effective participant set (initial spec plus
	// scope mutations). Delegates are not stored here; they live in Grants.
	Scope    []string        `json:"scope"`
	Votes    []CollabVote    `json:"votes"`
	Grants   []GrantRecord   `json:"grants"`
	Deadline *time.Time      `json:"deadline,omitempty"`
	Decision *CollabDecision `json:"decision,omitempty"`
	Decided  bool            `json:"decided"`
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

	// Collabs holds runtime state per collaborative wait node, keyed by node
	// id. State of finished waits is retained so later (late) inputs can be
	// attached as history and decisions remain traceable after restart.
	Collabs map[string]*CollabState `json:"collabs,omitempty"`

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
	// Collaborative-wait fields (ignored by classic waits):
	Voter string   `json:"voter,omitempty"` // participant or delegate submitting
	Kind  VoteKind `json:"kind,omitempty"`  // approve / reject
}

// GrantRequest delegates, revokes, or changes the receiving scope of a
// collaborative wait while it is pending.
type GrantRequest struct {
	WaitNode string `json:"waitNode"`
	Actor    string `json:"actor"`
	Party    string `json:"party"`
	// KeepPriorInputs states whether votes already given stay valid after the
	// change. Defaults to true.
	KeepPriorInputs *bool  `json:"keepPriorInputs,omitempty"`
	Note            string `json:"note,omitempty"`
}
