# 场景总览与选型

如果你已经知道自己想暴露什么服务，但还不确定应该创建哪种隧道，先看这一页。

如果你还没有完成第一次部署和连接验证，建议先看 [10 分钟快速开始](/getting-started/quick-start)。
如果你只是术语还不熟，建议再看 [规则选型总览](/guide/design/tunnel-selection)。

## 统一前置条件

在创建任何隧道前，先完成这些准备：

1. 启动 NPS 服务端。
2. 确认公网服务器可用，并记住管理端口和桥接端口。
3. 在 Web 管理界面创建客户端，记录 `vkey`。
4. 在内网机器启动 NPC，让客户端先连上服务端。
5. 确认内网目标服务本身可以从 NPC 所在机器访问。

第一次验证时，建议先使用 TCP 隧道，而不是先使用域名、HTTPS 或 P2P。先完成链路验证，再切换到正式类型。

下面用一个统一示例说明：

- 公网服务器 IP 是 `1.1.1.1`
- `bridge_tcp_port=8024`
- `bridge_tls_port=8025`
- `web_port=8081`

访问 Web 管理界面：

```text
http://1.1.1.1:8081
```

客户端最小启动命令：

```bash
./npc -server=1.1.1.1:8024 -vkey=YOUR_CLIENT_VKEY
```

如果使用 TLS 连接服务端：

```bash
./npc -server=1.1.1.1:8025 -vkey=YOUR_CLIENT_VKEY -type=tls
```

开始加隧道前，请先确认 NPC 所在机器能够访问目标地址，例如：

- `127.0.0.1:80`
- `10.1.50.101:22`
- `10.1.50.102:53`

如果这里本来就不通，创建隧道后也不会通。

## 按目标选择

| 我想得到什么结果 | 建议类型 | 继续阅读 |
| --- | --- | --- |
| 用域名和 HTTPS 发布网站、Webhook、管理后台 | 域名转发 | [域名转发](/guide/tunnels/domain-forwarding) |
| 暴露 SSH、RDP、数据库或其他 TCP 服务 | TCP 隧道 | [TCP 隧道](/guide/tunnels/tcp) |
| 暴露 DNS、游戏或其他 UDP 服务 | UDP 隧道 | [UDP 隧道](/guide/tunnels/udp) |
| 让浏览器、开发工具或系统代理访问整个内网 | HTTP 代理、Socks5 或混合代理 | [混合代理](/guide/tunnels/mixed-proxy) |
| 不想直接公开公网业务端口 | 私密代理 | [私密代理](/guide/tunnels/secret) |
| 希望尽量点对点直连，减少服务器中转带宽 | P2P 隧道 | [P2P 隧道](/guide/tunnels/p2p) |
| 暴露目录或下载文件 | 文件隧道 | [文件隧道](/guide/tunnels/file) |

## 常见结果入口

- 发布一个内网站点到公网：看 [域名转发](/guide/tunnels/domain-forwarding)
- 通过 `ssh -p <public-port> root@<server-ip>` 访问内网主机：看 [TCP 隧道](/guide/tunnels/tcp)
- 把外部设备的 DNS 指向公网服务器：看 [UDP 隧道](/guide/tunnels/udp)
- 把公网服务器当成一个代理入口访问整个内网：看 [混合代理](/guide/tunnels/mixed-proxy)
- 让访问者只连本地 `127.0.0.1:2000`，而不是公网业务端口：看 [私密代理](/guide/tunnels/secret)
- 希望优先直连，失败时再考虑回退：看 [P2P 隧道](/guide/tunnels/p2p)
- 需要公开下载目录或临时共享文件：看 [文件隧道](/guide/tunnels/file)

## 类型之外还要看什么

- 需要 HTTPS、证书、真实 IP、前置反向代理：看 [HTTPS 与反向代理](/guide/server/https-and-proxy)
- 需要限制谁可以访问入口：看 [访问控制与限制](/reference/features-access) 和 [访问控制与运行](/reference/server-config-runtime)
- 需要限制代理模式可以访问哪些目标：看 [混合代理](/guide/tunnels/mixed-proxy)
- 需要查询精确配置项：看 [服务端配置文件](/reference/server-config)

## 下一步

- 还没决定用哪种类型：回到 [规则选型总览](/guide/design/tunnel-selection)
- 已经知道类型：进入 [隧道与转发类型](/guide/tunnels/README)
- 已经知道字段名称，只想查接口或配置：进入 [参考资料](/reference/README)
