export type NodeType = "start" | "task" | "decision" | "wait" | "end";

export interface FlowNode {
  id: string;
  type: NodeType;
  name: string;
  compensateTo?: string;
  participants?: string[];
  allowDelegate?: boolean;
  timeoutSeconds?: number;
  timeoutPolicy?: string;
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

export type AffectedKind = "added" | "removed" | "changed" | "edge_added" | "edge_changed";

export interface AffectedStep {
  nodeId: string;
  name?: string;
  kind: AffectedKind;
}

export interface MigrationImpact {
  instanceId: string;
  waitNode: string;
  waitNodeName: string;
  fromVersion: number;
  toVersion: number;
  waitNodeInNew: boolean;
  removedNodes: string[];
  removedEdges: string[];
  affectedAhead: AffectedStep[];
}

export interface MigrationPreview {
  impact: MigrationImpact;
  from: Definition;
  to: Definition;
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
  compensating?: boolean;
  failedNode?: string;
  compensatedNodes?: Record<string, boolean>;
  definitionHistory?: { definitionId: string; name: string; version: number; since: string }[];
  wait?: WaitState;
  createdAt: string;
  updatedAt: string;
}

export interface PartyInput {
  key: string;
  from: string;
  authId: string;
  at: string;
}

export interface WaitAuthorization {
  id: string;
  participants: string[];
  allowDelegate: boolean;
  delegations?: Record<string, string>;
  since: string;
  reason?: string;
  priorInputsKept: boolean;
}

export interface WaitState {
  authId: string;
  received: Record<string, PartyInput>;
  authorizations: WaitAuthorization[];
  deadline?: string;
  decided?: boolean;
  decision?: string;
  decisionAuthId?: string;
}
