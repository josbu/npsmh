# 服务端配置：基础项与密钥

这一页集中放 `nps.conf` 里的基础运行项和密钥相关配置。

本页涉及的“默认/示例值”表述，优先以仓库当前自带 `conf/nps.conf` 为准。

如果你要找 Web 管理与登录安全，去看 [Web、HTTP 与安全](/reference/server-config-web)。
如果你要找节点与平台接入，去看 [节点与平台对接](/reference/server-config-node)。

## 1. 基础配置

| 名称 | 说明 |
| --- | --- |
| `appname` | 应用名称 |
| `runmode` | 运行模式（dev/pro） |
| `run_mode` | 节点运行角色（`standalone` / `node`） |
| `secure_mode` | 安全模式（仓库示例配置写为 `true`；如果该项缺失，程序默认 `false`，建议显式设置） |
| `dns_server` | DNS 服务器 |
| `timezone` | 时区 |
| `ntp_server` | NTP 服务器 |
| `ntp_interval` | NTP 最小查询间隔（分钟） |
| `geoip_path` | `geoip.dat` 路径；相对路径默认相对当前配置文件目录解析 |
| `geosite_path` | `geosite.dat` 路径；相对路径默认相对当前配置文件目录解析 |

## 2. 认证与密钥

| 名称 | 说明 |
| --- | --- |
| `auth_key` | 历史认证辅助密钥；新接入优先使用 token 或 session 登录 |
| `auth_crypt_key` | 获取 `authKey` 的 AES 加密密钥（16 位） |
| `public_vkey` | 客户端以配置文件模式启动时的密钥 |
| `visitor_vkey` | `secret` / `p2p` 访问侧优先展示使用的独立密钥；可用于访客访问和 `ip_limit` 注册，但仍不能建立客户端主控连接 |

## 3. 相关页面

- 需要 Web 管理、登录保护和反向代理安全：看 [Web、HTTP 与安全](/reference/server-config-web)
- 需要旧版兼容和系统限制补充：看 [补充说明](/reference/notes)
- 需要节点模式与平台接入：看 [节点与平台对接](/reference/server-config-node)
