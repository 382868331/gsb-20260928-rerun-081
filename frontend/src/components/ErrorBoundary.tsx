import { Component, type ErrorInfo, type ReactNode } from "react";

interface Props {
  children: ReactNode;
  label?: string;
}

interface State {
  error: Error | null;
}

// ErrorBoundary keeps an inner render error from unmounting the whole app
// (which previously showed a permanent white screen).
export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    // Surface in console for diagnostics without crashing the shell.
    console.error("[流程平台] 页面渲染失败:", error, info.componentStack);
  }

  reset = () => this.setState({ error: null });

  render() {
    if (this.state.error) {
      return (
        <div className="fatal">
          <h2>页面渲染失败</h2>
          <pre>{this.state.error.stack || String(this.state.error)}</pre>
          <button className="primary" onClick={this.reset}>重试</button>
        </div>
      );
    }
    return this.props.children;
  }
}
