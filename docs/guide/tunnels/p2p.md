# P2P 隧道

P2P 隧道适合大流量、低延迟和点对点访问场景。

它的目标是尽量让访问端与目标端直接通信，从而减少中转带宽消耗。

## 适合什么场景

- 游戏联机
- 大文件传输
- 低延迟点对点访问
- 希望减少服务器中转流量

## 工作方式

和私密代理类似，P2P 也需要访问端再启动一个 NPC。

区别在于：

- P2P 会先尝试打洞直连
- 成功时流量尽量不经过 NPS
- 失败时只有在 `fallback_secret=true` 时才会回退到私密代理链路

需要先明确的一点是：P2P 不是“创建好服务端规则就自动直连”。
访问端仍然要再启动一个 NPC，本地监听一个端口，然后通过这个本地端口去访问远端服务。

## 使用前准备

1. NPS 必须有公网 IP
2. 在 `nps.conf` 中启用 `p2p_port`
3. 防火墙放行 `p2p_port` 到 `p2p_port + 2` 的 UDP 端口

示例：

```ini
p2p_ip=0.0.0.0
p2p_port=6000
```

如果 `p2p_port=6000`，通常需要放行：

- `6000/udp`
- `6001/udp`
- `6002/udp`

## 最小示例

假设：

- 目标内网服务是 `10.2.50.2:22`
- 唯一密钥是 `p2pssh`

在 Web 管理界面创建 P2P 代理后，在访问端执行：

```bash
./npc -server=1.1.1.1:8024 -vkey=123 -type=tcp -password=p2pssh -target=10.2.50.2:22 -target_type=tcp
```

默认会监听本地 `2000` 端口。然后可以这样访问：

```bash
ssh -p 2000 root@127.0.0.1
```

## `npc.conf` 示例

提供服务的一侧：

```ini
[ssh_p2p]
mode=p2p
password=ssh3
```

访问端：

```ini
[p2p_ssh]
local_port=2002
password=ssh3
target_addr=10.2.50.2:22
target_type=tcp
fallback_secret=true
```

访问端配置里，section 名以 `p2p` 开头且未写 `mode` 时，NPC 会按本地 P2P 模式解析。

## 访问端命令的几种形式

Web 管理界面会根据用途提供不同的访问端命令：

- TCP 或 UDP 访问：直接指定 `-target`，或使用默认 `local_type=p2p`
- HTTP 或 Socks5 代理访问：使用 `-local_type=p2ps`
- 透明代理访问：使用 `-local_type=p2pt`

如果你只是暴露 SSH、数据库或普通端口，通常先使用这一页的最小命令即可。

这里的 `-target` 或 `target_addr` 应填写“你最终想访问的真实服务地址”，例如 `10.2.50.2:22`，而不是 NPS 的公网地址。

补充说明：

- `target_type` 可写 `tcp`、`udp` 或 `all`
- `target_type` 留空或写成其他值时，当前实现会按 `all` 处理
- `p2ps` 和 `p2pt` 这两类本地模式通常不再手写 `-target`
- 当前主线 Web 页面给“直接访问目标地址”的命令通常只展示 `-target`
- `fallback_secret=true` 只表示打洞失败时回退到 secret 中转；是否还能继续保持“动态代理/透明代理”语义，取决于服务端对应隧道是否把固定 `target` 留空
- 如果页面命令和手写示例不同，优先复制页面命令

## 成功率与 NAT

P2P 是否成功和网络环境关系很大。

需要特别注意：

- 双方都是 Symmetric NAT 时，通常无法成功直连
- NPC 与 NPS 不应处于同一内网环境下作为标准公网 P2P 测试
- 不同运营商、不同路由器和不同 NAT 类型都会影响结果
- 如果你已经明确不打算尝试直连，可以关闭 P2P 尝试，直接使用私密代理路径

## 安全提醒

- 不要公开 P2P 连接命令
- 公开后可能暴露 NPC 所在内网环境的信息
- `p2ps` / `p2pt` 这类代理访问如果要在失败时回退到 secret 并继续保持动态目标语义，服务端对应隧道不应配置固定 `target`
- 如果你更看重稳定可用，而不是尽量直连，优先考虑 [私密代理](/guide/tunnels/secret)

## 进一步配置

客户端还支持：

- `-p2p_type`
- `-p2p_timeout`
- `-fallback_secret`
- `-disable_p2p`

这些高级参数见 [NPC 命令行参数](/reference/npc-cli)。
