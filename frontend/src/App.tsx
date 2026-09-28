import { useCallback, useEffect, useState } from "react";
import Editor from "./components/Editor";
import Instances from "./components/Instances";
import { api } from "./api";
import type { Definition, Instance } from "./types";
import "./styles.css";

type Tab = "instances" | "editor";

export default function App() {
  const [tab, setTab] = useState<Tab>("instances");
  const [defs, setDefs] = useState<Definition[]>([]);
  const [instances, setInstances] = useState<Instance[]>([]);
  const [selectedInst, setSelectedInst] = useState<string | null>(null);
  const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);

  const notify = useCallback((msg: string, ok: boolean) => {
    setToast({ msg, ok });
    window.setTimeout(() => setToast(null), 3500);
  }, []);

  const refresh = useCallback(async () => {
    try {
      const [d, i] = await Promise.all([api.listDefinitions(), api.listInstances()]);
      setDefs(d);
      setInstances(i);
    } catch (e) {
      notify((e as Error).message, false);
    }
  }, [notify]);

  useEffect(() => { refresh(); }, [refresh]);

  return (
    <div className="app">
      <header className="app-header">
        <h1>业务流程运行平台</h1>
        <span className="sub">流程编辑 · 版本发布 · 实例执行（条件分支 / 等待输入 / 失败补偿）</span>
        <nav>
          <button className={tab === "instances" ? "active" : ""} onClick={() => setTab("instances")}>
            实例执行
          </button>
          <button className={tab === "editor" ? "active" : ""} onClick={() => setTab("editor")}>
            流程编辑
          </button>
        </nav>
      </header>
      <main>
        {tab === "editor" ? (
          <Editor defs={defs} onPublished={refresh} notify={notify} />
        ) : (
          <Instances
            defs={defs}
            instances={instances}
            selectedId={selectedInst}
            onSelect={setSelectedInst}
            onChanged={refresh}
            notify={notify}
          />
        )}
      </main>
      {toast && <div className={`toast ${toast.ok ? "ok" : "err"}`}>{toast.msg}</div>}
    </div>
  );
}
