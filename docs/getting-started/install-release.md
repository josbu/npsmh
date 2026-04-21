# 发布包安装

如果你不使用 Docker 或安装脚本，推荐直接使用发布包。

如果你是 Windows 新手，不想自己判断架构、旧版包和安装目录，也可以直接改用 [Windows 安装脚本](/getting-started/install-windows)。

适合：

- Windows
- 桌面环境
- 想解压即用
- 想手工选择具体平台和架构

## Windows 最短路径

如果你选择手工发布包方式，标准流程通常就是下面这几步。

标准流程就是：

1. 下载对应平台的发布包
2. 解压到一个你方便管理的目录，例如 `D:\nps`
3. 先直接运行 `nps.exe` 或 `npc.exe` 做一次前台验证
4. 确认命令和配置都正确后，再用管理员 PowerShell 执行 `nps.exe install` 或 `npc.exe install`

这样可以完成 Windows 的标准服务安装。
单独的 PowerShell 安装脚本只是在“自动下载和解压”这一层提供便利，不是功能前提。

统一下载入口：

- [最新发布页面](https://github.com/djylb/nps/releases/latest)

如果你的平台或架构不在下面的常见列表里，也先到发布页面查看完整资产。

## Windows

- Windows 10 / 11
  - [Server amd64](https://github.com/djylb/nps/releases/latest/download/windows_amd64_server.tar.gz)
  - [Client amd64](https://github.com/djylb/nps/releases/latest/download/windows_amd64_client.tar.gz)
  - [Server 386](https://github.com/djylb/nps/releases/latest/download/windows_386_server.tar.gz)
  - [Client 386](https://github.com/djylb/nps/releases/latest/download/windows_386_client.tar.gz)
  - [Server arm64](https://github.com/djylb/nps/releases/latest/download/windows_arm64_server.tar.gz)
  - [Client arm64](https://github.com/djylb/nps/releases/latest/download/windows_arm64_client.tar.gz)
- Windows 7 / 8 / 8.1
  - [Server amd64 old](https://github.com/djylb/nps/releases/latest/download/windows_amd64_server_old.tar.gz)
  - [Client amd64 old](https://github.com/djylb/nps/releases/latest/download/windows_amd64_client_old.tar.gz)
  - [Server 386 old](https://github.com/djylb/nps/releases/latest/download/windows_386_server_old.tar.gz)
  - [Client 386 old](https://github.com/djylb/nps/releases/latest/download/windows_386_client_old.tar.gz)

Windows 下载后建议继续看：

- 服务端首次启动： [启动 NPS 服务端](/getting-started/start-server)
- 客户端首次连接： [启动 NPC 客户端](/getting-started/start-client)

## Linux

- x86 / x86_64
  - [Server amd64](https://github.com/djylb/nps/releases/latest/download/linux_amd64_server.tar.gz)
  - [Client amd64](https://github.com/djylb/nps/releases/latest/download/linux_amd64_client.tar.gz)
  - [Server 386](https://github.com/djylb/nps/releases/latest/download/linux_386_server.tar.gz)
  - [Client 386](https://github.com/djylb/nps/releases/latest/download/linux_386_client.tar.gz)
- ARM
  - [Server arm64](https://github.com/djylb/nps/releases/latest/download/linux_arm64_server.tar.gz)
  - [Client arm64](https://github.com/djylb/nps/releases/latest/download/linux_arm64_client.tar.gz)
  - [Server arm v5](https://github.com/djylb/nps/releases/latest/download/linux_arm_v5_server.tar.gz)
  - [Client arm v5](https://github.com/djylb/nps/releases/latest/download/linux_arm_v5_client.tar.gz)
  - [Server arm v6](https://github.com/djylb/nps/releases/latest/download/linux_arm_v6_server.tar.gz)
  - [Client arm v6](https://github.com/djylb/nps/releases/latest/download/linux_arm_v6_client.tar.gz)
  - [Server arm v7](https://github.com/djylb/nps/releases/latest/download/linux_arm_v7_server.tar.gz)
  - [Client arm v7](https://github.com/djylb/nps/releases/latest/download/linux_arm_v7_client.tar.gz)

## macOS

- [Server Intel](https://github.com/djylb/nps/releases/latest/download/darwin_amd64_server.tar.gz)
- [Client Intel](https://github.com/djylb/nps/releases/latest/download/darwin_amd64_client.tar.gz)
- [Server Apple Silicon](https://github.com/djylb/nps/releases/latest/download/darwin_arm64_server.tar.gz)
- [Client Apple Silicon](https://github.com/djylb/nps/releases/latest/download/darwin_arm64_client.tar.gz)

## FreeBSD

- [Server amd64](https://github.com/djylb/nps/releases/latest/download/freebsd_amd64_server.tar.gz)
- [Client amd64](https://github.com/djylb/nps/releases/latest/download/freebsd_amd64_client.tar.gz)
- [Server 386](https://github.com/djylb/nps/releases/latest/download/freebsd_386_server.tar.gz)
- [Client 386](https://github.com/djylb/nps/releases/latest/download/freebsd_386_client.tar.gz)
- [Server arm](https://github.com/djylb/nps/releases/latest/download/freebsd_arm_server.tar.gz)
- [Client arm](https://github.com/djylb/nps/releases/latest/download/freebsd_arm_client.tar.gz)

下载并解压后，继续看 [启动服务端与客户端](/getting-started/deployment)。

## 下一步

- 启动服务端：看 [启动 NPS 服务端](/getting-started/start-server)
- 启动客户端：看 [启动 NPC 客户端](/getting-started/start-client)
