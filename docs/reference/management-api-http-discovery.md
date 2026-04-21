# 管理接口：发现与快照接口

这一页聚焦正式 `/api/` 管理接口里偏读取型的系统发现、状态、快照和变化流接口。

## 接口清单

| 方法 | 路径 | 用途 | 补充说明 |
| --- | --- | --- | --- |
| `GET` | `/api/system/health` | 最小健康检查入口 | 无需鉴权；适合反代、LB、k8s probe 和接入前预检查 |
| `GET` | `/api/system/discovery` | 正式管理 API 发现入口 | 前后端分离页面、外部前端或工具接入的首个请求 |
| `GET` | `/api/system/overview` | 推荐总览快照 | 支持 `?config=1`；只有 `full` 管理视角才会附带完整配置 |
| `GET` | `/api/system/dashboard` | 仪表盘统计 | 仅本地管理员或 `full` 平台管理员 |
| `GET` | `/api/system/registration` | 节点注册信息 | 比 `status` 多 `health`、`events_enabled`、`callbacks_ready` 等发现字段 |
| `GET` | `/api/system/status` | 节点状态、能力位、运行参数 | 远程 `platform_admin` / `platform_user` 可读；本地普通用户不可读 |
| `GET` | `/api/system/operations` | 操作摘要 | 支持 `operation_id`、`limit`；需要可读节点状态权限 |
| `GET` | `/api/system/changes` | 增量事件补偿 | 支持 `after`、`limit`；`durable=1` 或 `history=1` 走持久历史回放 |
| `GET` | `/api/system/usage-snapshot` | 当前作用域的资源与流量统计 | 本地普通用户也可读；这是统计快照，不等同资源详情接口 |
| `GET` | `/api/system/export` | 管理员级完整配置导出 | 仅本地管理员或 `full` 平台管理员 |

## 推荐首次接入顺序

1. 可先用 `GET /api/system/health` 做存活探针或预检查
2. `GET /api/system/discovery`
   如果配置了 `web_base_url`，这里的实际路径会自动带上规范化后的前缀，例如 `web_base_url=nps/` 最终也会发布 `/nps/api/system/discovery`
3. `GET /api/system/overview`
4. 如果你是本地管理员或 `full` 平台管理员，可以直接改用 `GET /api/system/overview?config=1`
5. 之后按需使用：
   - `GET /api/system/changes`
   - `GET /api/system/usage-snapshot`
   - `GET /api/ws`
   - callback

补充：

- `GET /api/system/health`
- `GET /api/system/discovery`

当前正式 `/api/*` 管理接口响应都会显式返回 `Cache-Control: no-store` 和 `Pragma: no-cache`，避免被浏览器或反代缓存成过期视图。

如果发现以下任意情况，应重新做一次全量同步：

- `boot_id` 变化
- `config_epoch` 变化
- `/api/system/changes` 返回 `gap=true`

## 访问边界

- standalone token
  - 来源：`POST /api/auth/token`
  - 只支持 `Authorization: Bearer <token>`
  - bearer 请求不会只看签名快照；本地 `user` / `client` principal 会按当前仓库状态刷新，主体被停用、过期、owner 约束失效或作用域变化后会立即收敛
- platform token
  - 建议放在 `X-Node-Token` 或 `Authorization: Bearer <platform_token>`
  - `POST` 也支持在 JSON 请求体里传 `token` 或 `node_token`
  - `GET` 请求当前不支持 URL 查询参数里的 `token`
- 只有 platform token，没有委托上下文时，远程平台请求默认按平台管理员 actor 处理
- 如果带了委托上下文但没有显式角色头，会默认按用户作用域处理
- 只支持反向管理的 `reverse` 平台不能直接访问这些 HTTP 接口

## 常用查询参数

`GET /api/system/discovery` 无需查询参数。

补充说明：

- discovery 返回的 `data.routes` 当前会按 actor 权限发布：
  - 公开入口始终包含 `api_base`、`health`、`discovery`、`session`、`token`、`logout`、`access_ip_limit_register`
  - `register` 只会在允许注册时出现
  - 资源、控制、`batch`、`ws`、`callbacks_queue`、`webhooks`、`realtime_subscriptions` 等受保护路径，只有当前 actor 真正可用时才会出现
- 客户端资源路由当前还会发布 `routes.clients_connections`，对应 `/api/clients/:id/connections` 这一类同 vkey 运行时实例查询入口
- callback queue 相关动作和路由还会额外受运行时能力约束：只有当前 actor 至少可见一个 callback-enabled 平台时才会发布
- `data.extensions.cluster.management_platforms` 也按 actor 可见性裁剪：
  - 本地管理员或 `full` 平台管理员可见全部平台状态
  - `platform_admin` / `platform_user` 只可见自己的 `platform_id`
  - 匿名、本地普通用户、本地 `client` principal 不会拿到这组平台配置
- 如果当前 actor 已认证，discovery 会发布：
  - `routes.realtime_subscriptions`
- 如果当前 actor 具备 webhook 管理权限，discovery 还会发布：
  - `routes.webhooks`
- 其中 `routes.webhooks` 对应普通 HTTP 管理接口
- `routes.realtime_subscriptions` 只用于 `/api/ws` 里的 `request.path`，不是普通 HTTP 路由
- 旧 HTML / 跳转入口不再作为 discovery 契约的一部分
- 如果你需要当前 session 状态，调 `GET /api/auth/session`
- 如果你需要创建当前 session，调 `POST /api/auth/session`
- `data.auth.allow_client_vkey_login` 表示当前节点是否允许 `client_vkey` 管理登录
- `data.auth` 当前稳定包含：
  - `login_delay_ms`
  - `totp_len`
  - `pow_enable`
  - `allow_client_vkey_login`
- `data.auth.pow_enable=true` 表示当前 session 登录链可能要求 PoW
  - `force_pow=true` 时始终要求
  - 否则只有 `secure_mode=true` 且命中登录失败封禁状态时才要求
  - discovery 只表示“可能要求”；客户端应在收到 `pow_required` 后按需计算并重试，而不是默认每次都预计算
- `data.features` 当前稳定包含：
  - `allow_user_login`
  - `allow_user_register`
  - `open_captcha`
  - 以及其他节点能力开关
- 只有当 `data.features.open_captcha=true` 时，discovery 才会发布 `routes.captcha_new`
- 客户端通过 `GET /captcha/new` 返回的 `url` 读取实际验证码图片，不再依赖额外的 captcha 基础路径字段
- `data.security.pow_bits` 表示当前 session 登录要求的 PoW 位数；当 session 登录返回 `error.code=pow_required` 时，客户端应提交同值的 `pow_bits`
- `data.session.kind` / `data.actor.kind` 当前会稳定落在 `anonymous`、`admin`、`user`、`client` 这几类值里

- `GET /api/system/overview`：`config=1`
- `GET /api/system/operations`：`operation_id`、`limit`
- `GET /api/system/changes`：`after`、`limit`、`durable=1`、`history=1`

`/api/system/changes` 的补充规则：

- 默认 `limit=100`
- 最大 `limit=500`
- 响应会带 `cursor`、`oldest_cursor`、`next_after`、`has_more`、`gap`
- `durable=1` 或 `history=1` 会改走持久历史窗口，不再只看当前内存补偿窗口

`/api/system/changes` 中 `items[]` 的常用字段：

- `sequence`：事件序号（用于游标推进与去重）
- `timestamp`：事件时间戳（Unix 秒）；若事件上报时未显式填写，节点会在发射事件时自动补齐
- `name`：事件名（例如资源变更事件）
- `resource` / `action`：资源类别与动作
- `actor`：事件操作者（如 `username`、`subject_id`、`kind`）
- `metadata`：请求元信息（如 `request_id`、`source`）
- `fields`：事件扩展字段

## 常用字段

这些字段可视为当前正式契约：

- `boot_id`
- `runtime_started_at`
- `config_epoch`
- `owner_user_id`
- `manager_user_ids`
- `source_platform_id`
- `source_actor_id`
- `revision`
- `updated_at`

`/api/system/status` 和 `/api/system/registration` 常用字段：

- `node_id`
- `schema_version`
- `api_base`
- `boot_id`
- `runtime_started_at`
- `config_epoch`
- `capabilities`
- `protocol`
- `counts`
- `revisions`
- `operations`
- `idempotency`
- `display`

`display` 的当前稳定用途：

- `display.bridge.primary` 提供当前节点推荐展示的主桥接入口摘要
- `display.bridge.tcp|kcp|tls|quic|ws|wss` 会发布各桥接传输的 `enabled`、`type`、`ip`、`port`、`addr`
- `display.bridge.path` 会发布 WS / WSS 相关桥接路径
- `display.http_proxy_port`、`display.https_proxy_port` 会发布当前代理入口端口
- `display.client_binary_suffix` 可用于展示建议客户端二进制名
- `display.visitor_vkey` 可用于 visitor / secret / p2p 一类访问命令或说明

`/api/system/usage-snapshot` 常用字段：

- `summary`
- `users`
- `clients`
- `revisions`
- `config_epoch`

补充说明：

- `usage/snapshot` 面向统计和同步判断，不保证和资源详情接口字段完全一一对应
- `usage_snapshot.clients[]` 与 `overview.usage_snapshot.clients[]` 里的敏感客户端字段也会按 actor 权限做脱敏
- 当前 `verify_key` 只有具备 `clients:update` 的 actor 才会收到；自定义只读 actor 即使能读取 `usage-snapshot`，该字段也会被清空
- `usage_snapshot.clients[]` 当前使用 `entry_acl_rule_count` 表示归一化后的入口 ACL 规则条数；旧的 `black_ip_count` 不再使用
- `overview` 内嵌的 `usage_snapshot` 与单独的 `GET /api/system/usage-snapshot` 使用同一套统计与脱敏规则

## 相关页面

- 需要认证方式、actor 作用域和同步模型：看 [管理接口总览](/reference/management-api)
- 需要资源 CRUD 路径：看 [资源接口](/reference/management-api-http-resources)
- 需要批量控制、同步和导入：看 [控制接口与示例](/reference/management-api-http-control)
- 需要 WS、reverse WS 和 callback：看 [实时通道与回调](/reference/management-api-realtime)

