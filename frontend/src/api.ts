import type { Definition, FlowEdge, FlowNode, Instance } from "./types";

async function req<T>(url: string, init?: RequestInit): Promise<T> {
  const res = await fetch(url, {
    headers: { "Content-Type": "application/json" },
    ...init
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }));
    throw new Error(body.error || "请求失败 " + res.status);
  }
  return res.json() as Promise<T>;
}

export const api = {
  listDefinitions: () => req<Definition[]>("/api/definitions"),
  publish: (name: string, nodes: FlowNode[], edges: FlowEdge[]) =>
    req<Definition>("/api/definitions", {
      method: "POST",
      body: JSON.stringify({ name, nodes, edges })
    }),
  getDefinition: (id: string) => req<Definition>("/api/definitions/" + id),
  listInstances: () => req<Instance[]>("/api/instances"),
  startInstance: (definitionId: string, data: Record<string, unknown>) =>
    req<Instance>("/api/instances", {
      method: "POST",
      body: JSON.stringify({ definitionId, data })
    }),
  getInstance: (id: string) =>
    req<{ instance: Instance; definition: Definition }>("/api/instances/" + id),
  deliverInput: (id: string, waitNode: string, key: string, payload: Record<string, unknown>) =>
    req<Instance>("/api/instances/" + id + "/inputs", {
      method: "POST",
      body: JSON.stringify({ waitNode, key, payload })
    })
};
