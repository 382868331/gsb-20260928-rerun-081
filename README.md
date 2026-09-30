# 业务流程运行平台（第二轮）

Go 流程引擎 + React 流程编辑器。

- 可视化流程编辑与**版本发布**：每次发布生成不可变的新版本定义
- 实例执行：实例启动时固定当时的定义 id，之后发布新版本**不影响运行中实例**
- 条件分支：边上的条件支持 `key == "x"`、`key != ...`、`key > >= < <= 数字`、布尔键与 `default`
- 等待外部输入：执行到 wait 节点挂起；页面人工提交，带**幂等键 key**，同一 key 重复提交只生效一次
- 失败补偿：任务失败后按执行路径逆序调用补偿；补偿本身也可能失败，实例记录**真实停在哪一步**（`stoppedAt`）
- 持久化与重启续跑：定义和实例均以 JSON 原子写盘（临时文件 + rename），重启进程后同一实例继续推进
- 页面中的绿色箭头只来自执行记录 `records` 里真实经过的 `edgeId`（branch/traverse），不是动画推断

## 第二轮修复与补强

- **修复白屏**：根因是「实例执行」页异步加载详情时存在竞态——快速切页后旧请求返回并把 `undefined` 详情写回新挂载的页面，`definitionToFlow(undefined)` 抛错导致整个 React 根卸载。现用 `mounted`/请求序号丢弃过期响应，并新增全局 `ErrorBoundary`（单页报错只显示可重试的错误面板，不再整站白屏）；编辑器同步 React Flow 状态改为走正确的 setter。
- 顶部三个可用页签：**流程定义**（编辑/发布/版本列表）、**实例列表**（启动、状态、真实停点、人工输入）、**执行历史**（按状态过滤、完整事件流、失败与补偿说明，自动刷新）。
- 等待/失败时清楚展示**停在哪一步**以及**哪些操作已生效**：「已生效操作」按真实 `succeed/compensated/compensation_fail` 事件汇总为 已生效 / 已补偿撤销 / 补偿失败仍生效；失败实例展示失败节点、补偿再次失败节点与最终 `stoppedAt`。
- 迟到/重复输入只追加 `duplicate_input` 记录并在页面提示，绝不新增 `branch/traverse/input_received`，因此不会重做已完成的业务动作。
- 新增 `POST /api/instances/{id}/resume`：进程崩溃后磁盘上停在 `running` 的实例可一键继续推进；等待/完成/失败实例调用为安全 no-op。
- 前端回归测试：`frontend/src/ui.test.tsx`（vitest + jsdom）覆盖白屏竞态与重复输入；`tsconfig` 已将测试文件排除出生产构建。

## 目录

- `backend/` Go 服务：`model`（定义/实例/执行记录）、`engine`（执行/条件/补偿）、`store`（JSON 持久化）、`api`（HTTP）
- `frontend/` React 19 + @xyflow/react：流程定义页、实例列表页、执行历史页

## 运行

后端（默认 :8080，数据在 `backend/data`）：

```bash
cd backend
go test ./...
go run ./cmd/server -addr :8080 -data ./data
```

前端（开发模式，已配置 /api 代理到 :8080）：

```bash
cd frontend
npm install
npm run dev
```

生产构建后可由 Go 直接托管：

```bash
cd frontend && npm run build
cd ../backend && go run ./cmd/server -static ../frontend/dist
```

## HTTP 接口

- `POST /api/definitions` 发布新版本 `{name, nodes, edges}`
- `GET  /api/definitions` / `GET /api/definitions/{id}`
- `POST /api/instances` 启动 `{definitionId, data}`（运行到等待/结束/失败后落盘）
- `GET  /api/instances` / `GET /api/instances/{id}`（返回实例 + 其固定版本定义）
- `POST /api/instances/{id}/inputs` 人工输入 `{waitNode, key, payload}`
- `POST /api/instances/{id}/resume` 重启恢复（仅推进 `running` 实例）

## 失败与补偿的页面模拟

启动实例时在初始数据中设置：

- `failAt`: 要模拟失败的任务节点 id（如 `ship`）
- `compFailAt`: 要模拟“补偿也失败”的任务节点 id（如 `reserve`）

实例状态变为 `failed`，执行记录逐条展示 compensate/compensated/compensation_fail，
`stoppedAt` 即真实停点，图上对应节点红框并标注“当前停点”。

## 关键语义测试

`backend/internal/engine/engine_test.go` 与 `backend/internal/api/api_test.go` 覆盖：
版本隔离、重复输入只推进一次、补偿失败停点、条件默认分支、重启后从磁盘继续。
