export type NodeType = "start" | "task" | "decision" | "wait" | "end";

export const NODE_TYPE_LABEL: Record<NodeType, string> = {
  start: "开始",
  task: "自动任务",
  decision: "条件分支",
  wait: "等待人工输入",
  end: "结束"
};

export const ACTION_LABEL: Record<string, string> = {
  start: "进入开始节点",
  end: "进入结束节点",
  enter: "进入节点",
  succeed: "业务动作成功",
  fail: "业务动作失败",
  branch: "选择分支",
  traverse: "沿边前进",
  wait: "挂起等待输入",
  input_received: "外部输入生效",
  duplicate_input: "迟到/重复输入已忽略",
  compensate: "开始补偿",
  compensated: "补偿成功（已撤销）",
  compensation_fail: "补偿失败",
  compensate_done: "补偿全部完成",
  complete: "流程完成",
  resume: "重启后恢复执行"
};

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
