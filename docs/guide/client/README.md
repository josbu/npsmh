# 客户端操作

这一部分聚焦 NPC 客户端，按“先建立连接，再长期运行，最后再使用高级启动能力”的顺序组织。

第一次启动时，建议先在 Web 管理界面的“客户端列表”复制快捷启动命令，先验证 `-server`、`-vkey`、`-type` 这一组核心参数。`npc.conf` 和 `-launch` 适合在首次连接已经确认可用之后再使用。

这一部分主要讲操作方法和任务路径。需要精确参数说明时，请查看 [NPC 命令行参数](/reference/npc-cli) 或 [NPC Launch 规范](/reference/npc-launch)。

## 先按任务找

| 你要做什么 | 建议先看 |
| --- | --- |
| 先让 NPC 连上服务端 | [客户端连接与配置](/guide/client/connect) |
| 查询常用命令行参数 | [NPC 命令行参数](/reference/npc-cli) |
| 修改配置后重启、检查状态、执行更新 | [维护与更新](/guide/client/maintenance) |
| 使用 `npc.conf`、文件隧道或本地访问模式 | [客户端连接与配置](/guide/client/connect) |
| 使用 `-launch`、远程源、JSON 或多实例 | [快速启动与远程源](/guide/client/launch) |
| 评估程序内集成方式 | [NPC SDK 状态](/reference/npc-sdk) |

## 分类边界

- 需要“第一次部署和第一次连接验证”：回到 [开始使用](/getting-started/README)
- 需要“精确参数或默认值”：看 [NPC 命令行参数](/reference/npc-cli)
- 需要“正式规范和兼容边界”：看 [NPC Launch 规范](/reference/npc-launch)
- 需要“接口与平台集成”：看 [接口与集成](/reference/integration/README)
