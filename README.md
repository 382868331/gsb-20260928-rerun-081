# 业务流程运行平台（第一轮）

Go 流程引擎 + React 流程编辑器。第一轮交付：

- 可视化流程编辑与**版本发布**：每次发布生成不可变的新版本定义
- 实例执行：实例启动时固定当时的定义 id，之后发布新版本**不影响运行中实例**
- 条件分支：边上的条件支持 `key == "x"`、`key != ...`、`key > >= < <= 数字`、布尔键与 `default`
- 等待外部输入：执行到 wait 节点挂起；页面人工提交，带**幂等键 key**，同一 key 重复提交只生效一次
- 迟到输入：实例已离开等待点（失败/完成）后再提交输入不再整体报错；记录 `late_input` 事件并忽略，不覆盖数据、不重做动作
- 失败补偿：任务失败后按执行路径逆序调用补偿；补偿本身也可能失败，实例记录**真实停在哪一步**（`stoppedAt`）
- 持久化与重启续跑：定义和实例均以 JSON 原子写盘（临时文件 + rename）；服务启动时自动恢复 `running` 状态实例，也可 `POST /api/instances/{id}/resume` 手动续跑
- 页面三个页签：**流程定义**（载入旧版本修改后同名发布新版本）、**实例列表**（按流程最新版本启动、显示停点/等待点、已生效 key）、**执行历史**（按状态筛选、逐条事件、已生效动作与补偿统计）
- 首次使用/空数据：空定义、空实例、列表加载失败、实例执行失败在页面上是四种不同反馈；未发布的编辑草稿自动存到浏览器本地，刷新不丢失
- 等待实例的**版本迁移**：新版本默认不套用到任何旧实例；等待人工输入的实例可先预览「等待点之后受影响的步骤」，确认后才切换固定版本，旧版本历史原样保留、不与新版本拼接
- 崩溃安全：任务成功后、补偿进行中被保存/重启，已完成的任务与补偿都不会再执行第二次（前向去重 + 补偿日志即时落盘 + 实例级锁防止重复输入并发提交）
- 画布中的绿色箭头只来自执行记录 `records` 里真实经过的 `edgeId`（branch/traverse），不是动画推断；前端带错误边界，局部渲染异常不再整页白屏

第二轮追加：

- **定义加载三态**：空定义返回 `[]`（后端空切片不再序列化为 `null`），前端对列表/nodes/edges 全部做数组归一化；空数据、加载失败（可重试，本地草稿仍可继续编辑/发布）、已有定义分别呈现，不再出现 “not iterable” 白屏
- **多方协作等待步骤**（wait 节点可开启 collab）：定义参与方、是否允许委托、通过所需赞成票数、到期秒数与到期策略（拒绝/自动通过/多数票平票拒绝）；只有部分输入到达时步骤绝不提前完成
- 等待期间可委托、撤回委托、扩大/缩小接收范围；撤回或移除时显式声明此前输入是否仍有效——声明失效的旧票只作历史保留、不再计数；每一次授权变更都形成 append-only 授权记录，新旧记录都保留
- 重复参与方意见、重复幂等键、撤回与到期并发到达时只形成一个明确决定（实例级锁 + 每次操作前先结算到期）；决定之后（含失败补偿期间）到达的输入仅记录为历史，不覆盖数据、不重做动作
- **保存重开后续等**：协作状态（票数/授权/截止时间/决定）随实例 JSON 落盘；重启后未到期继续等待、已确认不重复执行，已到期自动按策略形成决定；每个决定都带 scopeSnapshot，可追溯当时有效的参与范围与授权链路（受托人投票记录 onBehalfOf 与 viaGrantId）

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
- 协作等待投票同一端点：{waitNode, key, voter, kind: approve|reject}
- `POST /api/instances/{id}/collab/delegate` {waitNode, actor, party}
- `POST /api/instances/{id}/collab/revoke` {waitNode, actor, party, keepPriorInputs}
- `POST /api/instances/{id}/collab/scope/add` 与 /scope/remove {waitNode, actor, party, keepPriorInputs?}
- `POST /api/instances/{id}/collab/timeout` {waitNode} 立即按到期策略结算（也可等待真实到期/重启自动结算）
- `POST /api/instances/{id}/resume` 重启后把 `running` 实例推进到下一个等待/结束/失败
- `GET  /api/processes` 每个流程名的最新版本（空数据时返回 `{}`，前端据此引导发布）
- `GET  /api/instances/{id}/migration?definitionId=...` 预览等待实例迁移到新版本后受影响的步骤
- `POST /api/instances/{id}/migration` `{definitionId, confirm:true}` 用户确认后迁移；未确认或等待节点在新版本中缺失都会被拒绝

迁移只改变实例之后固定的定义 id：旧记录不删除、不改写、不与新版本事件拼接；继续提交人工输入后，等待点之后的步骤按新版本执行，已生效的幂等 key 继续有效。

重复与迟到输入的返回均为 200：是否生效看实例事件流中的 `input_received` / `duplicate_input` / `late_input`，业务动作永远不会因为重试而执行第二次。

## 失败与补偿的页面模拟

启动实例时在初始数据中设置：

- `failAt`: 要模拟失败的任务节点 id（如 `ship`）
- `compFailAt`: 要模拟“补偿也失败”的任务节点 id（如 `reserve`）

实例状态变为 `failed`，执行记录逐条展示 compensate/compensated/compensation_fail，
`stoppedAt` 即真实停点，图上对应节点红框并标注“当前停点”。

## 关键语义测试

`backend/internal/engine/engine_test.go` 与 `backend/internal/api/api_test.go` 覆盖：
版本隔离、重复输入只推进一次、补偿失败停点、条件默认分支、重启后从磁盘继续。
