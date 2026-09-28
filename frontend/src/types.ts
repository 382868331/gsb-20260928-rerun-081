export type NodeKind = "start" | "task" | "condition" | "wait" | "end";
export type EdgeBranch = "" | "true" | "false";

export interface FlowNode {
  id: string;
  kind: NodeKind;
  name: string;
  pos: { x: number; y: number };
  condition?: string;
  inputKey?: string;
  compensate?: string;
}

export interface FlowEdge {
  id: string;
  from: string;
  to: string;
  branch?: EdgeBranch;
}

export interface Definition {
  key: string;
  version: number;
  name: string;
  nodes: FlowNode[];
  edges: FlowEdge[];
  createdAt: string;
}

export type InstanceStatus =
  | "running"
  | "waiting"
  | "compensating"
  | "completed"
  | "failed"
  | "compensation_failed";

export interface StepRecord {
  seq: number;
  nodeId: string;
  kind: NodeKind;
  event: "enter" | "leave";
  comp: boolean;
  result?: string;
  message?: string;
  at: string;
}

export interface TransitionRecord {
  seq: number;
  edgeId: string;
  from: string;
  to: string;
  comp: boolean;
  at: string;
}

export interface InputRecord {
  seq: number;
  idempotencyKey: string;
  nodeId: string;
  inputKey: string;
  payload: unknown;
  duplicate: boolean;
  at: string;
}

export interface Instance {
  id: string;
  defKey: string;
  version: number;
  definition: Definition;
  status: InstanceStatus;
  variables: Record<string, unknown>;
  current: string[];
  waitingFor: Record<string, string>;
  steps: StepRecord[];
  transitions: TransitionRecord[];
  inputs: InputRecord[];
  error?: string;
  createdAt: string;
  updatedAt: string;
}

export const NODE_KIND_LABEL: Record<NodeKind, string> = {
  start: "开始",
  task: "任务",
  condition: "条件",
  wait: "等待输入",
  end: "结束"
};

export const STATUS_LABEL: Record<InstanceStatus, string> = {
  running: "运行中",
  waiting: "等待输入",
  compensating: "补偿中",
  completed: "已完成",
  failed: "已失败（补偿完成）",
  compensation_failed: "补偿失败"
};
