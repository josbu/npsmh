# 管理接口：实时通道与回调

这一页集中放 WebSocket、reverse WebSocket 和 callback / webhook。

如果你还没确认认证方式、平台作用域和首次同步顺序，先看 [管理接口总览](/reference/management-api)。

## WebSocket

入口：

- `GET /api/ws`

浏览器直接连 `GET /api/ws` 时，当前握手会校验同源；如果是跨域页面，需要先在服务端放行 `web_standalone_allowed_origins`。

浏览器接入补充：

- same-origin session 直接建连即可
- standalone token 模式下，浏览器也可以把原始 standalone token 作为一个 `Sec-WebSocket-Protocol` 候选值带上，用来替代浏览器 JS 不能直接设置 `Authorization` 头的问题
- standalone token 建连成功后，节点仍会在后续帧分发前按当前本地仓库状态刷新本地 `user` / `client` principal；主体被停用、过期、owner 约束失效或作用域变化后，下一次请求就会收敛

WS `request.path` 直接复用 HTTP 路径语义，也就是仍然写正式管理接口路径，例如 `/api/system/status`。
如果配置了 `web_base_url`，这里同样需要带上前缀。

常用帧类型：

- `hello`
- `request`
- `response`
- `event`
- `callback`
- `ping`
- `pong`
- `epoch_changed`
- `resync_required`
- `error`

建连后节点会先发送 `hello`，常用字段：

- `node_id`
- `schema_version`
- `api_base`
- `boot_id`
- `runtime_started_at`
- `config_epoch`
- `capabilities`
- `live_only_events`
- `initial_sync_required`
- `initial_sync_routes`
- `changes_window`
- `changes_durable`
- `changes_history_window`
- `batch_max_items`
- `idempotency_ttl_seconds`

平台推荐流程：

1. 收到 `hello`
2. 先按 `initial_sync_routes` 做一次同步
3. 后续用 `request/response` 做控制
4. 用 `event` 接收实时事件

一个最小 `request` 示例：

```json
{
  "type": "request",
  "id": "req-1",
  "method": "GET",
  "path": "/api/system/status"
}
```

补充说明：

- `request.path` 推荐只使用正式管理接口路径，例如 `/api/system/status`、`/api/clients`
- `request.method` 只接受 `GET` 或 `POST`
- 命中正式管理接口时，成功响应和错误响应都优先放在 `response.body` 里，结构与对应 HTTP 管理接口保持一致
- 命中正式资源 / 控制接口时，权限校验也与对应 HTTP 路由保持一致；WS formal request 不会绕过 `management_admin`、`clients:*`、`tunnels:*`、`hosts:*` 这些声明权限
- 正式管理路径仍按各自声明方法分发；例如 `GET /api/settings/global/actions/update`、`POST /api/settings/global` 这类方法不匹配请求会返回正式 management error `code=method_not_allowed`
- 如果在 `request.headers` 里携带 `X-Operation-ID`，支持操作摘要的写请求会在 `response.headers` 回显该值；后续可继续用 `GET /api/system/operations?operation_id=...` 查询运行态摘要
- WS 顶层 `response.error` 主要保留给协议级失败，例如请求帧无法解析、路径不受支持、幂等冲突等；不要把它当成正式管理接口错误体的唯一来源
- `request.path` 也可以调用 `/api/batch`
- `request.path` 当前还支持 WS 连接内的临时事件 sink 注册路径，也就是 realtime subscription 这一组 WS-only 订阅路径
- batch 内不能再嵌套 `/api/batch`
- batch 也不能转发 `/api/realtime/subscriptions...`；这一组路径只存在于当前 WS 连接内部
- WS 不支持把 `/api/ws` 当成子请求再次分发
- 收到 `epoch_changed` 后，应直接重新做一次全量同步
- 收到 `resync_required` 后，表示当前实时事件流已经不再安全，应重新同步，不要继续依赖旧 cursor

## WS 临时事件订阅

当前 `/api/ws` 已支持“连接内临时 callback 订阅”。

可用的 WS `request.path` 为：

- `/api/realtime/subscriptions`
- `/api/realtime/subscriptions/{id}`
- `/api/realtime/subscriptions/{id}/actions/update`
- `/api/realtime/subscriptions/{id}/actions/status`
- `/api/realtime/subscriptions/{id}/actions/delete`

对应方法仍按管理动作语义使用：

- 列表 / 读取：`GET`
- 创建 / 更新 / 启停 / 删除：`POST`
- 如果 `request.path` 的 action/path 形状本身合法但方法不对，返回正式 management error `code=method_not_allowed`
- 只有 action/path 本身不成立时，才返回正式 management error `code=unknown_ws_request_path`

这组路径只存在于 WS `request.path` 分发里，不开放成普通 HTTP 路由。
如果你是在查正式 HTTP API，可以直接把这组路径排除掉。

订阅成功后：

- 原始 `event` 帧仍然会继续发送
- 命中订阅规则的事件会额外再发一条 `callback` 帧
- 连接断开后订阅自动释放，不做持久化

订阅选择器字段：

- `event_names`
- `resources`
- `actions`
- `user_ids`
- `client_ids`
- `tunnel_ids`
- `host_ids`

订阅内容渲染字段：

- `content_mode`
- `content_type`
- `body_template`
- `header_templates`

当前行为固定为：

- `content_mode=canonical` 返回标准事件 envelope
- `content_mode=custom` 使用模板渲染 body 和 headers
- 删除 `user`、`client`、`tunnel`、`host` 后，会清理对应 selector 里的失效 ID
- 如果某条 WS 订阅的资源选择器因此整体失效，该订阅会自动移除

## reverse WS

当节点配置为 `reverse` 或 `dual` 时，会主动连接平台配置的 `reverse_ws_url`。

reverse 建连后：

- 节点先发 `hello`
- 平台应尽快回一个 `hello` 做恢复协商
- 节点会回 `hello_ack`
- 后续帧模型和 direct WS 相同

恢复协商常用字段：

- `last_boot_id`
- `changes_after`
- `changes_limit`

`hello_ack` 常见字段：

- `resync_required`
- `reason`
- `initial_sync_scope`
- `initial_sync_routes`
- `replay`

如果：

- `boot_id` 变了
- `config_epoch` 变了
- replay 出现 `gap`

那就直接重新全量同步，不要继续依赖旧 cursor。

补充说明：

- reverse `hello` 只适合在连接初期发送
- 一旦实时事件或普通请求已经开始流动，再发 reverse `hello` 会被拒绝
- `hello_ack.replay` 就是一次按 `changes_after` / `changes_limit` 请求到的回放结果
- `hello_ack.reason` 当前常见值有 `missing_boot_id`、`boot_changed`、`changes_gap`

## 运行态摘要事件

与前端实时刷新直接相关的 `resource=operations` 事件包括：

- `operations.updated`

`operations.updated` 当前固定语义：

- 属于 live-only 运行态事件：会通过 `/api/ws` 和 WS 临时订阅发出，但不会进入 durable changes 历史
- 不会再投递给 platform callback 或持久化 webhook sink，避免派生运行态事件再次进入外部 sink
- 常见 `fields` 包括：
  - `operation_id`
  - `request_id`
  - `kind`
  - `source`
  - `scope`
  - `finished_at`
  - `duration_ms`
  - `count`
  - `success_count`
  - `error_count`
  - `paths`
  - `actor_kind`
  - `actor_subject`
  - `actor_name`
  - `platform_id`

与前端实时刷新直接相关的 `resource=management_platforms` 事件包括：

- `management_platforms.updated`

`management_platforms.updated` 当前固定语义：

- 属于 live-only 运行态事件：会通过 `/api/ws` 和 WS 临时订阅发出，但不会进入 durable changes 历史
- 不会再投递给 platform callback 或持久化 webhook sink，避免节点自己的运行态事件再次流向外部 sink
- 当前只在真正有意义的平台运行态变化时发送，不会因为 reverse 心跳里的每次 ping / pong 都持续发事件
- 常见 `fields` 包括：
  - `platform_id`
  - `cause`
  - `connect_mode`
  - `reverse_ws_url`
  - `reverse_enabled`
  - `reverse_connected`
  - `callback_url`
  - `callback_enabled`
  - `callback_timeout_seconds`
  - `callback_retry_max`
  - `callback_retry_backoff_seconds`
  - `callback_queue_max`
  - `callback_queue_size`
  - `callback_dropped`
  - `last_connected_at`
  - `last_disconnected_at`
  - `last_callback_at`
  - `last_callback_success_at`
  - `last_callback_queued_at`
  - `last_callback_replay_at`
  - `last_callback_status_code`
  - `callback_deliveries`
  - `callback_failures`
  - `callback_consecutive_failures`
  - `last_reverse_error`
  - `last_reverse_error_at`
  - `last_callback_error`
  - `last_callback_error_at`
- `cause` 当前常见值包括：
  - `configured`
  - `callback_configured`
  - `reverse_connected`
  - `reverse_disconnected`
  - `callback_delivered`
  - `callback_queued`
  - `callback_replayed`
  - `callback_queue_size`
  - `callback_failed`

## callback / webhook

当平台启用了 callback：

- 节点会把事件以 HTTP webhook 方式投递到 `callback_url`
- callback 只负责通知，不承担资源 CRUD
- 回调失败会进入本地失败队列，可通过管理接口查看、重放、清空

当前边界：

- 当前已经同时支持三类事件 sink：
  - `platform_callback`
  - `webhook`
  - `ws_subscription`
- `platform_callback` 仍然来自节点配置里的 `platform_callback_*`
- `webhook` 是当前正式的运行时持久化事件 sink，只对本地管理员和 `platform_admin` 开放
- `ws_subscription` 是当前 WS 连接内的非持久化事件 sink
- 配置驱动 `platform_callback` 的失败队列接口仍然只有：
  `GET /api/callbacks/queue`、`POST /api/callbacks/queue/actions/replay`、`POST /api/callbacks/queue/actions/clear`

常见请求头：

- `Authorization: Bearer <platform_token>`
- `X-Node-Token: <platform_token>`
- `X-Node-ID`
- `X-Platform-ID`
- `X-Node-Schema-Version`
- `X-Node-Boot-ID`
- `X-Request-ID`

补充说明：

- 非浏览器客户端仍推荐直接使用 `Authorization` 或 `X-Node-Token`
- 浏览器 standalone token 接入优先用 `Sec-WebSocket-Protocol: <standalone_token>`

如果配置了签名 key，还会附带：

- `X-Node-Signature-Alg`
- `X-Node-Signature-Timestamp`
- `X-Node-Signature`

和 callback 失败队列一起使用的本地接口：

- `GET /api/callbacks/queue`
- `POST /api/callbacks/queue/actions/replay`
- `POST /api/callbacks/queue/actions/clear`

补充说明：

- `GET /api/callbacks/queue` 支持 `platform_id`、`limit`
- `POST /api/callbacks/queue/actions/replay` 支持 JSON body 里的 `platform_id`
- `POST /api/callbacks/queue/actions/clear` 支持 JSON body 里的 `platform_id`
- callback queue 这三条接口都要求当前 websocket actor 已认证；匿名请求会返回正式 management error `401 unauthorized`，而不是 `403`
- callback queue 这三条接口只会在当前 actor 至少可见一个 callback-enabled 平台时出现在 discovery 里；如果显式提交了不存在、不可见或未启用 callback 的 `platform_id`，会返回 `404`
- 队列返回的常用字段包括：
  - 平台级：`platform_id`、`callback_queue_size`、`callback_queue_max`、`callback_dropped`
  - 队列项：`event_name`、`event_resource`、`event_action`、`event_sequence`、`request_id`、`enqueued_at`、`last_attempt_at`、`attempts`

与前端实时刷新直接相关的 `resource=callbacks_queue` 事件包括：

- `callbacks_queue.updated`

`callbacks_queue.updated` 当前固定语义：

- 属于 live-only 运行态事件：会通过 `/api/ws` 和 WS 临时订阅发出，但不会进入 durable changes 历史
- 不会再投递给 platform callback 或持久化 webhook sink，避免 callback / webhook 自己监听到自己的投递结果
- 常见 `fields` 包括：
  - `platform_id`
  - `callback_queue_size`
  - `callback_queue_max`
  - `callback_dropped`
  - `last_callback_queued_at`
  - `last_callback_replay_at`
  - `last_callback_status_code`
  - `last_callback_error`
  - `last_callback_error_at`
  - `cause`
- `cause` 当前常见值包括：
  - `queued`
  - `attempted`
  - `cleared`
  - `delivered`
  - `delivery_failed`
  - `replayed`
- 当事件与某条具体队列项直接相关时，还会额外附带：
  - `id`
  - `request_id`
  - `event_name`
  - `event_resource`
  - `event_action`
  - `event_sequence`
  - `enqueued_at`
  - `last_attempt_at`
  - `attempts`

当前正式 webhook 注册管理接口为：

- `GET /api/webhooks`
- `GET /api/webhooks/{id}`
- `POST /api/webhooks`
- `POST /api/webhooks/{id}/actions/update`
- `POST /api/webhooks/{id}/actions/status`
- `POST /api/webhooks/{id}/actions/delete`
- 这组 webhook 管理路径要求当前 websocket actor 已认证；匿名请求返回正式 management error `401 unauthorized`

通过 `/api/ws` 的 `request.path` 调这组 webhook 管理路径时，方法语义保持一致：

- 列表 / 读取：`GET`
- 创建 / 更新 / 启停 / 删除：`POST`
- action/path 合法但方法不对时返回 `code=method_not_allowed`
- 只有 action/path 不存在或形状不成立时才返回 `code=unknown_ws_request_path`

webhook 当前正式字段包括：

- `name`
- `url`
- `enabled`
- `timeout_seconds`
- `event_names`
- `resources`
- `actions`
- `user_ids`
- `client_ids`
- `tunnel_ids`
- `host_ids`
- `content_mode`
- `content_type`
- `body_template`
- `header_templates`

与前端实时刷新直接相关的 `resource=webhook` 事件包括：

- `webhook.created`
- `webhook.updated`
- `webhook.status_changed`
- `webhook.deleted`
- `webhook.selector_scrubbed`
- `webhook.delivery_succeeded`
- `webhook.delivery_failed`

webhook 当前固定行为：

- webhook 持久化到节点运行态状态文件，不写回 `platform_callback_*`
- webhook 会由节点主动向外发 HTTP 请求，所以不向普通用户、客户端或 delegated `platform_user` 开放；这类接入请使用 WS 临时订阅
- webhook 仍按当前 actor 作用域过滤事件，不会越权订阅
- 删除 `user`、`client`、`tunnel`、`host` 后，会清理 webhook selector 里的失效 ID
- 如果某条 webhook 的资源选择器因此整体失效，该 webhook 会自动禁用
- 删除 webhook 只删除 webhook 自身，不清空 callback 失败队列
- `resource=webhook` 的实时事件现在都带当前 webhook 的完整快照字段，前端可以直接本地合并列表：
  包括基础配置、selector、内容模板、header、owner、创建/更新时间和投递运行态；`webhook.deleted` 也会带删除前快照
- `webhook.delivery_succeeded` 和 `webhook.delivery_failed` 属于 live-only 运行态事件：
  会通过 `/api/ws` 与 WS 临时订阅发出，但不会进入 durable changes 历史，也不会再投递给 platform callback 或持久化 webhook sink，避免 webhook 自己监听到自己的投递结果

## 相关页面

- 需要 HTTP 接口和批量控制：看 [HTTP 接口目录](/reference/management-api-http)
- 需要平台配置项：看 [节点与平台对接](/reference/server-config-node)

