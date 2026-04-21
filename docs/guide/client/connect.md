# 客户端连接与配置

这一页只聚焦 NPC 客户端如何连接 NPS，以及何时选择命令行直连、系统服务或配置文件模式。

如果你是第一次启动，建议先只看第 1 节和第 2 节，暂时不要先使用 `npc.conf` 或 `-launch`。

示例里的 `./npc` 表示“从发布包目录直接执行”。如果你已经把二进制安装到了系统路径，可以直接改成 `npc`。

如果你还不清楚“连接协议”和“隧道类型”的区别，再补看：

- [架构与核心概念](/getting-started/architecture)
- [规则选型总览](/guide/design/tunnel-selection)

## 先选入口

| 你现在要做什么 | 建议方式 |
| --- | --- |
| 第一次确认 NPC 能不能连上服务端 | 第 1 节：直接命令行连接 |
| 命令行验证已完成，想长期运行和开机自启 | 第 2 节：注册为系统服务 |
| 想把多个隧道、本地模式或文件隧道写进配置文件 | 第 3 节：配置文件模式（进阶） |
| 想用远程源、JSON 或多实例统一启动 | 第 4 节：`-launch`（进阶） |

## 1. 第一次连接：直接命令行

第一次连接时，建议不要手工拼参数，而是：

1. 登录 Web 管理界面
2. 进入“客户端列表”
3. 找到对应客户端并展开详情
4. 直接复制页面里的 TCP、KCP、TLS、QUIC、WS 或 WSS 快捷启动命令

客户端列表会按当前服务端开放的桥接方式生成启动命令。第一次连接时，建议直接复制该命令，以减少 `server`、`vkey`、`type` 的误配。

如果你在 Windows 上操作，把示例里的 `./npc` 换成 `npc.exe` 即可。

普通 TCP 连接：

```bash
./npc -server=<server-ip>:8024 -vkey=<client-vkey> -type=tcp
```

TLS 连接：

```bash
./npc -server=<server-ip>:8025 -vkey=<client-vkey> -type=tls
```

连接多个服务端：

```bash
./npc -server=<server-a>:8024,<server-b>:8025 -vkey=<vkey-a>,<vkey-b> -type=tcp,tls
```

说明：

- 默认普通桥接端口通常是 `8024`
- 默认 TLS / QUIC 桥接端口通常是 `8025`
- `-type` 虽然默认是 `tcp`，第一次仍建议显式写出
- 连接协议描述的是 NPC 如何连接 NPS，不是最终要暴露的业务类型
- 第一次连接时，不建议直接执行不带参数的 `npc`

源码里的实际行为是：当 `npc` 没有拿到 `-server`、`-vkey`、`-launch` 且没有进入本地模式时，会回退尝试读取默认 `conf/npc.conf`。第一次连接时，建议优先使用显式命令行参数。

## 2. 连通后再注册为系统服务

适合：

- 机器重启后自动运行
- 需要长期稳定保持连接
- 不希望人工手动启动

先用前台命令确认连通，再注册服务。

### Linux 或 macOS

安装服务：

```bash
sudo npc install -server=<server-ip>:8024 -vkey=<client-vkey> -type=tcp -log=off
```

启动、停止和卸载：

```bash
sudo npc start
sudo npc stop
sudo npc uninstall
```

### Windows

安装服务：

```powershell
npc.exe install -server="<server-ip>:8024" -vkey="<client-vkey>" -type="tcp" -log="off"
```

启动、停止和卸载：

```powershell
npc.exe start
npc.exe stop
npc.exe uninstall
```

如果要变更连接参数，先卸载再重新安装。

默认日志位置：

| 系统 | 默认位置 |
| --- | --- |
| Windows | 当前运行的 `npc.exe` 所在目录下的 `npc.log` |
| Linux / macOS | `/var/log/npc.log` |

更详细的日志参数说明见 [NPC 命令行参数](/reference/npc-cli)。

## 3. 配置文件模式

这是进阶用法。只有在“命令行直连已经确认正常”之后，再考虑切到 `npc.conf`。

适合：

- 不想把所有规则都放在 Web 管理界面
- 希望一份配置文件管理多个隧道或本地访问模式
- 需要使用文件隧道

显式指定配置文件启动：

```bash
./npc -config=/path/to/npc.conf
```

同时加载多份配置：

```bash
./npc -config=/path/to/npc1.conf,/path/to/npc2.conf
```

建议始终显式写 `-config=...`，不要依赖默认回退路径。默认行为是：

- Windows：`npc.exe` 同目录下的 `conf\\npc.conf`
- Linux / macOS：当前工作目录下的 `conf/npc.conf`

示例配置文件：

- [conf/npc.conf](https://github.com/djylb/nps/tree/master/conf/npc.conf)

最小示例：

```ini
[common]
server_addr=1.1.1.1:8024
conn_type=tcp
vkey=YOUR_CLIENT_VKEY

[tcp-demo]
mode=tcp
server_port=10080
target_addr=127.0.0.1:8080
```

按源码里的解析规则，配置文件大致分成四类 section：

- `[common]`：客户端与服务端的连接参数，例如 `server_addr`、`vkey`、`conn_type`
- 带 `host=` 的 section：域名转发
- 带 `mode=` 的 section：普通隧道，例如 TCP、UDP、代理、文件隧道
- 以 `secret` 或 `p2p` 开头且未写 `mode` 的 section：访问端本地模式

各类隧道配置示例应该看这些页面：

| 需求 | 建议页面 |
| --- | --- |
| 域名转发 | [域名转发](/guide/tunnels/domain-forwarding) |
| TCP 隧道 | [TCP 隧道](/guide/tunnels/tcp) |
| UDP 隧道 | [UDP 隧道](/guide/tunnels/udp) |
| HTTP 代理、Socks5、混合代理 | [混合代理](/guide/tunnels/mixed-proxy) |
| 私密代理 | [私密代理](/guide/tunnels/secret) |
| P2P 隧道 | [P2P 隧道](/guide/tunnels/p2p) |
| 文件隧道 | [文件隧道](/guide/tunnels/file) |

文件隧道只能通过配置文件模式启动。

## 4. `-launch` 入口

如果你需要远程源、JSON、多实例或统一的启动描述入口，不要继续在这一页找，直接看 [快速启动与远程源](/guide/client/launch)。

## 下一步

- 需要客户端更新和状态检查：看 [维护与更新](/guide/client/maintenance)
- 需要更多命令行参数：看 [NPC 命令行参数](/reference/npc-cli)
- 需要 `-launch`、远程源和多实例：看 [快速启动与远程源](/guide/client/launch)
- 想按类型查看具体配置：看 [隧道与转发类型](/guide/tunnels/README)
- 想按业务结果选择方案：看 [场景总览与选型](/guide/scenarios/common)
