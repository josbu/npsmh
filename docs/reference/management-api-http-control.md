# 管理接口：控制接口与示例

这一页聚焦正式 `/api/` 管理接口里偏写入型的控制接口、批量控制和常用请求示例。

## 控制接口

| 方法 | 路径 | 用途 | 补充说明 |
| --- | --- | --- | --- |
| `GET` | `/api/callbacks/queue` | 查看 callback 失败队列 | 仅在当前 actor 可见的 callback-enabled 平台存在时发布；支持 `platform_id`、`limit` |
| `GET` | `/api/webhooks` | 查看当前管理 actor 可见的 webhook 列表 | 只对本地管理员或 `platform_admin` 开放；持久化事件 sink；返回选择器、内容模板和运行态摘要 |
| `GET` | `/api/webhooks/{id}` | 查看单个 webhook | 只对本地管理员或 `platform_admin` 开放 |
| `POST` | `/api/batch` | 批量执行子请求 | 上限受 `node_batch_max_items` 控制；支持对象或顶层数组两种 JSON 体 |
| `POST` | `/api/traffic` | 写入流量增量 | 仅 `full` 管理视角；JSON body 里可用 `client_id` 或 `verify_key` 定位客户端；两者同时提供时必须指向同一客户端 |
| `POST` | `/api/clients/actions/kick` | 踢断客户端 | JSON body 里可用 `client_id` 或 `verify_key` 定位客户端；两者同时提供时必须指向同一客户端 |
| `POST` | `/api/system/actions/sync` | 按本地真源重载运行态 | 仅 `full` 管理视角；适合平台写入后触发重同步 |
| `POST` | `/api/system/import` | 管理员级导入完整业务配置 | 更适合本地管理员或 `full` 平台管理员 |
| `POST` | `/api/callbacks/queue/actions/replay` | 重放 callback 失败队列 | 仅在当前 actor 可见的 callback-enabled 平台存在时发布；支持 `platform_id` |
| `POST` | `/api/callbacks/queue/actions/clear` | 清空 callback 失败队列 | 仅在当前 actor 可见的 callback-enabled 平台存在时发布；支持 `platform_id` |
| `POST` | `/api/webhooks` | 新增 webhook | 只对本地管理员或 `platform_admin` 开放；JSON object body；正式运行时 webhook 注册入口 |
| `POST` | `/api/webhooks/{id}/actions/update` | 更新 webhook | 只对本地管理员或 `platform_admin` 开放；全量更新选择器、内容模板和投递配置 |
| `POST` | `/api/webhooks/{id}/actions/status` | 启停 webhook | 只对本地管理员或 `platform_admin` 开放；JSON body 里只接受 `enabled` |
| `POST` | `/api/webhooks/{id}/actions/delete` | 删除 webhook | 只对本地管理员或 `platform_admin` 开放；只删除 webhook 自身，不清空 callback 失败队列 |

## 写请求建议

- 建议对写请求附带 `Idempotency-Key` 或 `X-Idempotency-Key`
- 建议附带 `X-Operation-ID`，响应会回显同名头，后续可用 `/api/system/operations?operation_id=...` 查询结果摘要
- 当服务端把某次写请求识别为重复回放时，响应会带 `X-Idempotent-Replay: true`

补充说明：

- `POST /api/batch` 的 JSON 体既可以是 `{"items":[...]}`，也可以直接是顶层数组 `[...]`
- `POST /api/batch` 的子请求 `path` 直接复用正式管理接口路径，例如 `/api/system/status`、`/api/clients`
- `POST /api/batch` 的每个 item 里，`method` 只接受 `GET` 或 `POST`
- batch 内不支持继续嵌套 `/api/batch` 或 `/api/ws`
- batch 只接受正式 HTTP 管理路径；`/api/realtime/subscriptions...` 这组仅 WS 内部可用的 `request.path` 不能通过 batch 转发
- 除 `POST /api/batch` 外，新的写接口都只接受 JSON object body，不再兼容 form/query 别名
- 写接口里的数值字段必须使用 JSON number，例如 `client_id`、`port`、`flow_limit_total_bytes`、`rate_limit_total_bps`、`max_connections`
- `POST /api/traffic` 支持两种 canonical JSON 形式：`{"items":[...]}` 或单对象 `{"client_id":1,"in":3,"out":4}`
- `POST /api/traffic` 和 `POST /api/clients/actions/kick` 如果同时提交 `client_id` 与 `verify_key`，服务端会校验二者是否匹配；不匹配直接返回 `400`
- `POST /api/clients/actions/kick` 更适合按目标客户端执行单点控制，不要求 `full` 管理视角
- `POST /api/callbacks/queue/actions/replay` 和 `POST /api/callbacks/queue/actions/clear` 支持 JSON body 里的 `platform_id`
- `POST /api/callbacks/queue/actions/replay` 和 `POST /api/callbacks/queue/actions/clear` 的响应会带当前平台的 queue 摘要，前端可直接本地 patch `callback_queue_size / callback_queue_max`
- callback queue 这三条控制接口都要求当前 actor 已认证；未认证请求返回 `401` 和正式 management error `code=unauthorized`
- 如果显式提交了不存在、不可见或未启用 callback 的 `platform_id`，会返回 `404` 和 `error.code=management_platform_not_found`
- `POST /api/security/bans/actions/delete` 只接受 JSON object body，例如 `{"key":"1.2.3.4"}`，不再兼容 query / form 取值
- `POST /api/security/bans/actions/clean` 的响应会带 `removed_keys` 和 `total`，便于前端按当前结果本地 patch banlist
- 当前正式的运行时 webhook 注册管理接口为：
  `GET /api/webhooks`、`GET /api/webhooks/{id}`、`POST /api/webhooks`、`POST /api/webhooks/{id}/actions/update`、`POST /api/webhooks/{id}/actions/status`、`POST /api/webhooks/{id}/actions/delete`
- 这一组 webhook 会由节点主动向外发 HTTP 请求，所以默认只开放给本地管理员和 `platform_admin`
- 这一组 webhook 管理接口都要求当前 actor 已认证；未认证请求返回 `401` 和正式 management error `code=unauthorized`
- 普通用户、客户端或 delegated `platform_user` 如果只需要事件上报，应使用 `/api/ws` 里的临时订阅 `request.path`。
- 正式 HTTP 路由里并不存在 `/api/realtime/subscriptions...` 这一组接口；它只存在于 WS `request.path` 分发里，读取用 `GET`，创建/更新/启停/删除用 `POST`
- 对 `/api/ws` 里的 `webhooks` / `realtime/subscriptions` 管理路径，如果 action/path 形状合法但方法不对，返回正式 management error `code=method_not_allowed`；只有 action/path 本身无效时才返回 `code=unknown_ws_request_path`
- 这一组 webhook 不写回 `platform_callback_*`，而是独立持久化到节点运行态协议存储
- `platform_callback_*` 继续保留给配置驱动的平台级 callback；对应失败队列接口仍然只有：
  `GET /api/callbacks/queue`、`POST /api/callbacks/queue/actions/replay`、`POST /api/callbacks/queue/actions/clear`
- webhook 选择器与内容渲染当前正式字段为：
  - `event_names`、`resources`、`actions`
  - `user_ids`、`client_ids`、`tunnel_ids`、`host_ids`
  - `content_mode`
  - `content_type`
  - `body_template`
  - `header_templates`
- `content_mode=canonical` 会直接投递当前事件 envelope；`content_mode=custom` 会按模板渲染 body 和 headers
- 当 `user`、`client`、`tunnel`、`host` 被删除后，webhook 里对应的 selector ID 会被清理；如果某条 webhook 的资源选择器因此整体失效，该 webhook 会自动禁用

## 常用示例

### 读取总览

```bash
curl -H "X-Node-Token: <platform_token>" \
  http://127.0.0.1:8081/api/system/overview
```

### 读取客户端列表

```bash
curl -H "X-Node-Token: <platform_token>" \
  "http://127.0.0.1:8081/api/clients?offset=0&limit=20"
```

### 新增客户端

```bash
curl -X POST \
  -H "X-Node-Token: <platform_token>" \
  -H "Content-Type: application/json" \
  -d "{\"verify_key\":\"demo-key\",\"remark\":\"demo client\"}" \
  http://127.0.0.1:8081/api/clients
```

### 修改客户端状态

```bash
curl -X POST \
  -H "X-Node-Token: <platform_token>" \
  -H "Content-Type: application/json" \
  -d "{\"status\":0}" \
  http://127.0.0.1:8081/api/clients/7/actions/status
```

### 触发一次同步

```bash
curl -X POST \
  -H "X-Node-Token: <platform_token>" \
  -H "Idempotency-Key: sync-20260331-1" \
  -H "X-Operation-ID: deploy-sync-1" \
  http://127.0.0.1:8081/api/system/actions/sync
```

### 导出完整配置

```bash
curl -H "X-Node-Token: <full_platform_token>" \
  http://127.0.0.1:8081/api/system/export
```

### 导入完整配置

```bash
curl -X POST \
  -H "X-Node-Token: <full_platform_token>" \
  -H "Content-Type: application/json" \
  --data-binary @node-config-export.json \
  http://127.0.0.1:8081/api/system/import
```

### 批量请求

```bash
curl -X POST \
  -H "X-Node-Token: <platform_token>" \
  -H "Content-Type: application/json" \
  -d "{\"items\":[{\"method\":\"GET\",\"path\":\"/api/clients\"},{\"method\":\"GET\",\"path\":\"/api/system/status\"}]}" \
  http://127.0.0.1:8081/api/batch
```

### 查看 callback 失败队列

```bash
curl -H "X-Node-Token: <platform_token>" \
  "http://127.0.0.1:8081/api/callbacks/queue?limit=20"
```

### 新增自定义 webhook

```bash
curl -X POST \
  -H "X-Node-Token: <platform_token>" \
  -H "Content-Type: application/json" \
  -d "{\"name\":\"client-created\",\"url\":\"https://example.com/webhook\",\"event_names\":[\"client.created\"],\"resources\":[\"client\"],\"content_mode\":\"custom\",\"content_type\":\"application/json\",\"body_template\":\"{\\\"event\\\":{{ quote .Event.Name }},\\\"client_id\\\":{{ .IDs.ClientID }}}\",\"header_templates\":{\"X-Event-Action\":\"{{ .Event.Action }}\"}}" \
  http://127.0.0.1:8081/api/webhooks
```

## 导出与导入边界

管理员级备份恢复接口：

- `GET /api/system/export`
- `POST /api/system/import`

规则：

- 只有本地管理员和 `full` 平台管理员可用
- 只导出导入业务配置和业务数据
- 不导出导入 `changes`、幂等缓存、callback 队列等协议辅助运行态
- 导入成功后会切换到新的 `config_epoch`
- 导入成功后旧 `changes` cursor、旧幂等缓存、旧实时会话全部失效

## 跨节点限制边界

节点负责本地强约束：

- `flow_limit_total_bytes`
- `expire_at`
- `max_clients`
- `max_tunnels`
- `max_hosts`

外部管理平台负责跨节点总量限制：

- 总流量
- 总客户端数
- 总隧道数
- 总域名数
- 跨节点统一到期策略

以下内容不建议做跨节点强约束：

- `rate_limit_total_bps`
- `max_connections`

补充说明：

- `rate_limit_total_bps` 在正式管理接口里的单位固定是 bytes/s

## 相关页面

- 需要路径总览和快照接口：看 [发现与快照接口](/reference/management-api-http-discovery)
- 需要资源 CRUD：看 [资源接口](/reference/management-api-http-resources)
- 需要 WS、reverse WS 和 callback：看 [实时通道与回调](/reference/management-api-realtime)

