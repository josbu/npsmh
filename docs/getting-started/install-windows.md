# Windows 安装脚本

这一页介绍仓库根目录的 `install.ps1`。

它主要解决 Windows 新手最常见的问题：

- 不会判断该下载哪个架构
- 不会区分 Windows 7 / 8 / 8.1 和 Windows 10 及以上
- 不确定没有管理员权限时该装到哪里
- GitHub 下载慢或无法直接访问

如果你已经熟悉发布包选择，也可以继续直接使用 [发布包安装](/getting-started/install-release)。

## 适合什么场景

- Windows 新手
- 想自动选择架构和发布包
- 想少做手工下载、解压和拷贝
- 希望保留非交互命令行用法

## 默认行为

脚本默认保持非交互模式。

也就是说，不加菜单参数时，它会直接按参数执行，不会弹出选择菜单。

默认规则：

- 默认安装模式：`all`
- 默认版本：`latest`
- 默认架构：自动检测当前 Windows 架构
- 默认旧版包选择：自动判断
- 默认安装目录：
  - 有管理员权限：`C:\Program Files\nps`
  - 没有管理员权限：`%LOCALAPPDATA%\nps`

Windows 包选择规则：

- Windows 7 / 8 / 8.1：默认使用 `old` 结尾发布包
- Windows 10 / 11：默认使用普通发布包

下载回落规则：

- 先尝试 GitHub Release 或 GitHub API
- 失败后自动尝试 jsDelivr 镜像
- 适合中国网络环境直接访问 GitHub 不稳定的场景

## 先下载脚本

推荐先把脚本下载到本地，再执行。

```powershell
Invoke-WebRequest -UseBasicParsing -OutFile .\install.ps1 https://fastly.jsdelivr.net/gh/djylb/nps@master/install.ps1
```

如果这个地址不可用，也可以换成：

```powershell
Invoke-WebRequest -UseBasicParsing -OutFile .\install.ps1 https://cdn.jsdelivr.net/gh/djylb/nps@master/install.ps1
```

## 直接安装

安装服务端：

```powershell
powershell -ExecutionPolicy Bypass -File .\install.ps1 nps
```

安装客户端：

```powershell
powershell -ExecutionPolicy Bypass -File .\install.ps1 npc
```

同时安装两者：

```powershell
powershell -ExecutionPolicy Bypass -File .\install.ps1
```

## 菜单模式

如果你不想手动判断架构、版本或安装目录，可以使用菜单模式：

```powershell
powershell -ExecutionPolicy Bypass -File .\install.ps1 -Menu
```

菜单模式只是在启动时帮你选择参数。

它不会改变脚本的默认行为，也不会把脚本变成只能交互使用的工具。

## 常见覆盖参数

安装到自定义目录：

```powershell
powershell -ExecutionPolicy Bypass -File .\install.ps1 nps latest D:\nps
```

强制指定架构：

```powershell
powershell -ExecutionPolicy Bypass -File .\install.ps1 npc -Arch amd64
```

强制指定普通包或旧版包：

```powershell
powershell -ExecutionPolicy Bypass -File .\install.ps1 nps -PackageVariant modern
powershell -ExecutionPolicy Bypass -File .\install.ps1 nps -PackageVariant old
```

## 安装完成后

脚本负责下载、解压并把文件放到合适目录。

是否注册 Windows 服务，仍然交给现有二进制命令处理。

下一步通常看这里：

- 服务端启动与服务注册： [启动 NPS 服务端](/getting-started/start-server)
- 客户端启动与服务注册： [启动 NPC 客户端](/getting-started/start-client)
