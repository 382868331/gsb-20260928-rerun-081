package engine

import (
	"fmt"
	"strings"

	"workflow/internal/model"
)

// Validate 检查流程定义结构，避免发布无法执行的图。
func Validate(d *model.Definition) error {
	if strings.TrimSpace(d.Key) == "" {
		return fmt.Errorf("流程标识不能为空")
	}
	if len(d.Nodes) == 0 {
		return fmt.Errorf("流程至少需要一个节点")
	}
	nodes := map[string]*model.Node{}
	starts, ends := 0, 0
	for i := range d.Nodes {
		n := &d.Nodes[i]
		if n.ID == "" {
			return fmt.Errorf("第 %d 个节点缺少 ID", i+1)
		}
		if _, dup := nodes[n.ID]; dup {
			return fmt.Errorf("节点 ID 重复: %s", n.ID)
		}
		nodes[n.ID] = n
		switch n.Kind {
		case model.KindStart:
			starts++
		case model.KindEnd:
			ends++
		case model.KindTask:
		case model.KindCondition:
			if strings.TrimSpace(n.Condition) == "" {
				return fmt.Errorf("条件节点 %s 缺少表达式", n.ID)
			}
		case model.KindWait:
			if strings.TrimSpace(n.InputKey) == "" {
				return fmt.Errorf("等待节点 %s 缺少输入键", n.ID)
			}
		default:
			return fmt.Errorf("节点 %s 类型非法: %q", n.ID, n.Kind)
		}
	}
	if starts != 1 {
		return fmt.Errorf("流程必须恰好有一个 start 节点，当前 %d 个", starts)
	}
	if ends == 0 {
		return fmt.Errorf("流程至少需要一个 end 节点")
	}
	if d.Nodes[0].Kind != model.KindStart {
		return fmt.Errorf("第一个节点必须是 start")
	}

	edgeIDs := map[string]bool{}
	out := map[string][]model.Edge{}
	compTargets := map[string]bool{}
	for _, n := range d.Nodes {
		if n.Compensate != "" {
			compTargets[n.Compensate] = true
		}
	}
	for _, e := range d.Edges {
		if e.ID == "" {
			return fmt.Errorf("存在缺少 ID 的边")
		}
		if edgeIDs[e.ID] {
			return fmt.Errorf("边 ID 重复: %s", e.ID)
		}
		edgeIDs[e.ID] = true
		if nodes[e.From] == nil {
			return fmt.Errorf("边 %s 起点不存在: %s", e.ID, e.From)
		}
		if nodes[e.To] == nil {
			return fmt.Errorf("边 %s 终点不存在: %s", e.ID, e.To)
		}
		out[e.From] = append(out[e.From], e)
	}

	for id, n := range nodes {
		switch n.Kind {
		case model.KindEnd:
			if len(out[id]) != 0 {
				return fmt.Errorf("结束节点 %s 不能有出边", id)
			}
		case model.KindCondition:
			hasTrue, hasFalse := false, false
			for _, e := range out[id] {
				switch e.Branch {
				case model.BranchTrue:
					hasTrue = true
				case model.BranchFalse:
					hasFalse = true
				default:
					return fmt.Errorf("条件节点 %s 的出边必须标记 true/false", id)
				}
			}
			if !hasTrue || !hasFalse {
				return fmt.Errorf("条件节点 %s 必须同时有 true 和 false 出边", id)
			}
		case model.KindWait:
			if len(out[id]) != 1 || out[id][0].Branch != model.BranchDefault {
				return fmt.Errorf("等待节点 %s 必须恰好有一条普通出边", id)
			}
		case model.KindStart, model.KindTask:
			if n.Kind == model.KindTask && n.Compensate != "" && nodes[n.Compensate] == nil {
				return fmt.Errorf("任务 %s 的补偿节点不存在: %s", id, n.Compensate)
			}
			if n.Kind == model.KindTask && n.Compensate != "" && nodes[n.Compensate].Kind != model.KindTask {
				return fmt.Errorf("节点 %s 的补偿目标必须是 task 节点", id)
			}
			// 补偿节点通过“任务 -> 补偿节点”的补偿边进入，不参与普通出边要求。
			if n.Kind == model.KindTask && compTargets[id] {
				break
			}
			if len(out[id]) != 1 {
				return fmt.Errorf("节点 %s 必须恰好有一条出边", id)
			}
			if out[id][0].Branch != model.BranchDefault {
				return fmt.Errorf("节点 %s 的出边不能带分支标记", id)
			}
		}
	}

	// 从 start 做可达性检查，防止发布走不到的孤立分支。
	visited := map[string]bool{}
	queue := []string{d.Nodes[0].ID}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if visited[id] {
			continue
		}
		visited[id] = true
		for _, e := range out[id] {
			queue = append(queue, e.To)
		}
	}
	for id := range nodes {
		if compTargets[id] {
			continue // 补偿节点只通过补偿边到达
		}
		if !visited[id] {
			return fmt.Errorf("节点 %s 从起点不可达", id)
		}
	}
	return nil
}
