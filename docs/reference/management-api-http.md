# 管理接口：HTTP 接口目录

这一组页面把正式 `/api/` HTTP 接口按任务拆开，避免把路径表、使用示例和平台接入边界混在同一页里。

如果你还没确认认证方式、actor 作用域和推荐同步顺序，先看 [管理接口总览](/reference/management-api)。

## 先按任务找

| 你要做什么 | 建议页面 |
| --- | --- |
| 查总览、状态、增量变化、配置快照 | [发现与快照接口](/reference/management-api-http-discovery) |
| 查用户、客户端、隧道、域名和全局资源 CRUD | [资源接口](/reference/management-api-http-resources) |
| 查批量控制、流量写入、同步、导入和 curl 示例 | [控制接口与示例](/reference/management-api-http-control) |

## 先看几个边界

- `GET /api/ws` 不在这里，单独见 [实时通道与回调](/reference/management-api-realtime)
- `GET /api/system/overview?config=1` 适合 `full` 管理视角做首次全量快照
- 写操作建议带 `Idempotency-Key` 和 `X-Operation-ID`
- `GET` 请求当前不支持 URL 查询参数里的 `token`

## 推荐阅读顺序

1. [发现与快照接口](/reference/management-api-http-discovery)
2. [资源接口](/reference/management-api-http-resources)
3. [控制接口与示例](/reference/management-api-http-control)

