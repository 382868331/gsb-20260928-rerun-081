export type NodeType = "start" | "task" | "decision" | "wait" | "end";

export interface FlowNode {
  id: string;
  type: NodeType;
  name: string;
  compensateTo?: string;
  collab?: CollabSpec | null;
  x: number;
  y: number;
}

export type TimeoutPolicy = "reject" | "approve" | "majority";

export interface CollabSpec {
  participants: string[];
  delegatable: boolean;
  deadlineSeconds?: number;
  timeoutPolicy?: TimeoutPolicy;
  approveThreshold?: number;
}

export type VoteKind = "approve" | "reject";

export interface CollabVote {
  key: string;
  voter: string;
  onBehalfOf?: string;
  kind: VoteKind;
  viaGrantId?: string;
  valid: boolean;
  invalidReason?: string;
  at: string;
}

export interface GrantRecord {
  seq: number;
  action: "delegate" | "revoke" | "scope_add" | "scope_remove";
  actor: string;
  party?: string;
  keepPriorInputs: boolean;
  note?: string;
  at: string;
}

export interface CollabDecision {
  outcome: string;
  reason: string;
  approvals: number;
  rejections: number;
  threshold: number;
  scopeSnapshot: string[];
  at: string;
}

export interface CollabState {
  waitNode: string;
  scope: string[];
  votes: CollabVote[];
  grants: GrantRecord[];
  deadline?: string;
  decision?: CollabDecision;
  decided: boolean;
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
  collabs?: Record<string, CollabState>;
  createdAt: string;
  updatedAt: string;
}
