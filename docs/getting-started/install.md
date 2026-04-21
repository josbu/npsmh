# 安装指南

本页只回答一件事：如何把 `nps` 或 `npc` 放到目标机器上。

如果你当前的目标是先完成第一次部署和验证，多数情况下只需要先看这四种方式：

1. Docker：首次验证路径最短
2. 安装脚本：适合 Linux 服务器
3. Windows 安装脚本：适合 Windows 新手
4. 发布包：适合 Windows 和大多数通用环境

启动、注册系统服务、更新和多开方式放在 [启动服务端与客户端](/getting-started/deployment) 和 [客户端连接与配置](/guide/client/connect) 中说明。

Windows 现在提供独立的 PowerShell 安装脚本。
如果你希望脚本代替你选择 Windows 包、架构和安装目录，可以直接使用 [Windows 安装脚本](/getting-started/install-windows)。
如果你更习惯自己手动下载发布包，也可以继续使用 [发布包安装](/getting-started/install-release)。

## 先选安装方式

| 方式 | 适合什么情况 | 建议页面 |
| --- | --- | --- |
| Docker | 想用最短路径完成首次验证，且机器已经有 Docker | [Docker 安装](/getting-started/install-docker) |
| 安装脚本 | Linux 主机，想直接装到系统路径 | [脚本安装](/getting-started/install-script) |
| Windows 安装脚本 | Windows 新手，想自动下载、解压和选包 | [Windows 安装脚本](/getting-started/install-windows) |
| 发布包 | Windows、Linux、macOS、FreeBSD 等通用场景 | [发布包安装](/getting-started/install-release) |
| 源码编译 | 需要自行构建或做定制 | [源码安装](/getting-started/install-source) |
| Android / OpenWrt / 群晖 | 特定平台 | [特定平台](/getting-started/install-platforms) |

## 第一次通常这样选

- 想用最短路径验证服务端：优先使用 [Docker 安装](/getting-started/install-docker)
- Linux 服务器想直接装到系统里：优先使用 [脚本安装](/getting-started/install-script)
- Windows 新手不想手动判断发布包：优先使用 [Windows 安装脚本](/getting-started/install-windows)
- Windows、桌面环境或只想解压即用：优先使用 [发布包安装](/getting-started/install-release)
- 需要修改源码或自己构建：再看 [源码安装](/getting-started/install-source)

## 下一步

- 已经装好服务端：看 [启动 NPS 服务端](/getting-started/start-server)
- 已经装好客户端：看 [启动 NPC 客户端](/getting-started/start-client)
- 只想先完成一条最小可用链路：看 [10 分钟快速开始](/getting-started/quick-start)
