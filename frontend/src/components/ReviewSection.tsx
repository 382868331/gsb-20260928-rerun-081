import { useCallback, useEffect, useState } from "react";
import { api } from "../api";
import type { Definition, Instance, ReviewProgress, ReviewState, WaitState } from "../types";

const REVIEW_DECISION_LABEL: Record<string, string> = {
  approved: "复议通过（推翻原决定的后续效果）",
  rejected: "复议拒绝（原决定继续有效）",
  expired: "复议到期未成立（原决定继续有效）"
};

const ORIG_DECISION_LABEL: Record<string, string> = {
  approved: "全部确认通过",
  rejected: "已拒绝",
  timeout_approved: "超时按策略通过",
  timeout_rejected: "超时按策略拒绝"
};

function effectiveReviewParties(rv: ReviewState): string[] {
  const auth = rv.authorizations[rv.authorizations.length - 1];
  if (!auth) return [];
  const out: string[] = [];
  for (const p of auth.participants) out.push(auth.delegations?.[p] || p);
  return [...new Set(out)];
}

export function decidedWaitOf(inst: Instance): WaitState | null {
  if (inst.wait?.decided) return inst.wait;
  const h = inst.waitHistory;
  if (h && h.length && h[h.length - 1].decided) return h[h.length - 1];
  return null;
}

export function ReviewSection({
  detail,
  busy,
  setBusy,
  onChanged,
  onNotice,
  onError
}: {
  detail: { instance: Instance; definition: Definition };
  busy: boolean;
  setBusy: (b: boolean) => void;
  onChanged: () => Promise<void>;
  onNotice: (s: string) => void;
  onError: (s: string) => void;
}) {
  const inst = detail.instance;
  const decidedWait = decidedWaitOf(inst);
  const reviews = inst.reviews || [];
  const active = reviews.find((r) => !r.decided) || null;
  const [progress, setProgress] = useState<ReviewProgress | null>(null);
  const [participants, setParticipants] = useState("carol,dave");
  const [reason, setReason] = useState("发现原依据资料有误");
  const [materials, setMaterials] = useState('{\n  "doc": "v2"\n}');
  const [timeoutSec, setTimeoutSec] = useState("0");
  const [opFrom, setOpFrom] = useState("");
  const [opKey, setOpKey] = useState("r-1");
  const [opVote, setOpVote] = useState<"approve" | "reject">("approve");
  const [grantFrom, setGrantFrom] = useState("");
  const [grantTo, setGrantTo] = useState("");
  const [revoke, setRevoke] = useState("");

  const nodeName = useCallback(
    (id: string) => detail.definition.nodes.find((n) => n.id === id)?.name || id,
    [detail]
  );

  const loadProgress = useCallback(async () => {
    if (!reviews.length) return;
    try {
      const res = await api.getReview(inst.id);
      setProgress(res.progress);
    } catch {
      setProgress(null);
    }
  }, [inst.id, reviews.length]);

  useEffect(() => {
    loadProgress();
  }, [loadProgress]);

  async function run(fn: () => Promise<unknown>, ok: string) {
    onError("");
    onNotice("");
    setBusy(true);
    try {
      await fn();
      onNotice(ok);
      await onChanged();
      await loadProgress();
    } catch (e) {
      onError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }

  if (!decidedWait) return null;
  const auth = active ? active.authorizations[active.authorizations.length - 1] : null;

  return (
    <div className="collab-section review-section">
      <h3>复议（针对原决定）</h3>
      <p className="hint">
        原决定：<b>{ORIG_DECISION_LABEL[decidedWait.decision || ""] || decidedWait.decision}</b>
        （等待节点「{nodeName(decidedWait.nodeId || "")}」，依据授权范围 {decidedWait.decisionAuthId}）。
        原赞成、委托与生效范围保持不变；复议的新参与范围与资料只影响本轮，不改写历史。
      </p>

      {active && progress && (
        <div className="review-progress">
          <h4>原决定推进情况（复议 {active.id} 等待期间）</h4>
          <ul className="party-list">
            <li>已执行、只能按流程补偿：
              {progress.executedAfter.length
                ? progress.executedAfter.map((n) => (
                    <span key={n} className="key-chip">{nodeName(n)}{progress.pendingCompensation.includes(n) ? "（待补偿）" : "（已补偿）"}</span>
                  ))
                : <span className="hint">无</span>}
            </li>
            <li>尚未执行、仍可暂停：
              {progress.pausableAhead.length
                ? progress.pausableAhead.map((n) => <span key={n} className="key-chip">{nodeName(n)}</span>)
                : <span className="hint">无</span>}
            </li>
          </ul>
        </div>
      )}

      {!active && (
        <>
          <h4>发起复议</h4>
          <label>复议参与方（逗号分隔，只影响本轮）
            <input value={participants} onChange={(e) => setParticipants(e.target.value)} />
          </label>
          <label>复议依据资料 JSON（只影响本轮）
            <textarea rows={3} value={materials} onChange={(e) => setMaterials(e.target.value)} />
          </label>
          <label>发起理由<input value={reason} onChange={(e) => setReason(e.target.value)} /></label>
          <label>复议时限（秒，0 为不限时）<input value={timeoutSec} onChange={(e) => setTimeoutSec(e.target.value)} /></label>
          <button
            className="primary"
            disabled={busy}
            onClick={() =>
              run(async () => {
                let mats: Record<string, unknown> = {};
                try {
                  mats = JSON.parse(materials || "{}");
                } catch {
                  throw new Error("复议依据资料不是合法 JSON");
                }
                await api.openReview(inst.id, {
                  participants: participants.split(/[,，]/).map((s) => s.trim()).filter(Boolean),
                  materials: mats,
                  reason: reason || undefined,
                  timeoutSeconds: Number(timeoutSec) || 0
                });
              }, "复议已发起；原决定历史保持不变，后续动作已暂停推进")
            }
          >发起复议</button>
        </>
      )}

      {active && auth && (
        <>
          <h4>复议 {active.id} 进行中（{Object.keys(active.received).length}/{auth.participants.length}）</h4>
          {active.deadline && <p className="hint">复议到期：{new Date(active.deadline).toLocaleString()}</p>}
          <ul className="party-list">
            {auth.participants.map((p) => {
              const delegate = auth.delegations?.[p];
              const effective = delegate || p;
              const got = active.received[effective];
              return (
                <li key={p} className={got ? "party-done" : ""}>
                  {got ? "✅" : "⏳"} {p}
                  {delegate ? <span className="hint">（已委托 {delegate}）</span> : null}
                  {got ? <span className="hint">　已于 {new Date(got.at).toLocaleTimeString()} 表态（范围 {got.authId}）</span> : null}
                </li>
              );
            })}
          </ul>
          <label>表态参与方
            <select value={opFrom} onChange={(e) => setOpFrom(e.target.value)}>
              <option value="">请选择</option>
              {effectiveReviewParties(active).map((p) => (
                <option key={p} value={p}>{p}{active.received[p] ? "（已表态）" : ""}</option>
              ))}
            </select>
          </label>
          <label>意见幂等键 key（原业务等待已生效的旧 key 不能凑数）
            <input value={opKey} onChange={(e) => setOpKey(e.target.value)} />
          </label>
          <label>意见
            <select value={opVote} onChange={(e) => setOpVote(e.target.value as "approve" | "reject")}>
              <option value="approve">支持复议（推翻原决定后续效果）</option>
              <option value="reject">反对复议（维持原决定）</option>
            </select>
          </label>
          <button
            className="primary"
            disabled={busy || !opFrom || !opKey.trim()}
            onClick={() =>
              run(
                () => api.deliverReviewInput(inst.id, opKey.trim(), opFrom, { vote: opVote }),
                "复议意见已提交；未集齐前不形成决定"
              )
            }
          >提交复议意见</button>

          <h4>修改复议参与范围 / 委托</h4>
          <label>委托：参与方
            <select value={grantFrom} onChange={(e) => setGrantFrom(e.target.value)}>
              <option value="">（不新增委托）</option>
              {auth.participants.map((p) => <option key={p} value={p}>{p}</option>)}
            </select>
          </label>
          {grantFrom && (
            <label>代为表态的受托方<input value={grantTo} onChange={(e) => setGrantTo(e.target.value)} /></label>
          )}
          <label>撤回委托：参与方
            <select value={revoke} onChange={(e) => setRevoke(e.target.value)}>
              <option value="">（不撤回）</option>
              {Object.keys(auth.delegations || {}).map((p) => <option key={p} value={p}>{p}（受托 {auth.delegations![p]}）</option>)}
            </select>
          </label>
          <button
            disabled={busy || (!grantFrom && !revoke)}
            onClick={() =>
              run(async () => {
                const grants: Record<string, string> = {};
                if (grantFrom && grantTo) grants[grantFrom] = grantTo;
                await api.updateReviewScope(inst.id, {
                  grantDelegations: Object.keys(grants).length ? grants : undefined,
                  revokeDelegations: revoke ? [revoke] : undefined,
                  keepPriorInputs: true,
                  reason: "复议期间调整参与范围"
                });
                setGrantFrom("");
                setGrantTo("");
                setRevoke("");
              }, "复议范围已更新；新旧复议授权记录都保留，原决定授权历史不受影响")
            }
          >更新复议范围</button>
          <button
            disabled={busy}
            onClick={() => run(() => api.reviewTimeout(inst.id), "已做复议到期判定（决定最多形成一次）")}
          >立即做复议到期判定</button>
        </>
      )}

      {reviews.length > 0 && (
        <>
          <h4>复议历史</h4>
          <ul className="party-list">
            {reviews.map((rv) => (
              <li key={rv.id}>
                <b>{rv.id}</b>：{rv.decided ? REVIEW_DECISION_LABEL[rv.decision || ""] || rv.decision : "进行中"}
                {rv.decisionAuthId ? <span className="hint">（依据复议范围 {rv.decisionAuthId}）</span> : null}
                {rv.reason ? <div className="hint">理由：{rv.reason}</div> : null}
              </li>
            ))}
          </ul>
        </>
      )}

      {progress && progress.pendingCompensation.length > 0 && !active && (
        <p className="hint review-unfinished">
          补偿未完成范围仍在：{progress.pendingCompensation.map(nodeName).join("、")}。
          复议已结束不等于全部恢复，未完成部分按真实停点保留。
        </p>
      )}
    </div>
  );
}

