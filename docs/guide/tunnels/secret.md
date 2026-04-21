# 私密代理

私密代理适合不想直接暴露公网业务端口的内网服务。

最常见的是 SSH、RDP、数据库这类 TCP 服务，但当前实现并不只限于 TCP。

它的特点是：

- 公网侧不直接暴露目标业务端口
- 访问端需要再启动一个 NPC
- 流量仍经过 NPS 中转

## 适合什么场景

- SSH
- 安全要求较高的管理端口
- 希望外部访问者只知道一条连接命令，而不是一个公网业务端口

## 工作方式

私密代理需要两个角色：

- 提供服务的一侧：在 Web 管理界面创建私密代理，配置唯一密钥和内网目标
- 访问服务的一侧：启动一个 NPC，把本地端口映射到这个私密代理

需要先明确的一点是：访问端不需要再暴露一个公网端口。
访问端启动的 NPC 只会在自己本地监听，例如 `127.0.0.1:2000`。

## Web 管理界面最常用字段

| 字段 | 作用 |
| --- | --- |
| 唯一密钥 | 访问端连接时使用的密码 |
| 内网目标 | 最终要访问的内网服务地址 |
| 目标类型 | 本地访问端按 `tcp`、`udp` 或 `all` 监听与转发 |

这里的常规路径是：

- 服务提供侧在 Web 页面先写好内网目标
- 访问侧只拿密码和访问命令
- 访问侧默认不需要再手写 `-target`

## 最小示例

假设：

- 内网 SSH 服务是 `10.1.50.2:22`
- 私密代理唯一密钥是 `secrettest`

在 Web 管理界面创建私密代理后，在访问端执行：

```bash
./npc -server=1.1.1.1:8024 -vkey=vkey -type=tcp -password=secrettest -target_type=tcp -local_type=secret
```

默认会在本地监听 `2000` 端口。然后访问：

```bash
ssh -p 2000 root@127.0.0.1
```

如果需要指定本地端口：

```bash
./npc -server=1.1.1.1:8024 -vkey=vkey -type=tcp -password=secrettest -target_type=tcp -local_type=secret -local_port=2001
```

## `npc.conf` 示例

提供服务的一侧：

```ini
[ssh_secret]
mode=secret
password=ssh2
target_addr=10.1.50.2:22
```

访问端：

```ini
[secret_ssh]
local_port=2001
password=ssh2
target_type=tcp
```

访问端配置里，section 名以 `secret` 开头且未写 `mode` 时，NPC 会按本地私密代理解析。

## 注意事项

- 访问端命令里的 `password` 就是 Web 管理界面配置的唯一密钥
- `local_type=secret` 说的是访问端本地模式，不是服务端规则类型
- SSH、RDP、数据库这类常见场景一般把 `target_type` 设为 `tcp`
- 如果你要转发 UDP，或想同时接收 TCP 和 UDP，可以把 `target_type` 设为 `udp` 或 `all`
- `target_type` 留空或写成其他值时，当前实现会按 `all` 处理
- 当前主线 Web 页面生成的访问命令不会额外带 `-target`；常规使用时，服务端隧道里配置的目标地址就是最终目标
- 只要服务端隧道配置了 `target`，访问侧带入的目标地址都会被忽略；最终只会连向隧道自身配置的目标
- 只有服务端隧道把 `target` 留空时，访问侧带入的 `Link.Host` 才会作为最终目标，这时更适合做动态代理、透明代理或其它“按来流决定目标”的场景
- `target` 留空时，访问侧连接类型会按实际来流决定；配置了固定目标时，最终连接类型由隧道自身 `target_type` 决定，只有 `all` 才会按来流在 `tcp/udp` 之间切换
- `allow_secret_local` 只控制“目标留空时，访问侧是否允许把最终目标切到 NPS 服务器本地服务”；如果要连到 NPS 本地服务，除了 `allow_secret_local`，还需要服务端开启 `allow_local_proxy`
- Web 管理界面会显示可直接复制的访问端命令；如果页面命令和手写示例不同，优先以页面为准
- 如果你希望尽量走直连而不是服务器中转，请看 [P2P 隧道](/guide/tunnels/p2p)
