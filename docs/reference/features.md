# 功能清单与扩展能力

本页是能力总览，适合已经知道自己要找什么方向，但不希望在单页中查找所有内容的用户。

这里只保留分组入口和边界说明。具体能力细节按主题拆分，后续维护时也只需要更新对应主题页。

如果你要看按场景的操作步骤，优先看：

- [隧道与转发类型](/guide/tunnels/README)
- [HTTPS 与反向代理](/guide/server/https-and-proxy)
- [维护与更新](/guide/client/maintenance)
- [NPC 命令行参数](/reference/npc-cli)
- [快速启动与远程源](/guide/client/launch)

如果你还是第一次接触 NPS，建议先看：

- [架构与核心概念](/getting-started/architecture)
- [规则选型总览](/guide/design/tunnel-selection)
- [场景总览与选型](/guide/scenarios/common)

## 先按主题找

| 主题 | 适合找什么 | 建议页面 |
| --- | --- | --- |
| 传输与连接 | 压缩、加密、KCP、多路复用、断线判定 | [传输与连接](/reference/features-transport) |
| 站点与 HTTP | 证书、CORS、TLS、Header、URL 路由、404 | [站点与 HTTP](/reference/features-http) |
| 代理、转发与路由 | 嵌套转发、Proxy Protocol、端口映射、端口复用 | [代理、转发与路由](/reference/features-routing) |
| 访问控制与限制 | ACL、IP 限制、流量、带宽、连接数、隧道数 | [访问控制与限制](/reference/features-access) |
| 运维与调试 | 环境变量、健康检查、日志、pprof | [运维与调试](/reference/features-ops) |

## 最常见的几类问题

| 你想确认什么 | 建议页面 |
| --- | --- |
| 站点能不能自动申请证书、改 Header、做 URL 路由 | [站点与 HTTP](/reference/features-http) |
| 混合代理能不能限制访问哪些目标 | [访问控制与限制](/reference/features-access) |
| 能不能把多个端口或多个入口复用起来 | [代理、转发与路由](/reference/features-routing) |
| 客户端与服务端之间能不能压缩、加密或改断线判定 | [传输与连接](/reference/features-transport) |
| 如何做健康检查、日志和调试 | [运维与调试](/reference/features-ops) |

## 相关页面

- 需要精确配置项：看 [服务端配置文件](/reference/server-config)
- 需要接口字段：看 [管理接入入口](/reference/integration/management-api-entrypoints) 或 [管理接口说明](/reference/management-api)

