import { Component, type ReactNode } from "react";

interface State {
  error: Error | null;
}

export class ErrorBoundary extends Component<{ children: ReactNode }, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidCatch(error: Error) {
    // Keep a trace in console; the boundary itself prevents a full white screen.
    console.error("页面渲染失败：", error);
  }

  render() {
    if (this.state.error) {
      return (
        <div style={{ padding: 24, maxWidth: 720 }}>
          <h2 style={{ color: "#b91c1c" }}>页面渲染失败，但其他功能仍可使用</h2>
          <pre style={{ whiteSpace: "pre-wrap", background: "#f8fafc", padding: 12, borderRadius: 8 }}>
            {this.state.error.message}
          </pre>
          <button
            className="primary"
            style={{ width: "auto" }}
            onClick={() => this.setState({ error: null })}
          >
            重试渲染
          </button>
        </div>
      );
    }
    return this.props.children;
  }
}
