import type { Instance, StepRecord } from "../types";
import { ACTION_LABEL } from "../types";

const STATUS_LABEL: Record<string, string> = {
  running: "运行中",
  waiting: "等待人工输入",
  completed: "已完成",
  failed: "失败"
};

export function StatusPill({ status }: { status: string }) {
  return <span className={"status " + status}>{STATUS_LABEL[status] || status}</span>;
}

export function RecordList({ records }: { records: StepRecord[] }) {
  return (
    <>
      {records.map((r) => (
        <div key={r.seq} className={"record record-" + r.action}>
          <span className="seq">#{r.seq}</span>
          <span className="rec-main">
            <b>{ACTION_LABEL[r.action] || r.action}</b>
            <code className="rec-node">{r.nodeId}</code>
            {r.edgeId ? <em> 沿边 {r.edgeId}</em> : null}
            <span className="rec-time">{formatTime(r.occurredAt)}</span>
            {r.detail ? <div className="rec-detail">{r.detail}</div> : null}
          </span>
        </div>
      ))}
    </>
  );
}

export function WaitingPanel({
  instance,
  busy,
  inputKey,
  setInputKey,
  inputPayload,
  setInputPayload,
  onDeliver
}: {
  instance: Instance;
  busy: boolean;
  inputKey: string;
  setInputKey: (v: string) => void;
  inputPayload: string;
  setInputPayload: (v: string) => void;
  onDeliver: () => void;
}) {
  if (instance.status !== "waiting") {
    return (
      <div className="waiting-panel">
        <h2>人工输入</h2>
        <p className="hint">实例当前不等待输入（状态：{STATUS_LABEL[instance.status]}）。迟到的输入不会重做已完成的业务动作。</p>
      </div>
    );
  }
  const consumed = Object.keys(instance.consumedInputs || {});
  return (
    <div className="waiting-panel">
      <h2>人工输入 · 节点 {instance.waitNode}</h2>
      <p className="hint">相同幂等 key 重复/迟到提交只记录为「已忽略」，绝不二次执行业务动作。</p>
      <label>幂等键 key
        <input value={inputKey} onChange={(e) => setInputKey(e.target.value)} placeholder="例如 approve-001" />
      </label>
      <label>输入内容 JSON
        <textarea rows={4} value={inputPayload} onChange={(e) => setInputPayload(e.target.value)} />
      </label>
      <button className="primary" disabled={busy} onClick={onDeliver}>{busy ? "提交中…" : "提交输入并继续"}</button>
      {consumed.length > 0 && (
        <p className="hint">已消费的 key：{consumed.join(", ")}（再次提交将被忽略）</p>
      )}
    </div>
  );
}

export function formatTime(s: string): string {
  if (!s) return "";
  const d = new Date(s);
  if (isNaN(d.getTime())) return s;
  return d.toLocaleTimeString("zh-CN", { hour12: false }) + " " + d.toLocaleDateString("zh-CN");
}
