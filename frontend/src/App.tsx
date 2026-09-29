import { useCallback, useEffect, useState } from "react";
import type { Definition, FlowEdge, FlowNode } from "./types";
import { api } from "./api";
import { Editor } from "./components/Editor";
import { Instances } from "./components/Instances";

export default function App() {
  const [tab, setTab] = useState<"edit" | "run">("edit");
  const [definitions, setDefinitions] = useState<Definition[]>([]);
  const [error, setError] = useState("");

  const loadDefs = useCallback(async () => {
    try {
      setDefinitions(await api.listDefinitions());
    } catch (e) {
      setError((e as Error).message);
    }
  }, []);

  useEffect(() => {
    loadDefs();
  }, [loadDefs]);

  const publish = useCallback(
    async (name: string, nodes: FlowNode[], edges: FlowEdge[]): Promise<void> => {
      const def = await api.publish(name, nodes, edges);
      await loadDefs();
      setTab("run");
    },
    [loadDefs]
  );

  return (
    <div className="app">
      <header>
        <h1>业务流程运行平台 · 第一轮</h1>
        <nav>
          <button className={tab === "edit" ? "active" : ""} onClick={() => setTab("edit")}>流程编辑与发布</button>
          <button className={tab === "run" ? "active" : ""} onClick={() => setTab("run")}>实例执行</button>
        </nav>
        {error && <span className="error">{error}（请确认后端已在 :8080 启动）</span>}
      </header>
      {tab === "edit" ? (
        <Editor definitions={definitions} onPublish={publish} />
      ) : (
        <Instances definitions={definitions} refreshKey={definitions.length} />
      )}
    </div>
  );
}
