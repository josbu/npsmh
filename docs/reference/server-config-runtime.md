# 服务端配置：访问控制与运行

这一页集中放访问控制、日志、运行限制、高级开关和调试配置。

本页里的日志轮转数字如果写成“默认/示例值”，优先指仓库当前自带 `conf/nps.conf`。

如果你要找 Web 登录保护，去看 [Web、HTTP 与安全](/reference/server-config-web)。

## 1. 访问控制

| 名称 | 说明 |
| --- | --- |
| `ip_limit` | 是否启用“先登记公网 IP，后允许访问”的动态入口限制（`true` 或 `false`） |
| `allow_ports` | 允许客户端映射的端口范围（示例：`9001-9009,10001,11000-12000`） |
| `allow_user_login` | 是否允许用户登录管理（`true` 或 `false`） |
| `allow_user_vkey_login` | 是否允许真实客户端使用 `verify_key` 作为管理登录凭据（`true` 或 `false`）；登录后 actor.kind 为 `client` |
| `allow_user_register` | 是否允许用户注册（`true` 或 `false`） |
| `allow_user_change_username` | 是否允许用户修改用户名（`true` 或 `false`） |

补充说明：

- `ip_limit` 是“注册当前公网 IP 后临时放行访问”的功能，和 `login_acl_*` 静态来源 ACL 不是同一机制
- `ip_limit` 的过期记录会在访问时和后台定时清理中自动移除，不会无限累积
- `geoip_path` 为来源 ACL 提供 `geoip:xx` 数据；`geosite_path` 为目标 ACL 提供 `geosite:xx` 数据
- `allow_user_vkey_login` 是历史配置名；它控制的是 `client_vkey` 管理登录，不是“普通用户拿 vkey 登录”
- 绑定到用户的客户端也可以登录；但如果 owner 用户已禁用、过期或不再满足本地登录条件，该客户端 `verify_key` 也会被拒绝
- `allow_user_vkey_login` 留空时，会按 `allow_user_login` 继承默认行为
- `allow_ports` 在执行 `nps reload` 后会立即影响新的端口分配和可用端口校验；已经在运行的监听器不会因为它自动迁移端口

## 2. 日志与流量控制

| 名称 | 说明 |
| --- | --- |
| `log` | 日志模式（`stdout`、`file`、`both`、`off`） |
| `log_level` | 日志级别（`trace`、`debug`、`info`、`warn`、`error`、`fatal`、`panic`、`off`，默认为 `trace`） |
| `log_path` | 日志路径（可选 `path`、`off`、`docker`） |
| `log_compress` | 是否启用日志压缩（`true` 开启，`false` 关闭） |
| `log_max_files` | 允许存在的日志总文件个数（仓库示例配置写为 `10`；配置缺失时程序默认 `30`） |
| `log_max_days` | 允许保存日志的最大天数（仓库示例配置写为 `7`；配置缺失时程序默认 `30`） |
| `log_max_size` | 单个日志文件的最大大小（MB）（仓库示例配置写为 `2`；配置缺失时程序默认 `5`） |
| `flow_store_interval` | 流量数据持久化间隔（分钟），留空表示不持久化 |

补充说明：

- 上面这些日志配置在执行 `nps reload` 后会重新初始化日志输出
- `flow_store_interval` 在执行 `nps reload` 后会重新配置后台持久化间隔；从 `0` 改为非 `0` 会开始定时落盘，从非 `0` 改为 `0` 会停止定时落盘

## 3. 其他高级配置

| 名称 | 说明 |
| --- | --- |
| `allow_flow_limit` | 是否允许流量限制 |
| `allow_rate_limit` | 是否允许带宽限制 |
| `allow_time_limit` | 是否允许到期时间限制 |
| `allow_tunnel_num_limit` | 是否允许限制客户端最大隧道数 |
| `allow_local_proxy` | 是否允许 NPS 本地代理连接（相当于在 NPS 服务器上启动一个 NPC） |
| `allow_user_local` | 是否允许用户使用 NPS 本地代理连接 |
| `allow_secret_local` | 是否允许“未配置固定目标”的私密代理把最终目标切到服务器本地 |
| `allow_connection_num_limit` | 是否限制客户端最大连接数 |
| `allow_multi_ip` | 是否允许配置隧道监听 IP 地址 |
| `system_info_display` | 是否显示系统负载监控信息 |
| `disconnect_timeout` | TCP 中断超时等待时间（秒，默认 `30`） |

补充说明：

- `allow_user_local` 留空时，会按 `allow_local_proxy` 继承默认行为
- `allow_local_proxy=true` 后，NPS 会自动创建一个运行时客户端，备注为 `Local Proxy`，用于把目标直接指向 NPS 本机服务
- 私密代理只要配置了固定 `target`，访问侧带入的目标地址就不会覆盖服务端隧道目标；只有 `target` 留空时，访问侧来流目标才会成为最终路由依据
- `allow_secret_local` 只在 `target` 留空时有意义；它控制访问侧是否允许把最终目标切到 NPS 服务器本地服务
- `allow_local_proxy`、`allow_secret_local` 在执行 `nps reload` 后会作用到新的连接和新的运行时客户端状态
- `disconnect_timeout` 在执行 `nps reload` 后会更新桥接控制连接的断线判定时间

## 4. 运行时访客密钥

`public_vkey` 和 `visitor_vkey` 的字段定义放在 [基础项与密钥](/reference/server-config-basics)，但它们的运行时行为更适合在这里一起理解。

补充说明：

- 这两个值在执行 `nps reload` 后会同步到运行时客户端集合中
- 当前实现会为它们自动创建“仅运行时存在”的隐藏客户端，而不是要求你手动在 Web 页面先建一个普通客户端
- `public_vkey` 主要用于配置型接入和注册类流程
- `visitor_vkey` 主要用于 `secret` / `p2p` 访问侧命令展示与访问登记
- 如果两者设置成相同值，当前实现会把它视为同一个访问侧密钥使用，但它仍不能建立普通客户端主控连接

## 5. 调试配置

| 名称 | 说明 |
| --- | --- |
| `pprof_ip` | pprof 调试监听 IP（留空或注释表示关闭） |
| `pprof_port` | pprof 调试监听端口（需与 `pprof_ip` 配合启用） |

补充说明：

- `pprof_*` 改动仍需要重启；`reload` 不会帮你重建 pprof 监听器

## 6. 相关页面

- 需要能力边界和限制说明：看 [功能清单与扩展能力](/reference/features)
- 需要常见问题排查：看 [FAQ](/reference/faq)
- 需要操作步骤：看 [服务端运维](/guide/server/operations)
