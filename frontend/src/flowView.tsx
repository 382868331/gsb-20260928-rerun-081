import type { Edge, Node } from "@xyflow/react";
import type { Definition, FlowNode, StepRecord } from "./types";

export const TYPE_LABEL: Record<string, string> = {
  start: "开始",
  task: "自动任务",
  decision: "条件分支",
  wait: "等待人工输入",
  end: "结束"
};

const TYPE_COLOR: Record<string, string> = {
  start: "#16a34a",
  task: "#2563eb",
  decision: "#d97706",
  wait: "#7c3aed",
  end: "#64748b"
};

export function nodeColor(type: string): string {
  return TYPE_COLOR[type] || "#475569";
}

export function definitionToFlow(def: Pick<Definition, "nodes" | "edges">): {
  nodes: Node[];
  edges: Edge[];
} {
  const nodes: Node[] = def.nodes.map((n) => ({
    id: n.id,
    position: { x: n.x, y: n.y },
    data: { label: n.name + "\n[" + TYPE_LABEL[n.type] + "]" },
    draggable: false,
    selectable: false,
    style: {
      borderRadius: n.type === "decision" ? 6 : 10,
      background: "#fff",
      border: "2px solid " + nodeColor(n.type),
      color: "#0f172a",
      whiteSpace: "pre-line",
      minWidth: 120,
      textAlign: "center"
    }
  }));
  const edges: Edge[] = def.edges.map((e) => ({
    id: e.id,
    source: e.from,
    target: e.to,
    label: e.label || e.condition || "",
    animated: false,
    style: { stroke: "#cbd5e1", strokeWidth: 2 },
    labelStyle: { fontSize: 12, fill: "#64748b" }
  }));
  return { nodes, edges };
}

// executedEdges derives the arrow set ONLY from engine step records.
// An edge is marked traversed when a record explicitly carries that edgeId
// (branch / traverse events). Node state likewise comes from records.
export function applyExecution(
  nodes: Node[],
  edges: Edge[],
  records: StepRecord[],
  stoppedAt?: string
): { nodes: Node[]; edges: Edge[] } {
  const traversedEdges = new Set<string>();
  const nodeState = new Map<string, { failed: boolean; waiting: boolean; compensated: boolean; compFailed: boolean }>();

  for (const rec of records) {
    if (rec.edgeId) traversedEdges.add(rec.edgeId);
    const st = nodeState.get(rec.nodeId) || { failed: false, waiting: false, compensated: false, compFailed: false };
    if (rec.action === "fail") st.failed = true;
    if (rec.action === "compensated") { st.compensated = true; st.failed = false; }
    if (rec.action === "compensation_fail") { st.compFailed = true; st.failed = true; }
    if (rec.action === "wait") st.waiting = true;
    if (rec.action === "input_received" || rec.action === "succeed" || rec.action === "complete") st.waiting = false;
    nodeState.set(rec.nodeId, st);
  }

  const newEdges = edges.map((e) => {
    if (traversedEdges.has(e.id)) {
      return {
        ...e,
        animated: false,
        style: { stroke: "#16a34a", strokeWidth: 3 },
        labelStyle: { fontSize: 12, fill: "#15803d", fontWeight: 700 }
      };
    }
    return e;
  });

  const newNodes = nodes.map((n) => {
    const st = nodeState.get(n.id);
    const nodeDef = n as Node;
    const updated: Node = { ...nodeDef, style: { ...(nodeDef.style as React.CSSProperties) } };
    if (st?.compFailed) {
      updated.style = { ...updated.style, background: "#fee2e2", border: "3px solid #dc2626" };
    } else if (st?.failed) {
      updated.style = { ...updated.style, background: "#fecaca", border: "2px solid #dc2626" };
    } else if (st?.waiting) {
      updated.style = { ...updated.style, background: "#ede9fe", border: "3px solid #7c3aed" };
    } else if (st?.compensated) {
      updated.style = { ...updated.style, background: "#ffedd5", border: "2px dashed #ea580c" };
    } else if (st) {
      updated.style = { ...updated.style, background: "#dcfce7", border: "2px solid #16a34a" };
    }
    if (stoppedAt === n.id) {
      updated.data = { ...updated.data, label: updated.data.label + "\n◉ 当前停点" };
    }
    return updated;
  });

  return { nodes: newNodes, edges: newEdges };
}

export function findNode(nodes: FlowNode[], id: string): FlowNode | undefined {
  return nodes.find((n) => n.id === id);
}
