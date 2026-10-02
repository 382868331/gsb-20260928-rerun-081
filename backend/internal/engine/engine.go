package engine

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"flowplatform/internal/model"
)

// TaskHandler performs automatic work for a task node.
// Returning an error triggers compensation of succeeded tasks on the path.
type TaskHandler func(inst *model.Instance, node model.Node) error

// CompensateHandler undoes a task. It may itself fail.
type CompensateHandler func(inst *model.Instance, node model.Node) error

type Engine struct {
	task       TaskHandler
	compensate CompensateHandler
	// onPersist, when set, is called after every state-changing compensation
	// step so a crash mid-compensation resumes without redoing anything.
	onPersist func(*model.Instance)
}

func New(task TaskHandler, compensate CompensateHandler) *Engine {
	return &Engine{task: task, compensate: compensate}
}

// WithPersist installs a callback invoked after each durable engine step.
func (e *Engine) WithPersist(fn func(*model.Instance)) *Engine {
	e.onPersist = fn
	return e
}

func (e *Engine) persist(inst *model.Instance) {
	if e.onPersist != nil {
		e.onPersist(inst)
	}
}

type defLookup interface {
	GetDefinition(id string) (*model.Definition, error)
}

// MigrationImpact describes what changes for a waiting instance if it is
// switched to a new definition version. Only the wait point and the steps
// that still lie ahead are affected; history under the old version is kept.
type MigrationImpact struct {
	InstanceID    string         `json:"instanceId"`
	WaitNode      string         `json:"waitNode"`
	WaitNodeName  string         `json:"waitNodeName"`
	FromVersion   int            `json:"fromVersion"`
	ToVersion     int            `json:"toVersion"`
	WaitNodeInNew bool           `json:"waitNodeInNew"`
	NewNodes      []model.Node   `json:"newNodes"`
	RemovedNodes  []string       `json:"removedNodes"`
	NewEdges      []model.Edge   `json:"newEdges"`
	RemovedEdges  []string       `json:"removedEdges"`
	AffectedAhead []AffectedStep `json:"affectedAhead"`
}

// AffectedStep is a node reachable after the wait point in the new graph that
// is new/removed/changed and therefore changes what the instance will do.
type AffectedStep struct {
	NodeID string `json:"nodeId"`
	Name   string `json:"name,omitempty"`
	Kind   string `json:"kind"` // added / removed / changed / edge_added / edge_changed
}

// PreviewMigration computes the impact without changing the instance. The
// wait node must still exist in the target definition (same id).
func PreviewMigration(inst *model.Instance, from, to *model.Definition) (*MigrationImpact, error) {
	if inst.Status != model.StatusWaiting || inst.WaitNode == "" {
		return nil, fmt.Errorf("只有等待人工输入的实例才能迁移版本，当前状态为 %s", inst.Status)
	}
	if inst.DefinitionID == to.ID {
		return nil, fmt.Errorf("实例已经使用该版本")
	}
	waitInNew := false
	var waitName string
	oldByID := map[string]model.Node{}
	for _, n := range from.Nodes {
		if n.ID == inst.WaitNode {
			waitName = n.Name
		}
		oldByID[n.ID] = n
	}
	newByID := map[string]model.Node{}
	for _, n := range to.Nodes {
		newByID[n.ID] = n
		if n.ID == inst.WaitNode {
			waitInNew = true
			if waitName == "" {
				waitName = n.Name
			}
		}
	}
	if !waitInNew {
		return nil, fmt.Errorf("新版本中不存在等待节点 %s，无法迁移；已运行实例继续使用旧版本", inst.WaitNode)
	}

	impact := &MigrationImpact{
		InstanceID:    inst.ID,
		WaitNode:      inst.WaitNode,
		WaitNodeName:  waitName,
		FromVersion:   from.Version,
		ToVersion:     to.Version,
		WaitNodeInNew: true,
		NewNodes:      to.Nodes,
		NewEdges:      to.Edges,
	}
	for _, n := range to.Nodes {
		old, ok := oldByID[n.ID]
		if !ok {
			impact.AffectedAhead = append(impact.AffectedAhead, AffectedStep{NodeID: n.ID, Name: n.Name, Kind: "added"})
		} else if old.Type != n.Type || old.Name != n.Name || old.CompensateTo != n.CompensateTo {
			impact.AffectedAhead = append(impact.AffectedAhead, AffectedStep{NodeID: n.ID, Name: n.Name, Kind: "changed"})
		}
	}
	for _, n := range from.Nodes {
		if _, ok := newByID[n.ID]; !ok {
			impact.RemovedNodes = append(impact.RemovedNodes, n.ID)
			impact.AffectedAhead = append(impact.AffectedAhead, AffectedStep{NodeID: n.ID, Name: n.Name, Kind: "removed"})
		}
	}
	oldEdges := map[string]model.Edge{}
	for _, ed := range from.Edges {
		oldEdges[ed.ID] = ed
	}
	newEdges := map[string]model.Edge{}
	for _, ed := range to.Edges {
		newEdges[ed.ID] = ed
		old, ok := oldEdges[ed.ID]
		if !ok {
			impact.AffectedAhead = append(impact.AffectedAhead, AffectedStep{NodeID: ed.To, Kind: "edge_added"})
		} else if old != ed {
			impact.AffectedAhead = append(impact.AffectedAhead, AffectedStep{NodeID: ed.To, Kind: "edge_changed"})
		}
	}
	for _, ed := range from.Edges {
		if _, ok := newEdges[ed.ID]; !ok {
			impact.RemovedEdges = append(impact.RemovedEdges, ed.ID)
		}
	}
	// Keep only steps that actually lie ahead of (or at) the wait point in the
	// new graph, so the user is not warned about already-finished history.
	reachable := reachableFrom(to, inst.WaitNode)
	var ahead []AffectedStep
	for _, a := range impact.AffectedAhead {
		if a.NodeID == inst.WaitNode || reachable[a.NodeID] {
			ahead = append(ahead, a)
		}
	}
	impact.AffectedAhead = ahead
	return impact, nil
}

func reachableFrom(def *model.Definition, from string) map[string]bool {
	out := map[string][]string{}
	for _, ed := range def.Edges {
		out[ed.From] = append(out[ed.From], ed.To)
	}
	seen := map[string]bool{}
	queue := []string{from}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, next := range out[cur] {
			if !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	return seen
}

// MigrateWaiting switches a waiting instance to a new definition version
// after the user has reviewed the impact. Existing records remain exactly as
// they were under the old version; only future steps use the new graph.
func (e *Engine) MigrateWaiting(inst *model.Instance, from, to *model.Definition) (*MigrationImpact, error) {
	impact, err := PreviewMigration(inst, from, to)
	if err != nil {
		return nil, err
	}
	inst.DefinitionID = to.ID
	inst.Version = to.Version
	inst.DefinitionHistory = append(inst.DefinitionHistory, model.DefinitionRef{
		DefinitionID: to.ID,
		Name:         to.Name,
		Version:      to.Version,
		Since:        time.Now().UTC(),
	})
	e.addRecord(inst, inst.WaitNode, "", "definition_migrated", model.NodeEntered,
		fmt.Sprintf("实例从 %s v%d 迁移到 v%d；旧版本 %d 条历史记录原样保留，后续步骤按新版本执行", from.Name, from.Version, to.Version, len(inst.Records)))
	return impact, nil
}

func (e *Engine) addRecord(inst *model.Instance, nodeID, edgeID, action string, status model.NodeStatus, detail string) {
	inst.Records = append(inst.Records, model.StepRecord{
		Seq:        len(inst.Records) + 1,
		NodeID:     nodeID,
		EdgeID:     edgeID,
		Action:     action,
		Status:     status,
		Detail:     detail,
		OccurredAt: time.Now().UTC(),
	})
}

// Run executes forward until the instance completes, waits, or fails
// (with compensation attempted). Safe to call again after restart.
func (e *Engine) Run(def *model.Definition, inst *model.Instance) {
	nodeMap := map[string]model.Node{}
	for _, n := range def.Nodes {
		nodeMap[n.ID] = n
	}
	// Crash recovery: a forward Run must never re-execute a task that already
	// produced a succeed record. Reconcile the current node from the last real
	// movement event recorded on disk.
	if inst.Status == model.StatusRunning {
		reconcileCurrentNode(def, inst)
	}
	for inst.Status == model.StatusRunning {
		node, ok := nodeMap[inst.CurrentNode]
		if !ok {
			inst.Status = model.StatusFailed
			inst.StoppedAt = inst.CurrentNode
			return
		}
		switch node.Type {
		case model.NodeStart, model.NodeEnd:
			e.addRecord(inst, node.ID, "", string(node.Type), model.NodeEntered, node.Name)
			if node.Type == model.NodeEnd {
				e.addRecord(inst, node.ID, "", "complete", model.NodeSucceeded, "流程结束")
				inst.Status = model.StatusCompleted
				inst.StoppedAt = node.ID
				inst.CurrentNode = node.ID
				return
			}
			e.advance(def, inst, node, "")
		case model.NodeTask:
			e.addRecord(inst, node.ID, "", "enter", model.NodeEntered, node.Name)
			// Idempotent after a crash between task success and persistence of
			// the advance: if this node already succeeded, never run it twice.
			if nodeSucceeded(inst, node.ID) {
				e.advance(def, inst, node, "")
			} else {
				if err := e.task(inst, node); err != nil {
					e.addRecord(inst, node.ID, "", "fail", model.NodeFailed, err.Error())
					e.startCompensation(def, inst, node)
					return
				}
				e.addRecord(inst, node.ID, "", "succeed", model.NodeSucceeded, node.Name)
				e.advance(def, inst, node, "")
			}
		case model.NodeDecision:
			e.addRecord(inst, node.ID, "", "enter", model.NodeEntered, node.Name)
			edge, err := pickEdge(def.Edges, node.ID, inst.Data)
			if err != nil {
				e.addRecord(inst, node.ID, "", "fail", model.NodeFailed, err.Error())
				e.startCompensation(def, inst, node)
				return
			}
			e.addRecord(inst, node.ID, edge.ID, "branch", model.NodeSucceeded, edgeLabel(edge))
			inst.CurrentNode = edge.To
		case model.NodeWait:
			if node.Collab != nil {
				st := e.initCollab(inst, node)
				e.addRecord(inst, node.ID, "", "collab_wait", model.NodeEntered,
					fmt.Sprintf("%s：进入多方协作等待，参与方 %s；委托 %v；到期策略 %s",
						node.Name, strings.Join(st.Scope, "、"), node.Collab.Delegatable, effectivePolicy(node.Collab)))
			} else {
				e.addRecord(inst, node.ID, "", "wait", model.NodeEntered, node.Name)
			}
			inst.Status = model.StatusWaiting
			inst.WaitNode = node.ID
			inst.StoppedAt = node.ID
			// A deadline may already have elapsed while the instance was down:
			// startup recovery finalizes it immediately.
			if node.Collab != nil {
				e.CheckTimeouts(def, inst)
				if cs := collabState(inst, node.ID); cs != nil && cs.Decided {
					e.advanceFromWait(def, inst, node.ID)
					return
				}
			}
			return
		}
	}
}

func edgeLabel(edge model.Edge) string {
	if edge.Label != "" {
		return edge.Label
	}
	return edge.Condition
}

func effectivePolicy(spec *model.CollabSpec) model.TimeoutPolicy {
	if spec.TimeoutPolicy == "" {
		return model.TimeoutReject
	}
	return spec.TimeoutPolicy
}

// advanceFromWait moves an instance off a wait node after its condition was
// satisfied (single input or a collaborative decision) and continues running.
func (e *Engine) advanceFromWait(def *model.Definition, inst *model.Instance, waitNode string) {
	inst.WaitNode = ""
	inst.Status = model.StatusRunning
	out := outgoing(def.Edges, waitNode)
	if len(out) == 0 {
		inst.Status = model.StatusFailed
		inst.StoppedAt = waitNode
		e.addRecord(inst, waitNode, "", "fail", model.NodeFailed, "等待节点没有后继边")
		return
	}
	edge := out[0]
	e.addRecord(inst, edge.To, edge.ID, "traverse", model.NodeEntered, edgeLabel(edge))
	inst.CurrentNode = edge.To
	e.Run(def, inst)
}

func (e *Engine) advance(def *model.Definition, inst *model.Instance, node model.Node, via string) {
	edges := outgoing(def.Edges, node.ID)
	if len(edges) == 0 {
		inst.Status = model.StatusFailed
		inst.StoppedAt = node.ID
		e.addRecord(inst, node.ID, "", "fail", model.NodeFailed, "节点没有后继边")
		return
	}
	edge := edges[0]
	e.addRecord(inst, edge.To, edge.ID, "traverse", model.NodeEntered, edgeLabel(edge))
	inst.CurrentNode = edge.To
}

// DeliverInput resumes a waiting instance. Duplicate key -> no-op advance.
func (e *Engine) DeliverInput(def *model.Definition, inst *model.Instance, input model.ExternalInput) error {
	if input.Key == "" {
		return fmt.Errorf("缺少幂等键 key")
	}
	// A consumed key is accepted as a no-op forever: retried/duplicate
	// submissions never advance the instance a second time.
	if inst.ConsumedInputs[input.Key] {
		node := inputContextNode(inst, input)
		action := "duplicate_input"
		detail := "重复的输入 " + input.Key + " 已忽略，业务动作未重复执行"
		// During compensation / after a failed instance every input is only
		// kept as history; tag it late when the wait is already gone.
		if inst.Status != model.StatusWaiting {
			action = "late_input"
			detail = fmt.Sprintf("迟到/补偿期间的输入 %s 仅作为历史保留：实例当前状态为 %s，停在 %s，不会重复执行任何动作", input.Key, inst.Status, effectiveStop(inst))
		}
		e.addRecord(inst, node, "", action, model.NodeEntered, detail)
		if st := collabState(inst, node); st != nil {
			if !collabHasKey(st, input.Key) {
				st.Votes = append(st.Votes, model.CollabVote{
					Key: input.Key, Voter: input.Voter, Kind: input.Kind, Valid: false,
					InvalidReason: "重复幂等键", At: time.Now().UTC(),
				})
			}
		}
		return nil
	}
	if inst.Status != model.StatusWaiting || inst.WaitNode == "" {
		// Late input for a wait that is already gone (failed / completed) must not
		// rerun any business action. Record it as a visible event and stay put.
		node := inputContextNode(inst, input)
		e.addRecord(inst, node, "", "late_input", model.NodeEntered,
			fmt.Sprintf("迟到的输入 %s 仅作为历史保留：实例当前状态为 %s，停在 %s，不覆盖数据、不重做动作", input.Key, inst.Status, effectiveStop(inst)))
		if st := collabState(inst, node); st != nil {
			inst.ConsumedInputs[input.Key] = true
			if !collabHasKey(st, input.Key) {
				st.Votes = append(st.Votes, model.CollabVote{
					Key: input.Key, Voter: input.Voter, Kind: input.Kind, Valid: false,
					InvalidReason: "迟到输入（实例已离开等待点）", At: time.Now().UTC(),
				})
			}
		}
		return nil
	}
	if input.WaitNode != "" && input.WaitNode != inst.WaitNode {
		return fmt.Errorf("输入目标节点 %s 与等待节点 %s 不一致", input.WaitNode, inst.WaitNode)
	}
	// Resolve any deadline that is already due before accepting new input, so
	// a vote and a timeout arriving together can only produce one decision.
	e.CheckTimeouts(def, inst)
	node, spec := collabNode(def, inst.WaitNode)
	if spec != nil {
		if cs := collabState(inst, node.ID); cs != nil && cs.Decided {
			// Timeout decided concurrently: this input is late, kept as history.
			inst.ConsumedInputs[input.Key] = true
			cs.Votes = append(cs.Votes, model.CollabVote{
				Key: input.Key, Voter: input.Voter, Kind: input.Kind, Valid: false,
				InvalidReason: "决定已形成（到期），迟到输入仅作为历史保留", At: time.Now().UTC(),
			})
			e.addRecord(inst, node.ID, "", "late_input", model.NodeEntered,
				fmt.Sprintf("参与方 %s 的迟到输入 %s 在决定形成后到达，仅作为历史保留", input.Voter, input.Key))
			return nil
		}
		return e.deliverCollabVote(def, inst, node, spec, input)
	}
	inst.ConsumedInputs[input.Key] = true
	for k, v := range input.Payload {
		inst.Data[k] = v
	}
	waitNode := inst.WaitNode
	e.addRecord(inst, waitNode, "", "input_received", model.NodeSucceeded, "接收输入 "+input.Key)
	e.advanceFromWait(def, inst, waitNode)
	return nil
}

// deliverCollabVote accepts one party's input for a collaborative wait. The
// step is never completed by a partial set of votes; it only finishes when
// votes determine the outcome (approval threshold reached, or rejection made
// unavoidable) or the deadline fires.
func (e *Engine) deliverCollabVote(def *model.Definition, inst *model.Instance, node model.Node, spec *model.CollabSpec, input model.ExternalInput) error {
	st := collabState(inst, node.ID)
	if st == nil {
		st = e.initCollab(inst, node)
	}
	if st.Decided {
		return nil
	}
	if input.Voter == "" {
		return fmt.Errorf("协作等待需要提供参与方 voter")
	}
	if input.Kind != model.VoteApprove && input.Kind != model.VoteReject {
		return fmt.Errorf("协作输入的 kind 必须是 approve 或 reject")
	}
	onBehalf, grantSeq, authorized := voteAuthorization(st, spec, input.Voter)
	if !authorized {
		return fmt.Errorf("参与方 %s 当前不在有效接收范围内，且无有效委托，不能提交输入", input.Voter)
	}
	// One participant can only vote once; a repeat vote (even by a delegate
	// already used) is recorded as a duplicate and ignored.
	approvals, rejections, byP := validTallies(st)
	if _, exists := byP[onBehalf]; exists {
		inst.ConsumedInputs[input.Key] = true
		st.Votes = append(st.Votes, model.CollabVote{
			Key: input.Key, Voter: input.Voter, OnBehalfOf: onBehalf, Kind: input.Kind, Valid: false,
			InvalidReason: "该参与方已提交过意见", At: time.Now().UTC(),
		})
		e.addRecord(inst, node.ID, "", "duplicate_input", model.NodeEntered,
			fmt.Sprintf("参与方 %s 的重复输入 %s 已忽略，意见不重复计数", input.Voter, input.Key))
		return nil
	}
	inst.ConsumedInputs[input.Key] = true
	vote := model.CollabVote{
		Key: input.Key, Voter: input.Voter, OnBehalfOf: onBehalf, Kind: input.Kind, Valid: true, At: time.Now().UTC(),
	}
	if onBehalf != input.Voter {
		vote.ViaGrantID = fmt.Sprintf("g%d", grantSeq)
	}
	st.Votes = append(st.Votes, vote)
	if onBehalf != input.Voter {
		e.addRecord(inst, node.ID, "", "collab_vote", model.NodeEntered,
			fmt.Sprintf("受托人 %s 代表 %s 提交 %s（授权记录 %s，幂等键 %s）", input.Voter, onBehalf, input.Kind, vote.ViaGrantID, input.Key))
	} else {
		e.addRecord(inst, node.ID, "", "collab_vote", model.NodeEntered,
			fmt.Sprintf("参与方 %s 提交 %s（幂等键 %s）", input.Voter, input.Kind, input.Key))
	}
	approvals, rejections, _ = validTallies(st)
	threshold := approveThreshold(spec, len(st.Scope))
	// Partial inputs never finish the step early. It finishes while waiting
	// only when enough approvals exist, or when everyone in scope has voted.
	// (A deadline, if configured, is the other way a partial vote set ends.)
	switch {
	case approvals >= threshold:
		e.commitDecision(inst, node, spec, st, true, "approved",
			fmt.Sprintf("赞成达到通过所需 %d 票", threshold), approvals, rejections, threshold)
	case approvals+rejections >= len(st.Scope) && len(st.Scope) > 0:
		approved := approvals >= threshold
		outcome, result := "rejected", "所有有效参与方均已投票，赞成未达通过票数"
		if approved {
			outcome, result = "approved", "所有有效参与方均已投票，赞成达到通过票数"
		}
		e.commitDecision(inst, node, spec, st, approved, outcome, result, approvals, rejections, threshold)
	default:
		// Partial inputs: stay waiting; the step is NOT marked complete.
		return nil
	}
	e.advanceFromWait(def, inst, node.ID)
	return nil
}

// GrantDelegate records that actor (a participant) delegates their vote to
// party. Allowed only when the wait definition permits delegation and the
// step is still pending.
func (e *Engine) GrantDelegate(def *model.Definition, inst *model.Instance, req model.GrantRequest) error {
	node, spec, st, err := pendingCollab(def, inst, req.WaitNode)
	if err != nil {
		return err
	}
	if !spec.Delegatable {
		return fmt.Errorf("等待节点 %s 未开启委托", node.ID)
	}
	if !inScope(st.Scope, req.Actor) {
		return fmt.Errorf("只有当前参与方可以委托，%s 不在有效范围内", req.Actor)
	}
	if req.Party == "" || req.Party == req.Actor {
		return fmt.Errorf("受托人不能为空或委托人本人")
	}
	g := addGrant(st, model.GrantDelegate, req.Actor, req.Party, true, req.Note)
	e.addRecord(inst, node.ID, "", "collab_grant", model.NodeEntered,
		fmt.Sprintf("新增授权记录 #%d：%s 委托 %s 代为参与；授权历史新旧记录均保留", g.Seq, req.Actor, req.Party))
	return nil
}

// RevokeDelegate withdraws a prior delegation. keepPriorInputs declares
// whether a vote the delegate already gave still counts.
func (e *Engine) RevokeDelegate(def *model.Definition, inst *model.Instance, req model.GrantRequest) error {
	node, spec, st, err := pendingCollab(def, inst, req.WaitNode)
	if err != nil {
		return err
	}
	if !spec.Delegatable {
		return fmt.Errorf("等待节点 %s 未开启委托", node.ID)
	}
	if !inScope(st.Scope, req.Actor) {
		return fmt.Errorf("只有当前参与方可以撤回委托，%s 不在有效范围内", req.Actor)
	}
	exists := false
	for _, g := range st.Grants {
		if g.Actor == req.Actor && g.Party == req.Party && g.Action == model.GrantDelegate {
			exists = true
		}
	}
	if !exists {
		return fmt.Errorf("没有找到 %s 对 %s 的有效委托记录", req.Actor, req.Party)
	}
	keep := true
	if req.KeepPriorInputs != nil {
		keep = *req.KeepPriorInputs
	}
	g := addGrant(st, model.GrantRevoke, req.Actor, req.Party, keep, req.Note)
	msg := fmt.Sprintf("撤回授权记录 #%d：%s 撤回对 %s 的委托；此前输入%s",
		g.Seq, req.Actor, req.Party, priorInputNote(keep))
	if !keep {
		invalidatePriorVotes(st, []string{req.Actor, req.Party}, "委托已撤回且声明此前输入失效")
	}
	e.addRecord(inst, node.ID, "", "collab_grant", model.NodeEntered, msg)
	return e.reevaluateAfterScopeChange(def, inst, node, spec, st)
}

// ChangeScope adds or removes a participant from the receiving scope while
// the wait is pending.
func (e *Engine) ChangeScope(def *model.Definition, inst *model.Instance, add bool, req model.GrantRequest) error {
	node, spec, st, err := pendingCollab(def, inst, req.WaitNode)
	if err != nil {
		return err
	}
	if req.Party == "" {
		return fmt.Errorf("参与方不能为空")
	}
	if add {
		if inScope(st.Scope, req.Party) {
			return fmt.Errorf("%s 已在接收范围内", req.Party)
		}
		st.Scope = append(st.Scope, req.Party)
		sort.Strings(st.Scope)
		g := addGrant(st, model.GrantScopeAdd, req.Actor, req.Party, true, req.Note)
		e.addRecord(inst, node.ID, "", "collab_grant", model.NodeEntered,
			fmt.Sprintf("授权记录 #%d：接收范围新增参与方 %s；新旧授权记录均保留", g.Seq, req.Party))
		return nil
	}
	if !inScope(st.Scope, req.Party) {
		return fmt.Errorf("%s 不在当前接收范围内", req.Party)
	}
	keep := true
	if req.KeepPriorInputs != nil {
		keep = *req.KeepPriorInputs
	}
	st.Scope = removeParty(st.Scope, req.Party)
	g := addGrant(st, model.GrantScopeRemove, req.Actor, req.Party, keep, req.Note)
	msg := fmt.Sprintf("授权记录 #%d：接收范围移除参与方 %s；其此前输入%s",
		g.Seq, req.Party, priorInputNote(keep))
	if !keep {
		invalidatePriorVotes(st, []string{req.Party}, "接收范围已变更且声明此前输入失效")
	}
	e.addRecord(inst, node.ID, "", "collab_grant", model.NodeEntered, msg)
	return e.reevaluateAfterScopeChange(def, inst, node, spec, st)
}

// ForceTimeout finalizes a wait as if its deadline had passed (used by tests
// and the admin timeout endpoint). It only applies the timeout policy when
// the deadline is actually due; callers may pass true to force it.
func (e *Engine) ForceTimeout(def *model.Definition, inst *model.Instance, waitNode string) error {
	node, spec, st, err := pendingCollab(def, inst, waitNode)
	if err != nil {
		return err
	}
	e.finalizeByTimeout(inst, node, spec, st)
	e.advanceFromWait(def, inst, node.ID)
	return nil
}

// reevaluateAfterScopeChange completes the wait if the outcome became certain
// under the new scope (e.g. removing a party makes votes unanimous).
func (e *Engine) reevaluateAfterScopeChange(def *model.Definition, inst *model.Instance, node model.Node, spec *model.CollabSpec, st *model.CollabState) error {
	if st.Decided {
		return nil
	}
	approvals, rejections, _ := validTallies(st)
	threshold := approveThreshold(spec, len(st.Scope))
	switch {
	case approvals >= threshold:
		e.commitDecision(inst, node, spec, st, true, "approved",
			"范围变更后赞成达到通过所需票数", approvals, rejections, threshold)
	case len(st.Scope) == 0:
		e.commitDecision(inst, node, spec, st, false, "rejected",
			"接收范围变更后已无有效参与方", approvals, rejections, threshold)
	case len(st.Scope) > 0 && approvals+rejections >= len(st.Scope):
		e.commitDecision(inst, node, spec, st, false, "rejected",
			"范围变更后所有有效参与方均已投票，赞成未达通过票数", approvals, rejections, threshold)
	default:
		return nil
	}
	e.advanceFromWait(def, inst, node.ID)
	return nil
}

func priorInputNote(keep bool) string {
	if keep {
		return "仍然有效并继续计数（记录留档）"
	}
	return "按声明不再有效，原输入作为历史保留、不再计数"
}

func pendingCollab(def *model.Definition, inst *model.Instance, waitNode string) (model.Node, *model.CollabSpec, *model.CollabState, error) {
	if inst.Status != model.StatusWaiting || inst.WaitNode == "" || waitNode != inst.WaitNode {
		return model.Node{}, nil, nil, fmt.Errorf("实例当前不在等待节点 %s", waitNode)
	}
	node, spec := collabNode(def, waitNode)
	if spec == nil {
		return model.Node{}, nil, nil, fmt.Errorf("等待节点 %s 不是协作等待", waitNode)
	}
	st := collabState(inst, waitNode)
	if st == nil {
		st = &model.CollabState{WaitNode: waitNode, Scope: append([]string{}, spec.Participants...), Votes: []model.CollabVote{}, Grants: []model.GrantRecord{}}
		if inst.Collabs == nil {
			inst.Collabs = map[string]*model.CollabState{}
		}
		inst.Collabs[waitNode] = st
	}
	if st.Decided {
		return model.Node{}, nil, nil, fmt.Errorf("协作等待 %s 已形成决定，不能再变更授权", waitNode)
	}
	return node, spec, st, nil
}

// inputContextNode picks the most meaningful node for an input that does not
// advance the instance: the wait node when still waiting, otherwise the node
// supplied by the caller or the recorded stopping/current node.
func inputContextNode(inst *model.Instance, input model.ExternalInput) string {
	if inst.WaitNode != "" {
		return inst.WaitNode
	}
	if input.WaitNode != "" {
		return input.WaitNode
	}
	return effectiveStop(inst)
}

func effectiveStop(inst *model.Instance) string {
	if inst.StoppedAt != "" {
		return inst.StoppedAt
	}
	return inst.CurrentNode
}

// Resume continues an instance that was persisted in "running" state, for
// example after a process restart. Waiting / completed / failed instances
// are left untouched.
func (e *Engine) Resume(def *model.Definition, inst *model.Instance) {
	// Collaborative waits whose deadline passed while the service was down
	// are finalized on startup; the saved votes are not re-submitted or
	// re-executed.
	if inst.Status == model.StatusWaiting {
		e.CheckTimeouts(def, inst)
		if cs := collabState(inst, inst.WaitNode); inst.WaitNode != "" && cs != nil && cs.Decided {
			e.advanceFromWait(def, inst, inst.WaitNode)
		}
		if inst.Status != model.StatusRunning {
			return
		}
	}
	if inst.Status != model.StatusRunning {
		return
	}
	if inst.Compensating {
		// A crash happened while compensating. The journal tells us exactly
		// which compensations already completed; never invoke them again.
		failed := defNodeOrMissing(def, inst.FailedNode)
		e.continueCompensation(def, inst, failed)
		return
	}
	e.Run(def, inst)
}

// startCompensation opens the compensation journal and starts undoing
// succeeded tasks in reverse execution order.
func (e *Engine) startCompensation(def *model.Definition, inst *model.Instance, failed model.Node) {
	inst.Compensating = true
	inst.FailedNode = failed.ID
	if inst.CompensatedNodes == nil {
		inst.CompensatedNodes = map[string]bool{}
	}
	e.continueCompensation(def, inst, failed)
}

// continueCompensation walks succeeded task nodes back along the recorded
// path and invokes their compensation, skipping any node already marked in
// the journal. A failing compensation stops everything and records the true
// stopping node. Safe to re-enter after a crash/restart.
func (e *Engine) continueCompensation(def *model.Definition, inst *model.Instance, failed model.Node) {
	nodeMap := map[string]model.Node{}
	for _, n := range def.Nodes {
		nodeMap[n.ID] = n
	}
	// Succeeded task nodes in execution order (from records).
	var succeeded []string
	seen := map[string]bool{}
	for _, rec := range inst.Records {
		if rec.Action == "succeed" {
			n := nodeMap[rec.NodeID]
			if n.Type == model.NodeTask && !seen[n.ID] {
				succeeded = append(succeeded, n.ID)
				seen[n.ID] = true
			}
		}
	}
	// Reverse order; the failed node itself never succeeded so is not listed.
	for i := len(succeeded) - 1; i >= 0; i-- {
		node := nodeMap[succeeded[i]]
		if inst.CompensatedNodes[node.ID] {
			// Completed before the crash (or on a previous resume): never twice.
			continue
		}
		targetID := node.CompensateTo
		if targetID == "" {
			targetID = node.ID
		}
		target := nodeMap[targetID]
		e.addRecord(inst, node.ID, "", "compensate", model.NodeCompensating, "补偿 "+node.Name)
		if err := e.compensate(inst, target); err != nil {
			e.addRecord(inst, node.ID, "", "compensation_fail", model.NodeCompFailed, err.Error())
			inst.Status = model.StatusFailed
			inst.StoppedAt = node.ID
			inst.Compensating = false
			return
		}
		e.addRecord(inst, node.ID, "", "compensated", model.NodeCompensated, node.Name)
		// Journal each success immediately so a later crash cannot redo it.
		inst.CompensatedNodes[node.ID] = true
		e.persist(inst)
	}
	if failed.ID != "" {
		e.addRecord(inst, failed.ID, "", "compensate_done", model.NodeFailed, "失败后补偿全部完成")
	}
	inst.Status = model.StatusFailed
	inst.StoppedAt = failed.ID
	inst.Compensating = false
}

func defNodeOrMissing(def *model.Definition, id string) model.Node {
	for _, n := range def.Nodes {
		if n.ID == id {
			return n
		}
	}
	return model.Node{ID: id}
}

// nodeSucceeded reports whether a task node already produced a succeed event
// in the current execution segment, i.e. since the last external input (or
// from the start). Events from before a wait/input boundary belong to an
// already-finished segment and must not suppress execution after migration
// to a definition that happens to reuse a node id.
func nodeSucceeded(inst *model.Instance, nodeID string) bool {
	start := 0
	for i, rec := range inst.Records {
		if rec.Action == "input_received" {
			start = i + 1
		}
	}
	for i := start; i < len(inst.Records); i++ {
		rec := inst.Records[i]
		if rec.NodeID == nodeID && rec.Action == "succeed" {
			return true
		}
	}
	return false
}

// reconcileCurrentNode rebuilds CurrentNode from the last recorded movement so
// a crash between advancing and saving does not replay an executed node.
func reconcileCurrentNode(def *model.Definition, inst *model.Instance) {
	current := inst.CurrentNode
	for _, rec := range inst.Records {
		if rec.Action == "traverse" && rec.NodeID != "" {
			current = rec.NodeID
		} else if rec.Action == "branch" && rec.EdgeID != "" {
			for _, edge := range def.Edges {
				if edge.ID == rec.EdgeID {
					current = edge.To
				}
			}
		}
	}
	inst.CurrentNode = current
}

func outgoing(edges []model.Edge, from string) []model.Edge {
	var out []model.Edge
	for _, e := range edges {
		if e.From == from {
			out = append(out, e)
		}
	}
	return out
}

func pickEdge(edges []model.Edge, from string, data map[string]any) (model.Edge, error) {
	out := outgoing(edges, from)
	var defaultEdge model.Edge
	for _, edge := range out {
		if strings.TrimSpace(edge.Condition) == "" || edge.Condition == "default" {
			defaultEdge = edge
			continue
		}
		ok, err := evalCond(edge.Condition, data)
		if err != nil {
			return model.Edge{}, fmt.Errorf("条件 %q 解析失败: %w", edge.Condition, err)
		}
		if ok {
			return edge, nil
		}
	}
	if defaultEdge.ID != "" {
		return defaultEdge, nil
	}
	return model.Edge{}, fmt.Errorf("节点 %s 没有命中的分支", from)
}

// evalCond supports a small safe expression grammar:
//
//	key == "str" | key == 123 | key != ... | key > >= < <= number | true/false
//
// and boolean keys. No arbitrary code execution.
func evalCond(expr string, data map[string]any) (bool, error) {
	expr = strings.TrimSpace(expr)
	for _, op := range []string{">=", "<=", "==", "!=", ">", "<"} {
		if i := strings.Index(expr, op); i >= 0 {
			key := strings.TrimSpace(expr[:i])
			raw := strings.TrimSpace(expr[i+len(op):])
			val := data[key]
			return compare(val, raw, op)
		}
	}
	switch expr {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		b, ok := data[expr].(bool)
		if !ok {
			return false, fmt.Errorf("未知条件 %q", expr)
		}
		return b, nil
	}
}

func compare(val any, raw, op string) (bool, error) {
	raw = strings.TrimSpace(raw)
	if n, err := strconv.ParseFloat(raw, 64); err == nil {
		f, ok := toFloat(val)
		if !ok {
			return false, fmt.Errorf("值不是数字")
		}
		switch op {
		case "==":
			return f == n, nil
		case "!=":
			return f != n, nil
		case ">":
			return f > n, nil
		case ">=":
			return f >= n, nil
		case "<":
			return f < n, nil
		case "<=":
			return f <= n, nil
		}
	}
	// string literal
	s := raw
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		s = s[1 : len(s)-1]
	}
	got := fmt.Sprintf("%v", val)
	switch op {
	case "==":
		return got == s, nil
	case "!=":
		return got != s, nil
	default:
		return false, fmt.Errorf("操作符 %s 不能用于字符串", op)
	}
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	}
	return 0, false
}
