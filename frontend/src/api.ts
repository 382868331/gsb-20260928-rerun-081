import type { Definition, FlowEdge, FlowNode, Instance, MigrationPreview, VoteKind } from "./types";

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

function asArray<T>(v: T[] | null | undefined): T[] {
  return Array.isArray(v) ? v : [];
}

function normalizeDefinition(d: Definition): Definition {
  return { ...d, nodes: asArray(d.nodes), edges: asArray(d.edges) };
}

function normalizeInstance(i: Instance): Instance {
  return { ...i, records: asArray(i.records), data: i.data || {} };
}

export const api = {
  listDefinitions: async (): Promise<Definition[]> => {
    const list = (await req<Definition[] | null>("/api/definitions")) || [];
    return asArray(list).map(normalizeDefinition);
  },
  publish: (name: string, nodes: FlowNode[], edges: FlowEdge[]) =>
    req<Definition>("/api/definitions", {
      method: "POST",
      body: JSON.stringify({ name, nodes, edges })
    }),
  getDefinition: async (id: string) => normalizeDefinition(await req<Definition>("/api/definitions/" + id)),
  listInstances: async (): Promise<Instance[]> => {
    const list = (await req<Instance[] | null>("/api/instances")) || [];
    return asArray(list).map(normalizeInstance);
  },
  startInstance: (definitionId: string, data: Record<string, unknown>, name?: string) =>
    req<Instance>("/api/instances", {
      method: "POST",
      body: JSON.stringify({ definitionId, data, name })
    }),
  getInstance: async (id: string) => {
    const v = await req<{ instance: Instance; definition: Definition }>("/api/instances/" + id);
    return { instance: normalizeInstance(v.instance), definition: normalizeDefinition(v.definition) };
  },
  deliverInput: (id: string, waitNode: string, key: string, payload: Record<string, unknown>) =>
	    req<Instance>("/api/instances/" + id + "/inputs", {
	      method: "POST",
	      body: JSON.stringify({ waitNode, key, payload })
	    }),
  deliverCollabVote: (id: string, waitNode: string, key: string, voter: string, kind: VoteKind) =>
    req<Instance>("/api/instances/" + id + "/inputs", {
      method: "POST",
      body: JSON.stringify({ waitNode, key, voter, kind, payload: {} })
    }),
  collabDelegate: (id: string, body: { waitNode: string; actor: string; party: string }) =>
    req<Instance>(`/api/instances/${id}/collab/delegate`, { method: "POST", body: JSON.stringify(body) }),
  collabRevoke: (id: string, body: { waitNode: string; actor: string; party: string; keepPriorInputs: boolean }) =>
    req<Instance>(`/api/instances/${id}/collab/revoke`, { method: "POST", body: JSON.stringify(body) }),
  collabScope: (id: string, add: boolean, body: { waitNode: string; actor: string; party: string; keepPriorInputs?: boolean }) =>
    req<Instance>(`/api/instances/${id}/collab/scope/${add ? "add" : "remove"}`, { method: "POST", body: JSON.stringify(body) }),
  collabTimeout: (id: string, waitNode: string) =>
    req<Instance>(`/api/instances/${id}/collab/timeout`, { method: "POST", body: JSON.stringify({ waitNode }) }),
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
};
