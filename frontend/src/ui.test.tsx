import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, fireEvent, cleanup, waitFor } from "@testing-library/react";
import App from "./App";
import type { Definition, Instance } from "./types";

class RO { observe() {} unobserve() {} disconnect() {} }

const def: Definition = {
  id: "def_1", name: "order-demo", version: 1, publishedAt: "2026-01-01T00:00:00Z",
  nodes: [
    { id: "start", type: "start", name: "开始", x: 0, y: 0 },
    { id: "w", type: "wait", name: "等待", x: 200, y: 0 },
    { id: "end", type: "end", name: "结束", x: 400, y: 0 }
  ],
  edges: [
    { id: "e1", from: "start", to: "w" },
    { id: "e2", from: "w", to: "end" }
  ]
};

function waiting(): Instance {
  return {
    id: "inst_1", definitionId: "def_1", version: 1, status: "waiting",
    data: {}, currentNode: "w", waitNode: "w", stoppedAt: "w",
    consumedInputs: {}, createdAt: "", updatedAt: "",
    records: [
      { seq: 1, nodeId: "start", action: "start", occurredAt: "" },
      { seq: 2, nodeId: "w", edgeId: "e1", action: "traverse", occurredAt: "" },
      { seq: 3, nodeId: "w", action: "wait", occurredAt: "" }
    ]
  };
}

function json(v: unknown) {
  return new Response(JSON.stringify(v), { status: 200, headers: { "Content-Type": "application/json" } });
}

beforeEach(() => { (globalThis as any).ResizeObserver = RO; });
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

describe("white screen regression", () => {
  it("rapid tab switches while detail requests are in flight never blank the app", async () => {
    let resolveDetail: ((v: Response) => void) | null = null;
    vi.spyOn(globalThis, "fetch").mockImplementation((async (url: string) => {
      if (url.endsWith("/api/definitions")) return json([def]);
      if (url.endsWith("/api/instances/inst_1")) {
        return new Promise<Response>((res) => { resolveDetail = () => res(json({ instance: waiting(), definition: def })); });
      }
      if (url.endsWith("/api/instances")) return json([waiting()]);
      return json([]);
    }) as any);

    const { getByText } = render(<App />);
    fireEvent.click(getByText("实例列表"));
    fireEvent.click(getByText("流程定义"));
    fireEvent.click(getByText("执行历史"));
    await new Promise((r) => setTimeout(r, 30));
    fireEvent.click(getByText("实例列表"));
    // resolve the stale detail request after the user navigated around
    if (resolveDetail) resolveDetail(null as unknown as Response);
    await new Promise((r) => setTimeout(r, 100));
    // App shell stays mounted and one of the page layouts is present.
    expect(document.querySelector(".app")).toBeTruthy();
    expect(document.querySelector(".instance-layout")).toBeTruthy();
    // No fatal error boundary fallback.
    expect(document.querySelector(".fatal")).toBeNull();
  });

  it("duplicate (late) input is reported but business actions do not run again", async () => {
    let inst = waiting();
    const calls: string[] = [];
    vi.spyOn(globalThis, "fetch").mockImplementation((async (url: string, init?: RequestInit) => {
      if (url.endsWith("/api/definitions")) return json([def]);
      if (url.endsWith("/api/instances/inst_1")) return json({ instance: inst, definition: def });
      if (url.endsWith("/api/instances") && (!init || init.method !== "POST")) return json([inst]);
      if (url.endsWith("/inputs") && init?.method === "POST") {
        const body = JSON.parse(String(init.body));
        calls.push(body.key);
        inst = {
          ...waiting(),
          records: [
            ...waiting().records,
            { seq: 4, nodeId: "w", action: "duplicate_input", detail: "重复的输入 k1 已忽略", occurredAt: "" }
          ]
        };
        return json(inst);
      }
      return json([]);
    }) as any);

    const { getByText, findByPlaceholderText } = render(<App />);
    fireEvent.click(getByText("实例列表"));
    await waitFor(() => expect(document.querySelector(".instance-layout")).toBeTruthy());
    const keyInput = await findByPlaceholderText("例如 approve-001");
    fireEvent.change(keyInput, { target: { value: "k1" } });
    fireEvent.click(getByText("提交输入并继续"));
    await waitFor(() => expect(getByText(/未重复执行任何业务动作/)).toBeTruthy());
    expect(calls).toEqual(["k1"]);
    // The duplicate is surfaced as an explicit record, never as new progress.
    await waitFor(() => expect(document.querySelector(".record-duplicate_input")).toBeTruthy());
    expect(document.querySelectorAll(".record-traverse, .record-branch").length).toBe(1);
  });
});
