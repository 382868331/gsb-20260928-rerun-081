import { useCallback, useEffect, useRef, useState } from "react";
import { ReactFlow, ReactFlowProvider } from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { api } from "../api";
import type { Definition, Instance, StepRecord } from "../types";
import { ACTION_LABEL } from "../types";
import { applyExecution, definitionToFlow, findNode } from "../flowView";
import { RecordList, StatusPill, WaitingPanel } from "./ExecutionViews";

const STATUS_LABEL: Record<string, string> = {
  running: "运行中",
  waiting: "等待人工输入",
  completed: "已完成",
  failed: "失败"
};

interface Detail {
  instance: Instance;
  definition: Definition | null;
}

export function Instances({ definitions, refreshKey }: { definitions: Definition[]; refreshKey: number }) {
  const [instances, setInstances] = useState<Instance[]>([]);
  const [selectedId, setSelectedId] = useState<string>("");
  const [detail, setDetail] = useState<Detail | null>(null);
  const [defId, setDefId] = useState("");
  const [initData, setInitData] = useState('{"amount": 200}');
  const [inputKey, setInputKey] = useState("");
  const [inputPayload, setInputPayload] = useState('{"approved": true}');
  const [notice, setNotice] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  // Guards against async responses from a previous mount/selection overwriting
  // current state (this race previously caused the white screen).
  const mountedRef = useRef(true);
  const detailReqRef = useRef(0);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  const loadList = useCallback(async (preferId?: string) => {
    try {
      const list = await api.listInstances();
      if (!mountedRef.current) return;
      setInstances(list);
      setError("");
      const target = preferId || (selectedId && list.some((i) => i.id === selectedId) ? selectedId : list[0]?.id || "");
      if (target && target !== selectedId) setSelectedId(target);
      else if (!selectedId && target) setSelectedId(target);
    } catch (e) {
      if (mountedRef.current) setError((e as Error).message);
    }
  }, [selectedId]);

  const loadDetail = useCallback(async (id: string) => {
    if (!id) {
      setDetail(null);
      return;
    }
    const reqId = ++detailReqRef.current;
    try {
      const d = await api.getInstance(id);
      // Drop a late response belonging to an older selection.
      if (!mountedRef.current || reqId !== detailReqRef.current) return;
      setDetail(d);
      setError("");
    } catch (e) {
      if (!mountedRef.current || reqId !== detailReqRef.current) return;
      setDetail(null);
      setError((e as Error).message);
    }
  }, []);

  useEffect(() => {
    loadList();
  }, [refreshKey, loadList]);

  useEffect(() => {
    loadDetail(selectedId);
  }, [selectedId, loadDetail]);

  const refreshSelected = useCallback(async () => {
    if (!selectedId) return;
    await loadDetail(selectedId);
    await loadList(selectedId);
  }, [loadDetail, loadList, selectedId]);

  async function start() {
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const data = JSON.parse(initData || "{}");
      const chosen = defId || definitions[definitions.length - 1]?.id || "";
      if (!chosen) throw new Error("请先在「流程定义」中发布一个流程版本");
      const inst = await api.startInstance(chosen, data);
      setSelectedId(inst.id);
      await loadDetail(inst.id);
      await loadList(inst.id);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }

  const deliver = useCallback(async () => {
    if (!detail) return;
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const payload = JSON.parse(inputPayload || "{}");
      const key = inputKey.trim() || "input-" + Date.now();
      const updated = await api.deliverInput(detail.instance.id, detail.instance.waitNode || "", key, payload);
      if (updated.records.some((r) => r.action === "duplicate_input")) {
        setNotice("幂等键 " + key + " 已消费过：未重复执行任何业务动作");
      } else {
        setNotice("输入 " + key + " 已生效");
      }
      await refreshSelected();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }, [detail, inputKey, inputPayload, refreshSelected]);

  const resume = useCallback(async () => {
    if (!detail) return;
    setBusy(true);
    setError("");
    try {
      await api.resume(detail.instance.id);
      await refreshSelected();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }, [detail, refreshSelected]);

  const inst = detail?.instance;
  const def = detail?.definition || null;
  const hasGraph = !!def && def.nodes.length > 0;
  const base = hasGraph ? definitionToFlow(def as Definition) : { nodes: [], edges: [] };
  const executed = inst && hasGraph
    ? applyExecution(base.nodes, base.edges, inst.records, inst.stoppedAt)
    : base;
  const stopNode = hasGraph && inst?.stoppedAt ? findNode((def as Definition).nodes, inst.stoppedAt) : undefined;

  return (
    <div className="instance-layout">
      <aside className="panel">
        <h2>启动新实例</h2>
        <label>使用定义版本（新版本只影响之后启动的实例）
          <select value={defId} onChange={(e) => setDefId(e.target.value)}>
            <option value="">最新版本</option>
            {definitions.map((d) => (
              <option key={d.id} value={d.id}>{d.name} v{d.version}（{d.id}）</option>
            ))}
          </select>
        </label>
        <label>初始数据 JSON
          <textarea rows={5} value={initData} onChange={(e) => setInitData(e.target.value)} />
        </label>
        <p className="hint">模拟失败：failAt 设为任务节点 id；模拟补偿再次失败：compFailAt 设为节点 id。</p>
        <button className="primary" disabled={busy || definitions.length === 0} onClick={start}>
          {definitions.length === 0 ? "尚无已发布定义" : busy ? "运行中…" : "启动并运行到等待/结束"}
        </button>
        {notice && <div className="notice">{notice}</div>}
        {error && <div className="error">{error}</div>}

        <h2>实例列表（{instances.length}）</h2>
        <ul className="instance-list">
          {instances.map((i) => (
            <li key={i.id} className={i.id === selectedId ? "active" : ""} onClick={() => setSelectedId(i.id)}>
              <span className={"status " + i.status}>{STATUS_LABEL[i.status]}</span>
              <span className="hint">{i.definitionId} · v{i.version}</span>
              <span className="hint">停点 {i.stoppedAt || "-"} · 事件 {i.records.length}</span>
            </li>
          ))}
          {instances.length === 0 && <li className="hint">暂无实例，启动一个试试。</li>}
        </ul>
      </aside>

      <div className="canvas execution">
        {inst ? (
          hasGraph ? (
            <>
              <div className="execution-bar">
                <StatusPill status={inst.status} />
                <span>固定定义 v{inst.version} · {inst.definitionId}</span>
                <span>真实停点：<b>{stopNode ? stopNode.name + "（" + inst.stoppedAt + "）" : inst.stoppedAt}</b></span>
                {inst.status === "waiting" && <span className="waiting-tag">等待 {inst.waitNode} 的外部输入</span>}
                {inst.status === "running" && (
                  <button className="mini" disabled={busy} onClick={resume}>重启后恢复 / 继续推进</button>
                )}
                <button className="mini" disabled={busy} onClick={refreshSelected}>刷新</button>
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
              <p className="hint arrow-note">绿色箭头只来自执行记录中真实发生的 branch/traverse 事件，非动画推断。</p>
            </>
          ) : (
            <div className="empty">
              <p>实例 {inst.id} 固定的定义版本 {inst.definitionId} 在当前数据目录中找不到（可能来自其它后端数据目录）。</p>
              <p className="hint">执行记录仍可在右侧与「执行历史」中查看。</p>
            </div>
          )
        ) : (
          <div className="empty">请选择或启动一个实例</div>
        )}
      </div>

      <aside className="panel records-panel">
        {inst && <WaitingPanel instance={inst} busy={busy} inputKey={inputKey} setInputKey={setInputKey}
          inputPayload={inputPayload} setInputPayload={setInputPayload} onDeliver={deliver} />}

        {inst && <EffectSummary records={inst.records} status={inst.status} />}

        <h2>执行历史（真实事件 {inst?.records.length || 0}）</h2>
        <div className="records">
          {inst ? <RecordList records={inst.records} /> : <p className="hint">未选择实例。</p>}
        </div>
      </aside>
    </div>
  );
}

// EffectSummary turns the raw event stream into an operator-facing answer to
// "which business actions already took effect" — especially after failure or a
// failed compensation.
function EffectSummary({ records, status }: { records: StepRecord[]; status: string }) {
  const effective = new Map<string, { node: string; name: string; compensated: boolean; compFailed: boolean }>();
  let failedAt = "";
  let failDetail = "";
  for (const r of records) {
    if (r.action === "succeed") {
      effective.set(r.nodeId, { node: r.nodeId, name: r.detail || r.nodeId, compensated: false, compFailed: false });
    } else if (r.action === "compensated") {
      const e = effective.get(r.nodeId);
      if (e) e.compensated = true;
    } else if (r.action === "compensation_fail") {
      const e = effective.get(r.nodeId);
      if (e) e.compFailed = true;
    } else if (r.action === "fail") {
      failedAt = r.nodeId;
      failDetail = r.detail || "";
    }
  }
  const items = [...effective.values()];
  return (
    <div className="effect-summary">
      <h2>已生效操作</h2>
      {status === "failed" && (
        <div className="effect-alert">
          实例失败于节点 <b>{failedAt}</b>{failDetail ? `（${failDetail}）` : ""}。
          补偿按执行路径逆序进行；若补偿再次失败，流程停在该补偿节点，已成功但尚未撤销的动作仍然生效。
        </div>
      )}
      {items.length === 0 ? <p className="hint">尚无成功的业务动作。</p> : (
        <ul>
          {items.map((e) => (
            <li key={e.node} className={e.compFailed ? "bad" : e.compensated ? "undone" : "kept"}>
              <b>{e.name}</b>（{e.node}）
              {e.compFailed ? <span className="tag tag-bad">补偿失败，仍生效</span>
                : e.compensated ? <span className="tag tag-undone">已补偿撤销</span>
                : <span className="tag tag-kept">已生效</span>}
            </li>
          ))}
        </ul>
      )}
      <p className="hint">动作中文含义：{Object.entries({ succeed: ACTION_LABEL.succeed, compensated: ACTION_LABEL.compensated }).map(([k, v]) => k + "=" + v).join("；")}。</p>
    </div>
  );
}
