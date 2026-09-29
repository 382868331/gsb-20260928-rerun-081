# 业务流程运行平台（第一轮）

Go 流程引擎 + React 流程编辑器。第一轮交付：

- 可视化流程编辑与**版本发布**：每次发布生成不可变的新版本定义
- 实例执行：实例启动时固定当时的定义 id，之后发布新版本**不影响运行中实例**
- 条件分支：边上的条件支持 `key == "x"`、`key != ...`、`key > >= < <= 数字`、布尔键与 `default`
- 等待外部输入：执行到 wait 节点挂起；页面人工提交，带**幂等键 key**，同一 key 重复提交只生效一次
- 失败补偿：任务失败后按执行路径逆序调用补偿；补偿本身也可能失败，实例记录**真实停在哪一步**（`stoppedAt`）
- 持久化与重启续跑：定义和实例均以 JSON 原子写盘（临时文件 + rename），重启进程后同一实例继续推进
- 页面中的绿色箭头只来自执行记录 `records` 里真实经过的 `edgeId`（branch/traverse），不是动画推断

## 目录

- `backend/` Go 服务：`model`（定义/实例/执行记录）、`engine`（执行/条件/补偿）、`store`（JSON 持久化）、`api`（HTTP）
- `frontend/` React 19 + @xyflow/react：编辑发布页、实例执行页

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

## 失败与补偿的页面模拟

启动实例时在初始数据中设置：

- `failAt`: 要模拟失败的任务节点 id（如 `ship`）
- `compFailAt`: 要模拟“补偿也失败”的任务节点 id（如 `reserve`）

实例状态变为 `failed`，执行记录逐条展示 compensate/compensated/compensation_fail，
`stoppedAt` 即真实停点，图上对应节点红框并标注“当前停点”。

## 关键语义测试

`backend/internal/engine/engine_test.go` 与 `backend/internal/api/api_test.go` 覆盖：
版本隔离、重复输入只推进一次、补偿失败停点、条件默认分支、重启后从磁盘继续。
