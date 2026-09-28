import {
  type Edge as RFEdge,
  MarkerType,
  type Node as RFNode
} from "@xyflow/react";
import type { Definition, FlowEdge, FlowNode, Instance, TransitionRecord } from "./types";

// 定义图节点：编辑态与实例态共用布局，实例态额外显示执行状态。
export function toRFNodes(
  def: Definition,
  inst?: Instance
): RFNode[] {
  const entered = new Set<string>();
  const failedNodes = new Set<string>();
  inst?.steps.forEach((s) => {
    if (s.event === "enter") entered.add(s.nodeId + (s.comp ? "::comp" : ""));
    if (s.event === "leave" && s.result === "error") failedNodes.add(s.nodeId + (s.comp ? "::comp" : ""));
  });
  const current = new Set(inst?.current ?? []);
  const waiting = new Set(Object.keys(inst?.waitingFor ?? {}));

  return def.nodes.map((n: FlowNode) => {
    const isCompEntered = entered.has(n.id + "::comp");
    const isEntered = entered.has(n.id) || isCompEntered;
    const isFailed = failedNodes.has(n.id) || failedNodes.has(n.id + "::comp");
    const isCurrent = current.has(n.id);
    const isWaiting = waiting.has(n.id);
    const cls = [
      "flow-node",
      `kind-${n.kind}`,
      isEntered ? "entered" : "",
      isCompEntered ? "comp-entered" : "",
      isFailed ? "failed" : "",
      isCurrent ? "current" : "",
      isWaiting ? "waiting" : ""
    ]
      .filter(Boolean)
      .join(" ");
    const badges: string[] = [];
    if (isWaiting) badges.push("等待输入");
    if (isCurrent && !isWaiting) badges.push("停留点");
    if (isFailed) badges.push(isCompEntered ? "补偿失败" : "执行失败");
    return {
      id: n.id,
      position: { x: n.pos.x, y: n.pos.y },
      data: {
        node: n,
        badges,
        compBadge: isCompEntered ? "已补偿" : ""
      },
      className: cls
    };
  });
}

// 实例图上的箭头严格来自执行记录：
// 定义边只有在 transitions 中存在对应记录时才高亮；补偿边用 comp 记录动态生成。
export function toRFEdges(def: Definition, inst?: Instance): RFEdge[] {
  const records = inst?.transitions ?? [];
  const recordByKey = new Map<string, TransitionRecord>();
  records.forEach((r) => recordByKey.set(r.comp ? r.edgeId : r.edgeId, r));

  const edges: RFEdge[] = def.edges.map((e: FlowEdge) => {
    const rec = recordByKey.get(e.id);
    return baseEdge({
      id: e.id,
      source: e.from,
      target: e.to,
      label: e.branch ? (e.branch === "true" ? "是" : "否") : undefined,
      fired: Boolean(rec),
      comp: false,
      seq: rec?.seq
    });
  });

  // 补偿边可能指向定义中已存在补偿关系但未画成普通边的节点，
  // 仍依据真实流转记录渲染红色虚线箭头。
  records
    .filter((r) => r.comp)
    .forEach((r) => {
      edges.push(
        baseEdge({
          id: `comp-${r.seq}`,
          source: r.from,
          target: r.to,
          label: `补偿 #${r.seq}`,
          fired: true,
          comp: true,
          seq: r.seq
        })
      );
    });
  return edges;
}

function baseEdge(o: {
  id: string;
  source: string;
  target: string;
  label?: string;
  fired: boolean;
  comp: boolean;
  seq?: number;
}): RFEdge {
  return {
    id: o.id,
    source: o.source,
    target: o.target,
    label: o.comp ? o.label : o.label ? `${o.label}${o.fired ? ` · #${o.seq}` : ""}` : o.fired ? `#${o.seq}` : undefined,
    animated: false, // 明确不用动画表示执行；只有真实记录才高亮
    className: ["flow-edge", o.fired ? "fired" : "idle", o.comp ? "comp-edge" : ""]
      .filter(Boolean)
      .join(" "),
    style: {
      stroke: o.comp ? "#dc2626" : o.fired ? "#16a34a" : "#cbd5e1",
      strokeWidth: o.fired ? 2.5 : 1.5,
      strokeDasharray: o.comp ? "6 4" : undefined
    },
    markerEnd: {
      type: MarkerType.ArrowClosed,
      color: o.comp ? "#dc2626" : o.fired ? "#16a34a" : "#94a3b8"
    }
  };
}
