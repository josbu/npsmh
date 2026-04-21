# 目录、面板与日志

这一页聚焦服务端运维里最常见的四件事：

- 确认运行目录和配置目录
- 登录 Web 管理界面
- 前台排查启动失败
- 查看基础监控和日志

## 1. 先明确运行目录和配置目录

NPS 不要求在仓库根目录运行。

真正需要注意的是：

- `nps` 会从安装目录或 `-conf_path` 指定目录解析 `conf/`、`web/static/` 和 `web/views/`
- 使用官方发布包、安装脚本或标准安装目录时，相关资源会自动放到正确位置
- 如果使用自定义目录，请保持 `conf/` 和 `web/` 资源目录布局完整
- Linux / macOS 标准安装后，系统服务通常使用 `/etc/nps`；如果你手动前台运行做排障，建议显式传 `-conf_path=/etc/nps`

常见路径：

- Linux 默认配置目录：`/etc/nps`
- Windows 管理员标准安装目录：`C:\Program Files\nps`
- Windows 无管理员脚本安装目录：`%LOCALAPPDATA%\nps`
- 自定义目录：通过 `-conf_path` 指定

## 2. 访问 Web 管理界面

- 浏览器访问 `http://<server-ip>:8081`
- 仓库自带示例配置的初始账号：`admin`
- 仓库自带示例配置的初始密码：`123`

首次登录后建议尽快完成：

1. 修改默认密码
2. 创建第一个客户端
3. 检查服务端实际对外地址和端口

## 3. 查看日志与运行状态

### 调试启动失败

如果服务启动失败，先停止服务，再前台运行：

如果二进制已经安装到系统路径，直接使用 `nps`。
如果你是从发布包目录直接运行，请在命令前加 `./`。

```bash
nps stop
nps -conf_path=/etc/nps
```

Windows：

```powershell
nps.exe stop
nps.exe
```

如果你不是标准安装，而是从发布包目录直接运行，请把上面的 Linux 命令改成：

```bash
./nps
```

### 日志位置

- Windows 默认日志：当前运行的 `nps.exe` 所在目录下的 `nps.log`
- Linux 默认日志：`/var/log/nps.log`
- 如果在 `nps.conf` 设置了 `log_path`，以配置值为准

## 4. 监控与统计

Web 管理界面可以直接查看：

- 客户端在线状态
- 客户端当前连接地址
- 流量统计
- 当前带宽
- 系统信息

相关配置项：

```ini
system_info_display=true
flow_store_interval=10
```

说明：

- `system_info_display` 控制是否显示系统信息
- `flow_store_interval` 控制流量统计的持久化周期
- 当前带宽和流量统计更适合运维观察，数值可能与业务端观测略有差异

## 下一步

- 需要重载、重启和更新：看 [重载、重启与更新](/guide/server/operations-lifecycle)
- 需要 HTTPS、证书和前置代理：看 [HTTPS 与反向代理](/guide/server/https-and-proxy)
