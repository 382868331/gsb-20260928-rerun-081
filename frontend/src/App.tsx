import { useCallback, useEffect, useMemo, useState } from "react";
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
  const [defsStatus, setDefsStatus] = useState<"loading" | "ready" | "error">("loading");

  const loadDefs = useCallback(async () => {
    try {
      setError("");
      setDefsStatus("loading");
      const list = await api.listDefinitions();
      setDefinitions(list);
      setDefsStatus("ready");
    } catch (e) {
      setError((e as Error).message);
      setDefsStatus("error");
    }
  }, []);

  useEffect(() => {
    loadDefs();
  }, [loadDefs]);

  const publish = useCallback(
    async (name: string, nodes: FlowNode[], edges: FlowEdge[]): Promise<void> => {
      await api.publish(name, nodes, edges);
      await loadDefs();
      setTab("instances");
    },
    [loadDefs]
  );

  const latestByName = useMemo(() => {
    const m = new Map<string, Definition>();
    for (const d of definitions ?? []) {
      if (!d || !Array.isArray(d.nodes) || !Array.isArray(d.edges)) continue;
      const cur = m.get(d.name);
      if (!cur || d.version > cur.version) m.set(d.name, d);
    }
    return m;
  }, [definitions]);

  return (
    <ErrorBoundary resetKey={tab}>
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
        {error && <span className="error header-error">{error}（请确认后端已在 :8080 启动）</span>}
      </header>
      <ErrorBoundary resetKey={tab}>
        {tab === "definitions" && (
          <Editor
            definitions={definitions}
            onPublish={publish}
            status={defsStatus}
            loadError={error}
            onRetryLoad={loadDefs}
          />
        )}
        {tab === "instances" && (
          <Instances
            definitions={definitions}
            latestByName={latestByName}
            refreshKey={definitions.length}
            onEditedPublished={() => loadDefs()}
            onGoEditor={() => setTab("definitions")}
          />
        )}
        {tab === "history" && <History definitions={definitions} />}
      </ErrorBoundary>
    </div>
    </ErrorBoundary>
  );
}
