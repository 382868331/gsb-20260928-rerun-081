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

const TAB_KEY = "flow.ui.tab";

export default function App() {
  // Refresh must not lose the user's place: restore the last active tab.
  const [tab, setTabState] = useState<Tab>(() => {
    const saved = localStorage.getItem(TAB_KEY);
    return saved === "instances" || saved === "history" ? saved : "definitions";
  });
  const setTab = useCallback((t: Tab) => {
    localStorage.setItem(TAB_KEY, t);
    setTabState(t);
  }, []);
  const [definitions, setDefinitions] = useState<Definition[]>([]);
  const [error, setError] = useState("");

  const loadDefs = useCallback(async () => {
    try {
      setError("");
      // Empty first-run collection must stay an array, never crash the page.
      setDefinitions((await api.listDefinitions()) ?? []);
    } catch (e) {
      setError((e as Error).message);
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
    for (const d of definitions) {
      const cur = m.get(d.name);
      if (!cur || d.version > cur.version) m.set(d.name, d);
    }
    return m;
  }, [definitions]);

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
        {error && (
          <span className="error header-error">
            加载失败：{error}（已有操作已保留，可）
            <button className="link" onClick={loadDefs}>重试</button>
          </span>
        )}
      </header>
      <ErrorBoundary>
        {tab === "definitions" && <Editor definitions={definitions} onPublish={publish} />}
        {tab === "instances" && (
          <Instances
            definitions={definitions}
            latestByName={latestByName}
            refreshKey={definitions.length}
            onEditedPublished={() => loadDefs()}
          />
        )}
        {tab === "history" && <History definitions={definitions} />}
      </ErrorBoundary>
    </div>
  );
}
