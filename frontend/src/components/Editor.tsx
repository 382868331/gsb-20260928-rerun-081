import { useCallback, useEffect, useState } from "react";
import FlowCanvas from "./FlowCanvas";
import { api } from "../api";
import type { Definition, EdgeBranch, FlowEdge, FlowNode, NodeKind } from "../types";
import { NODE_KIND_LABEL } from "../types";

let idSeq = 1;
const nextId = (kind: string) => `${kind}_${Date.now().toString(36)}_${idSeq++}`;

function emptyDraft(key: string): Omit<Definition, "version" | "createdAt"> {
  const startId = nextId("n");
  const endId = nextId("n");
  return {
    key,
    name: "新流程",
    nodes: [
      { id: startId, kind: "start", name: "开始", pos: { x: 60, y: 200 } },
      { id: endId, kind: "end", name: "结束", pos: { x: 560, y: 200 } }
    ],
    edges: [{ id: nextId("e"), from: startId, to: endId, branch: "" }]
  };
}

export default function Editor({
  defs,
  onPublished,
  notify
}: {
  defs: Definition[];
  onPublished: () => void;
  notify: (msg: string, ok: boolean) => void;
}) {
  const keys = [...new Set(defs.map((d) => d.key))];
  const [selectedKey, setSelectedKey] = useState(keys[0] ?? "order-demo");
  const [viewVersion, setViewVersion] = useState<number | "new" | "">("");
  const [draft, setDraft] = useState<Omit<Definition, "version" | "createdAt"> | null>(null);
  const [selectedNode, setSelectedNode] = useState<string | null>(null);
  const [selectedEdge, setSelectedEdge] = useState<string | null>(null);
  const [publishing, setPublishing] = useState(false);

  const versions = defs.filter((d) => d.key === selectedKey).sort((a, b) => b.version - a.version);

  useEffect(() => {
    if (viewVersion !== "") return;
    if (versions.length) setViewVersion(versions[0].version);
    else setViewVersion("new");
  }, [selectedKey]);

  const loadVersion = useCallback(async (key: string, version: number | "new") => {
    setViewVersion(version);
    setSelectedNode(null);
    setSelectedEdge(null);
    if (version === "new") {
      setDraft(emptyDraft(key));
      return;
    }
    try {
      const d = await api.getDefinition(key, version);
      setDraft({ key: d.key, name: d.name, nodes: d.nodes, edges: d.edges });
    } catch (e) {
      notify((e as Error).message, false);
    }
  }, [notify]);

  useEffect(() => {
    loadVersion(selectedKey, viewVersion === "" ? "new" : viewVersion);
  }, [selectedKey]);

  if (!draft) return <div className="panel">加载中…</div>;

  const defForCanvas: Definition = {
    ...draft,
    version: typeof viewVersion === "number" ? viewVersion : 0,
    createdAt: ""
  };
  const node = draft.nodes.find((n) => n.id === selectedNode) ?? null;
  const edge = draft.edges.find((e) => e.id === selectedEdge) ?? null;

  const updateNode = (id: string, patch: Partial<FlowNode>) => {
    setDraft({ ...draft, nodes: draft.nodes.map((n) => (n.id === id ? { ...n, ...patch } : n)) });
  };

  const addNode = (kind: NodeKind) => {
    const id = nextId("n");
    const col = 240 + draft.nodes.length * 30;
    const n: FlowNode = {
      id,
      kind,
      name: NODE_KIND_LABEL[kind],
      pos: { x: col, y: 120 + (draft.nodes.length % 5) * 90 }
    };
    if (kind === "condition") n.condition = "x == 1";
    if (kind === "wait") n.inputKey = "input";
    setDraft({ ...draft, nodes: [...draft.nodes, n] });
    setSelectedNode(id);
  };

  const deleteNode = (id: string) => {
    const target = draft.nodes.find((n) => n.id === id);
    if (target?.kind === "start" || target?.kind === "end") {
      notify("开始/结束节点不能删除", false);
      return;
    }
    setDraft({
      ...draft,
      nodes: draft.nodes.filter((n) => n.id !== id),
      edges: draft.edges.filter((e) => e.from !== id && e.to !== id)
    });
    setSelectedNode(null);
  };

  const addEdge = () => {
    const candidates = draft.nodes.map((n) => n.id);
    if (candidates.length < 2) return;
    const e: FlowEdge = { id: nextId("e"), from: candidates[0], to: candidates[1], branch: "" };
    setDraft({ ...draft, edges: [...draft.edges, e] });
    setSelectedEdge(e.id);
    setSelectedNode(null);
  };

  const publish = async () => {
    setPublishing(true);
    try {
      const res = await api.publish(draft);
      notify(`已发布 ${res.key} v${res.version}（旧实例继续使用各自版本）`, true);
      setSelectedKey(res.key);
      await onPublished();
      await loadVersion(res.key, res.version);
    } catch (e) {
      notify("发布失败: " + (e as Error).message, false);
    } finally {
      setPublishing(false);
    }
  };

  const taskNodes = draft.nodes.filter((n) => n.kind === "task");

  return (
    <div className="editor-layout">
      <div className="toolbar">
        <label>
          流程标识
          <input
            value={selectedKey}
            onChange={(e) => setSelectedKey(e.target.value.trim() || "new-wf")}
          />
        </label>
        <label>
          版本
          <select value={String(viewVersion)} onChange={(e) => {
            const v = e.target.value === "new" ? "new" : Number(e.target.value);
            loadVersion(selectedKey, v);
          }}>
            {versions.map((d) => (
              <option key={d.version} value={d.version}>v{d.version}</option>
            ))}
            <option value="new">新版本草稿</option>
          </select>
        </label>
        <input
          className="name-input"
          value={draft.name}
          onChange={(e) => setDraft({ ...draft, name: e.target.value })}
          placeholder="流程名称"
        />
        <span className="hint">发布只新增版本，永不覆盖旧定义</span>
        <button className="primary" disabled={publishing} onClick={publish}>
          {publishing ? "发布中…" : "发布新版本"}
        </button>
      </div>

      <div className="editor-body">
        <div className="palette panel">
          <h4>添加节点</h4>
          {(["task", "condition", "wait"] as NodeKind[]).map((k) => (
            <button key={k} onClick={() => addNode(k)}>+ {NODE_KIND_LABEL[k]}</button>
          ))}
          <h4>连线</h4>
          <button onClick={addEdge}>+ 添加连线</button>
          <h4>边列表</h4>
          <div className="edge-list">
            {draft.edges.map((e) => (
              <div
                key={e.id}
                className={`edge-row ${selectedEdge === e.id ? "sel" : ""}`}
                onClick={() => { setSelectedEdge(e.id); setSelectedNode(null); }}
              >
                {e.from} → {e.to} {e.branch ? `[${e.branch}]` : ""}
              </div>
            ))}
          </div>
        </div>

        <div className="canvas-wrap">
          <FlowCanvas
            def={defForCanvas}
            editable
            selectedId={selectedNode}
            onSelect={(id) => { setSelectedNode(id); setSelectedEdge(null); }}
            onMoveNode={(id, x, y) => updateNode(id, { pos: { x, y } })}
          />
        </div>

        <div className="inspector panel">
          {node && (
            <>
              <h4>节点属性</h4>
              <label>ID<input value={node.id} readOnly /></label>
              <label>名称<input value={node.name} onChange={(e) => updateNode(node.id, { name: e.target.value })} /></label>
              {node.kind === "condition" && (
                <label>条件表达式
                  <input value={node.condition ?? ""} onChange={(e) => updateNode(node.id, { condition: e.target.value })} />
                </label>
              )}
              {node.kind === "wait" && (
                <label>外部输入键
                  <input value={node.inputKey ?? ""} onChange={(e) => updateNode(node.id, { inputKey: e.target.value })} />
                </label>
              )}
              {node.kind === "task" && (
                <label>失败补偿节点
                  <select value={node.compensate ?? ""} onChange={(e) => updateNode(node.id, { compensate: e.target.value })}>
                    <option value="">无补偿（直接失败）</option>
                    {taskNodes.filter((t) => t.id !== node.id).map((t) => (
                      <option key={t.id} value={t.id}>{t.id} · {t.name}</option>
                    ))}
                  </select>
                </label>
              )}
              <button className="danger" onClick={() => deleteNode(node.id)}>删除节点</button>
            </>
          )}
          {edge && (
            <EdgeEditor
              edge={edge}
              nodes={draft.nodes}
              onChange={(patch) =>
                setDraft({ ...draft, edges: draft.edges.map((x) => (x.id === edge.id ? { ...x, ...patch } : x)) })
              }
              onDelete={() => {
                setDraft({ ...draft, edges: draft.edges.filter((x) => x.id !== edge.id) });
                setSelectedEdge(null);
              }}
            />
          )}
          {!node && !edge && <div className="hint">点击画布节点或边列表进行编辑；拖动节点调整布局。</div>}
        </div>
      </div>
    </div>
  );
}

function EdgeEditor({
  edge,
  nodes,
  onChange,
  onDelete
}: {
  edge: FlowEdge;
  nodes: FlowNode[];
  onChange: (p: Partial<FlowEdge>) => void;
  onDelete: () => void;
}) {
  const fromNode = nodes.find((n) => n.id === edge.from);
  const branchable = fromNode?.kind === "condition";
  return (
    <>
      <h4>连线属性</h4>
      <label>起点
        <select value={edge.from} onChange={(e) => onChange({ from: e.target.value, branch: "" })}>
          {nodes.map((n) => <option key={n.id} value={n.id}>{n.id}</option>)}
        </select>
      </label>
      <label>终点
        <select value={edge.to} onChange={(e) => onChange({ to: e.target.value })}>
          {nodes.map((n) => <option key={n.id} value={n.id}>{n.id}</option>)}
        </select>
      </label>
      <label>分支
        <select
          value={branchable ? edge.branch ?? "" : ""}
          disabled={!branchable}
          onChange={(e) => onChange({ branch: e.target.value as EdgeBranch })}
        >
          <option value="">普通</option>
          <option value="true">true（是）</option>
          <option value="false">false（否）</option>
        </select>
      </label>
      <button className="danger" onClick={onDelete}>删除连线</button>
    </>
  );
}
