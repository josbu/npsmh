# NPS 服务端配置文件

本页是 `nps.conf` 的总入口，适合以下场景：

- 你已经知道要找哪一类配置，只是还没确定应该进入哪一组
- 你在部署或排查问题前，先确认配置目录、默认路径和生效方式
- 你希望以后更新配置时，只改一类主题页，而不是在一张超长页面里来回找

默认配置文件路径：

- Linux / macOS：`/etc/nps/conf/nps.conf`
- Windows 管理员标准安装：`C:\Program Files\nps\conf\nps.conf`
- Windows 无管理员脚本安装：`%LOCALAPPDATA%\nps\conf\nps.conf`

说明：

- 本组页面里写成“默认”的值，优先指仓库当前自带 `conf/nps.conf` 示例配置
- 少量字段在“配置缺失时的程序内置默认值”与“示例配置值”并不完全相同；需要精确确认时，请同时查看实际配置文件和对应实现
- 上面列出的路径是“标准安装目录中的常见配置文件位置”；如果你要手动前台运行做排障，更合适的方式仍然是显式传 `-conf_path`

如果要使用自定义配置目录，可以在启动时指定 `-conf_path`：

```bash
./nps -conf_path=/app/nps
```

```powershell
nps.exe -conf_path=D:\test\nps
```

阅读这组文档前，先记住三点：

- 参数留空通常表示回落到程序内置默认值、空值或关闭状态，具体以该字段实现为准
- 大多数配置变更都需要重启 NPS 才会完整生效
- `nps reload` 现在已经覆盖日志、时区、DNS / NTP、GeoIP / GeoSite / ACL、部分运行限制和大多数管理面配置，但它仍不等同于完整热更新；监听端口、共享端口复用、bridge 入口、P2P / QUIC / pprof 等配置改动仍需要重启；Windows 当前不支持该命令

正式部署时，至少要修改这些默认安全项：

- `web_password`
- `auth_key`
- `auth_crypt_key`
- `totp_secret`

## 先按主题找

| 你要找什么 | 建议页面 |
| --- | --- |
| 基础运行项、时区、`secure_mode`、密钥 | [基础项与密钥](/reference/server-config-basics) |
| Web 管理后台、登录保护、真实 IP、前置代理安全 | [Web、HTTP 与安全](/reference/server-config-web) |
| `bridge_*`、`http_proxy_port`、`https_proxy_port`、P2P 端口 | [入口端口与桥接](/reference/server-config-ports) |
| `run_mode=node`、多平台、reverse WS、callback | [节点与平台对接](/reference/server-config-node) |
| 访问控制、日志、限制开关、pprof | [访问控制与运行](/reference/server-config-runtime) |

## 第一次部署通常先看哪几页

1. [基础项与密钥](/reference/server-config-basics)
2. [Web、HTTP 与安全](/reference/server-config-web)
3. [入口端口与桥接](/reference/server-config-ports)

只有在你要接入外部平台或多节点统一控制面时，再继续看 [节点与平台对接](/reference/server-config-node)。

## 相关页面

- 需要启动、重载、更新和服务命令：看 [服务端运维](/guide/server/operations)
- 需要 HTTPS、证书和前置代理：看 [HTTPS 与反向代理](/guide/server/https-and-proxy)
- 需要先在服务端打开节点模式：看 [启用节点模式](/guide/server/node-management)
- 需要节点控制面和平台接入：看 [平台接入总览](/reference/integration/platform-onboarding)
