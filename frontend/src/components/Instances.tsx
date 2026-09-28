import { useEffect, useMemo, useState } from "react";
import FlowCanvas from "./FlowCanvas";
import { api } from "../api";
import type { Definition, Instance } from "../types";
import { NODE_KIND_LABEL, STATUS_LABEL } from "../types";

export default function Instances({
  defs,
  instances,
  selectedId,
  onSelect,
  onChanged,
  notify
}: {
  defs: Definition[];
  instances: Instance[];
  selectedId: string | null;
  onSelect: (id: string | null) => void;
  onChanged: () => void;
  notify: (m: string, ok: boolean) => void;
}) {
  const selected = instances.find((i) => i.id === selectedId) ?? null;

  return (
    <div className="instances-layout">
      <div className="panel instance-list">
        <h4>实例</h4>
        <StartForm defs={defs} onChanged={onChanged} onSelect={onSelect} notify={notify} />
        <div className="instance-rows">
          {instances.map((i) => (
            <div
              key={i.id}
              className={`instance-row status-${i.status} ${selectedId === i.id ? "sel" : ""}`}
              onClick={() => onSelect(i.id)}
            >
              <div className="row-top">
                <strong>{i.defKey} v{i.version}</strong>
                <span className={`status-tag tag-${i.status}`}>{STATUS_LABEL[i.status]}</span>
              </div>
              <code>{i.id}</code>
            </div>
          ))}
          {instances.length === 0 && <div className="hint">还没有实例，先启动一个流程。</div>}
        </div>
      </div>
      <div className="instance-detail">
        {selected ? (
          <InstanceDetail key={selected.id} inst={selected} onChanged={onChanged} notify={notify} />
        ) : (
          <div className="panel empty-hint">选择左侧实例查看执行记录与流程图。</div>
        )}
      </div>
    </div>
  );
}

function StartForm({
  defs,
  onChanged,
  onSelect,
  notify
}: {
  defs: Definition[];
  onChanged: () => void;
  onSelect: (id: string) => void;
  notify: (m: string, ok: boolean) => void;
}) {
  const latest = useMemo(() => {
    const map = new Map<string, Definition>();
    defs.forEach((d) => {
      const cur = map.get(d.key);
      if (!cur || d.version > cur.version) map.set(d.key, d);
    });
    return [...map.values()];
  }, [defs]);
  const [key, setKey] = useState(latest[0]?.key ?? "");
  const [vars, setVars] = useState(
    '{\n  "amount": 200\n}'
  );

  useEffect(() => {
    if (!key && latest[0]) setKey(latest[0].key);
  }, [defs]);

  const start = async () => {
    let input: unknown = {};
    const trimmed = vars.trim();
    if (trimmed) {
      try {
        input = JSON.parse(trimmed);
      } catch (e) {
        notify("起始变量不是合法 JSON: " + (e as Error).message, false);
        return;
      }
    }
    try {
      const inst = await api.startInstance(key, input as Record<string, unknown>);
      notify(`实例已启动：${inst.id}（${STATUS_LABEL[inst.status]}）`, true);
      onSelect(inst.id);
      onChanged();
    } catch (e) {
      notify("启动失败: " + (e as Error).message, false);
    }
  };

  return (
    <div className="start-form">
      <select value={key} onChange={(e) => setKey(e.target.value)}>
        {latest.map((d) => (
          <option key={d.key} value={d.key}>{d.key}（最新 v{d.version}）</option>
        ))}
      </select>
      <textarea rows={5} value={vars} onChange={(e) => setVars(e.target.value)} spellCheck={false} />
      <div className="hint">变量 failTasks/failComp 可填节点 ID 模拟任务或补偿失败</div>
      <button className="primary" onClick={start}>启动实例</button>
    </div>
  );
}

function InstanceDetail({
  inst: initial,
  onChanged,
  notify
}: {
  inst: Instance;
  onChanged: () => void;
  notify: (m: string, ok: boolean) => void;
}) {
  const [inst, setInst] = useState(initial);
  const [auto, setAuto] = useState(true);

  useEffect(() => setInst(initial), [initial]);

  const refresh = async () => {
    try {
      setInst(await api.getInstance(inst.id));
    } catch (e) {
      notify((e as Error).message, false);
    }
  };

  useEffect(() => {
    if (!auto) return;
    const t = setInterval(refresh, 2000);
    return () => clearInterval(t);
  }, [auto, inst.id, inst.status]);

  const waitingEntries = Object.entries(inst.waitingFor);

  return (
    <div className="detail-grid">
      <div className="panel detail-canvas">
        <div className="detail-head">
          <div>
            <strong>{inst.defKey} v{inst.version}</strong>
            <code className="inst-id">{inst.id}</code>
          </div>
          <span className={`status-tag tag-${inst.status}`}>{STATUS_LABEL[inst.status]}</span>
          <label className="auto-label">
            <input type="checkbox" checked={auto} onChange={(e) => setAuto(e.target.checked)} /> 自动刷新
          </label>
          <button onClick={refresh}>刷新</button>
        </div>
        {inst.error && <div className="error-banner">{inst.error}</div>}
        <div className="legend">
          <span><i className="dot fired" />已执行（来自流转记录）</span>
          <span><i className="dot comp" />补偿流转（来自 comp 记录）</span>
          <span><i className="dot idle" />定义中未触发的边
          </span>
        </div>
        <FlowCanvas def={inst.definition} inst={inst} />
      </div>

      <div className="detail-side">
        {waitingEntries.length > 0 && (
          <InputPanel
            instId={inst.id}
            waiting={waitingEntries}
            onDone={async (updated) => {
              setInst(updated);
              onChanged();
            }}
            notify={notify}
          />
        )}
        <RecordPanel title="流转记录（箭头数据源）" empty="尚无流转记录">
          {[...inst.transitions].reverse().map((t) => (
            <div key={t.seq} className={`rec-row ${t.comp ? "rec-comp" : ""}`}>
              <span className="seq">#{t.seq}</span>
              <span>{t.from} → {t.to}</span>
              {t.comp && <span className="mini-tag comp-tag">补偿</span>}
            </div>
          ))}
        </RecordPanel>
        <RecordPanel title="步骤记录（真实停留点）" empty="尚无步骤">
          {[...inst.steps].reverse().map((s) => (
            <div key={s.seq} className={`rec-row ${s.comp ? "rec-comp" : ""} ${s.result === "error" ? "rec-err" : ""}`}>
              <span className="seq">#{s.seq}</span>
              <span>{NODE_KIND_LABEL[s.kind]} {s.nodeId} · {s.event === "enter" ? "进入" : "离开"}</span>
              {s.comp && <span className="mini-tag comp-tag">补偿</span>}
              {s.result === "error" && <span className="mini-tag err-tag">失败</span>}
              {s.result === "ok" && <span className="mini-tag ok-tag">成功</span>}
            </div>
          ))}
        </RecordPanel>
        <RecordPanel title="外部输入" empty="尚无外部输入">
          {[...inst.inputs].reverse().map((inp) => (
            <div key={inp.seq} className={`rec-row ${inp.duplicate ? "rec-dup" : ""}`}>
              <span className="seq">#{inp.seq}</span>
              <span>{inp.nodeId}（{inp.inputKey || "-"}）</span>
              {inp.duplicate
                ? <span className="mini-tag dup-tag">重复·未推进</span>
                : <span className="mini-tag ok-tag">已生效</span>}
              <code className="idem">{inp.idempotencyKey}</code>
            </div>
          ))}
        </RecordPanel>
        <RecordPanel title="流程变量" empty="无变量">
          <pre className="vars-json">{JSON.stringify(inst.variables, null, 2)}</pre>
        </RecordPanel>
      </div>
    </div>
  );
}

function InputPanel({
  instId,
  waiting,
  onDone,
  notify
}: {
  instId: string;
  waiting: [string, string][];
  onDone: (inst: Instance) => void;
  notify: (m: string, ok: boolean) => void;
}) {
  const [nodeId, setNodeId] = useState(waiting[0][0]);
  const [idem, setIdem] = useState("");
  const [payloadText, setPayloadText] = useState('{\n  "approved": true\n}');
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!waiting.some(([id]) => id === nodeId)) setNodeId(waiting[0][0]);
  }, [waiting]);

  const submit = async () => {
    let payload: unknown = payloadText;
    const t = payloadText.trim();
    if (t.startsWith("{") || t.startsWith("[")) {
      try {
        payload = JSON.parse(t);
      } catch (e) {
        notify("输入内容不是合法 JSON: " + (e as Error).message, false);
        return;
      }
    }
    setBusy(true);
    try {
      const updated = await api.submitInput(instId, nodeId, idem.trim(), payload);
      notify("外部输入已提交", true);
      onDone(updated);
    } catch (e) {
      notify("提交失败: " + (e as Error).message, false);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="panel input-panel">
      <h4>人工模拟外部输入</h4>
      <label>等待节点
        <select value={nodeId} onChange={(e) => setNodeId(e.target.value)}>
          {waiting.map(([id, key]) => (
            <option key={id} value={id}>{id}（键: {key}）</option>
          ))}
        </select>
      </label>
      <label>幂等键（相同键重复提交只生效一次；留空自动生成）
        <input value={idem} onChange={(e) => setIdem(e.target.value)} placeholder="例如 approval-001" />
      </label>
      <textarea rows={4} value={payloadText} onChange={(e) => setPayloadText(e.target.value)} spellCheck={false} />
      <button className="primary" disabled={busy} onClick={submit}>
        {busy ? "提交中…" : "提交输入并推进"}
      </button>
    </div>
  );
}

function RecordPanel({ title, empty, children }: { title: string; empty: string; children: React.ReactNode }) {
  const hasChildren = Array.isArray(children) ? children.length > 0 : Boolean(children);
  return (
    <div className="panel record-panel">
      <h4>{title}</h4>
      {hasChildren ? children : <div className="hint">{empty}</div>}
    </div>
  );
}
