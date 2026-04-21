# 服务端操作

这一部分聚焦 NPS 服务端本身，包括启动后的运行维护、HTTPS 处理、前置反向代理和节点模式启用。

如果你只是部署一台普通 NPS 服务端，通常不需要先看“启用节点模式”。单节点用户一般先看运维和 HTTPS 即可。

这一部分主要讲操作步骤和任务路径，不重复列出 `nps.conf` 的全部字段定义。需要精确字段时，请同时查看 [服务端配置文件](/reference/server-config)。

## 先看哪一页

| 你要做什么 | 建议先看 |
| --- | --- |
| 首次部署服务端 | [安装指南](/getting-started/install) 和 [启动 NPS 服务端](/getting-started/start-server) |
| 检查目录、登录面板和日志 | [目录、面板与日志](/guide/server/operations-basics) |
| 重载、停止、重启和更新 | [重载、重启与更新](/guide/server/operations-lifecycle) |
| 配置 HTTPS、证书、真实 IP 或前置代理 | [HTTPS 与反向代理](/guide/server/https-and-proxy) |
| 查询精确配置项 | [服务端配置文件](/reference/server-config) |
| 在服务端启用节点控制面 | [启用节点模式](/guide/server/node-management) |
| 对接外部管理平台或多节点控制面 | [平台接入总览](/reference/integration/platform-onboarding) |
| 查询新的节点管理接口 | [管理接口说明](/reference/management-api) |

## 这部分主要涉及什么

- Web 管理端：客户端、隧道、域名转发和用户管理
- Bridge 端口：NPC 与 NPS 的控制连接入口
- HTTP / HTTPS 入口：外部业务流量入口
- 节点模式：面向外部平台的统一控制面

## 推荐阅读顺序

单节点部署建议：

1. [服务端运维](/guide/server/operations)
2. [目录、面板与日志](/guide/server/operations-basics)
3. [重载、重启与更新](/guide/server/operations-lifecycle)
4. [HTTPS 与反向代理](/guide/server/https-and-proxy)
5. [服务端配置文件](/reference/server-config)

平台接入建议：

1. [启用节点模式](/guide/server/node-management)
2. [平台接入总览](/reference/integration/platform-onboarding)
3. [管理接口说明](/reference/management-api)
4. [服务端配置文件](/reference/server-config)

