# 平台接入总览

这一页给外部平台、统一控制面和多节点管理程序一个最短路径：如何把 NPS 节点接入成可持续同步的控制面。

这一页聚焦接入顺序、模式选择和运行建议，不展开所有接口字段或配置字段。精确定义请看 [管理接口说明](/reference/management-api) 和 [节点与平台对接](/reference/server-config-node)。

## 什么时候看这一页

- 你准备把 NPS 节点接入自己的管理平台
- 你需要一个统一控制面来管理多台节点
- 你要决定使用直连、反向 WS 还是双向模式
- 你要理解正式 `/api/` 管理接口、`changes`、callback 和配置导入导出的关系

## 先明确几个原则

- 节点本地数据始终是真源，平台写入后也以节点返回结果为准
- 正式对外的节点管理协议统一使用 `/api/` 前缀
- `run_mode=node` 只表示开启节点控制面能力，不等于把配置真源迁到平台
- 平台接入时，优先组合使用快照、增量同步和实时通道，而不是频繁全量拉取

## 三种推荐连接模式

### 1. `direct`

适合平台可以直接访问节点 Web 管理地址的场景。

- 平台主动调用节点公开的正式管理接口
- 配置相对简单
- 适合内网互通、同机房或已有安全隧道的环境

### 2. `reverse`

适合平台无法主动连入节点，但节点可以主动出网的场景。

- 节点主动建立 reverse WebSocket
- 平台通过反向通道下发请求
- 常与 callback 一起使用，减少轮询
- 使用 `reverse` 的平台 token 不能再直接访问节点 HTTP 管理接口

### 3. `dual`

适合既要保留平台主动探活，又要保证复杂网络环境可达性的场景。

- 同时启用直连与反向通道
- 平台可按实际网络状况选择通路
- 更适合生产环境逐步迁移

## 最小配置示例

```ini
run_mode=node

platform_ids=main
platform_tokens=replace-with-long-random-token
platform_scopes=full
platform_enabled=true
platform_urls=https://control.example.com

platform_connect_modes=dual
platform_reverse_enabled=true
platform_reverse_ws_urls=wss://control.example.com/node/ws
platform_reverse_heartbeat_seconds=30

platform_callback_enabled=true
platform_callback_urls=https://control.example.com/node/callback
platform_callback_timeout_seconds=5
platform_callback_retry_max=2
platform_callback_retry_backoff_seconds=2
platform_callback_queue_max=100
platform_callback_signing_keys=replace-with-hmac-key

node_changes_window=1024
node_idempotency_ttl_seconds=300
node_batch_max_items=50
```

如果你是从旧单平台配置迁移，`master_url` 和 `node_token` 仍会被自动迁移为第一条平台配置，但新部署应直接使用上面的多平台配置组。

## 推荐接入顺序

1. `GET /api/system/overview`
2. 如需完整管理快照，再拉 `GET /api/system/export`
3. 建立后续同步链路：
   - `GET /api/system/changes`
   - `GET /api/system/usage-snapshot`
   - `GET /api/ws`
   - callback

以下情况建议立即重新做一次全量同步：

- `boot_id` 变化
- `config_epoch` 变化
- `/api/system/changes` 返回 `gap=true`

## 实时与补偿怎么配合

- `changes` 负责可补偿的增量事件窗口
- `changes` 既可读当前补偿窗口，也可用 `durable=1` 或 `history=1` 读取持久历史窗口
- WS 负责低延迟实时请求和事件
- callback 负责把重要事件主动推送到平台
- callback 投递失败时，可通过本地失败队列查看、重放或清空
- 幂等缓存用于避免写请求在重试链路中被重复执行

一个稳定的生产接入通常是：

1. 首次全量同步
2. 持续消费 `changes`
3. 同时启用 WS 或 callback
4. 遇到窗口断裂再回退到全量同步

## 运行建议

- 每个平台使用独立 token 和独立签名 key
- 优先给每个平台配置专用 `platform_service` 用户
- 对外平台只开放必要作用域，默认不要给 `full`
- 保留 callback 失败队列，避免平台短时故障时直接丢事件
- 把 `node_changes_window`、callback 队列上限和幂等 TTL 作为容量参数一起评估

## 继续阅读

- 管理接口入口：看 [管理接口说明](/reference/management-api)
- HTTP 路径与示例：看 [HTTP 接口目录](/reference/management-api-http)
- WS、reverse 与 callback：看 [实时通道与回调](/reference/management-api-realtime)
- 节点配置项：看 [节点与平台对接](/reference/server-config-node)
- 如果你现在还没把节点模式打开：看 [启用节点模式](/guide/server/node-management)

