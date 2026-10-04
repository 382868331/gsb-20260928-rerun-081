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

第二轮新增：

- **定义加载三态分离**：空定义（引导发布）、加载失败（可重试、编辑与发布不中断）、已有定义列表是三种不同界面；后端空列表固定返回 `[]`，前端对不可遍历数据做防御，任何局部异常都不会再整页空白；未发布草稿仍存浏览器本地，失败恢复后可继续编辑发布
- **协作等待（多方输入）**：等待节点可配置参与方列表、是否允许委托、到期时间与到期策略（通过/拒绝）；需全部有效参与方确认后步骤才完成，部分输入只记录不推进
- **授权范围版本化**：等待期间可修改参与方、授予/撤回委托，每次变更生成新的授权记录并说明此前输入是否仍有效，新旧记录全部保留；每个输入与最终决定都标记当时有效的授权范围 id，可追溯
- **单一决定**：重复输入、撤回委托与超时同时到达时（实例级锁串行化）只形成一个决定；已有决定后的迟到输入（含补偿期间）仅作为历史事件保留，不重做任何动作
- **续跑安全**：协作等待状态随实例落盘，重启后继续等待，已有确认不丢失、不重复执行

第三轮新增：

- **决定复议**：协作决定通过并推进后续步骤后，可针对该决定发起复议（`POST /api/instances/{id}/reconsiderations`）。复议与原决定相连：原赞成、委托与生效范围原样保留（`waitDecisions` 审计），本轮参与范围与新资料只影响本次复议，不改写历史
- **复议期间可见进度**：`GET /api/instances/{id}/reconsiderations/{rid}` 返回原决定已推进到哪里——已执行的后续动作（只能按流程补偿）与尚可暂停的步骤；复议未决期间后续输入被暂扣（`input_held`），resume 不推进
- **唯一决定**：复议通过 / 拒绝 / 到期各自只形成一个决定（`reconsider_decided`）；旧投票的幂等键已被消费，不能凑新一轮人数；复议通过只补偿原决定**之后**执行的动作（scoped 补偿），补偿期间原业务动作不会再被推动
- **按意见表决**：每条复议意见必须明确表态 `payload.opinion`（`uphold` 支持复议 / `reject` 反对复议），未表态的意见被拒绝且不消费幂等键；意见集齐后按实际表态计票——任一反对即复议不成立（`rejected`），原决定与已生效动作保留、不触发补偿；全体一致支持才撤销原决定（`upheld`）。页面可明确提交反对意见，并实时显示各方表态与计票
- **交错与补偿失败**：部分新意见、委托撤回（`POST .../reconsiderations/{rid}/scope`）、原后续步骤失败与复议到期可任意交错（实例级锁 + 到期优先判定）；补偿失败保留未完成范围（`reconsider_compensation_incomplete`），复议结束不会显示全部恢复；重启后可沿原决定、复议依据与每条真实事件解释当前状态

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
- `POST /api/instances/{id}/resume` 重启后把 `running` 实例推进到下一个等待/结束/失败
- `GET  /api/processes` 每个流程名的最新版本（空数据时返回 `{}`，前端据此引导发布）
- `GET  /api/instances/{id}/migration?definitionId=...` 预览等待实例迁移到新版本后受影响的步骤
- `POST /api/instances/{id}/migration` `{definitionId, confirm:true}` 用户确认后迁移；未确认或等待节点在新版本中缺失都会被拒绝
- `POST /api/instances/{id}/wait/scope` 修改协作等待的接收范围 `{participants, grantDelegations, revokeDelegations, keepPriorInputs, reason}`
- `POST /api/instances/{id}/timeout` 对协作等待立即做一次到期判定（决定最多形成一次）
- `POST /api/instances/{id}/reconsiderations` 对已通过的协作决定发起复议 `{waitNode, participants, allowDelegate, grantDelegations, materials, reason, timeoutSeconds, timeoutPolicy}`
- `GET  /api/instances/{id}/reconsiderations/{rid}` 复议进度视图（已执行待补偿 / 尚可暂停 / 未完成补偿范围）
- `POST /api/instances/{id}/reconsiderations/{rid}/inputs` 提交本轮复议意见 `{key, from, payload}`
- `POST /api/instances/{id}/reconsiderations/{rid}/scope` 变更本轮范围（授予/撤回委托、调整参与方）
- `POST /api/instances/{id}/reconsiderations/{rid}/timeout` 复议到期判定（`timeoutPolicy: "uphold"` 到期视为复议通过，默认复议不成立）

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
