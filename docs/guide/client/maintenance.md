# 客户端维护与更新

本页用于说明已经完成基础连通后的日常维护，包括：

- 查看状态
- 修改配置后重新生效
- 执行客户端更新
- 通过代理连接 NPS
- 兼容旧版环境

如果你只是第一次让 NPC 连上服务端，先看 [客户端连接与配置](/guide/client/connect)。

## 先按任务找

| 你要做什么 | 建议先看 |
| --- | --- |
| 查看一条直连命令当前是否能连上服务端 | 第 1 节 |
| 修改配置文件后让服务重新读取配置 | 第 2 节 |
| 更新客户端 | 第 3 节 |
| 通过已有 HTTP / Socks5 代理接入 NPS | 第 4 节 |
| 兼容旧客户端 | 第 5 节 |

## 1. 状态检查

`npc status` 更适合命令行直连或单一 `-launch` 目标，不适合直接读取 `npc.conf`。

最常见的用法是检查“这组连接参数当前能否连上服务端”：

```bash
./npc status -server=127.0.0.1:8024 -vkey=YOUR_CLIENT_VKEY -type=tcp
```

也可以配合单一 `-launch` 输入使用：

```bash
./npc status -launch="npc://demo-vkey@127.0.0.1:8024?type=tcp"
```

如果你使用的是 `npc.conf` 配置文件模式，更实用的检查方式通常是：

- 看 NPC 日志
- 看 Web 管理界面中的客户端在线状态
- 看系统服务状态

补充说明：

- `status` 不支持多 profile `-launch`
- `status` 主要用于检查连接参数是否可用，不是读取完整运行态明细

## 2. 修改配置后如何生效

需要先明确的一点是：NPC 没有单独的“热重载配置”命令。

如果你改了 `npc.conf` 或启动参数，需要让进程重新启动，新的配置才会生效。

### 2.1 已注册为系统服务

```bash
sudo npc restart
```

```powershell
npc.exe restart
```

前提是你安装服务时已经把正确的参数写进服务，例如：

```bash
sudo npc install -config=/path/to/npc.conf -log=off
```

### 2.2 以前台进程运行

直接结束旧进程，再用原命令重新启动。

例如：

```bash
./npc -config=/path/to/npc.conf
```

或者：

```bash
./npc -server=127.0.0.1:8024 -vkey=YOUR_CLIENT_VKEY -type=tcp
```

如果你连安装服务时的参数都需要改，通常应先卸载再重新安装服务。基础说明见 [客户端连接与配置](/guide/client/connect)。

## 3. 客户端更新

标准更新顺序：

1. 停止客户端
2. 执行更新
3. 重新启动

Linux 或 macOS：

```bash
sudo npc stop
sudo npc update
sudo npc start
```

也兼容旧的更新入口：

```bash
sudo npc stop
sudo npc-update update
sudo npc start
```

Windows：

```powershell
npc.exe stop
npc.exe update
npc.exe start
```

也兼容旧的更新入口：

```powershell
npc.exe stop
npc-update.exe update
npc.exe start
```

如果更新失败，请从 [GitHub Releases](https://github.com/djylb/nps/releases/latest) 手动下载对应平台的发布包并覆盖二进制文件。

## 4. 通过代理连接 NPS

当 NPC 所在机器不能直接访问服务端时，可以先经过 Socks5 或 HTTP 代理再连接 NPS。

### 4.1 配置文件方式

在 `npc.conf` 文件中添加：

```ini
[common]
proxy_url=socks5://user:password@127.0.0.1:1080
```

### 4.2 命令行方式

```bash
./npc -server=xxx:123 -vkey=xxx -proxy=socks5://user:password@127.0.0.1:1080
```

支持的代理地址格式：

| 代理类型 | 示例格式 |
| --- | --- |
| Socks5 | `socks5://username:password@ip:port` |
| HTTP | `http://username:password@ip:port` |

## 5. 版本兼容与旧版接入

新部署建议显式保持 `secure_mode=true`，不要为了兼容历史环境先提前关闭安全能力。

### 服务端默认行为

- 新部署通常会显式设置 `secure_mode=true`
- 这会提高默认安全性，但不再兼容非常旧的客户端

### 需要兼容旧客户端时

可以按下面思路处理：

1. 服务端确认是否需要临时关闭严格兼容限制
2. 旧客户端连接时增加 `-proto_version=0`

示例：

```bash
./npc -server=ip:8024 -vkey=YOUR_CLIENT_VKEY -type=tcp -proto_version=0
```

### 排查版本问题时先看什么

- 客户端和服务端是否来自兼容版本
- 是否错误地把旧客户端接到了启用 `secure_mode=true` 的服务端
- 是否把连接协议和隧道类型混淆

## 下一步

- 需要常用命令行参数：看 [NPC 命令行参数](/reference/npc-cli)
- 需要统一启动描述、远程源和多实例：看 [快速启动与远程源](/guide/client/launch)
- 需要评估程序内集成方式：看 [NPC SDK 状态](/reference/npc-sdk)
