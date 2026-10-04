import { useEffect, useState } from "react";
import { api } from "../api";
import type { Instance, ReconsiderProgress } from "../types";

const DECISION_LABEL: Record<string, string> = {
  approved: "通过",
  timeout_approved: "到期通过",
  rejected: "复议不成立（原决定保留）",
  timeout_rejected: "到期拒绝",
  upheld: "复议通过（原决定撤销）",
  expired_upheld: "复议到期通过",
  expired_rejected: "复议到期不成立"
};

// ReconsiderSection shows reconsideration rounds linked to decided
// collaborative waits: how far the original decision advanced, which
// follow-up actions are pausable and which are compensate-only, and lets
// participants open a round, vote, change scope and evaluate expiry.
export function ReconsiderSection({
  instance,
  busy,
  setBusy,
  onChanged,
  onNotice,
  onError
}: {
  instance: Instance;
  busy: boolean;
  setBusy: (b: boolean) => void;
  onChanged: () => Promise<void>;
  onNotice: (s: string) => void;
  onError: (s: string) => void;
}) {
  const decisions = instance.waitDecisions || [];
  const rounds = instance.reconsiderations || [];
  const [participants, setParticipants] = useState("");
  const [reason, setReason] = useState("");
  const [materials, setMaterials] = useState("{}");
  const [targetNode, setTargetNode] = useState("");
  const [voteKey, setVoteKey] = useState("rec-vote-001");
  const [voteFrom, setVoteFrom] = useState("");
  const [voteComment, setVoteComment] = useState("");
  const [progress, setProgress] = useState<ReconsiderProgress | null>(null);

  const open = rounds.find((r) => !r.decided);
  const shown = open || rounds[rounds.length - 1];

  useEffect(() => {
    setProgress(null);
    if (shown) {
      api.reconsiderProgress(instance.id, shown.id)
        .then((r) => setProgress(r.progress))
        .catch(() => setProgress(null));
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [instance.id, shown?.id, shown?.decided, instance.records.length]);

  if (decisions.length === 0 && rounds.length === 0) return null;

  async function run(fn: () => Promise<unknown>, ok: string): Promise<boolean> {
    setBusy(true);
    onError("");
    try {
      await fn();
      await onChanged();
      onNotice(ok);
      return true;
    } catch (e) {
      onError((e as Error).message);
      return false;
    } finally {
      setBusy(false);
    }
  }

  const approvedDecisions = decisions.filter((d) => d.decision === "approved" || d.decision === "timeout_approved");
  const effectiveParties = (r?: typeof open) => {
    if (!r) return [] as string[];
    const set = new Set<string>();
    r.participants.forEach((p) => set.add((r.allowDelegate && r.delegations?.[p]) || p));
    return [...set].sort();
  };
  const choiceLabel = (choice?: string) => choice === "rejected" ? "反对撤销" : choice === "uphold" ? "赞成撤销" : "未明确";

  return (
    <div className="reconsider-section">
      <h3>复议（针对已通过的协作决定）</h3>
      {!open && approvedDecisions.length > 0 && (
        <>
          <p className="hint">原决定的赞成、委托与生效范围保持不变；新参与范围与资料只影响本轮复议。</p>
          <label>复议对象（已通过的等待节点）
            <select value={targetNode || approvedDecisions[0]?.nodeId || ""} onChange={(e) => setTargetNode(e.target.value)}>
              {approvedDecisions.map((d) => (
                <option key={d.nodeId + d.seq} value={d.nodeId}>{d.nodeId}（{DECISION_LABEL[d.decision] || d.decision}，范围 {d.authId}）</option>
              ))}
            </select>
          </label>
          <label>本轮参与方（逗号分隔，可与原范围不同）
            <input value={participants} onChange={(e) => setParticipants(e.target.value)} placeholder="alice,bob" />
          </label>
          <label>复议依据 / 原因<input value={reason} onChange={(e) => setReason(e.target.value)} placeholder="发现采用错误资料" /></label>
          <label>新资料 JSON（仅本轮可见，不改写实例数据）
            <textarea rows={2} value={materials} onChange={(e) => setMaterials(e.target.value)} />
          </label>
          <button
            className="primary"
            disabled={busy}
            onClick={() =>
              run(async () => {
                const list = participants.split(/[,，\s]+/).filter(Boolean);
                if (list.length === 0) throw new Error("请填写本轮参与方");
                await api.openReconsideration(instance.id, {
                  waitNode: targetNode || approvedDecisions[0].nodeId,
                  participants: list,
                  reason,
                  materials: JSON.parse(materials || "{}")
                });
              }, "复议已开启，后续动作已暂停")
            }
          >发起复议</button>
        </>
      )}

      {shown && progress && (
        <div className="reconsider-progress">
          <p>
            <b>{shown.id}</b>（针对 {progress.originWaitNode} 的原决定 {DECISION_LABEL[progress.originDecision] || progress.originDecision} / 范围 {progress.originAuthId}）：
            {shown.decided ? <>已结束，唯一决定：<b>{DECISION_LABEL[shown.decision || ""] || shown.decision}</b></> : <b>进行中，后续动作已暂停</b>}
          </p>
          <p className="hint">本轮统计：赞成撤销 {progress.upheldVotes} 票、反对撤销 {progress.rejectedVotes} 票，已收到 {progress.receivedVotes}/{progress.totalVotes}。</p>
          <p className="hint">{shown.decision === "upheld" || shown.decision === "expired_upheld" ? "原决定已推进到：已执行（只能按流程补偿）：" : "原决定已推进并继续生效的动作："}
            {progress.executedAfterDecision.length ? progress.executedAfterDecision.join("、") : "无"}
            {progress.compensationRemaining?.length ? "；待补偿：" + progress.compensationRemaining.join("、") : ""}
            {progress.compensated?.length ? "；已补偿：" + progress.compensated.join("、") : ""}
          </p>
          <p className="hint">尚可暂停的后续步骤：{progress.pausableAhead.length ? progress.pausableAhead.join("、") : "无"}</p>
          {!shown.decided && (
            <>
              <p className="hint">
                本轮意见：赞成撤销 {progress.upheldVotes} 票、反对撤销 {progress.rejectedVotes} 票，已收到 {progress.receivedVotes}/{progress.totalVotes}。
                集齐全部意见后，全票赞成才撤销并补偿；只要有一票反对即拒绝复议，原决定和已生效动作保留。到期则按本轮到期策略单独决定。
              </p>
              <label>意见来自（本轮有效参与方）
                <select value={voteFrom} onChange={(e) => setVoteFrom(e.target.value)}>
                  <option value="">请选择</option>
                  {effectiveParties(shown).map((p) => (
                    <option key={p} value={p}>{p}{shown.received?.[p] ? "（已表达）" : ""}</option>
                  ))}
                </select>
              </label>
              <label>幂等键 key<input value={voteKey} onChange={(e) => setVoteKey(e.target.value)} /></label>
              <label>反对/赞成理由（可选）<input value={voteComment} onChange={(e) => setVoteComment(e.target.value)} placeholder="说明本轮意见依据" /></label>
              <button className="primary" disabled={busy || !voteFrom || Boolean(shown.received?.[voteFrom])} onClick={() =>
                run(() => api.reconsiderVote(instance.id, shown.id, voteKey.trim() + "-uphold", voteFrom, "uphold", voteComment), "已提交赞成撤销意见").then((ok) => ok && setVoteComment(""))
              }>提交赞成撤销</button>
              <button disabled={busy || !voteFrom || Boolean(shown.received?.[voteFrom])} onClick={() =>
                run(() => api.reconsiderVote(instance.id, shown.id, voteKey.trim() + "-reject", voteFrom, "rejected", voteComment), "已提交反对撤销意见；本轮将保留原决定").then((ok) => ok && setVoteComment(""))
              }>提交反对撤销</button>
              {shown.deadline && (
                <button disabled={busy} onClick={() =>
                  run(() => api.reconsiderTimeout(instance.id, shown.id), "已做到期判定（决定最多形成一次）")
                }>立即到期判定</button>
              )}
            </>
          )}
          {Object.entries(shown.received || {}).length > 0 && (
            <p className="hint">已表态：{Object.entries(shown.received || {}).map(([from, vote]) => from + "（" + choiceLabel(vote.choice) + "）").join("、")}</p>
          )}
        </div>
      )}
      {rounds.length > 1 && <p className="hint">历史复议轮次：{rounds.map((r) => r.id + "(" + (DECISION_LABEL[r.decision || ""] || "进行中") + ")").join("、")}</p>}
    </div>
  );
}
