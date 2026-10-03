import { useCallback, useEffect, useMemo, useState } from "react";
import { ReactFlow, ReactFlowProvider } from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { api } from "../api";
import type { Definition, Instance, MigrationPreview, StepRecord, WaitState } from "../types";
import { applyExecution, definitionToFlow } from "../flowView";
import { RecordList, STATUS_LABEL, stopLabel, consumedKeys } from "./RecordList";
import { ReconsiderSection } from "./Reconsider";

interface Props {
  definitions: Definition[];
  latestByName: Map<string, Definition>;
  refreshKey: number;
  onEditedPublished: () => void;
  onGoEditor: () => void;
}

export function Instances({ definitions, latestByName, refreshKey, onEditedPublished, onGoEditor }: Props) {
  const [instances, setInstances] = useState<Instance[]>([]);
  const [selectedId, setSelectedId] = useState<string>("");
  const [detail, setDetail] = useState<{ instance: Instance; definition: Definition } | null>(null);
  const [defName, setDefName] = useState("order-demo");
  const [initData, setInitData] = useState('{\n  "amount": 200\n}');
  const [inputKey, setInputKey] = useState("approve-001");
  const [inputPayload, setInputPayload] = useState('{\n  "approved": true\n}');
  const [inputFrom, setInputFrom] = useState("");
  const [notice, setNotice] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [listStatus, setListStatus] = useState<"loading" | "ready" | "error">("loading");
  const [listError, setListError] = useState("");

  const defNameOf = useCallback(
    (id: string) => definitions.find((d) => d.id === id)?.name || id,
    [definitions]
  );

  const loadList = useCallback(async (preferId?: string) => {
    const list = await api.listInstances();
    setInstances(list);
    setListStatus("ready");
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
    setListStatus("loading");
    api.listInstances().then((list) => {
      setInstances(list);
      setListStatus("ready");
      setSelectedId((cur) => (cur && list.some((i) => i.id === cur) ? cur : list[0]?.id || ""));
    }).catch((e) => {
      setListError((e as Error).message);
      setListStatus("error");
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [refreshKey]);

  function retryList() {
    setListStatus("loading");
    setListError("");
    api.listInstances().then((list) => {
      setInstances(list);
      setListStatus("ready");
      setSelectedId((cur) => (cur && list.some((i) => i.id === cur) ? cur : list[0]?.id || ""));
    }).catch((e) => {
      setListError((e as Error).message);
      setListStatus("error");
    });
  }

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
      const inst = await api.deliverInput(detail.instance.id, detail.instance.waitNode || "", key, payload, inputFrom || undefined);
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
        {latestByName.size === 0 ? (
          <div className="state-empty block-state">
            <p><b>还没有可启动的流程定义</b></p>
            <p className="hint">先到「流程定义」页发布一个定义（页面已附带示例模板，直接发布即可），发布后回到这里就能启动实例。</p>
            <button className="primary" onClick={onGoEditor}>去发布流程定义</button>
          </div>
        ) : (
          <>
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
          </>
        )}
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
          {listStatus === "loading" && <li className="state-loading">正在加载实例…</li>}
          {listStatus === "error" && (
            <li className="state-error">
              <span className="error">实例列表加载失败：{listError}</span>
              <button onClick={retryList}>重新加载</button>
            </li>
          )}
          {listStatus === "ready" && instances.length === 0 && (
            <li className="state-empty">暂无实例{latestByName.size > 0 ? "，在上方启动一个试试" : "，请先发布流程定义"}。</li>
          )}
        </ul>
      </aside>

      <div className="canvas execution">
        {detail && detail.definition ? (
          <>
            <div className="execution-bar">
              <span className={"status " + detail.instance.status}>{STATUS_LABEL[detail.instance.status]}</span>
              <span>{detail.definition.name} v{detail.instance.version}</span>
              <span>停在：<b>{stopLabel(detail.instance, detail.definition)}</b></span>
              {detail.instance.compensating && (
                <span className="compensating-tag">补偿进行中…（已完成的补偿不会重复执行，服务重启也会安全续跑）</span>
              )}
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
            {detail.instance.status === "failed" && !detail.instance.compensating && (
              <div className="execution-fail-banner">
                执行失败，真实停点：<b>{stopLabel(detail.instance, detail.definition)}</b>。下方事件流中可查看失败与补偿详情；刷新页面不会丢失这些记录。
              </div>
            )}
            <p className="hint arrow-note">绿色箭头只来自执行记录中真实发生的 branch / traverse 事件。</p>
          </>
        ) : listStatus === "loading" ? (
          <div className="empty">正在加载实例…</div>
        ) : listStatus === "error" ? (
          <div className="empty state-error-col">
            <span className="error">实例加载失败：{listError}</span>
            <button onClick={retryList}>重新加载</button>
          </div>
        ) : (
          <div className="empty">请选择或启动一个实例</div>
        )}
      </div>

      <aside className="panel records-panel">
        <h2>人工输入</h2>
        {detail?.instance.status === "waiting" ? (
          <>
            {detail.instance.wait && (
              <label>参与方 from（协作等待必须指明）
                <select value={inputFrom} onChange={(e) => setInputFrom(e.target.value)}>
                  <option value="">请选择参与方</option>
                  {effectiveParties(detail.instance.wait).map((p) => (
                    <option key={p} value={p}>{p}{detail.instance.wait!.received[p] ? "（已确认）" : ""}</option>
                  ))}
                </select>
              </label>
            )}
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

        {detail?.instance.wait && (
          <CollabWaitSection
            wait={detail.instance.wait}
            instanceId={detail.instance.id}
            waiting={detail.instance.status === "waiting"}
            busy={busy}
            setBusy={setBusy}
            onChanged={async () => {
              await loadDetail(detail.instance.id);
              await loadList(detail.instance.id);
            }}
            onNotice={setNotice}
            onError={setError}
          />
        )}

        {detail && (
          <ReconsiderSection
            instance={detail.instance}
            busy={busy}
            setBusy={setBusy}
            onChanged={async () => {
              await loadDetail(detail.instance.id);
              await loadList(detail.instance.id);
            }}
            onNotice={setNotice}
            onError={setError}
          />
        )}

        {detail?.instance.status === "waiting" && (
          <MigrationSection
            detail={detail}
            definitions={definitions}
            busy={busy}
            onMigrated={async () => {
              if (detail) {
                await loadDetail(detail.instance.id);
                await loadList(detail.instance.id);
              }
            }}
            onNotice={setNotice}
            onError={setError}
            setBusy={setBusy}
          />
        )}

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

const IMPACT_KIND_LABEL: Record<string, string> = {
  added: "新增步骤",
  removed: "删除步骤",
  changed: "步骤定义变化",
  edge_added: "新增连线",
  edge_changed: "连线条件变化"
};

function MigrationSection({
  detail,
  definitions,
  busy,
  onMigrated,
  onNotice,
  onError,
  setBusy
}: {
  detail: { instance: Instance; definition: Definition };
  definitions: Definition[];
  busy: boolean;
  onMigrated: () => Promise<void>;
  onNotice: (s: string) => void;
  onError: (s: string) => void;
  setBusy: (b: boolean | ((b: boolean) => boolean)) => void;
}) {
  const processName = detail.definition.name;
  const currentVersion = detail.instance.version;
  const newer = definitions
    .filter((d) => d.name === processName && d.version > currentVersion)
    .sort((a, b) => b.version - a.version);
  const [targetId, setTargetId] = useState(newer[0]?.id || "");
  const [preview, setPreview] = useState<MigrationPreview | null>(null);
  const [previewError, setPreviewError] = useState("");

  useEffect(() => {
    setPreview(null);
    setPreviewError("");
    setTargetId(newer[0]?.id || "");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [detail.instance.id, detail.instance.version, definitions.length]);

  if (newer.length === 0) {
    return (
      <div className="migration-section">
        <h2>版本迁移</h2>
        <p className="hint">该实例固定使用 {processName} v{currentVersion}；暂无更新版本。已运行/等待中的实例永远不会被新发布自动改写。</p>
      </div>
    );
  }

  async function doPreview() {
    onError("");
    onNotice("");
    setBusy(true);
    try {
      setPreview(await api.previewMigration(detail.instance.id, { definitionId: targetId }));
      setPreviewError("");
    } catch (e) {
      setPreview(null);
      setPreviewError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }

  async function doConfirm() {
    onError("");
    onNotice("");
    setBusy(true);
    try {
      await api.confirmMigration(detail.instance.id, { definitionId: targetId });
      onNotice("实例已迁移到所选版本；旧版本历史原样保留，提交输入后按新版本执行。");
      await onMigrated();
    } catch (e) {
      onError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="migration-section">
      <h2>迁移到新版本（仅影响等待点之后）</h2>
      <p className="hint">
        该实例正等待人工输入并固定 {processName} v{currentVersion}。新版本不会自动套用；
        迁移后旧版本产生的历史记录原样保留，不会与新版本拼接。
      </p>
      <label>目标版本
        <select value={targetId} onChange={(e) => { setTargetId(e.target.value); setPreview(null); }}>
          {newer.map((d) => (
            <option key={d.id} value={d.id}>{processName} v{d.version}</option>
          ))}
        </select>
      </label>
      <button disabled={busy || !targetId} onClick={doPreview}>先查看受影响步骤</button>
      {previewError && <div className="error">{previewError}</div>}
      {preview && (
        <div className="migration-preview">
          <p className="hint">
            等待点「{preview.impact.waitNodeName}」之后的变化（v{preview.impact.fromVersion} → v{preview.impact.toVersion}）：
          </p>
          {preview.impact.affectedAhead.length === 0 ? (
            <p className="hint">等待点之后没有差异，迁移不改变后续行为。</p>
          ) : (
            <ul className="impact-list">
              {preview.impact.affectedAhead.map((a, idx) => (
                <li key={a.nodeId + "-" + a.kind + "-" + idx}>
                  <span className={"impact-kind impact-" + a.kind}>{IMPACT_KIND_LABEL[a.kind] || a.kind}</span>
                  <span>{a.name ? a.name + "（" + a.nodeId + "）" : a.nodeId}</span>
                </li>
              ))}
            </ul>
          )}
          <p className="hint">确认迁移只改变固定版本；已完成步骤不重做，已生效的幂等 key 仍然有效。</p>
          <button className="primary danger-confirm" disabled={busy} onClick={doConfirm}>我已了解受影响步骤，确认迁移</button>
        </div>
      )}
    </div>
  );
}

// effectiveParties lists who may confirm right now (delegates replace their
// delegators) under the current authorization scope.
function effectiveParties(wait: WaitState): string[] {
  const auth = wait.authorizations[wait.authorizations.length - 1];
  if (!auth) return [];
  const out: string[] = [];
  for (const p of auth.participants) {
    const d = auth.delegations?.[p];
    out.push(d || p);
  }
  return [...new Set(out)];
}

const DECISION_LABEL: Record<string, string> = {
  approved: "全部确认通过",
  rejected: "已拒绝",
  timeout_approved: "超时按策略通过",
  timeout_rejected: "超时按策略拒绝"
};

function CollabWaitSection({
  wait,
  instanceId,
  waiting,
  busy,
  setBusy,
  onChanged,
  onNotice,
  onError
}: {
  wait: WaitState;
  instanceId: string;
  waiting: boolean;
  busy: boolean;
  setBusy: (b: boolean) => void;
  onChanged: () => Promise<void>;
  onNotice: (s: string) => void;
  onError: (s: string) => void;
}) {
  const auth = wait.authorizations[wait.authorizations.length - 1];
  const [participants, setParticipants] = useState(auth?.participants.join(",") || "");
  const [reason, setReason] = useState("");
  const [keepPrior, setKeepPrior] = useState(true);
  const [grantFrom, setGrantFrom] = useState("");
  const [grantTo, setGrantTo] = useState("");
  const [revoke, setRevoke] = useState("");

  async function run(fn: () => Promise<unknown>, ok: string) {
    onError("");
    onNotice("");
    setBusy(true);
    try {
      await fn();
      onNotice(ok);
      await onChanged();
    } catch (e) {
      onError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }

  function applyScope() {
    const list = participants.split(/[,，]/).map((s) => s.trim()).filter(Boolean);
    const grants: Record<string, string> = {};
    if (grantFrom && grantTo) grants[grantFrom] = grantTo;
    return run(
      () =>
        api.updateWaitScope(instanceId, {
          participants: list,
          grantDelegations: Object.keys(grants).length ? grants : undefined,
          revokeDelegations: revoke ? [revoke] : undefined,
          keepPriorInputs: keepPrior,
          reason: reason || undefined
        }),
      "接收范围已更新；新旧授权记录都保留在下方历史中"
    );
  }

  return (
    <div className="collab-section">
      <h3>协作等待（多方输入）</h3>
      {wait.decided ? (
        <p className="hint">
          已决定：<b>{DECISION_LABEL[wait.decision || ""] || wait.decision}</b>
          （依据当时授权范围 {wait.decisionAuthId}）
        </p>
      ) : (
        <p className="hint">
          需全部参与方确认后才完成；当前 {Object.keys(wait.received).length}/{auth?.participants.length ?? 0}。
          {wait.deadline ? "到期时间：" + new Date(wait.deadline).toLocaleString() : "不限时。"}
        </p>
      )}
      <ul className="party-list">
        {auth?.participants.map((p) => {
          const delegate = auth.delegations?.[p];
          const effective = delegate || p;
          const got = wait.received[effective];
          return (
            <li key={p} className={got ? "party-done" : ""}>
              {got ? "✅" : "⏳"} {p}
              {delegate ? <span className="hint">（已委托 {delegate}）</span> : null}
              {got ? <span className="hint">　已于 {new Date(got.at).toLocaleTimeString()} 确认（范围 {got.authId}）</span> : null}
            </li>
          );
        })}
      </ul>

      {waiting && !wait.decided && (
        <>
          <h4>修改接收范围 / 委托</h4>
          <label>参与方（逗号分隔）
            <input value={participants} onChange={(e) => setParticipants(e.target.value)} />
          </label>
          <label>委托：参与方
            <select value={grantFrom} onChange={(e) => setGrantFrom(e.target.value)}>
              <option value="">（不新增委托）</option>
              {auth?.participants.map((p) => <option key={p} value={p}>{p}</option>)}
            </select>
          </label>
          {grantFrom && (
            <label>代为确认的受托方
              <input value={grantTo} onChange={(e) => setGrantTo(e.target.value)} placeholder="dave" />
            </label>
          )}
          <label>撤回委托（参与方）
            <select value={revoke} onChange={(e) => setRevoke(e.target.value)}>
              <option value="">（不撤回）</option>
              {Object.keys(auth?.delegations || {}).map((p) => <option key={p} value={p}>{p}（现受托 {auth!.delegations![p]}）</option>)}
            </select>
          </label>
          <label className="checkbox">
            <input type="checkbox" checked={keepPrior} onChange={(e) => setKeepPrior(e.target.checked)} />
            此前已收到的输入仍然有效（取消勾选则全部失效，仅作历史保留）
          </label>
          <label>变更说明<input value={reason} onChange={(e) => setReason(e.target.value)} placeholder="bob 休假，委托 dave" /></label>
          <button disabled={busy} onClick={applyScope}>应用范围变更（旧授权记录保留）</button>
          <button disabled={busy} onClick={() => run(() => api.evaluateTimeout(instanceId), "已执行到期判定")}>立即进行到期判定</button>
        </>
      )}

      <h4>授权记录（{wait.authorizations.length} 个版本，全部保留）</h4>
      <ul className="auth-history">
        {wait.authorizations.map((a) => (
          <li key={a.id}>
            <b>{a.id}</b>：{a.participants.join("、")}；委托{a.allowDelegate ? "允许" : "不允许"}
            {Object.keys(a.delegations || {}).length > 0 &&
              "；" + Object.entries(a.delegations!).map(([k, v]) => k + "→" + v).join("，")}
            {a.priorInputsKept ? "；此前输入有效" : "；此前输入失效"}
            {a.reason ? "；" + a.reason : ""}
          </li>
        ))}
      </ul>
    </div>
  );
}
