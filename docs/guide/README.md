# 操作指南

这一部分面向“已经完成安装和首次连接验证”的用户。

这里主要回答两个问题：

1. 服务端或客户端接下来应该怎样操作？
2. 某个常见任务应该先看哪一页？

这一部分不负责列出所有精确配置字段、协议细节或接口字段。需要精确值时，请切换到 [参考资料](/reference/README) 或 [接口与集成](/reference/integration/README)。

## 适合在这里查什么

| 你要做什么 | 建议先看 |
| --- | --- |
| 管理服务端运行、日志、重载和更新 | [服务端操作](/guide/server/README) |
| 管理客户端连接、服务、自启动和维护 | [客户端操作](/guide/client/README) |
| 自定义系统服务或运行多实例 | [服务与多实例](/guide/service-instances) |
| 在服务端启用节点模式 | [启用节点模式](/guide/server/node-management) |
| 选择规则类型或按类型查看说明 | [选型与规则](/guide/design/README) |

## 常见任务

| 任务 | 建议页面 |
| --- | --- |
| 检查目录、登录面板和日志 | [目录、面板与日志](/guide/server/operations-basics) |
| 重载、停止、重启和更新服务端 | [重载、重启与更新](/guide/server/operations-lifecycle) |
| 配置 HTTPS、证书、真实 IP 或前置代理 | [HTTPS 与反向代理](/guide/server/https-and-proxy) |
| 使用网页提供的命令启动 NPC | [客户端连接与配置](/guide/client/connect) |
| 管理客户端服务、自启动、状态和更新 | [维护与更新](/guide/client/maintenance) |
| 管理多实例或手写 systemd / `sc` 服务 | [服务与多实例](/guide/service-instances) |
| 查询客户端常用参数 | [NPC 命令行参数](/reference/npc-cli) |
| 使用 `-launch`、远程源或多实例 | [快速启动与远程源](/guide/client/launch) |

## 分类边界

- 需要“第一次部署和第一次验证”的最短路径：回到 [开始使用](/getting-started/README)
- 需要“按目标选择规则类型”：进入 [选型与规则](/guide/design/README)
- 需要“精确配置字段、行为边界和限制说明”：进入 [参考资料](/reference/README)
- 需要“API、SDK、启动协议或平台接入”：进入 [接口与集成](/reference/integration/README)
