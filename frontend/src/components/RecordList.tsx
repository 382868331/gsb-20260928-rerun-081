import type { Definition, Instance, StepRecord } from "../types";

export const STATUS_LABEL: Record<string, string> = {
  running: "运行中",
  waiting: "等待人工输入",
  completed: "已完成",
  failed: "失败"
};

const ACTION_LABEL: Record<string, string> = {
  start: "开始",
  enter: "进入节点",
  traverse: "沿边前进",
  branch: "选择分支",
  succeed: "任务生效",
  wait: "挂起等待",
  input_received: "输入生效",
  duplicate_input: "重复输入忽略",
  late_input: "迟到输入忽略",
  fail: "任务失败",
  compensate: "开始补偿",
  compensated: "补偿生效",
  compensation_fail: "补偿失败",
  compensate_done: "补偿全部完成",
  complete: "流程完成"
};

export function stopLabel(inst: Instance, def?: Definition): string {
  const sid = inst.stoppedAt || inst.currentNode;
  if (!sid) return "-";
  const found = def?.nodes.find((x) => x.id === sid);
  return found ? found.name + "（节点 " + sid + "）" : sid;
}

export function consumedKeys(inst?: Instance): string[] {
  if (!inst) return [];
  return Object.keys(inst.consumedInputs).filter((k) => inst.consumedInputs[k]);
}

export function RecordList({ records, nodeName }: { records: StepRecord[]; nodeName: Map<string, string> }) {
  return (
    <div className="records">
      {records.map((r) => (
        <div key={r.seq} className={"record record-" + r.action}>
          <span className="seq">#{r.seq}</span>
          <span className="rec-main">
            <b>{ACTION_LABEL[r.action] || r.action}</b>
            <span className="rec-node"> @ {nodeName.get(r.nodeId) || r.nodeId || "（全局）"}</span>
            {r.edgeId ? <em> 沿 {r.edgeId}</em> : null}
            {r.detail ? <div className="rec-detail">{r.detail}</div> : null}
          </span>
        </div>
      ))}
    </div>
  );
}
