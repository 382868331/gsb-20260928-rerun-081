import { useCallback, useEffect, useMemo, useState } from "react";
import { api } from "../api";
import type { Definition, Instance } from "../types";
import { STATUS_LABEL, RecordList, stopLabel } from "./RecordList";

export function History({ definitions }: { definitions: Definition[] }) {
  const [instances, setInstances] = useState<Instance[]>([]);
  const [selectedId, setSelectedId] = useState("");
  const [detail, setDetail] = useState<{ instance: Instance; definition: Definition } | null>(null);
  const [filter, setFilter] = useState("all");
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    const list = (await api.listInstances()) ?? [];
    setInstances(list);
    if (!selectedId && list[0]) setSelectedId(list[0].id);
  }, [selectedId]);

  useEffect(() => {
    load().catch((e) => setError((e as Error).message));
  }, [load]);

  useEffect(() => {
    if (!selectedId) {
      setDetail(null);
      return;
    }
    api.getInstance(selectedId).then(setDetail).catch(() => setDetail(null));
  }, [selectedId]);

  const defOf = useCallback((id: string) => definitions.find((d) => d.id === id), [definitions]);

  const filtered = useMemo(
    () => (filter === "all" ? instances : instances.filter((i) => i.status === filter)),
    [instances, filter]
  );

  const nodeName = useMemo(() => {
    const m = new Map<string, string>();
    detail?.definition.nodes.forEach((n) => m.set(n.id, n.name));
    return m;
  }, [detail]);

  const inst = detail?.instance;
  const succeededActions = inst?.records.filter((r) => r.action === "succeed").length || 0;
  const compActions = inst?.records.filter((r) => r.action === "compensated").length || 0;
  const ignoredInputs = inst?.records.filter((r) => r.action === "duplicate_input" || r.action === "late_input").length || 0;

  return (
    <div className="history-layout">
      <aside className="panel">
        <h2>执行历史</h2>
        <label>按状态筛选
          <select value={filter} onChange={(e) => setFilter(e.target.value)}>
            <option value="all">全部</option>
            <option value="running">运行中</option>
            <option value="waiting">等待输入</option>
            <option value="completed">已完成</option>
            <option value="failed">失败</option>
          </select>
        </label>
        {error && <div className="error">{error}</div>}
        <ul className="instance-list">
          {filtered.map((i) => {
            const d = defOf(i.definitionId);
            return (
              <li key={i.id} className={i.id === selectedId ? "active" : ""} onClick={() => setSelectedId(i.id)}>
                <span className={"status " + i.status}>{STATUS_LABEL[i.status]}</span>
                <span className="hint">{d?.name || i.definitionId} v{i.version}</span>
                <span className="hint mono">{i.id}</span>
                <span className="hint">{new Date(i.createdAt).toLocaleString()}</span>
              </li>
            );
          })}
          {filtered.length === 0 && <li className="hint">没有匹配的实例。</li>}
        </ul>
      </aside>

      <section className="panel history-detail">
        {inst && detail.definition ? (
          <>
            <h2>
              {detail.definition.name} v{inst.version} <span className={"status " + inst.status}>{STATUS_LABEL[inst.status]}</span>
            </h2>
            <div className="summary-grid">
              <div><span className="hint">真实停点</span><b>{stopLabel(inst, detail.definition)}</b></div>
              <div><span className="hint">已生效任务</span><b>{succeededActions} 个</b></div>
              <div><span className="hint">已完成补偿</span><b>{compActions} 个</b></div>
              <div><span className="hint">被忽略的重复/迟到输入</span><b>{ignoredInputs} 次</b></div>
              <div><span className="hint">固定定义</span><b className="mono">{inst.definitionId}</b></div>
              <div><span className="hint">实例</span><b className="mono">{inst.id}</b></div>
            </div>
            <h3>事件流（全部来自后端真实记录）</h3>
            <RecordList records={inst.records} nodeName={nodeName} />
          </>
        ) : (
          <div className="empty">选择左侧实例查看执行历史</div>
        )}
      </section>
    </div>
  );
}
