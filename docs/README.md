# NPS 文档

NPS 是一款轻量级的内网穿透代理服务器，支持 TCP、UDP、HTTP、HTTPS、SOCKS5、P2P 以及多种客户端连接协议。

这套文档按“首次使用、日常操作、规则选型、精确参考、接口集成”五类任务组织。这样做有两个目标：

- 让第一次使用的用户按固定顺序完成部署和首次验证
- 让已经在使用的用户快速定位到精确页面，方便后续维护和新增功能

## 30 秒了解 NPS

如果你的服务位于内网，外部不能直接访问，NPS 可以帮助你把它接到公网入口。

一个最小部署通常包括三部分：

- 一台公网服务器运行 `NPS`
- 一台内网机器运行 `NPC`
- 一条规则，决定外部如何访问内网服务

最常见的用途包括：

| 目标 | 常用方式 |
| --- | --- |
| 发布网站、Webhook 或管理后台 | 域名转发 |
| 暴露 SSH、RDP、数据库 | TCP 隧道 |
| 暴露 DNS、游戏或其他 UDP 服务 | UDP 隧道 |
| 通过代理访问整个内网 | HTTP、Socks5 或混合代理 |
| 不直接公开业务端口 | 私密代理或 P2P |

第一次让 NPC 连上服务端时，建议优先在 Web 管理界面的“客户端列表”复制快捷启动命令，再根据需要进入后续的服务、自启动或配置文件流程。

## 文档结构

### 开始使用

面向第一次部署，只放最短路径需要的内容：

- 安装
- 启动服务端和客户端
- 首次验证
- 基本概念和规则总览

入口： [开始使用](/getting-started/README)

### 操作指南

面向已经完成首次验证的用户，只放任务型操作说明：

- 服务端运维
- 客户端连接、维护和高级启动
- HTTPS 与反向代理

入口： [操作指南](/guide/README)

### 选型与规则

面向“要选什么规则”和“选定后怎么看具体说明”的场景：

- 转发方式总览
- 按业务目标选型
- 按规则类型查看说明

入口： [选型与规则](/guide/design/README)

### 参考资料

面向“已经知道自己要查什么，只需要准确答案”的场景：

- 精确配置字段
- 功能行为和限制说明
- FAQ 和补充说明

入口： [参考资料](/reference/README)

### 接口与集成

面向脚本、平台、SDK 和协议使用者：

- 管理接口
- NPC Launch 规范
- SDK 状态

入口： [接口与集成](/reference/integration/README)

## 先选择阅读路径

### 第一次部署

1. 阅读 [10 分钟快速开始](/getting-started/quick-start)
2. 按环境补看 [安装指南](/getting-started/install)
3. 先阅读 [启动 NPS 服务端](/getting-started/start-server)
4. 再阅读 [启动 NPC 客户端](/getting-started/start-client)
5. 需要理解规则类型时，阅读 [首次选择隧道类型](/getting-started/tunnel-types)

### 已经完成首次验证

- 需要服务端操作：看 [服务端操作](/guide/server/README)
- 需要客户端操作：看 [客户端操作](/guide/client/README)
- 需要按目标选择规则：看 [选型与规则](/guide/design/README)

### 已经知道要查什么

- 需要精确配置或行为说明：看 [参考资料](/reference/README)
- 需要 API、协议或平台接入：看 [接口与集成](/reference/integration/README)

## 任务速查

| 我想做什么 | 建议先看 |
| --- | --- |
| 完成第一次部署和首次验证 | [10 分钟快速开始](/getting-started/quick-start) |
| 选择安装方式 | [安装指南](/getting-started/install) |
| 启动 NPS 服务端 | [启动 NPS 服务端](/getting-started/start-server) |
| 启动 NPC 客户端 | [启动 NPC 客户端](/getting-started/start-client) |
| 选择规则类型 | [规则选型总览](/guide/design/tunnel-selection) |
| 按规则类型查看说明 | [隧道与转发类型](/guide/tunnels/README) |
| 管理服务端运行和更新 | [服务端操作](/guide/server/README) |
| 管理客户端连接和维护 | [客户端操作](/guide/client/README) |
| 查询服务端配置项 | [服务端配置文件](/reference/server-config) |
| 查询能力、限制和兼容边界 | [功能清单与扩展能力](/reference/features) |
| 查询问题排查入口 | [FAQ](/reference/faq) |
| 对接当前管理前端、控制台或脚本 | [管理接入入口](/reference/integration/management-api-entrypoints) |
| 对接新的平台控制面 | [管理接口说明](/reference/management-api) |
| 使用 `npc -launch`、远程源或多实例规范 | [NPC Launch 规范](/reference/npc-launch) |

## 继续阅读

- 需要基本概念：看 [架构与核心概念](/getting-started/architecture)
- 需要规则类型总览：看 [规则选型总览](/guide/design/tunnel-selection)
- 需要平台接口选择建议：看 [接口与集成](/reference/integration/README)

