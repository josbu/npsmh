# 启用节点模式

这一页只回答服务端管理员的一个问题：如何把一台已经部署好的 NPS 打开成“可被外部平台接入”的节点。

平台如何消费正式 `/api/` 管理接口、怎样做快照与增量同步、怎样选择 `direct` / `reverse` / `dual`，统一放到 [平台接入总览](/reference/integration/platform-onboarding)。

## 什么时候看这一页

- 你准备让这台 NPS 被外部平台纳管
- 你要把单节点部署切换为 `run_mode=node`
- 你想先把节点侧配置和启动方式整理好，再交给平台侧联调

如果你只是单机使用 NPS，不需要先看这一页。

## 节点模式会带来什么

- 服务端会暴露正式节点控制面，所有公开管理接口统一使用 `/api/` 前缀
- 平台可以通过 token、session 或反向通道管理这台节点
- 配置真源仍然在节点本地，不会因为开启节点模式而自动迁到平台

## 启用步骤

1. 在 `nps.conf` 中设置 `run_mode=node`
2. 至少配置一组平台参数，例如平台 ID、token、URL 和连接模式
3. 重启 NPS
4. 再让平台侧按管理接口完成接入

当前实现里，`run_mode` 变更需要重启，不能只靠 `nps reload`。

## 最小配置示例

```ini
run_mode=node

platform_ids=main
platform_tokens=replace-with-long-random-token
platform_enabled=true
platform_urls=https://control.example.com
platform_connect_modes=direct
```

如果平台不能直接访问节点，再考虑：

- `platform_connect_modes=reverse`
- `platform_reverse_enabled=true`
- `platform_reverse_ws_urls=wss://control.example.com/node/ws`
- callback 相关参数

这些字段的精确定义见 [节点与平台对接](/reference/server-config-node)。

## 三种连接模式怎么选

- `direct`：平台能直接访问节点时，优先用这个
- `reverse`：平台不能主动连入节点，但节点可以主动出网时使用
- `dual`：既保留平台主动访问，又保留反向通道，适合复杂网络或渐进迁移

这里先完成“节点侧可用”即可；平台侧完整接入顺序放在 [平台接入总览](/reference/integration/platform-onboarding)。

## 本地验证

至少确认这些点：

- NPS 已按新配置重启成功
- 平台 token 已写入节点配置
- 节点 Web 基地址与平台计划访问的地址一致
- 管理接口可以返回基础概览，例如 `GET /api/system/overview`

如果接口路径前配置了 `web_base_url`，实际路径会带上该前缀。

## 继续阅读

- 平台侧接入顺序：看 [平台接入总览](/reference/integration/platform-onboarding)
- 管理接口入口：看 [管理接口说明](/reference/management-api)
- 节点配置项：看 [节点与平台对接](/reference/server-config-node)
- 服务端基础运维：看 [服务端运维](/guide/server/operations)

