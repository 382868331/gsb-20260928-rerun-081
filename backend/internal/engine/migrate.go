package engine

import (
	"fmt"

	"flowplatform/internal/model"
)

// AffectedStep describes one step of a waiting instance that a version
// migration would touch. Shown to the user before they confirm.
type AffectedStep struct {
	NodeID   string `json:"nodeId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Issue    string `json:"issue"`
	Blocking bool   `json:"blocking"`
}

// MigrationPreview is the impact analysis of moving a waiting instance
// from its pinned definition to a newer one.
type MigrationPreview struct {
	InstanceID     string         `json:"instanceId"`
	FromDefinition string         `json:"fromDefinition"`
	FromVersion    int            `json:"fromVersion"`
	ToDefinition   string         `json:"toDefinition"`
	ToVersion      int            `json:"toVersion"`
	CanMigrate     bool           `json:"canMigrate"`
	AffectedSteps  []AffectedStep `json:"affectedSteps"`
}

// PreviewMigration compares the instance's pinned definition with the
// target definition and lists every affected step. It never mutates.
func PreviewMigration(inst *model.Instance, from, to *model.Definition) *MigrationPreview {
	newNodes := map[string]model.Node{}
	for _, n := range to.Nodes {
		newNodes[n.ID] = n
	}
	oldNodes := map[string]model.Node{}
	for _, n := range from.Nodes {
		oldNodes[n.ID] = n
	}
	prev := &MigrationPreview{
		InstanceID:     inst.ID,
		FromDefinition: from.ID,
		FromVersion:    from.Version,
		ToDefinition:   to.ID,
		ToVersion:      to.Version,
		CanMigrate:     true,
	}
	// The step the instance is currently parked on must still exist.
	if inst.WaitNode != "" {
		old := oldNodes[inst.WaitNode]
		nn, ok := newNodes[inst.WaitNode]
		switch {
		case !ok:
			prev.AffectedSteps = append(prev.AffectedSteps, AffectedStep{
				NodeID: inst.WaitNode, Name: old.Name, Kind: "wait", Blocking: true,
				Issue: fmt.Sprintf("当前等待的步骤在新版本 v%d 中已被删除，无法迁移", to.Version),
			})
			prev.CanMigrate = false
		case nn.Type != old.Type:
			prev.AffectedSteps = append(prev.AffectedSteps, AffectedStep{
				NodeID: inst.WaitNode, Name: nn.Name, Kind: "wait", Blocking: true,
				Issue: fmt.Sprintf("当前等待的步骤在新版本中类型由 %s 变为 %s，无法迁移", old.Type, nn.Type),
			})
			prev.CanMigrate = false
		default:
			prev.AffectedSteps = append(prev.AffectedSteps, AffectedStep{
				NodeID: inst.WaitNode, Name: nn.Name, Kind: "wait",
				Issue: "当前等待的步骤在新版本中保留，迁移后继续在此等待输入",
			})
		}
	}
	// Already-succeeded steps: history is kept as-is (never re-run, never
	// spliced with the new definition), but flag ones the new version dropped.
	seen := map[string]bool{}
	for _, rec := range inst.Records {
		if rec.Action != "succeed" || seen[rec.NodeID] {
			continue
		}
		seen[rec.NodeID] = true
		old := oldNodes[rec.NodeID]
		if _, ok := newNodes[rec.NodeID]; !ok {
			prev.AffectedSteps = append(prev.AffectedSteps, AffectedStep{
				NodeID: rec.NodeID, Name: old.Name, Kind: "succeeded",
				Issue: "已生效的步骤在新版本中不存在；历史保留，不会重放",
			})
			continue
		}
		if old.CompensateTo != "" {
			if _, ok := newNodes[old.CompensateTo]; !ok {
				prev.AffectedSteps = append(prev.AffectedSteps, AffectedStep{
					NodeID: rec.NodeID, Name: old.Name, Kind: "compensate",
					Issue: fmt.Sprintf("其补偿目标 %s 在新版本中缺失，若后续失败将跳过该补偿", old.CompensateTo),
				})
			}
		}
	}
	return prev
}

// Migrate switches a waiting instance to the target definition. History
// stays one continuous record stream: a migrate event is appended, no
// records are copied from or replayed against the new definition.
func (e *Engine) Migrate(inst *model.Instance, from, to *model.Definition) {
	inst.DefinitionID = to.ID
	inst.Version = to.Version
	e.addRecord(inst, inst.WaitNode, "", "migrate", model.NodeEntered,
		fmt.Sprintf("定义由 %s v%d 迁移到 v%d；历史记录保持连续，已完成步骤不重放", to.Name, from.Version, to.Version))
}
