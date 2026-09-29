import { useEffect, useState } from "react";
import { ReactFlow, ReactFlowProvider } from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { api } from "../api";
import type { Definition, Instance } from "../types";
import { applyExecution, definitionToFlow } from "../flowView";

const STATUS_LABEL: Record<string, string> = {
  running: "运行中",
  waiting: "等待人工输入",
  completed: "已完成",
  failed: "失败"
};

export function Instances({ definitions, refreshKey }: { definitions: Definition[]; refreshKey: number }) {
  const [instances, setInstances] = useState<Instance[]>([]);
  const [selectedId, setSelectedId] = useState<string>("");
  const [detail, setDetail] = useState<{ instance: Instance; definition: Definition } | null>(null);
  const [defId, setDefId] = useState("");
  const [initData, setInitData] = useState('{"amount": 200}');
  const [inputKey, setInputKey] = useState("");
  const [inputPayload, setInputPayload] = useState('{"approved": true}');
  const [error, setError] = useState("");

  async function loadList() {
    const list = await api.listInstances();
    setInstances(list);
    if (!selectedId && list[0]) setSelectedId(list[0].id);
  }

  async function loadDetail(id: string) {
    if (!id) return;
    setDetail(await api.getInstance(id));
  }

  useEffect(() => {
    loadList().catch((e) => setError(e.message));
  }, [refreshKey]);

  useEffect(() => {
    loadDetail(selectedId).catch(() => setDetail(null));
  }, [selectedId]);

  async function start() {
    setError("");
    try {
      const data = JSON.parse(initData || "{}");
      const inst = await api.startInstance(defId || definitions[definitions.length - 1]?.id || "", data);
      await loadList();
      setSelectedId(inst.id);
    } catch (e) {
      setError((e as Error).message);
    }
  }

  async function deliver() {
    if (!detail) return;
    setError("");
    try {
      const payload = JSON.parse(inputPayload || "{}");
      const key = inputKey || "input-" + Date.now();
      await api.deliverInput(detail.instance.id, detail.instance.waitNode || "", key, payload);
      await loadDetail(detail.instance.id);
      await loadList();
    } catch (e) {
      setError((e as Error).message);
    }
  }

  const flow = detail ? definitionToFlow(detail.definition) : { nodes: [], edges: [] };
  const executed = detail
    ? applyExecution(flow.nodes, flow.edges, detail.instance.records, detail.instance.stoppedAt)
    : flow;

  return (
    <div className="instance-layout">
      <aside className="panel">
        <h2>启动实例</h2>
        <label>使用定义版本
          <select value={defId} onChange={(e) => setDefId(e.target.value)}>
            <option value="">最新版本</option>
            {definitions.map((d) => (
              <option key={d.id} value={d.id}>{d.name} v{d.version}</option>
            ))}
          </select>
        </label>
        <label>初始数据 JSON
          <textarea rows={5} value={initData} onChange={(e) => setInitData(e.target.value)} />
        </label>
        <p className="hint">模拟失败：设置 failAt 为节点 id；模拟补偿失败：compFailAt 为节点 id。</p>
        <button className="primary" onClick={start}>启动并运行到等待/结束</button>
        {error && <div className="error">{error}</div>}

        <h2>实例列表</h2>
        <ul className="instance-list">
          {instances.map((i) => (
            <li key={i.id} className={i.id === selectedId ? "active" : ""} onClick={() => setSelectedId(i.id)}>
              <span className={"status " + i.status}>{STATUS_LABEL[i.status]}</span>
              <span className="hint">v{i.version} · {i.id}</span>
            </li>
          ))}
        </ul>
      </aside>

      <div className="canvas execution">
        {detail ? (
          <>
            <div className="execution-bar">
              <span className={"status " + detail.instance.status}>{STATUS_LABEL[detail.instance.status]}</span>
              <span>定义 v{detail.instance.version}（{detail.instance.definitionId}）</span>
              <span>真实停点：<b>{detail.instance.stoppedAt || "-"}</b></span>
              {detail.instance.status === "waiting" && (
                <span className="waiting-tag">等待节点 {detail.instance.waitNode} 的外部输入</span>
              )}
            </div>
            <ReactFlowProvider>
              <ReactFlow
                nodes={executed.nodes}
                edges={executed.edges}
                nodesDraggable={false}
                nodesConnectable={false}
                fitView
                proOptions={{ hideAttribution: true }}
              />
            </ReactFlowProvider>
            <p className="hint arrow-note">绿色箭头只来自执行记录中的真实边（branch/traverse），不是动画。</p>
          </>
        ) : <div className="empty">请选择或启动一个实例</div>}
      </div>

      <aside className="panel records-panel">
        <h2>人工模拟输入</h2>
        {detail?.instance.status === "waiting" ? (
          <>
            <label>幂等键 key（相同 key 重复提交只生效一次）
              <input value={inputKey} onChange={(e) => setInputKey(e.target.value)} placeholder="例如 approve-001" />
            </label>
            <label>输入内容 JSON<textarea rows={4} value={inputPayload} onChange={(e) => setInputPayload(e.target.value)} /></label>
            <button className="primary" onClick={deliver}>提交输入并继续</button>
          </>
        ) : <p className="hint">实例当前不等待输入。</p>}

        <h2>执行记录</h2>
        <div className="records">
          {detail?.instance.records.map((r) => (
            <div key={r.seq} className={"record record-" + r.action}>
              <span className="seq">#{r.seq}</span>
              <span className="rec-main">
                <b>{r.action}</b> @ {r.nodeId}
                {r.edgeId ? <em> 沿 {r.edgeId}</em> : null}
                {r.detail ? <div>{r.detail}</div> : null}
              </span>
            </div>
          ))}
        </div>
      </aside>
    </div>
  );
}
