# 脚本安装

安装脚本只适用于 Linux 或其他类 Unix 环境，不支持 Windows。

这不表示 Windows 不支持标准安装。
Windows 的推荐路径是使用发布包，然后执行 `nps.exe install` 或 `npc.exe install` 注册服务。
如果你希望 Windows 端也用脚本完成下载、解压和选包，可以看 [Windows 安装脚本](/getting-started/install-windows)。

适合：

- Linux 服务器
- 想直接装到系统路径
- 不想手工下载和解压发布包

## 默认行为

脚本仓库内默认行为与 `install.sh` 一致：

- 默认安装模式：`all`
- 默认版本：`latest`
- 默认安装目录：系统目录
- 默认配置目录：`/etc/nps`
- 默认二进制目录：优先 `/usr/bin`，失败时回退到 `/usr/local/bin`

## 安装 NPS

```bash
wget -qO- https://fastly.jsdelivr.net/gh/djylb/nps@master/install.sh | sudo sh -s nps
```

## 安装 NPC

```bash
wget -qO- https://fastly.jsdelivr.net/gh/djylb/nps@master/install.sh | sudo sh -s npc
```

## 安装脚本支持的参数

```text
install.sh [mode] [version] [install_dir]
```

含义：

- `mode`：`nps`、`npc` 或 `all`
- `version`：发布版本号，例如 `v0.35.0`
- `install_dir`：自定义解压目录；设置后不会再安装到系统路径

也支持下列环境变量：

- `NPS_INSTALL_MODE`
- `NPS_INSTALL_VERSION`
- `NPS_INSTALL_DIR`

## 下一步

- 安装完成后的启动和服务管理：看 [启动服务端与客户端](/getting-started/deployment)
- 想直接启动服务端：看 [启动 NPS 服务端](/getting-started/start-server)
- 想直接启动客户端：看 [启动 NPC 客户端](/getting-started/start-client)
