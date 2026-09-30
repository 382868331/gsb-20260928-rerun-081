import { useEffect, useMemo, useState } from "react";
import { ReactFlow, ReactFlowProvider, useEdgesState, useNodesState } from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import type { Definition, FlowNode, NodeType } from "../types";
import { NODE_TYPE_LABEL } from "../types";
import { templateEdges, templateNodes } from "../template";
import { definitionToFlow, nodeColor } from "../flowView";

interface Props {
  definitions: Definition[];
  onPublish: (name: string, nodes: FlowNode[], edges: typeof templateEdges) => Promise<unknown>;
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
  const [rfNodes, setRfNodes, onNodesChange] = useNodesState(flow.nodes);
  const [rfEdges, setRfEdges, onEdgesChange] = useEdgesState(flow.edges);

  // Sync derived React Flow elements through the proper state setter instead of
  // mutating the returned array in place.
  useEffect(() => {
    setRfNodes(flow.nodes);
  }, [flow.nodes, setRfNodes]);
  useEffect(() => {
    setRfEdges(flow.edges);
  }, [flow.edges, setRfEdges]);

  const selectedNode = nodes.find((n) => n.id === selected) || null;

  function addNode(type: NodeType) {
    const id = type + "_" + counter++;
    setNodes((prev) => [
      ...prev,
      { id, type, name: NODE_TYPE_LABEL[type] + counter, x: 300 + (counter % 5) * 40, y: 300 + (counter % 4) * 40 }
    ]);
    setSelected(id);
  }

  function updateNode(patch: Partial<FlowNode>) {
    if (!selectedNode) return;
    setNodes((prev) => prev.map((n) => (n.id === selectedNode.id ? { ...n, ...patch } : n)));
  }

  function deleteNode(id: string) {
    setNodes((prev) => prev.filter((n) => n.id !== id));
    setEdges((prev) => prev.filter((e) => e.from !== id && e.to !== id));
    if (selected === id) setSelected(null);
  }

  function addEdge() {
    if (!from || !to || from === to) {
      setError("请选择不同的起点和终点");
      return;
    }
    const id = "e_" + from + "_" + to + "_" + counter++;
    setEdges((prev) => [...prev, { id, from, to, condition: condition.trim() || undefined, label: edgeLabel.trim() || undefined }]);
    setCondition("");
    setEdgeLabel("");
    setError("");
  }

  function deleteEdge(id: string) {
    setEdges((prev) => prev.filter((e) => e.id !== id));
  }

  async function publish() {
    setBusy(true);
    setError("");
    try {
      await onPublish(name.trim(), nodes, edges);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="editor-layout">
      <aside className="panel">
        <h2>流程编辑（新版本草稿）</h2>
        <p className="hint">在此修改只影响之后发布并启动的实例；已经在运行的实例继续遵守启动时固定的版本。</p>
        <label>
          流程名称
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
              <select
                value={selectedNode.compensateTo || ""}
                onChange={(e) => updateNode({ compensateTo: e.target.value || undefined })}
              >
                <option value="">（失败时无需补偿）</option>
                {nodes.filter((n) => n.type === "task").map((n) => <option key={n.id} value={n.id}>{n.name}</option>)}
              </select>
            </label>
            <div className="node-prop-row">
              <span className="hint" style={{ color: nodeColor(selectedNode.type) }}>类型：{NODE_TYPE_LABEL[selectedNode.type]}</span>
              <button className="link-danger" onClick={() => deleteNode(selectedNode.id)}>删除节点</button>
            </div>
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
              <button className="link-danger" onClick={() => deleteEdge(e.id)}>删除</button>
            </li>
          ))}
        </ul>

        <button className="primary" disabled={busy || !name.trim()} onClick={publish}>
          {busy ? "发布中…" : "发布为不可变新版本"}
        </button>
        {error && <div className="error">{error}</div>}
      </aside>

      <div className="canvas">
        <ReactFlowProvider>
          <ReactFlow
            nodes={rfNodes}
            edges={rfEdges}
            onNodesChange={onNodesChange}
            onEdgesChange={onEdgesChange}
            onNodeClick={(_, n) => setSelected(n.id)}
            nodesDraggable
            fitView
            proOptions={{ hideAttribution: true }}
          />
        </ReactFlowProvider>
      </div>

      <aside className="panel versions">
        <h2>已发布版本（{definitions.length}）</h2>
        <p className="hint">每次发布生成不可变新版本；运行中实例固定旧定义。</p>
        {definitions.map((d) => (
          <div key={d.id} className="version-card">
            <strong>{d.name}</strong> <span className="badge">v{d.version}</span>
            <div className="hint">{d.id}</div>
            <div className="hint">节点 {d.nodes.length} · 边 {d.edges.length} · {new Date(d.publishedAt).toLocaleString("zh-CN")}</div>
          </div>
        ))}
        {definitions.length === 0 && <p className="hint">还没有发布过版本。</p>}
      </aside>
    </div>
  );
}
