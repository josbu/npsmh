# 接口与集成

这一部分收纳“对外接口、平台接入、程序集成和启动协议”相关内容。

这里和 [参考资料](/reference/README) 的区别是：

- 参考资料：更偏向配置字段、能力行为和排查
- 接口与集成：更偏向 API、协议、SDK 和外部系统接入

## 先按任务找

| 你要做什么 | 建议页面 |
| --- | --- |
| 开发新的本地管理前端、控制台或工具 | [管理接入入口](/reference/integration/management-api-entrypoints) |
| 对接当前管理前端、控制台或单节点自动化 | [管理接入入口](/reference/integration/management-api-entrypoints) |
| 对接新的平台控制面或多节点管理 | [平台接入总览](/reference/integration/platform-onboarding) |
| 评估客户端程序内集成方式 | [NPC SDK 状态](/reference/npc-sdk) |
| 使用 `-launch`、`npc://`、远程源或多实例规范 | [NPC Launch 规范](/reference/npc-launch) |

## 选择建议

- 当前脚本、控制台或管理前端：优先使用 [管理接入入口](/reference/integration/management-api-entrypoints)
- 新的管理前端、控制台或工具：先看 [管理接入入口](/reference/integration/management-api-entrypoints)，再进入 [管理接口说明](/reference/management-api)
- 新的平台或统一控制面：先看 [平台接入总览](/reference/integration/platform-onboarding)，再进入 [管理接口说明](/reference/management-api)
- 需要标准化启动描述：使用 [NPC Launch 规范](/reference/npc-launch)
- 需要评估内嵌客户端：查看 [NPC SDK 状态](/reference/npc-sdk)

