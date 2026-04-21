# 管理接口：资源接口

这一页聚焦正式 `/api/` 管理接口里和用户、客户端、隧道、域名以及全局资源相关的 HTTP 接口。

## 常见列表查询

根据当前实现，列表接口通常支持这些基础参数：

- `offset`
- `limit`

常见补充筛选：

- 用户：`search`、`sort`、`order`
- 客户端：`search`、`sort`、`order`
- 隧道：`mode`、`client_id`、`search`、`sort`、`order`
- 域名：`client_id`、`search`、`sort`、`order`

常见排序字段取决于资源类型；当前实现除了 `Id`、`TotalFlow`、`NowConn`、`Status` 这类传统字段外，也支持按实时速率排序：

- 用户、客户端：`NowRate`（继续兼容旧别名 `Rate.NowRate`）
- 隧道、域名：`NowRate`

资源更新接口当前还支持一个可选并发控制字段：

- `expected_revision`
- 只对 `actions/update` 这类修改接口有意义
- 如果提供该字段且和服务端当前资源 `revision` 不一致，会返回 `409`，错误码是 `revision_conflict`
- 不提供时，表示接受“最后一次写入覆盖前面的写入”

## 用户

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| `GET` | `/api/users` | 用户列表 |
| `GET` | `/api/users/:id` | 用户详情 |
| `POST` | `/api/users` | 新增用户 |
| `POST` | `/api/users/:id/actions/update` | 修改用户 |
| `POST` | `/api/users/:id/actions/status` | 启停用户 |
| `POST` | `/api/users/:id/actions/delete` | 删除用户 |

补充说明：

- 用户写操作只适合本地管理员或 `full` 平台管理员
- 用户写接口只接受 JSON object body
- 用户写接口只接受 canonical 字段，例如 `username`、`password`、`totp_secret`、`expire_at`、`flow_limit_total_bytes`、`rate_limit_total_bps`、`max_connections`、`reset_flow`
- `POST /api/users/:id/actions/update` 可选 `expected_revision`
- 创建用户时，`password` 和 `totp_secret` 不能同时为空；如果设置了 `totp_secret`，则允许创建空密码本地用户
- 更新用户时，省略 `password` / `totp_secret` 表示保持当前值；显式传空字符串表示清空该字段，但最终仍不能让两者同时为空
- `rate_limit_total_bps` 的单位是 bytes/s，不是 KB/s
- 用户读接口会返回 `total_in_bytes`、`total_out_bytes`、`total_bytes`，以及 `now_rate_in_bps`、`now_rate_out_bps`、`now_rate_total_bps`
- `POST /api/users/:id/actions/status` 的 body 必须显式提供 `status`

## 客户端

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| `GET` | `/api/clients` | 客户端列表 |
| `GET` | `/api/tools/qrcode` | 生成二维码图片 |
| `POST` | `/api/tools/qrcode` | 用 JSON body 生成二维码图片 |
| `GET` | `/api/clients/:id` | 客户端详情 |
| `GET` | `/api/clients/:id/connections` | 查看同 vkey 下当前在线的具体连接实例 |
| `POST` | `/api/clients` | 新增客户端 |
| `POST` | `/api/clients/actions/clear` | 批量清理客户端状态或统计 |
| `POST` | `/api/clients/:id/actions/update` | 修改客户端 |
| `POST` | `/api/clients/:id/actions/ping` | 探测客户端连通状态 |
| `POST` | `/api/clients/:id/actions/status` | 启停客户端 |
| `POST` | `/api/clients/:id/actions/clear` | 清理单个客户端状态或统计 |
| `POST` | `/api/clients/:id/actions/delete` | 删除客户端 |

补充说明：

- `GET /api/tools/qrcode` 和 `POST /api/tools/qrcode` 成功时都直接返回 PNG 内容，并显式返回 `Cache-Control: no-store` 和 `Pragma: no-cache`；失败时返回标准 JSON 错误响应
- `GET /api/tools/qrcode` 可直接传 `text`，也可传 `account` 和 `secret`
- `POST /api/tools/qrcode` 接受严格 JSON object body；如果需要传 `account` 和 `secret`，优先使用 `POST`，避免把敏感内容放进 URL
- 客户端相关资源会受当前 actor 作用域限制
- 客户端写接口只接受 JSON object body
- 客户端写接口只接受 canonical 字段，例如 `verify_key`、`owner_user_id`、`manager_user_ids`、`expire_at`、`flow_limit_total_bytes`、`rate_limit_total_bps`、`max_connections`、`max_tunnel_num`、`reset_flow`
- `POST /api/clients/:id/actions/update` 可选 `expected_revision`
- 更新客户端时，省略 `password` 表示保持当前基础认证密码；显式传空字符串表示清空基础认证密码
- `rate_limit_total_bps` 的单位是 bytes/s，不是 KB/s
- 客户端读接口会同时返回 `bridge_*`、`service_*`、`total_*` 三组累计流量字段，以及 `bridge_now_rate_*`、`service_now_rate_*`、`total_now_rate_*` 三组实时速率字段
- 客户端读接口里的 `expire_at` 和 `flow_limit_total_bytes` 表示当前生效值；如果客户端绑定了 owner 用户且自身未单独配置，这两个字段会回落到 owner 用户当前生效的时间/流量限制
- 客户端读接口当前还会返回 `connection_count`，表示同一逻辑客户端（同 vkey）下当前可见的运行时连接实例数量
- 客户端读接口里的 `verify_key` 属于敏感身份字段；只有具备 `clients:update` 权限的 actor 才会收到，纯只读 actor 会被脱敏
- 客户端资源响应里的 `config` 当前稳定只返回 `user`、`compress`、`crypt`；不会返回 `config.password`
- `GET /api/clients/:id/connections` 用于列出同 vkey 下的具体连接实例；当前稳定字段包括 `uuid`、`version`、`base_ver`、`remote_addr`、`local_addr`、`has_signal`、`has_tunnel`、`is_online`、`connected_at`、`connected_at_text`、`now_conn`，以及 `bridge_*`、`service_*`、`total_*` 三组累计流量/实时速率字段
- `GET /api/clients/:id/connections` 返回的实例统计是纯运行时临时数据，不落盘；实例断开后会直接从列表消失，流量累计值只覆盖该实例当前在线期间
- `POST /api/clients/:id/actions/ping` 的 `data` 当前稳定只返回 `id` 和 `rtt`；时间戳和 `config_epoch` 统一放在外层 `meta`
- `POST /api/clients/:id/actions/status` 的 body 必须显式提供 `status`
- `POST /api/clients/actions/clear` 需要 `mode`
- `POST /api/clients/actions/clear` 可选 `client_ids`；不传时会清理当前作用域内全部可见客户端
- `POST /api/clients/actions/clear` 和 `POST /api/clients/:id/actions/clear` 当前稳定支持 `flow`、`flow_limit`、`time_limit`、`rate_limit`、`conn_limit`、`tunnel_limit`

## 隧道

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| `GET` | `/api/tunnels` | 隧道列表 |
| `GET` | `/api/tunnels/:id` | 隧道详情 |
| `POST` | `/api/tunnels` | 新增隧道 |
| `POST` | `/api/tunnels/:id/actions/update` | 修改隧道 |
| `POST` | `/api/tunnels/:id/actions/start` | 启动隧道 |
| `POST` | `/api/tunnels/:id/actions/stop` | 停止隧道 |
| `POST` | `/api/tunnels/:id/actions/clear` | 清理隧道状态或统计 |
| `POST` | `/api/tunnels/:id/actions/delete` | 删除隧道 |

补充说明：

- 隧道写接口只接受 JSON object body
- 隧道写接口当前稳定接受 canonical 字段：`client_id`、`port`、`server_ip`、`mode`、`target_type`、`target`、`proxy_protocol`、`local_proxy`、`auth`、`remark`、`password`、`local_path`、`strip_pre`、`enable_http`、`enable_socks5`、`entry_acl_mode`、`entry_acl_rules`、`dest_acl_mode`、`dest_acl_rules`、`expire_at`、`flow_limit_total_bytes`、`rate_limit_total_bps`、`max_connections`、`reset_flow`
- `POST /api/tunnels/:id/actions/update` 可选 `expected_revision`
- `enable_http` 和 `enable_socks5` 只对 `mixProxy` 隧道有明确意义；其他模式下前端不应主动写入这两个字段
- 隧道限制相关写字段当前只接受 `expire_at`、`flow_limit_total_bytes`、`rate_limit_total_bps`、`max_connections`、`reset_flow`
- 隧道读接口也使用同一套 canonical 字段名：`auth`、`enable_http`、`enable_socks5`
- 隧道读接口里的 `password` / `auth` 属于敏感写配置；只有具备 `tunnels:update` 权限的 actor 才会收到这两个字段，纯只读 actor 会被脱敏
- 隧道读接口会返回 `service_in_bytes`、`service_out_bytes`、`service_total_bytes`，以及 `now_rate_in_bps`、`now_rate_out_bps`、`now_rate_total_bps`
- 隧道读接口里的嵌套 `client` 只作为摘要显示使用；当前不会再返回该客户端的 `verify_key` 或 `config.user/config.password`
- 隧道读接口里的嵌套 `client` 当前还会返回 `connection_count`，表示该逻辑客户端（同 vkey）当前在线的运行时连接实例数量
- `rate_limit_total_bps` 的单位是 bytes/s，不是 KB/s
- `POST /api/tunnels/:id/actions/start` 和 `POST /api/tunnels/:id/actions/stop` 可选 `mode`；当前 `mixProxy` 隧道支持 `http` 和 `socks5`
- `POST /api/tunnels/:id/actions/clear` 的 body 需要 `mode`
- `POST /api/tunnels/:id/actions/clear` 当前稳定支持 `flow`、`flow_limit`、`time_limit`

## 域名

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| `GET` | `/api/hosts` | 域名转发列表 |
| `GET` | `/api/hosts/cert-suggestion` | 查询可复用的手动证书建议 |
| `GET` | `/api/hosts/:id` | 域名转发详情 |
| `POST` | `/api/hosts` | 新增域名转发 |
| `POST` | `/api/hosts/:id/actions/update` | 修改域名转发 |
| `POST` | `/api/hosts/:id/actions/start` | 启用域名转发 |
| `POST` | `/api/hosts/:id/actions/stop` | 停用域名转发 |
| `POST` | `/api/hosts/:id/actions/clear` | 清理域名转发状态或统计 |
| `POST` | `/api/hosts/:id/actions/delete` | 删除域名转发 |

补充说明：

- `GET /api/hosts/cert-suggestion` 可传 `host`，也可配合 `exclude_id` 排除当前规则
- 当前实现会优先建议仍然有效、可以复用的手动证书
- 域名转发写接口只接受 JSON object body
- 域名转发写接口当前稳定接受 canonical 字段：`client_id`、`host`、`target`、`proxy_protocol`、`local_proxy`、`auth`、`header`、`resp_header`、`host_change`、`remark`、`location`、`path_rewrite`、`redirect_url`、`entry_acl_mode`、`entry_acl_rules`、`scheme`、`https_just_proxy`、`tls_offload`、`auto_ssl`、`key_file`、`cert_file`、`auto_https`、`auto_cors`、`compat_mode`、`target_is_https`、`expire_at`、`flow_limit_total_bytes`、`rate_limit_total_bps`、`max_connections`、`reset_flow`、`sync_cert_to_matching_hosts`
- `POST /api/hosts/:id/actions/update` 可选 `expected_revision`
- 域名转发限制相关写字段当前只接受 `expire_at`、`flow_limit_total_bytes`、`rate_limit_total_bps`、`max_connections`、`reset_flow`
- 域名转发读接口也使用同一套 canonical 字段名：`auth`、`header`、`resp_header`、`host_change`
- 域名转发读接口里的 `auth` / `cert_file` / `key_file` 属于敏感写配置；只有具备 `hosts:update` 权限的 actor 才会收到这些字段，纯只读 actor 会被脱敏
- 域名转发读接口会返回 `service_in_bytes`、`service_out_bytes`、`service_total_bytes`，以及 `now_rate_in_bps`、`now_rate_out_bps`、`now_rate_total_bps`
- 域名转发读接口里的嵌套 `client` 只作为摘要显示使用；当前不会再返回该客户端的 `verify_key` 或 `config.user/config.password`
- 域名转发读接口里的嵌套 `client` 当前还会返回 `connection_count`，表示该逻辑客户端（同 vkey）当前在线的运行时连接实例数量
- `rate_limit_total_bps` 的单位是 bytes/s，不是 KB/s
- `POST /api/hosts/:id/actions/update` 额外支持 `sync_cert_to_matching_hosts`
- `GET /api/hosts/cert-suggestion` 只有候选证书来源是文件证书且当前 actor 具备 `hosts:update` 权限时，才会直接返回 `cert_file` 和 `key_file`，同时 `can_apply_to_form=true`
- `POST /api/hosts/:id/actions/start` 和 `POST /api/hosts/:id/actions/stop` 可选 `mode`；当前稳定支持 `auto_https`、`auto_cors`、`compat_mode`、`https_just_proxy`、`tls_offload`、`auto_ssl`、`target_is_https`
- `POST /api/hosts/:id/actions/clear` 的 body 需要 `mode`
- `POST /api/hosts/:id/actions/clear` 当前稳定支持 `flow`、`flow_limit`、`time_limit`

## 节点级配置

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| `GET` | `/api/settings/global` | 读取全局配置 |
| `POST` | `/api/settings/global/actions/update` | 修改全局配置 |
| `GET` | `/api/security/bans` | 读取封禁列表 |
| `POST` | `/api/security/bans/actions/delete` | 解除单项封禁 |
| `POST` | `/api/security/bans/actions/delete_all` | 清空全部封禁 |
| `POST` | `/api/security/bans/actions/clean` | 清理过期封禁 |

补充说明：

- `GET /api/settings/global` 只返回节点级入口 ACL 相关字段，不等同完整配置
- `POST /api/settings/global/actions/update` 当前也只修改 `entry_acl_mode` 和 `entry_acl_rules`
- `POST /api/security/bans/actions/delete` 的 body 需要 `key`
- `POST /api/security/bans/actions/delete`、`delete_all`、`clean` 的 `data` 当前稳定只返回动作相关字段；时间戳和 `config_epoch` 统一放在外层 `meta`
- 配置写接口只接受 JSON object body 和 canonical 字段
- 资源 mutation 成功响应的 `data` 当前稳定字段是 `resource`、`action`、`id`、`item`；时间戳和 `config_epoch` 统一放在外层 `meta`

## 相关页面

- 需要同步顺序、认证和作用域说明：看 [管理接口总览](/reference/management-api)
- 需要批量控制、流量写入和导入示例：看 [控制接口与示例](/reference/management-api-http-control)
- 需要通用入口和接入顺序：看 [管理接入入口](/reference/integration/management-api-entrypoints)

