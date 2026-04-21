# 服务端配置：节点与平台对接

这一页集中放 `run_mode=node`、多平台管理、reverse WebSocket 和 callback 相关配置。

如果你只是单节点部署，可以先跳过这一页。

## 1. 节点管理与多平台配置

当 `run_mode=node` 时，节点仍然使用本地数据作为真源；如果配置了管理平台，只是额外开放远程管理接口。

推荐新部署使用以下配置项：

| 名称 | 说明 |
| --- | --- |
| `platform_ids` | 管理平台 ID 列表，逗号分隔 |
| `platform_tokens` | 管理平台 token 列表，逗号分隔 |
| `platform_scopes` | 控制范围列表，支持 `full`、`account` |
| `platform_enabled` | 是否启用，逗号分隔 |
| `platform_service_users` | 节点上的平台服务用户名列表 |
| `platform_urls` | 管理平台地址列表，仅用于诊断、能力发现和审计 |
| `platform_connect_modes` | 平台连接模式列表，支持 `direct`、`reverse`、`dual` |
| `platform_reverse_ws_urls` | 节点主动连接的平台反向 WS 地址列表 |
| `platform_reverse_enabled` | 是否启用反向 WS 通道，逗号分隔 |
| `platform_reverse_heartbeat_seconds` | 反向 WS 心跳周期列表，逗号分隔 |
| `platform_callback_urls` | 节点主动向平台投递事件回调的 HTTP 地址列表 |
| `platform_callback_enabled` | 是否启用事件回调通道，逗号分隔 |
| `platform_callback_timeout_seconds` | 事件回调 HTTP 超时时间列表，单位秒 |
| `platform_callback_retry_max` | 事件回调失败后的最大重试次数列表，默认 `2` |
| `platform_callback_retry_backoff_seconds` | 事件回调重试间隔列表，单位秒，默认 `2` |
| `platform_callback_queue_max` | 事件回调失败队列上限列表，默认 `100`，设置为 `0` 表示禁用本地补偿队列 |
| `platform_callback_signing_keys` | 事件回调 HMAC 签名 key 列表，留空表示不签名 |
| `node_changes_window` | 节点变更事件窗口大小，默认 `1024`，最小 `100` |
| `node_batch_max_items` | 节点批量接口最大子请求数，默认 `50`，范围 `1-500` |
| `node_idempotency_ttl_seconds` | 节点写请求幂等缓存 TTL（秒），默认 `300`，范围 `10-86400` |
| `node_traffic_report_interval_seconds` | 客户端流量事件最小上报间隔（秒），`0` 表示关闭，建议如 `1` |
| `node_traffic_report_step_bytes` | 客户端流量事件累计步长（字节），`0` 表示关闭，建议如 `10485760`（10 MiB） |

说明：

- `platform_connect_modes` 及其配套的反向 WS 配置已经实现，推荐新部署直接按该组配置使用
- 兼容旧配置时，`master_url`、`node_token` 仍会被自动迁移为第一条平台配置

旧版兼容配置：

| 名称 | 说明 |
| --- | --- |
| `master_url` | 旧单平台地址，启动时会自动合成为第一条平台配置 |
| `node_token` | 旧单平台 token，启动时会自动合成为第一条平台配置 |

补充说明：

- `master_url` 不表示节点配置真源地址
- 外部管理平台发起的写请求会直接写入当前节点本地数据，并以节点返回结果为准
- 一个节点可以配置多个管理平台
- 每个平台在节点上建议对应一个隐藏的 `platform_service` 用户
- 如果未显式设置 `platform_service_users`，节点会自动生成默认服务用户名
- `platform_connect_modes=direct` 表示平台主动连节点
- `platform_connect_modes=reverse` 表示节点主动连平台；该平台 token 不能再直接访问公开的 HTTP 管理接口
- `platform_connect_modes=dual` 表示两个方向都启用
- `platform_urls` 继续用于诊断、能力发现和审计，不等同于反向 WS 实际连接地址
- `platform_reverse_ws_urls` 才是节点在 `reverse` 或 `dual` 模式下主动拨出的目标地址
- `platform_callback_urls` 是节点主动投递事件 webhook 的目标地址，适合只做 HTTP 集成的平台
- `platform_callback_retry_max` 表示单次事件投递失败后最多额外重试多少次，不含首次投递
- `platform_callback_retry_backoff_seconds` 表示 callback 每次失败后的固定重试间隔
- `platform_callback_queue_max` 表示 callback 在当前节点本地允许保留多少条失败待补偿事件；超过上限时会丢弃最旧事件
- `platform_callback_signing_keys` 用于为 webhook 增加 `X-Node-Signature-*` 头，方便平台校验请求来源与报文完整性
- `platform_callback_*` 继续只管理配置驱动的平台级 callback；当前管理 API 另外已经提供独立的运行时 webhook 管理接口：
  `GET /api/webhooks`、`GET /api/webhooks/{id}`、`POST /api/webhooks`、`POST /api/webhooks/{id}/actions/update`、`POST /api/webhooks/{id}/actions/status`、`POST /api/webhooks/{id}/actions/delete`
- `node_changes_window` 会影响 `/api/system/changes` 的当前补偿窗口和 WS `hello` 中的 `changes_window`
- 持久历史窗口会按 `node_changes_window` 自动推导，并通过 `status`、`registration`、WS `hello` 暴露为 `changes_history_window`
- `node_batch_max_items` 会影响 `/api/batch` 以及 WS 批量请求的上限
- `node_idempotency_ttl_seconds` 会影响节点控制面写请求的幂等重放窗口，并出现在 `status` / WS `hello` 的幂等运行态里
- `node_traffic_report_interval_seconds` 和 `node_traffic_report_step_bytes` 都是可选开关；任一项大于 `0` 时，节点会在 `/api/traffic` 写入后，针对“配置了流量限制的客户端”按阈值发出 `client.traffic.reported`
- `client.traffic.reported` 不额外引入新接口，直接复用实时 WS `event` 和实时 callback；它不会进入 `/changes` 持久化补偿窗口，也不会进入 callback 失败队列
- 节点会在当前配置目录下自动维护本地协议状态文件，用于保存 `/changes` 事件窗口、幂等缓存和 callback 失败队列；通常无需手工修改
- 全量业务配置备份导出使用 `GET /api/system/export`；全量业务配置恢复使用 `POST /api/system/import`，仅管理员可用
- 配置导入成功后节点会切换到新的 `config_epoch`，旧 `changes` cursor、旧幂等缓存和旧会话状态全部失效

## 2. 相关页面

- 需要先在服务端打开节点模式：看 [启用节点模式](/guide/server/node-management)
- 需要接入顺序和实际用法：看 [平台接入总览](/reference/integration/platform-onboarding)
- 需要正式接口定义：看 [管理接口说明](/reference/management-api)
- 需要基础运行项和密钥：看 [基础项与密钥](/reference/server-config-basics)

