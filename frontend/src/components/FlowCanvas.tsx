import { Background, ReactFlow, ReactFlowProvider, useNodesState, useEdgesState } from "@xyflow/react";
import { useEffect } from "react";
import "@xyflow/react/dist/style.css";
import FlowNodeView from "./FlowNodeView";
import { toRFEdges, toRFNodes } from "../flowUtils";
import type { Definition, Instance } from "../types";

const nodeTypes = { custom: FlowNodeView };

export default function FlowCanvas({
  def,
  inst,
  editable = false,
  selectedId,
  onSelect,
  onMoveNode
}: {
  def: Definition;
  inst?: Instance;
  editable?: boolean;
  selectedId?: string | null;
  onSelect?: (id: string) => void;
  onMoveNode?: (id: string, x: number, y: number) => void;
}) {
  const rfNodes = toRFNodes(def, inst).map((n) => ({
    ...n,
    type: "custom" as const,
    selected: n.id === selectedId
  }));
  const rfEdges = toRFEdges(def, inst);
  const [nodes, setNodes] = useNodesState(rfNodes);
  const [edges, setEdges] = useEdgesState(rfEdges);

  useEffect(() => {
    setNodes(rfNodes);
  }, [def, inst, selectedId]);
  useEffect(() => {
    setEdges(rfEdges);
  }, [def, inst]);

  return (
    <ReactFlowProvider>
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        nodesDraggable={editable}
        nodesConnectable={false}
        elementsSelectable
        onNodeClick={(_, n) => onSelect?.(n.id)}
        onNodeDragStop={(_, n) => {
          if (!editable || !onMoveNode) return;
          onMoveNode(n.id, Math.round(n.position.x), Math.round(n.position.y));
        }}
        fitView
        proOptions={{ hideAttribution: true }}
      >
        <Background gap={20} color="#e2e8f0" />
      </ReactFlow>
    </ReactFlowProvider>
  );
}
