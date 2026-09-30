import { useMemo, useState } from "react";
import { ReactFlow, ReactFlowProvider } from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import type { Definition, FlowNode, NodeType } from "../types";
import { templateEdges, templateNodes } from "../template";
import { TYPE_LABEL, definitionToFlow, nodeColor } from "../flowView";

interface Props {
  definitions: Definition[];
  onPublish: (name: string, nodes: FlowNode[], edges: typeof templateEdges) => Promise<void>;
}

let counter = 100;

export function Editor({ definitions, onPublish }: Props) {
  const [name, setName] = useState("order-demo");
  const [nodes, setNodes] = useState<FlowNode[]>(templateNodes);
  const [edges, setEdges] = useState(templateEdges);
  const [selected, setSelected] = useState<string | null>("check");
  const [from, setFrom] = useState("check");
  const [to, setTo] = useState("end");
  const [condition, setCondition] = useState("");
  const [edgeLabel, setEdgeLabel] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const flow = useMemo(() => definitionToFlow({ nodes, edges }), [nodes, edges]);
  const selectedNode = nodes.find((n) => n.id === selected) || null;

  function addNode(type: NodeType) {
    const id = type + "_" + counter++;
    const n: FlowNode = {
      id,
      type,
      name: TYPE_LABEL[type] + counter,
      x: 300 + (counter % 5) * 40,
      y: 300 + (counter % 4) * 40
    };
    setNodes((prev) => [...prev, n]);
    setSelected(id);
  }

  function updateNode(patch: Partial<FlowNode>) {
    if (!selectedNode) return;
    setNodes((prev) => prev.map((n) => (n.id === selectedNode.id ? { ...n, ...patch } : n)));
  }

  function addEdge() {
    if (!from || !to || from === to) {
      setError("请选择不同的起点和终点");
      return;
    }
    if (edges.some((e) => e.from === from && e.to === to)) {
      setError("这条连接已经存在");
      return;
    }
    const id = "e_" + from + "_" + to + "_" + counter++;
    setEdges((prev) => [...prev, { id, from, to, condition: condition || undefined, label: edgeLabel || undefined }]);
    setError("");
  }

  function loadVersion(d: Definition) {
    setName(d.name);
    setNodes(d.nodes.map((n) => ({ ...n })));
    setEdges(d.edges.map((e) => ({ ...e })));
    setSelected(d.nodes[0]?.id ?? null);
    setError("");
  }

  function resetTemplate() {
    setNodes(templateNodes.map((n) => ({ ...n })));
    setEdges(templateEdges.map((e) => ({ ...e })));
    setName("order-demo");
    setSelected("check");
  }

  async function publish() {
    setBusy(true);
    setError("");
    try {
      await onPublish(name, nodes, edges);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="editor-layout">
      <aside className="panel">
        <h2>流程定义编辑</h2>
        <label>
          流程名称（同名发布会生成新版本）
          <input value={name} onChange={(e) => setName(e.target.value)} />
        </label>
        <div className="btn-row">
          <button onClick={() => addNode("task")}>+ 任务</button>
          <button onClick={() => addNode("decision")}>+ 分支</button>
          <button onClick={() => addNode("wait")}>+ 等待输入</button>
          <button onClick={() => addNode("end")}>+ 结束</button>
        </div>

        {selectedNode && (
          <div className="node-props">
            <h3>节点属性（{selectedNode.id}）</h3>
            <label>名称<input value={selectedNode.name} onChange={(e) => updateNode({ name: e.target.value })} /></label>
            <label>补偿目标节点
              <select value={selectedNode.compensateTo || ""} onChange={(e) => updateNode({ compensateTo: e.target.value || undefined })}>
                <option value="">（失败时无需补偿）</option>
                {nodes.filter((n) => n.type === "task").map((n) => <option key={n.id} value={n.id}>{n.name}</option>)}
              </select>
            </label>
            <span className="hint" style={{ color: nodeColor(selectedNode.type) }}>类型：{TYPE_LABEL[selectedNode.type]}</span>
          </div>
        )}

        <h3>连接 / 分支条件</h3>
        <label>起点
          <select value={from} onChange={(e) => setFrom(e.target.value)}>
            {nodes.map((n) => <option key={n.id} value={n.id}>{n.name}({n.id})</option>)}
          </select>
        </label>
        <label>终点
          <select value={to} onChange={(e) => setTo(e.target.value)}>
            {nodes.map((n) => <option key={n.id} value={n.id}>{n.name}({n.id})</option>)}
          </select>
        </label>
        <label>条件（如 amount &gt;= 100；留空或 default 为默认分支）
          <input value={condition} onChange={(e) => setCondition(e.target.value)} placeholder="amount >= 100" />
        </label>
        <label>边上显示文字<input value={edgeLabel} onChange={(e) => setEdgeLabel(e.target.value)} /></label>
        <button onClick={addEdge}>添加边</button>

        <ul className="edge-list">
          {edges.map((e) => (
            <li key={e.id}>{e.from} → {e.to} {e.condition ? <b>当 {e.condition}</b> : null}
              <button className="link-danger" onClick={() => setEdges((prev) => prev.filter((x) => x.id !== e.id))}>删除</button>
            </li>
          ))}
        </ul>

        <button className="primary" disabled={busy} onClick={publish}>{busy ? "发布中…" : "发布为新版本（不可变）"}</button>
        <button onClick={resetTemplate} style={{ marginTop: 6 }}>重置为示例模板</button>
        {error && <div className="error">{error}</div>}
      </aside>

      <div className="canvas">
        <ReactFlowProvider>
          <ReactFlow
            nodes={flow.nodes}
            edges={flow.edges}
            onNodeClick={(_, n) => setSelected(n.id)}
            fitView
            proOptions={{ hideAttribution: true }}
          />
        </ReactFlowProvider>
      </div>

      <aside className="panel versions">
        <h2>已发布定义（{definitions.length}）</h2>
        <p className="hint">点开版本可载入画布修改，再以同名发布新版本；已有实例继续固定启动时版本。</p>
        {definitions.length === 0 && <p className="hint">还没有已发布定义，先发布当前模板。</p>}
        {[...definitions].reverse().map((d) => (
          <div key={d.id} className="version-card">
            <strong>{d.name}</strong> <span className="badge">v{d.version}</span>
            <div className="hint">{d.id}</div>
            <div className="hint">节点 {d.nodes.length} · 边 {d.edges.length}</div>
            <button onClick={() => loadVersion(d)}>载入到画布修改</button>
          </div>
        ))}
      </aside>
    </div>
  );
}
