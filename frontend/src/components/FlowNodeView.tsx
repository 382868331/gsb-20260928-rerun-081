import { Handle, Position } from "@xyflow/react";
import type { FlowNode } from "../types";
import { NODE_KIND_LABEL } from "../types";

export interface FlowNodeData {
  node: FlowNode;
  badges: string[];
  compBadge: string;
  [key: string]: unknown;
}

export default function FlowNodeView({ data }: { data: FlowNodeData }) {
  const n = data.node;
  return (
    <div className="flow-node-card">
      <Handle type="target" position={Position.Left} />
      <div className="flow-node-head">
        <span className="flow-node-kind">{NODE_KIND_LABEL[n.kind]}</span>
        <span className="flow-node-id">{n.id}</span>
      </div>
      <div className="flow-node-name">{n.name}</div>
      {n.condition && <div className="flow-node-meta">条件: {n.condition}</div>}
      {n.inputKey && <div className="flow-node-meta">输入键: {n.inputKey}</div>}
      {n.compensate && <div className="flow-node-meta">失败补偿 → {n.compensate}</div>}
      <div className="flow-node-badges">
        {data.badges.map((b) => (
          <span key={b} className="badge badge-state">{b}</span>
        ))}
        {data.compBadge && <span className="badge badge-comp">{data.compBadge}</span>}
      </div>
      <Handle type="source" position={Position.Right} />
    </div>
  );
}
