import type { Definition, FlowEdge, FlowNode, Instance, MigrationPreview, Reconsideration, ReconsiderProgress } from "./types";

async function req<T>(url: string, init?: RequestInit): Promise<T> {
  const res = await fetch(url, {
    headers: { "Content-Type": "application/json" },
    ...init
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error((body as { error?: string }).error || res.statusText || "请求失败 " + res.status);
  }
  return res.json() as Promise<T>;
}

// listReq never lets a malformed/null body (e.g. an old backend returning
// null for empty data) crash the whole page: lists are always arrays.
async function listReq<T>(url: string): Promise<T[]> {
  const data = await req<unknown>(url);
  if (data == null) return [];
  if (!Array.isArray(data)) throw new Error("服务返回了不可遍历的数据");
  return data as T[];
}

export const api = {
  listDefinitions: () => listReq<Definition>("/api/definitions"),
  publish: (name: string, nodes: FlowNode[], edges: FlowEdge[]) =>
    req<Definition>("/api/definitions", {
      method: "POST",
      body: JSON.stringify({ name, nodes, edges })
    }),
  getDefinition: (id: string) => req<Definition>("/api/definitions/" + id),
  listInstances: () => listReq<Instance>("/api/instances"),
  startInstance: (definitionId: string, data: Record<string, unknown>, name?: string) =>
    req<Instance>("/api/instances", {
      method: "POST",
      body: JSON.stringify({ definitionId, data, name })
    }),
  getInstance: (id: string) =>
    req<{ instance: Instance; definition: Definition }>("/api/instances/" + id),
  deliverInput: (id: string, waitNode: string, key: string, payload: Record<string, unknown>, from?: string) =>
    req<Instance>("/api/instances/" + id + "/inputs", {
      method: "POST",
      body: JSON.stringify({ waitNode, key, payload, from: from || undefined })
    }),
  evaluateTimeout: (id: string) =>
    req<Instance>("/api/instances/" + id + "/timeout", { method: "POST" }),
  updateWaitScope: (id: string, req2: {
    participants?: string[];
    allowDelegate?: boolean;
    grantDelegations?: Record<string, string>;
    revokeDelegations?: string[];
    keepPriorInputs: boolean;
    reason?: string;
  }) =>
    req<Instance>("/api/instances/" + id + "/wait/scope", {
      method: "POST",
      body: JSON.stringify(req2)
    }),
  resumeInstance: (id: string) =>
    req<Instance>("/api/instances/" + id + "/resume", { method: "POST" })
  ,
  previewMigration: (id: string, target: { definitionId?: string; name?: string }) => {
    const q = new URLSearchParams();
    if (target.definitionId) q.set("definitionId", target.definitionId);
    if (target.name) q.set("name", target.name);
    return req<MigrationPreview>("/api/instances/" + id + "/migration?" + q.toString());
  },
  confirmMigration: (id: string, target: { definitionId?: string; name?: string }) =>
    req<{ instance: Instance }>("/api/instances/" + id + "/migration", {
      method: "POST",
      body: JSON.stringify({ ...target, confirm: true })
    })
  ,
  openReconsideration: (id: string, body: {
    waitNode: string;
    participants: string[];
    allowDelegate?: boolean;
    materials?: Record<string, unknown>;
    reason?: string;
    timeoutSeconds?: number;
    timeoutPolicy?: string;
  }) =>
    req<{ reconsideration: Reconsideration; instance: Instance }>("/api/instances/" + id + "/reconsiderations", {
      method: "POST",
      body: JSON.stringify(body)
    }),
  reconsiderVote: (id: string, rid: string, key: string, from: string, payload: Record<string, unknown>) =>
    req<Instance>("/api/instances/" + id + "/reconsiderations/" + rid + "/inputs", {
      method: "POST",
      body: JSON.stringify({ key, from, payload })
    }),
  reconsiderScope: (id: string, rid: string, body: { revokeDelegations?: string[]; grantDelegations?: Record<string, string>; participants?: string[]; reason?: string }) =>
    req<Instance>("/api/instances/" + id + "/reconsiderations/" + rid + "/scope", {
      method: "POST",
      body: JSON.stringify(body)
    }),
  reconsiderTimeout: (id: string, rid: string) =>
    req<Instance>("/api/instances/" + id + "/reconsiderations/" + rid + "/timeout", { method: "POST" }),
  reconsiderProgress: (id: string, rid: string) =>
    req<{ reconsideration: Reconsideration; progress: ReconsiderProgress }>("/api/instances/" + id + "/reconsiderations/" + rid)
};
