import { useCallback, useEffect, useState } from "react";
import type { Definition, FlowEdge, FlowNode } from "./types";
import { api } from "./api";
import { Editor } from "./components/Editor";
import { Instances } from "./components/Instances";
import { History } from "./components/History";
import { ErrorBoundary } from "./components/ErrorBoundary";

type Tab = "definitions" | "instances" | "history";

const TABS: { id: Tab; label: string }[] = [
  { id: "definitions", label: "流程定义" },
  { id: "instances", label: "实例列表" },
  { id: "history", label: "执行历史" }
];

export default function App() {
  const [tab, setTab] = useState<Tab>("definitions");
  const [definitions, setDefinitions] = useState<Definition[]>([]);
  const [error, setError] = useState("");

  const loadDefs = useCallback(async () => {
    try {
      setDefinitions(await api.listDefinitions());
      setError("");
    } catch (e) {
      setError((e as Error).message);
    }
  }, []);

  useEffect(() => {
    loadDefs();
  }, [loadDefs]);

  const publish = useCallback(
    async (name: string, nodes: FlowNode[], edges: FlowEdge[]): Promise<Definition> => {
      const def = await api.publish(name, nodes, edges);
      await loadDefs();
      setTab("instances");
      return def;
    },
    [loadDefs]
  );

  return (
    <div className="app">
      <header>
        <h1>业务流程运行平台</h1>
        <nav>
          {TABS.map((t) => (
            <button key={t.id} className={tab === t.id ? "active" : ""} onClick={() => setTab(t.id)}>
              {t.label}
            </button>
          ))}
        </nav>
        <button className="retry-link" onClick={loadDefs}>重新连接后端</button>
        {error && <span className="error">{error}（请确认后端已在 :8080 启动）</span>}
      </header>
      <ErrorBoundary>
        {tab === "definitions" && <Editor definitions={definitions} onPublish={publish} />}
        {tab === "instances" && <Instances definitions={definitions} refreshKey={definitions.length} />}
        {tab === "history" && <History />}
      </ErrorBoundary>
    </div>
  );
}
