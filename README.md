# 业务流程运行平台（第一轮）

Go + React 实现的轻量业务流程运行平台。第一轮提供：

- **流程编辑**：浏览器内拖拽编辑节点与连线（任务 / 条件分支 / 等待外部输入 / 开始结束），任务可配置补偿节点。
- **版本发布**：发布只追加新版本号，定义不可变；每个实例固化创建时的**定义版本快照**，新发布不影响在跑的旧实例。
- **实例执行**：条件表达式分支、等待外部输入挂起、任务失败自动走补偿；补偿本身也可能失败，实例真实停在失败的补偿节点。
- **持久化与重启**：定义与实例均落盘为 JSON（原子写），服务重启后 `running/compensating` 实例自动继续，`waiting` 实例保持挂起，等待同一实例的人工输入。
- **页面箭头 = 执行记录**：图上只有出现在 `transitions` 流转记录中的边才高亮，补偿箭头来自 `comp=true` 的流转记录，不使用动画冒充执行。

## 目录结构

```
cmd/server/        Go 入口（embed 前端产物，SPA 回退）
internal/model/    定义 / 实例 / 执行记录模型
internal/store/    JSON 文件存储（原子 rename 写入）
internal/engine/   执行引擎、表达式求值、定义校验
internal/api/      HTTP API
frontend/          React + Vite + @xyflow/react 前端
data/              运行后生成的 JSON 数据目录
```

## 快速开始

需要 Go 1.27+、Node 20+（依赖已预装时可跳过 install）。

```bash
make all          # 构建前端到 cmd/server/webdist 并编译 Go
make run          # 监听 :8080，数据目录 ./data
```

开发模式（前端热更新，代理 /api 到 8080）：

```bash
make dev-backend   # 终端 1
make dev-frontend  # 终端 2，打开 http://localhost:5173
```

服务首次启动会内置示例流程 `order-demo`（条件分支 + 经理审批等待 + 扣款失败退款补偿）。

## HTTP API

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/api/definitions` | 发布新版本（版本号自动递增，不覆盖旧版） |
| GET  | `/api/definitions` | 流程列表（每项为最新版本） |
| GET  | `/api/definitions/{key}/{version}` | 获取指定版本定义 |
| POST | `/api/instances` | 启动实例，body: `{"defKey":"...","input":{...}}` |
| GET  | `/api/instances` / `/api/instances/{id}` | 实例列表 / 详情（含全部执行记录） |
| POST | `/api/instances/{id}/inputs` | 人工模拟外部输入，body: `{"nodeId","idempotencyKey","payload"}` |

## 关键语义

- **幂等输入**：相同 `idempotencyKey` 的重复提交只记录为 `duplicate`，不重复推进；同一等待节点在首次有效输入之后再次提交同样拒绝推进。
- **条件表达式**：支持 `== != > >= < <=`、`&& || !`、括号、数字/字符串/布尔、点路径变量（如 `order.amount >= 1000`）。
- **失败模拟**：起始变量或输入 payload 中可带 `failTasks` / `failComp`（节点 ID 的数组或逗号分隔字符串），分别注入任务失败、补偿失败，便于演示与测试。
- **补偿流转**：失败任务 → 补偿节点不需要画在定义图上，引擎会生成 `comp:true` 流转记录，前端据此渲染红色虚线箭头。
- **状态**：`running / waiting / compensating / completed / failed（补偿成功）/ compensation_failed（补偿失败）`。

## 测试

```bash
make test   # go test ./... + 前端 tsc 类型检查
```

引擎测试覆盖：条件分支路径、等待与幂等输入、补偿成功/失败的真实停留点、新版本不污染旧实例、重启恢复；API 测试覆盖完整 HTTP 生命周期。
