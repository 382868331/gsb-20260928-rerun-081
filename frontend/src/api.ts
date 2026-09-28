import type { Definition, Instance } from "./types";

async function req<T>(url: string, init?: RequestInit): Promise<T> {
  const res = await fetch(url, {
    headers: { "Content-Type": "application/json" },
    ...init
  });
  if (!res.ok) {
    let msg = `HTTP ${res.status}`;
    try {
      const body = await res.json();
      msg = body.error || msg;
    } catch {
      /* ignore */
    }
    throw new Error(msg);
  }
  return res.json() as Promise<T>;
}

export const api = {
  listDefinitions: () => req<Definition[]>("/api/definitions"),
  getDefinition: (key: string, version: number) =>
    req<Definition>(`/api/definitions/${key}/${version}`),
  publish: (def: Omit<Definition, "version" | "createdAt">) =>
    req<{ key: string; version: number }>("/api/definitions", {
      method: "POST",
      body: JSON.stringify(def)
    }),
  listInstances: () => req<Instance[]>("/api/instances"),
  getInstance: (id: string) => req<Instance>(`/api/instances/${id}`),
  startInstance: (defKey: string, input: unknown) =>
    req<Instance>("/api/instances", {
      method: "POST",
      body: JSON.stringify({ defKey, input })
    }),
  submitInput: (id: string, nodeId: string, idempotencyKey: string, payload: unknown) =>
    req<Instance>(`/api/instances/${id}/inputs`, {
      method: "POST",
      body: JSON.stringify({ nodeId, idempotencyKey, payload })
    })
};
