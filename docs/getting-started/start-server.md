# 启动 NPS 服务端

这一页只聚焦一件事：启动服务端，并能登录 Web 管理界面。

如果你还没有把二进制文件或容器准备好，请先看 [安装指南](/getting-started/install)。

Windows 常见流程是先在发布包目录里运行 `nps.exe` 做一次前台验证。
确认启动正常后，再执行 `nps.exe install` 注册服务。
如果你使用管理员权限安装，标准目录通常是 `C:\Program Files\nps`。
如果你使用无管理员权限的 Windows 安装脚本，默认目录通常是 `%LOCALAPPDATA%\nps`。

## 第一次通常这样做

第一次排查环境时，建议先直接前台运行。

这样更容易看到启动报错，也更容易确认当前使用的是哪一份配置。

## 直接前台运行

如果你是从发布包目录直接运行，并且 `conf/`、`web/` 也在同一目录结构下，可以直接前台启动。

如果你是 Linux / macOS 标准安装到系统路径，且配置放在 `/etc/nps`，前台验证时建议显式带上 `-conf_path=/etc/nps`。
这样可以减少“服务模式使用的配置目录”和“前台直接运行时的查找目录”混淆。

Linux 或 macOS 标准安装后，推荐这样前台验证：

```bash
nps -conf_path=/etc/nps
```

如果你是从发布包目录直接运行：

```bash
./nps
```

Windows：

```powershell
nps.exe
```

如果你使用自定义配置目录：

```bash
nps -conf_path=/app/nps
```

```powershell
nps.exe -conf_path=D:\nps
```

## 注册为系统服务

确认前台启动正常后，再注册系统服务。

如果你是从发布包目录直接操作，而二进制还没有进入系统路径，请把下面命令里的 `nps` 改成 `./nps`。
Windows 注册服务时，建议使用管理员 PowerShell。

Linux 或 macOS：

```bash
sudo nps install
sudo nps start
```

Windows：

```powershell
nps.exe install
nps.exe start
```

如果你要在安装时指定自定义配置目录，请在 `install` 时一并带上：

```bash
nps install -conf_path=/app/nps
```

```powershell
nps.exe install -conf_path=D:\nps
```

后续再执行 `nps start` 或 `nps.exe start` 即可，不需要重复传 `-conf_path`。
Windows 上如果不想使用默认目录，也建议在第一次 `install` 时就传入 `-conf_path`。

## 常用服务命令

Linux 或 macOS：

```bash
sudo nps start
sudo nps stop
sudo nps restart
sudo nps uninstall
```

Windows：

```powershell
nps.exe start
nps.exe stop
nps.exe restart
nps.exe uninstall
```

## 常见配置位置

- Linux / macOS 标准安装：
  - 配置目录通常是 `/etc/nps`
  - 配置文件通常是 `/etc/nps/conf/nps.conf`
  - 默认日志通常是 `/var/log/nps.log`
  - 如果要前台直接验证这套配置，建议显式使用 `nps -conf_path=/etc/nps`
- Windows 管理员标准安装：
  - 配置目录通常是 `C:\Program Files\nps`
  - 配置文件通常是 `C:\Program Files\nps\conf\nps.conf`
- Windows 无管理员脚本安装：
  - 配置目录通常是 `%LOCALAPPDATA%\nps`
  - 配置文件通常是 `%LOCALAPPDATA%\nps\conf\nps.conf`
- Windows 默认日志：
  - 日志文件默认在当前运行的 `nps.exe` 所在目录，例如 `nps.log`
- 自定义目录：
  - 如果你在 `install` 时传了 `-conf_path`，或在 `install.ps1` 里传了目标目录，以那个目录为准

## 访问 Web 管理界面

仓库自带示例配置下，浏览器访问：

```text
http://<server-ip>:8081
```

仓库自带示例配置的初始账号密码：

```text
admin / 123
```

正式环境请尽快修改默认密码。

## 下一步

- 让客户端连上服务端：看 [启动 NPC 客户端](/getting-started/start-client)
- 需要服务注册、多实例和手动服务：看 [服务与多实例](/guide/service-instances)
- 需要运维、重载和更新：看 [服务端运维](/guide/server/operations)
