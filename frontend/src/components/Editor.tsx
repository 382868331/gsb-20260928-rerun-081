import { useEffect, useMemo, useState } from "react";
import { ReactFlow, ReactFlowProvider } from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import type { Definition, FlowNode, NodeType } from "../types";
import { templateEdges, templateNodes } from "../template";
import { TYPE_LABEL, definitionToFlow, nodeColor } from "../flowView";

interface Props {
  definitions: Definition[];
  onPublish: (name: string, nodes: FlowNode[], edges: typeof templateEdges) => Promise<void>;
  status: "loading" | "ready" | "error";
  loadError: string;
  onRetryLoad: () => void;
}

let counter = 100;
const DRAFT_KEY = "flowplatform.editor.draft.v1";

interface Draft {
  name: string;
  nodes: FlowNode[];
  edges: typeof templateEdges;
}

function loadDraft(): Draft | null {
  try {
    const raw = localStorage.getItem(DRAFT_KEY);
    if (!raw) return null;
    const d = JSON.parse(raw) as Draft;
    if (!Array.isArray(d.nodes) || d.nodes.length === 0) return null;
    return d;
  } catch {
    return null;
  }
}

export function Editor({ definitions, onPublish, status, loadError, onRetryLoad }: Props) {
  const restored = useMemo(loadDraft, []);
  const [name, setName] = useState(restored?.name ?? "order-demo");
  const [nodes, setNodes] = useState<FlowNode[]>(restored?.nodes ?? templateNodes);
  const [edges, setEdges] = useState(restored ? restored.edges : templateEdges);
  const [draftRestored, setDraftRestored] = useState(!!restored);
  const [selected, setSelected] = useState<string | null>("check");
  const [from, setFrom] = useState("check");
  const [to, setTo] = useState("end");
  const [condition, setCondition] = useState("");
  const [edgeLabel, setEdgeLabel] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  // Persist the in-progress draft so a browser refresh never loses edits
  // that have not been published yet.
  useEffect(() => {
    try {
      localStorage.setItem(DRAFT_KEY, JSON.stringify({ name, nodes, edges }));
    } catch {
      // Storage may be unavailable (private mode); editing still works.
    }
  }, [name, nodes, edges]);

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
    setDraftRestored(false);
    try {
      localStorage.removeItem(DRAFT_KEY);
    } catch {
      // ignore
    }
  }

  async function publish() {
    setBusy(true);
    setError("");
    try {
      await onPublish(name, nodes, edges);
      try {
        localStorage.removeItem(DRAFT_KEY);
      } catch {
        // ignore
      }
      setDraftRestored(false);
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
            {selectedNode.type === "wait" && (
              <fieldset className="wait-props">
                <legend>协作等待（多方输入）</legend>
                <label>参与方（逗号分隔；留空则为单人等待）
                  <input
                    value={(selectedNode.participants || []).join(",")}
                    placeholder="alice,bob,carol"
                    onChange={(e) =>
                      updateNode({
                        participants: e.target.value.split(/[,，]/).map((s) => s.trim()).filter(Boolean)
                      })
                    }
                  />
                </label>
                {(selectedNode.participants || []).length > 0 && (
                  <>
                    <label className="checkbox">
                      <input
                        type="checkbox"
                        checked={!!selectedNode.allowDelegate}
                        onChange={(e) => updateNode({ allowDelegate: e.target.checked })}
                      />
                      允许参与方委托他人代为确认
                    </label>
                    <label>到期时间（秒，0 为不限）
                      <input
                        type="number"
                        min={0}
                        value={selectedNode.timeoutSeconds || 0}
                        onChange={(e) => updateNode({ timeoutSeconds: Number(e.target.value) || 0 })}
                      />
                    </label>
                    <label>到期后如何决定
                      <select
                        value={selectedNode.timeoutPolicy || "reject"}
                        onChange={(e) => updateNode({ timeoutPolicy: e.target.value })}
                      >
                        <option value="reject">视为拒绝（进入失败补偿）</option>
                        <option value="approve">视为通过（继续流程）</option>
                      </select>
                    </label>
                    <span className="hint">需全部参与方确认后步骤才完成；部分输入只会记录，不会提前完成。</span>
                  </>
                )}
              </fieldset>
            )}
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
        {draftRestored && <div className="notice">已从浏览器本地恢复上次未发布的草稿；发布成功后会自动清除。</div>}
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
        {status === "loading" && <div className="state-loading">正在加载已发布定义…</div>}
        {status === "error" && (
          <div className="state-error">
            <div className="error">定义列表加载失败：{loadError || "网络错误"}</div>
            <button onClick={onRetryLoad}>重新加载</button>
          </div>
        )}
        {status === "ready" && definitions.length === 0 && (
          <div className="state-empty">
            <p className="hint">还没有任何流程定义。</p>
            <p className="hint">左侧已准备好示例模板：检查节点与连接后点击「发布为新版本」，即可到「实例列表」启动实例。</p>
          </div>
        )}
        {status === "ready" &&
          [...definitions].reverse().map((d) => (
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
