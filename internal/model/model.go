package model

import "time"

// Definition 是一个流程定义版本。发布后内容不可变；正在运行的实例
// 保存自己创建时的版本快照，因此后续发布新版本不会影响旧实例。
type Definition struct {
	Key       string    `json:"key"`
	Version   int       `json:"version"`
	Name      string    `json:"name"`
	Nodes     []Node    `json:"nodes"`
	Edges     []Edge    `json:"edges"`
	CreatedAt time.Time `json:"createdAt"`
}

type Position struct {
	X int `json:"x"`
	Y int `json:"y"`
}

type Node struct {
	ID          string   `json:"id"`
	Kind        NodeKind `json:"kind"`
	Name        string   `json:"name"`
	Pos         Position `json:"pos"`
	Condition   string   `json:"condition,omitempty"`   // condition 节点：表达式为真走 true 边
	InputKey    string   `json:"inputKey,omitempty"`    // wait 节点：等待的外部输入键
	TimeoutSecs int      `json:"timeoutSecs,omitempty"` // 0 表示永不超时
	Compensate  string   `json:"compensate,omitempty"`  // task 节点失败时调用的补偿任务 ID
}

type NodeKind string

const (
	KindStart     NodeKind = "start"
	KindTask      NodeKind = "task"
	KindCondition NodeKind = "condition"
	KindWait      NodeKind = "wait"
	KindEnd       NodeKind = "end"
)

type Edge struct {
	ID     string     `json:"id"`
	From   string     `json:"from"`
	To     string     `json:"to"`
	Branch EdgeBranch `json:"branch,omitempty"`
}

type EdgeBranch string

const (
	BranchDefault EdgeBranch = ""
	BranchTrue    EdgeBranch = "true"
	BranchFalse   EdgeBranch = "false"
)

// Instance 是流程实例。Definition 是创建时固化的版本快照。
type Instance struct {
	ID          string             `json:"id"`
	DefKey      string             `json:"defKey"`
	Version     int                `json:"version"`
	Definition  Definition         `json:"definition"`
	Status      InstanceStatus     `json:"status"`
	Variables   map[string]any     `json:"variables"`
	Current     []string           `json:"current"`    // 挂起时停留的节点（等待输入可能多个）
	WaitingFor  map[string]string  `json:"waitingFor"` // nodeID -> inputKey
	Steps       []StepRecord       `json:"steps"`
	Transitions []TransitionRecord `json:"transitions"`
	Inputs      []InputRecord      `json:"inputs"`
	Error       string             `json:"error,omitempty"`
	CreatedAt   time.Time          `json:"createdAt"`
	UpdatedAt   time.Time          `json:"updatedAt"`
}

type InstanceStatus string

const (
	StatusRunning      InstanceStatus = "running"      // 引擎仍可推进
	StatusWaiting      InstanceStatus = "waiting"      // 等待外部输入
	StatusCompensating InstanceStatus = "compensating" // 补偿链尚未走完
	StatusCompleted    InstanceStatus = "completed"
	StatusFailed       InstanceStatus = "failed"              // 补偿成功
	StatusCompFailed   InstanceStatus = "compensation_failed" // 补偿本身也失败
)

// StepRecord 记录“进入/离开某个节点”这一事实。补偿节点使用独立记录，
// 因此可以真实展示实例最终停在哪一步（包括停在失败的补偿节点上）。
type StepRecord struct {
	Seq     int       `json:"seq"`
	NodeID  string    `json:"nodeId"`
	Kind    NodeKind  `json:"kind"`
	Event   StepEvent `json:"event"`
	Comp    bool      `json:"comp"`
	Result  string    `json:"result,omitempty"` // ok | error，用于任务/补偿
	Message string    `json:"message,omitempty"`
	At      time.Time `json:"at"`
}

type StepEvent string

const (
	StepEnter StepEvent = "enter"
	StepLeave StepEvent = "leave"
)

// TransitionRecord 是页面箭头的唯一数据来源：每条记录对应图上一条边。
type TransitionRecord struct {
	Seq    int       `json:"seq"`
	EdgeID string    `json:"edgeId"`
	From   string    `json:"from"`
	To     string    `json:"to"`
	Comp   bool      `json:"comp"`
	At     time.Time `json:"at"`
}

// InputRecord 记录外部输入。IdempotencyKey 相同的重复提交只生效一次。
type InputRecord struct {
	Seq            int       `json:"seq"`
	IdempotencyKey string    `json:"idempotencyKey"`
	NodeID         string    `json:"nodeId"`
	InputKey       string    `json:"inputKey"`
	Payload        any       `json:"payload"`
	Duplicate      bool      `json:"duplicate"`
	At             time.Time `json:"at"`
}
