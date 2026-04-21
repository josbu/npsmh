# NPC 命令行参数

本页集中放 `npc` 的常用命令行参数、默认值和常见组合示例，适合“已经知道要查哪个参数，只需要准确答案”的场景。

如果你还没有完成第一次连通，先看 [客户端连接与配置](/guide/client/connect)，并优先直接复制 Web 管理界面“客户端列表”里提供的快捷启动命令。

如果你要找的是状态检查、更新、代理接入或兼容旧版环境，请先看 [维护与更新](/guide/client/maintenance)。

示例里的 `./npc` 表示“从发布包目录直接执行”。如果你已经把二进制安装到了系统路径，可以直接改成 `npc`。

## 先按任务找

| 你要做什么 | 建议先看 |
| --- | --- |
| 只想查某个命令行参数的作用 | 第 1 节 |
| 想看一条常见命令应该怎样组合 | 第 2 节 |
| 想继续看 `-launch`、远程源或多实例 | 第 3 节 |

## 1. 常用命令行参数

这些参数可以直接与启动命令组合使用，例如：

```bash
./npc -server=xxx:123,yyy:456 -vkey=xxx,yyy -type=tls,tcp -log=off -debug=false
```

| 参数 | 说明 | 默认值 |
| --- | --- | --- |
| `-server` | 指定 NPS 服务器地址（`addr:port[@host_or_sni[:port]][/ws_path_or_quic_alpn]`） | 无 |
| `-vkey` | 客户端认证密钥 | 无 |
| `-type` | 服务器连接方式（`tcp` / `tls` / `kcp` / `quic` / `ws` / `wss`） | `tcp` |
| `-config` | 显式指定配置文件路径，可逗号分隔多份配置 | 空；当未提供 `-server`、`-vkey`、`-launch` 且没有进入本地模式时，会回退尝试默认配置路径 |
| `-launch` | 快速启动参数，可重复传入；支持 `npc://`、远程 URL、base64、JSON | 无 |
| `-proxy` | 通过代理连接 NPS（支持 `socks5://`、`http://`、`https://`） | 无 |
| `-local_ip` | 指定客户端出站绑定的本地 IP（可逗号分隔，和 `-server` 一一对应） | 无 |
| `-local_ip_forward` | 是否让 `local_ip` 同时作用于隧道转发出口（仅对公网 IP 与域名生效，私网 IP 忽略） | `false` |
| `-debug` | 是否启用调试模式 | `true` |
| `-log` | 日志输出模式（`stdout` / `file` / `both` / `off`） | `file` |
| `-log_path` | NPC 日志路径（为空时按系统路径自动推导） | Linux 为 `/var/log/npc.log`，Windows 为 `npc.exe` 同目录下的 `npc.log` |
| `-log_level` | 日志级别（trace、debug、info、warn、error、fatal、panic、off） | `trace` |
| `-log_compress` | 是否启用日志压缩 | `false` |
| `-log_max_days` | 日志最大保留天数（0 关闭） | `7` |
| `-log_max_files` | 最大日志文件数（0 关闭） | `10` |
| `-log_max_size` | 单个日志文件最大大小（MB） | `5` |
| `-log_color` | 控制台输出启用 ANSI 彩色 | `true` |
| `-auto_reconnect` | 断线后自动重连 | `true` |
| `-disconnect_timeout` | 客户端与服务端控制连接的断线判定超时（秒） | `30` |
| `-keepalive` | 保活（KeepAlive）周期（秒） | `0`（不覆盖内置 KeepAlive 周期） |
| `-pprof` | 启用 PProf 调试（格式 `ip:port`） | 无 |
| `-local_type` | 本地访问模式，常见值为 `secret`、`p2p`、`p2ps`、`p2pt` | `p2p` |
| `-local_port` | 本地监听端口 | `2000` |
| `-password` | 本地访问模式使用的共享密钥 | 无 |
| `-target` | 本地访问模式的目标地址或目标标识 | 无 |
| `-target_type` | 本地访问模式监听的目标协议（`all` / `tcp` / `udp`） | `all` |
| `-p2p_timeout` | P2P 超时时间（秒） | `5` |
| `-p2p_type` | P2P 打洞通道类型（`quic` / `kcp`） | `quic` |
| `-disable_p2p` | 禁用 P2P 连接 | `false` |
| `-fallback_secret` | P2P 不可用时回退到私密代理中转 | `true` |
| `-dns_server` | 配置 DNS 服务器 | `8.8.8.8` |
| `-ntp_server` | 配置 NTP 服务器 | 无 |
| `-ntp_interval` | NTP 最小查询间隔（分钟） | `5` |
| `-timezone` | 配置时区（Asia/Shanghai） | 无 |
| `-time` | 客户端注册时间（小时） | `2` |
| `-gen2fa` | 生成 TOTP 双因素认证密钥 | `false` |
| `-get2fa` | 根据提供的密钥输出一次性 TOTP 验证码 | 无 |
| `-version` | 显示当前版本 | 无 |

默认配置路径补充：

- Linux / macOS：当前工作目录下的 `conf/npc.conf`
- Windows：`npc.exe` 同目录下的 `conf\\npc.conf`

使用建议：

- 第一次连通时，显式写 `-server`、`-vkey`、`-type`
- 需要配置文件时，显式写 `-config=...`，不要依赖默认回退
- 需要远程源、多实例或统一启动描述时，再转去看 `-launch`

## 2. 常见参数组合

最常见的几类组合如下：

- 基础直连：

```bash
./npc -server=127.0.0.1:8024 -vkey=YOUR_CLIENT_VKEY -type=tcp
```

- 关闭文件日志并以前台运行：

```bash
./npc -server=127.0.0.1:8024 -vkey=YOUR_CLIENT_VKEY -type=tcp -log=stdout -debug=false
```

- 高级：使用配置文件：

```bash
./npc -config=/path/to/npc.conf -log=off
```

- 通过代理接入：

```bash
./npc -server=127.0.0.1:8024 -vkey=YOUR_CLIENT_VKEY -proxy=socks5://127.0.0.1:1080
```

## 3. 下一步

- 需要状态检查、更新和代理接入：看 [维护与更新](/guide/client/maintenance)
- 需要 `-launch`、远程源和多实例：看 [快速启动与远程源](/guide/client/launch)
- 需要按隧道类型理解本地访问模式：看 [私密代理](/guide/tunnels/secret) 和 [P2P 隧道](/guide/tunnels/p2p)
