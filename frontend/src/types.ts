export type NodeType = "start" | "task" | "decision" | "wait" | "end";

export interface FlowNode {
  id: string;
  type: NodeType;
  name: string;
  compensateTo?: string;
  x: number;
  y: number;
}

export interface FlowEdge {
  id: string;
  from: string;
  to: string;
  condition?: string;
  label?: string;
}

export interface Definition {
  id: string;
  name: string;
  version: number;
  nodes: FlowNode[];
  edges: FlowEdge[];
  publishedAt: string;
}

export type InstanceStatus = "running" | "waiting" | "completed" | "failed";

export interface StepRecord {
  seq: number;
  nodeId: string;
  edgeId?: string;
  action: string;
  detail?: string;
  status?: string;
  occurredAt: string;
}

export interface Instance {
  id: string;
  definitionId: string;
  version: number;
  status: InstanceStatus;
  data: Record<string, unknown>;
  currentNode?: string;
  waitNode?: string;
  records: StepRecord[];
  consumedInputs: Record<string, boolean>;
  stoppedAt?: string;
  createdAt: string;
  updatedAt: string;
}
