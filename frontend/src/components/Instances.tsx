import { useCallback, useEffect, useMemo, useState } from "react";
import { ReactFlow, ReactFlowProvider } from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { api } from "../api";
import type { Definition, Instance, StepRecord } from "../types";
import { applyExecution, definitionToFlow } from "../flowView";
import { RecordList, STATUS_LABEL, stopLabel, consumedKeys } from "./RecordList";

interface Props {
  definitions: Definition[];
  latestByName: Map<string, Definition>;
  refreshKey: number;
  onEditedPublished: () => void;
}

export function Instances({ definitions, latestByName, refreshKey, onEditedPublished }: Props) {
  const [instances, setInstances] = useState<Instance[]>([]);
  const [selectedId, setSelectedId] = useState<string>("");
  const [detail, setDetail] = useState<{ instance: Instance; definition: Definition } | null>(null);
  const [defName, setDefName] = useState("order-demo");
  const [initData, setInitData] = useState('{\n  "amount": 200\n}');
  const [inputKey, setInputKey] = useState("approve-001");
  const [inputPayload, setInputPayload] = useState('{\n  "approved": true\n}');
  const [notice, setNotice] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const defNameOf = useCallback(
    (id: string) => definitions.find((d) => d.id === id)?.name || id,
    [definitions]
  );

  const loadList = useCallback(async (preferId?: string) => {
    const list = await api.listInstances();
    setInstances(list);
    const pick = preferId || (list.some((i) => i.id === selectedId) ? selectedId : list[0]?.id || "");
    if (pick && pick !== selectedId) setSelectedId(pick);
    return pick;
  }, [selectedId]);

  const loadDetail = useCallback(async (id: string) => {
    if (!id) {
      setDetail(null);
      return;
    }
    try {
      setDetail(await api.getInstance(id));
    } catch {
      setDetail(null);
    }
  }, []);

  useEffect(() => {
    api.listInstances().then((list) => {
      setInstances(list);
      setSelectedId((cur) => (cur && list.some((i) => i.id === cur) ? cur : list[0]?.id || ""));
    }).catch((e) => setError((e as Error).message));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [refreshKey]);

  useEffect(() => {
    loadDetail(selectedId).catch(() => setDetail(null));
  }, [selectedId, loadDetail]);

  async function start() {
    setError("");
    setNotice("");
    setBusy(true);
    try {
      const data = JSON.parse(initData || "{}");
      const target = latestByName.get(defName);
      if (!target) throw new Error("没有名为 “" + defName + "” 的已发布流程，请先在“流程定义”页发布");
      const inst = await api.startInstance(target.id, data);
      await loadList(inst.id);
      await loadDetail(inst.id);
      setNotice("实例已启动，固定使用 " + target.name + " v" + target.version);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }

  async function deliver() {
    if (!detail) return;
    setError("");
    setNotice("");
    setBusy(true);
    try {
      const payload = JSON.parse(inputPayload || "{}");
      const key = inputKey.trim();
      if (!key) throw new Error("请填写幂等键 key（相同 key 的迟到/重复输入不会重做业务动作）");
      const inst = await api.deliverInput(detail.instance.id, detail.instance.waitNode || "", key, payload);
      await loadDetail(inst.id);
      await loadList(inst.id);
      const late = lastSpecial(inst.records, key);
      setNotice(late || "输入已提交");
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }

  async function resume() {
    if (!detail) return;
    setBusy(true);
    setError("");
    try {
      const inst = await api.resumeInstance(detail.instance.id);
      await loadDetail(inst.id);
      await loadList(inst.id);
      setNotice("重启恢复完成，当前状态：" + STATUS_LABEL[inst.status]);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }

  const flow = detail && detail.definition ? definitionToFlow(detail.definition) : { nodes: [], edges: [] };
  const executed = detail
    ? applyExecution(flow.nodes, flow.edges, detail.instance.records, detail.instance.stoppedAt)
    : flow;

  const nodeName = useMemo(() => {
    const m = new Map<string, string>();
    detail?.definition.nodes.forEach((n) => m.set(n.id, n.name));
    return m;
  }, [detail]);

  return (
    <div className="instance-layout">
      <aside className="panel">
        <h2>启动新实例</h2>
        <label>选择流程（始终启动其最新版本）
          <select value={defName} onChange={(e) => setDefName(e.target.value)}>
            {[...latestByName.keys()].map((n) => (
              <option key={n} value={n}>{n} v{latestByName.get(n)!.version}</option>
            ))}
          </select>
        </label>
        <label>初始数据 JSON
          <textarea rows={6} value={initData} onChange={(e) => setInitData(e.target.value)} />
        </label>
        <p className="hint">
          分支演示：amount ≥ 100 走大额分支，否则走默认分支。<br />
          失败演示：failAt 填任务节点 id（如 ship）；补偿再失败：compFailAt 填 id（如 reserve）。
        </p>
        <button className="primary" disabled={busy} onClick={start}>启动并运行到等待 / 结束 / 失败</button>
        <button onClick={onEditedPublished} style={{ marginTop: 6 }}>刷新定义列表</button>
        {notice && <div className="notice">{notice}</div>}
        {error && <div className="error">{error}</div>}

        <h2>实例列表（{instances.length}）</h2>
        <ul className="instance-list">
          {instances.map((i) => (
            <li key={i.id} className={i.id === selectedId ? "active" : ""} onClick={() => setSelectedId(i.id)}>
              <span className={"status " + i.status}>{STATUS_LABEL[i.status]}</span>
              <span className="hint">
                {defNameOf(i.definitionId)} v{i.version}
              </span>
              <span className="hint">
                {i.status === "waiting"
                  ? "等待：" + (nodeNameFor(definitions, i.definitionId, i.waitNode) || i.waitNode)
                  : "停点：" + (nodeNameFor(definitions, i.definitionId, i.stoppedAt || i.currentNode) || "-")}
              </span>
              <span className="hint mono">{i.id}</span>
            </li>
          ))}
          {instances.length === 0 && <li className="hint">暂无实例，启动一个试试。</li>}
        </ul>
      </aside>

      <div className="canvas execution">
        {detail && detail.definition ? (
          <>
            <div className="execution-bar">
              <span className={"status " + detail.instance.status}>{STATUS_LABEL[detail.instance.status]}</span>
              <span>{detail.definition.name} v{detail.instance.version}</span>
              <span>停在：<b>{stopLabel(detail.instance, detail.definition)}</b></span>
              {detail.instance.status === "waiting" && (
                <span className="waiting-tag">等待节点「{detail.definition.nodes.find((n) => n.id === detail.instance.waitNode)?.name || detail.instance.waitNode}」的外部输入</span>
              )}
              {detail.instance.status === "running" && (
                <button disabled={busy} onClick={resume}>模拟重启后继续（resume）</button>
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
            <p className="hint arrow-note">绿色箭头只来自执行记录中真实发生的 branch / traverse 事件。</p>
          </>
        ) : <div className="empty">请选择或启动一个实例</div>}
      </div>

      <aside className="panel records-panel">
        <h2>人工输入</h2>
        {detail?.instance.status === "waiting" ? (
          <>
            <label>幂等键 key（相同 key 的重复 / 迟到提交只生效一次）
              <input value={inputKey} onChange={(e) => setInputKey(e.target.value)} placeholder="approve-001" />
            </label>
            <label>输入内容 JSON<textarea rows={4} value={inputPayload} onChange={(e) => setInputPayload(e.target.value)} /></label>
            <button className="primary" disabled={busy} onClick={deliver}>提交输入并继续</button>
          </>
        ) : (
          <>
            <p className="hint">实例当前不等待输入（状态：{detail ? STATUS_LABEL[detail.instance.status] : "-"}）。迟到输入仍可提交，系统会记录并忽略，不会重做业务动作。</p>
            {detail && detail.instance.status !== "running" && (
              <>
                <label>迟到输入的幂等键 key<input value={inputKey} onChange={(e) => setInputKey(e.target.value)} /></label>
                <label>内容 JSON<textarea rows={3} value={inputPayload} onChange={(e) => setInputPayload(e.target.value)} /></label>
                <button disabled={busy} onClick={deliver}>提交迟到输入（应被忽略）</button>
              </>
            )}
          </>
        )}

        <h3>已生效的输入 key</h3>
        <div className="consumed">{consumedKeys(detail?.instance).length ? consumedKeys(detail!.instance).map((k) => <span key={k} className="key-chip">{k}</span>) : <span className="hint">无</span>}</div>

        <h2>执行历史（{detail?.instance.records.length || 0} 条真实事件）</h2>
        <RecordList records={detail?.instance.records || []} nodeName={nodeName} />
      </aside>
    </div>
  );
}

function nodeNameFor(defs: Definition[], defId: string, nodeId?: string): string {
  if (!nodeId) return "";
  const d = defs.find((x) => x.id === defId);
  return d?.nodes.find((n) => n.id === nodeId)?.name || nodeId;
}

function lastSpecial(recs: StepRecord[], key: string): string {
  for (let i = recs.length - 1; i >= 0; i--) {
    const r = recs[i];
    if (r.detail?.includes(key)) {
      if (r.action === "duplicate_input") return "重复输入已被幂等忽略，业务动作未重复执行";
      if (r.action === "late_input") return "迟到输入已记录并忽略，实例停点保持不变";
      if (r.action === "input_received") return "输入 " + key + " 生效，流程继续";
    }
  }
  return "输入已提交";
}
