import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "../api";
import type { Instance } from "../types";
import { ACTION_LABEL } from "../types";
import { RecordList, StatusPill, formatTime } from "./ExecutionViews";

type Filter = "all" | "waiting" | "failed" | "completed";

const FILTERS: { id: Filter; label: string }[] = [
  { id: "all", label: "全部" },
  { id: "waiting", label: "等待中" },
  { id: "failed", label: "失败/补偿" },
  { id: "completed", label: "已完成" }
];

export function History() {
  const [instances, setInstances] = useState<Instance[]>([]);
  const [selected, setSelected] = useState<Instance | null>(null);
  const [filter, setFilter] = useState<Filter>("all");
  const [error, setError] = useState("");
  const mountedRef = useRef(true);

  const load = useCallback(async () => {
    try {
      const list = await api.listInstances();
      if (!mountedRef.current) return;
      setInstances(list);
      setError("");
      setSelected((prev) => {
        if (!prev) return list[0] || null;
        return list.find((i) => i.id === prev.id) || list[0] || null;
      });
    } catch (e) {
      if (mountedRef.current) setError((e as Error).message);
    }
  }, []);

  useEffect(() => {
    mountedRef.current = true;
    load();
    const timer = setInterval(load, 4000);
    return () => {
      mountedRef.current = false;
      clearInterval(timer);
    };
  }, [load]);

  const visible = instances.filter((i) => (filter === "all" ? true : i.status === filter));

  return (
    <div className="history-layout">
      <aside className="panel">
        <div className="history-head">
          <h2>执行历史</h2>
          <button className="mini" onClick={load}>刷新</button>
        </div>
        <div className="btn-row filter-row">
          {FILTERS.map((f) => (
            <button key={f.id} className={filter === f.id ? "chip active" : "chip"} onClick={() => setFilter(f.id)}>
              {f.label}
            </button>
          ))}
        </div>
        {error && <div className="error">{error}</div>}
        <ul className="instance-list history-list">
          {visible.map((i) => (
            <li key={i.id} className={selected?.id === i.id ? "active" : ""} onClick={() => setSelected(i)}>
              <StatusPill status={i.status} />
              <span className="hint">{i.id} · v{i.version}</span>
              <span className="hint">停点 {i.stoppedAt || "-"} · {i.records.length} 个事件</span>
              <span className="hint">{formatTime(i.createdAt)}</span>
            </li>
          ))}
          {visible.length === 0 && <li className="hint">没有匹配的实例。</li>}
        </ul>
      </aside>

      <section className="panel records-panel big-records">
        {selected ? (
          <>
            <div className="execution-bar static">
              <StatusPill status={selected.status} />
              <span>实例 <code>{selected.id}</code></span>
              <span>固定定义 v{selected.version}（{selected.definitionId}）</span>
              <span>真实停点：<b>{selected.stoppedAt || "-"}</b></span>
            </div>
            <FailedExplanation instance={selected} />
            <h2>完整事件流（{selected.records.length}）</h2>
            <div className="records tall">
              <RecordList records={selected.records} />
            </div>
          </>
        ) : (
          <div className="empty">暂无历史实例</div>
        )}
      </section>
    </div>
  );
}

function FailedExplanation({ instance }: { instance: Instance }) {
  if (instance.status !== "failed") return null;
  const fail = instance.records.filter((r) => r.action === "fail").at(-1);
  const compFail = instance.records.filter((r) => r.action === "compensation_fail").at(-1);
  const kept = new Set<string>();
  const undone = new Set<string>();
  for (const r of instance.records) {
    if (r.action === "succeed") kept.add(r.nodeId);
    if (r.action === "compensated") { undone.add(r.nodeId); kept.delete(r.nodeId); }
  }
  return (
    <div className="effect-alert full">
      <div>失败节点：<b>{fail?.nodeId || "-"}</b>{fail?.detail ? `（${fail.detail}）` : ""}</div>
      {compFail && (
        <div>补偿再次失败于：<b>{compFail.nodeId}</b>（{compFail.detail}）；实例真实停点为 <b>{instance.stoppedAt}</b>。</div>
      )}
      <div>仍生效未撤销：{kept.size ? [...kept].join("，") : "无"}；已补偿撤销：{undone.size ? [...undone].join("，") : "无"}。</div>
      <div className="hint">动作含义参考：succeed={ACTION_LABEL.succeed}；compensation_fail={ACTION_LABEL.compensation_fail}。</div>
    </div>
  );
}
