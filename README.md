# 业务流程运行平台（第一轮）

## 第二轮新增

- **空数据首次可用**：定义/实例空集合返回 `[]` 而非 `null`，页面不再白屏；三个页签始终可见，空列表显示引导提示（先发布定义再启动实例）
- **三类反馈区分**：空列表（引导提示）、加载失败（页头错误条 + 重试，保留已有数据）、执行失败（面板内联错误）
- **刷新不丢操作**：当前页签、编辑器草稿（名称/节点/边）、实例表单与选中实例均持久化在 localStorage
- **等待中实例的版本迁移**：等待人工输入的实例可迁移到同名流程的新版本。先 `POST /api/instances/{id}/migrate`（不带 confirm）预览受影响步骤，用户确认后再带 `confirm: true` 执行；当前等待步骤在新版本被删除或改类型时阻断迁移。运行中/已结束实例不可迁移，新实例自动采用最新版本
- **历史不拼接**：迁移只在同一事件流追加一条 `migrate` 记录，已完成步骤绝不按新定义重放
- **续跑幂等**：重启恢复时，已有 `succeed` 记录的节点跳过重放（记 `skip_redone`）；崩溃在补偿中途的实例先补完补偿链，同一步绝不重复补偿

## 第一轮

Go 流程引擎 + React 流程编辑器。第一轮交付：

- 可视化流程编辑与**版本发布**：每次发布生成不可变的新版本定义
- 实例执行：实例启动时固定当时的定义 id，之后发布新版本**不影响运行中实例**
- 条件分支：边上的条件支持 `key == "x"`、`key != ...`、`key > >= < <= 数字`、布尔键与 `default`
- 等待外部输入：执行到 wait 节点挂起；页面人工提交，带**幂等键 key**，同一 key 重复提交只生效一次
- 迟到输入：实例已离开等待点（失败/完成）后再提交输入不再整体报错；记录 `late_input` 事件并忽略，不覆盖数据、不重做动作
- 失败补偿：任务失败后按执行路径逆序调用补偿；补偿本身也可能失败，实例记录**真实停在哪一步**（`stoppedAt`）
- 持久化与重启续跑：定义和实例均以 JSON 原子写盘（临时文件 + rename）；服务启动时自动恢复 `running` 状态实例，也可 `POST /api/instances/{id}/resume` 手动续跑
- 页面三个页签：**流程定义**（载入旧版本修改后同名发布新版本）、**实例列表**（按流程最新版本启动、显示停点/等待点、已生效 key）、**执行历史**（按状态筛选、逐条事件、已生效动作与补偿统计）
- 画布中的绿色箭头只来自执行记录 `records` 里真实经过的 `edgeId`（branch/traverse），不是动画推断；前端带错误边界，局部渲染异常不再整页白屏

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
- `POST /api/instances/{id}/migrate` 等待中实例迁移定义版本 `{targetDefinitionId, confirm}`；`confirm=false` 返回受影响步骤预览，`confirm=true` 执行迁移

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
