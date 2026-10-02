import type { FlowEdge, FlowNode } from "./types";

export const templateNodes: FlowNode[] = [
  { id: "start", type: "start", name: "开始下单", x: 40, y: 180 },
  { id: "check", type: "decision", name: "金额判断", x: 260, y: 180 },
  { id: "reserve", type: "task", name: "预占库存", compensateTo: "reserve", x: 500, y: 80 },
  {
    id: "manual",
    type: "wait",
    name: "等待多方会签",
    x: 740,
    y: 80,
    collab: {
      participants: ["reviewer", "manager"],
      delegatable: true,
      deadlineSeconds: 0,
      timeoutPolicy: "reject",
      approveThreshold: 0
    }
  },
  { id: "ship", type: "task", name: "发货", compensateTo: "ship", x: 980, y: 80 },
  { id: "directEnd", type: "end", name: "小额直接结束", x: 520, y: 300 },
  { id: "end", type: "end", name: "订单完成", x: 1220, y: 80 }
];

export const templateEdges: FlowEdge[] = [
  { id: "e_start_check", from: "start", to: "check" },
  { id: "e_check_reserve", from: "check", to: "reserve", condition: "amount >= 100", label: "大额 amount>=100" },
  { id: "e_reserve_manual", from: "reserve", to: "manual" },
  { id: "e_manual_ship", from: "manual", to: "ship" },
  { id: "e_ship_end", from: "ship", to: "end" },
  { id: "e_check_direct", from: "check", to: "directEnd", condition: "default", label: "默认(小额)" }
];
