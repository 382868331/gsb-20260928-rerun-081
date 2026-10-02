import { useEffect, useState } from "react";
import { api } from "../api";
import type { CollabSpec, CollabState, Instance } from "../types";

const POLICY_LABEL: Record<string, string> = {
  reject: "到期未达通过票数则拒绝",
  approve: "到期自动通过",
  majority: "到期按多数票（平票拒绝）"
};

const ACTION_LABEL: Record<string, string> = {
  delegate: "委托",
  revoke: "撤回委托",
  scope_add: "接收范围新增",
  scope_remove: "接收范围移除"
};

export function CollabPanel({
  instance,
  spec,
  collab,
  busy,
  setBusy,
  onNotice,
  onError,
  onChanged
}: {
  instance: Instance;
  spec: CollabSpec;
  collab: CollabState;
  busy: boolean;
  setBusy: (b: boolean) => void;
  onNotice: (s: string) => void;
  onError: (s: string) => void;
  onChanged: (inst: Instance) => Promise<void> | void;
}) {
  const threshold = spec.approveThreshold && spec.approveThreshold > 0 ? spec.approveThreshold : collab.scope.length;
  const approvals = collab.votes.filter((v) => v.valid && v.kind === "approve").length;
  const rejections = collab.votes.filter((v) => v.valid && v.kind === "reject").length;
  const [voter, setVoter] = useState(collab.scope[0] || "");
  const [key, setKey] = useState("vote-" + Math.random().toString(36).slice(2, 8));
  const [actor, setActor] = useState("");
  const [party, setParty] = useState("");
  const [keep, setKeep] = useState(true);

  useEffect(() => {
    if (!collab.scope.includes(voter)) setVoter(collab.scope[0] || "");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [collab.scope.join(",")]);

  async function run(fn: () => Promise<Instance>, notice: string) {
    setBusy(true);
    onError("");
    onNotice("");
    try {
      const inst = await fn();
      await onChanged(inst);
      onNotice(notice);
    } catch (e) {
      onError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }

  const remaining = collab.scope.length - approvals - rejections;
  const deadlineText = spec.deadlineSeconds
    ? spec.deadlineSeconds + " 秒，" + (POLICY_LABEL[spec.timeoutPolicy || "reject"] || spec.timeoutPolicy)
    : "不设期限";

  return (
    <div className="collab-panel">
      <h2>多方协作等待</h2>
      <p className="hint">
        需 {threshold} 票赞成（参与方 {collab.scope.length} 人）；委托：{spec.delegatable ? "允许" : "不允许"}；到期：{deadlineText}。
      </p>
      {collab.deadline && (
        <p className="hint">到期时间：{new Date(collab.deadline).toLocaleString()}（重开后自动按到期处理，已确认不重复执行）</p>
      )}

      <div className="collab-tally">
        <span className="tag approve">赞成 {approvals}</span>
        <span className="tag reject">反对 {rejections}</span>
        <span className="tag">尚未到达 {remaining}</span>
        {!collab.decided && remaining > 0 && <span className="hint">部分输入已到达，步骤仍在等待，未记为完成。</span>}
      </div>

      {!collab.decided && instance.status === "waiting" && (
        <>
          <div className="collab-vote">
            <label>我是
              <input list="collab-voters" value={voter} onChange={(e) => setVoter(e.target.value)} placeholder="参与方或受托人" />
              <datalist id="collab-voters">
                {collab.scope.map((p) => <option key={p} value={p} />)}
              </datalist>
            </label>
            <label>幂等键 key
              <input value={key} onChange={(e) => setKey(e.target.value)} />
            </label>
            <div className="btn-row">
              <button className="primary" disabled={busy} onClick={() =>
                run(() => api.deliverCollabVote(instance.id, collab.waitNode, key.trim(), voter.trim(), "approve"), "赞成已提交")
              }>提交赞成</button>
              <button disabled={busy} onClick={() =>
                run(() => api.deliverCollabVote(instance.id, collab.waitNode, key.trim(), voter.trim(), "reject"), "反对已提交")
              }>提交反对</button>
            </div>
            <p className="hint">同一人重复意见或相同 key 只记录一次；决定后到达的输入仅作为历史保留。</p>
          </div>

          {spec.delegatable && (
            <div className="collab-grant">
              <h3>委托 / 撤回（新旧授权记录均保留）</h3>
              <label>委托参与方<input placeholder="如 alice" value={actor} onChange={(e) => setActor(e.target.value)} /></label>
              <label>受托人<input placeholder="如 dave" value={party} onChange={(e) => setParty(e.target.value)} /></label>
              <div className="btn-row">
                <button disabled={busy} onClick={() =>
                  run(() => api.collabDelegate(instance.id, { waitNode: collab.waitNode, actor: actor.trim(), party: party.trim() }), "委托已记录")
                }>新增委托</button>
                <button disabled={busy} onClick={() =>
                  run(() => api.collabRevoke(instance.id, { waitNode: collab.waitNode, actor: actor.trim(), party: party.trim(), keepPriorInputs: keep }), "撤回已记录")
                }>撤回委托</button>
              </div>
              <label className="check-line">
                <input type="checkbox" checked={keep} onChange={(e) => setKeep(e.target.checked)} />
                撤回时：受托人此前输入仍有效（取消勾选则此前输入作历史保留、不再计数）
              </label>
            </div>
          )}

          <div className="collab-scope">
            <h3>修改接收范围</h3>
            <label>操作人 / 参与方<input placeholder="如 admin 或 bob" value={actor} onChange={(e) => setActor(e.target.value)} /></label>
            <label>被调整参与方<input placeholder="如 zoe" value={party} onChange={(e) => setParty(e.target.value)} /></label>
            <label className="check-line">
              <input type="checkbox" checked={keep} onChange={(e) => setKeep(e.target.checked)} />
              移除时：其此前输入仍有效（取消勾选则仅作历史保留）
            </label>
            <div className="btn-row">
              <button disabled={busy} onClick={() =>
                run(() => api.collabScope(instance.id, true, { waitNode: collab.waitNode, actor: actor.trim(), party: party.trim() }), "接收范围已扩大")
              }>加入接收范围</button>
              <button disabled={busy} onClick={() =>
                run(() => api.collabScope(instance.id, false, { waitNode: collab.waitNode, actor: actor.trim(), party: party.trim(), keepPriorInputs: keep }), "接收范围已缩小")
              }>移出接收范围</button>
            </div>
            {spec.deadlineSeconds ? (
              <button disabled={busy} onClick={() =>
                run(() => api.collabTimeout(instance.id, collab.waitNode), "已按到期策略形成决定")
              }>模拟到期（立即按到期策略决定）</button>
            ) : null}
          </div>
        </>
      )}

      {collab.decision && (
        <div className={"collab-decision " + (collab.decision.outcome.includes("approved") ? "ok" : "no")}>
          <h3>唯一决定：{collab.decision.outcome}</h3>
          <p className="hint">{collab.decision.reason}</p>
          <p className="hint">赞成 {collab.decision.approvals} / 反对 {collab.decision.rejections}（需 {collab.decision.threshold}）</p>
          <p className="hint">决定时有效的参与范围：{collab.decision.scopeSnapshot.join("、")}</p>
          <p className="hint">形成时间：{new Date(collab.decision.at).toLocaleString()}</p>
        </div>
      )}

      <h3>输入记录（含重复 / 失效 / 迟到）</h3>
      <ul className="collab-votes">
        {collab.votes.map((v) => (
          <li key={v.key + v.at} className={v.valid ? "valid" : "invalid"}>
            <b>{v.voter}</b>{v.onBehalfOf && v.onBehalfOf !== v.voter ? <em> 代表 {v.onBehalfOf}{v.viaGrantId ? "（" + v.viaGrantId + "）" : ""}</em> : null}
            <span className={v.kind === "approve" ? "ok" : "no"}>{v.kind === "approve" ? "赞成" : "反对"}</span>
            {!v.valid && <span className="hint">（{v.invalidReason || "无效"}）</span>}
            <span className="hint mono">{v.key}</span>
          </li>
        ))}
        {collab.votes.length === 0 && <li className="hint">暂无输入</li>}
      </ul>

      <h3>授权记录（全部保留，可追溯）</h3>
      <ul className="collab-grants">
        {collab.grants.map((g) => (
          <li key={g.seq}>
            <span className="badge">#{g.seq}</span>
            <b>{ACTION_LABEL[g.action] || g.action}</b>
            {g.party ? <span> {g.actor} → {g.party}</span> : <span> {g.actor}</span>}
            <em className="hint"> 此前输入{g.keepPriorInputs ? "有效" : "失效（留档）"}</em>
            <span className="hint"> {new Date(g.at).toLocaleString()}</span>
          </li>
        ))}
        {collab.grants.length === 0 && <li className="hint">暂无授权变更</li>}
      </ul>
    </div>
  );
}
