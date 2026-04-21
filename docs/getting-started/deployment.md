# 启动服务端与客户端

本页只做启动总览，避免把服务端、客户端、系统服务和多实例全部堆在一页里。

如果你当前的目标只是完成第一次验证，通常只需要先完成两件事：

1. 先把 `NPS` 跑起来
2. 再把 `NPC` 连上服务端

确认这两步都正常后，再决定是否注册系统服务或使用多实例。

## 最短路径

第一次上线建议按这个顺序做：

1. [启动 NPS 服务端](/getting-started/start-server)
2. 登录 Web 管理界面，创建客户端
3. 在 Web 的“客户端列表”复制启动命令，然后看 [启动 NPC 客户端](/getting-started/start-client)
4. 确认客户端在线后，再创建第一条规则

## 先按任务找

| 你要做什么 | 建议页面 |
| --- | --- |
| 先把服务端跑起来并能登录 Web 管理界面 | [启动 NPS 服务端](/getting-started/start-server) |
| 先让客户端连上服务端 | [启动 NPC 客户端](/getting-started/start-client) |
| 需要系统服务、多实例或手动服务 | [服务与多实例](/guide/service-instances) |

## 最小验收清单

服务端和客户端都启动后，至少确认这些结果：

1. `http://<server-ip>:8081` 可以访问
2. Web 管理界面里客户端状态显示为在线
3. NPC 所在机器可以访问它要转发的内网目标
4. 你已经创建了一条域名转发或隧道规则

## 下一步

- 想先完成第一条规则验证：看 [10 分钟快速开始](/getting-started/quick-start)
- 需要服务端运维、重载和更新说明：看 [服务端运维](/guide/server/operations)
- 需要客户端详细连接方式；`npc.conf` 属于进阶用法：看 [客户端连接与配置](/guide/client/connect)
- 需要按类型查看规则怎么配：看 [隧道与转发类型](/guide/tunnels/README)
