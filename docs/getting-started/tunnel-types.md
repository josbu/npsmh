# 首次选择隧道类型

这一页只服务第一次部署阶段，帮助你先做出一个足够正确的选择。

如果你已经在做正式方案设计，请直接进入 [规则选型总览](/guide/design/tunnel-selection)。

## 第一次使用时的建议

- 只是想先验证链路是否可用：优先用 TCP 隧道
- 先把“服务端可启动、客户端在线、外部可访问”跑通，再继续做 HTTPS、代理或 P2P
- 文件访问、`npc.conf` 和更复杂的本地访问模式，不适合作为第一次验证入口

## 先用这张简表判断

| 你的目标 | 第一次建议 | 之后去哪里看完整说明 |
| --- | --- | --- |
| 先暴露 SSH、RDP、数据库或任意 TCP 服务 | TCP 隧道 | [TCP 隧道](/guide/tunnels/tcp) |
| 先暴露 DNS、游戏或其他 UDP 服务 | UDP 隧道 | [UDP 隧道](/guide/tunnels/udp) |
| 网站、Webhook、管理后台 | 域名转发 | [域名转发](/guide/tunnels/domain-forwarding) |
| 不想直接公开业务端口 | 私密代理 | [私密代理](/guide/tunnels/secret) |
| 更关心低延迟和直连 | P2P | [P2P 隧道](/guide/tunnels/p2p) |
| 通过代理访问整个内网 | 混合代理 | [混合代理](/guide/tunnels/mixed-proxy) |

## 两组最常见判断

### 网站还是端口

- 服务本身是网站：优先用域名转发
- 服务只是一个 TCP 端口：优先用 TCP 隧道

### 稳定中转还是尽量直连

- 更看重稳定可用：优先用私密代理
- 更看重低延迟和带宽：再考虑 P2P

## 继续阅读

- 需要完整选型表和易混淆场景：看 [规则选型总览](/guide/design/tunnel-selection)
- 已经知道规则类型：看 [隧道与转发类型](/guide/tunnels/README)
- 还没完成第一次部署：回到 [10 分钟快速开始](/getting-started/quick-start)
