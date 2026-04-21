# 功能清单：访问控制与限制

这一页集中放 ACL、IP 限制、代理目标限制和配额能力。

如果你要看日志、pprof 或健康检查，去看 [运维与调试](/reference/features-ops)。

## 混合代理黑/白名单

混合代理（HTTP/SOCKS5）支持配置目标黑/白名单，启用后 Socks5 不支持 UDP 代理。

目标 ACL 当前支持：

- 所有出站链路通用的 IP 规则：精确 IP、CIDR、IPv4 前缀通配符（如 `10.*`）、`geoip:xx`
- 仅混合代理额外支持的域名规则：`full:example.com`、`*.example.com`、`*example.com`、`keyword:`、`regexp:`、`dotless:`、`geosite:xx`
- 用户目标 ACL 与隧道目标 ACL 会叠加，任一侧拒绝即拒绝

## 流量限制

支持客户端级总流量限制。

当客户端累计入口流量与出口流量达到上限后：

- 域名转发会返回 `404`
- 其他代理或隧道会拒绝新的访问

使用前需要在 `nps.conf` 中启用 `allow_flow_limit=true`。

## 带宽限制

支持客户端级带宽限制，统计口径为入口与出口总和。

使用前需要在 `nps.conf` 中启用 `allow_rate_limit=true`。

## 时间限制

支持客户端级到期时间限制，到期后会拒绝新的访问。

使用前需要在 `nps.conf` 中启用 `allow_time_limit=true`。

补充说明：

- 留空表示不限制
- 支持多种日期格式和时间戳
- 实际解析会受系统时区影响
- 示例：`2025-01-01`

## 来源 IP 黑白名单

当前访问控制能力分为两类：

- **静态来源 ACL**：全局、用户、客户端、隧道、域名转发（Host）均支持关闭、白名单、黑名单三种模式
- **动态登录封禁**：只按用户名 / IP 统计失败次数，独立于静态 ACL

来源 ACL 规则支持：

- 精确 IP
- CIDR（IPv4 / IPv6）
- IPv4 前缀通配符（如 `10.*`）
- `geoip:xx` / `geoip:private`

规则按预编译 matcher 判定，避免热路径重复排序和重复解析。
来源判定顺序固定为 `global -> user -> client -> tunnel/host`。

## 端口白名单

为了避免公网端口被滥用，可以在 `nps.conf` 中用 `allow_ports` 限制允许开放的端口范围。

留空表示不限制。格式示例：

```ini
allow_ports=9001-9009,10001,11000-12000
```

## 限制 IP 访问

如果把 SSH 这类高风险端口直接暴露到公网，可以配合这个功能只允许已经登记的公网 IP 访问。

**使用方法：** 在 `nps.conf` 中设置 `ip_limit=true`。开启后，只有登记过的公网 IP 才能访问相应入口。

**IP 注册：**

方式一：

在需要访问的机器上运行客户端：

```bash
./npc register -server=ip:port -vkey=PUBLIC_KEY_OR_CLIENT_VKEY -time=2
```

`time` 表示有效小时数。
例如 `time=2` 时，从当前时间起 2 小时内，本机公网 IP 可以访问对应入口。

方式二：

成功通过管理接口认证后，当前来源 IP 也会自动获得 2 小时的允许访问权限。

当前自动登记范围包括：

- 管理员登录
- 普通用户登录
- `client_vkey` 登录
- 后续带有效 session 或 bearer token 的管理请求续期

**注意：** 公网 IP 可能变化，同一网络里的多台机器也可能共用同一个公网 IP，因此要合理设置有效期。

过期的注册 IP 会在访问时和后台定时清理时自动删除，不会无限累积。

## 客户端最大连接数

为避免单个客户端占用过多长连接，可以为每个客户端设置最大连接数。

该能力对这些类型生效：

- `mixProxy`（兼容旧数据里的 `httpProxy` / `socks5`）
- 域名转发
- `tcp`
- `udp`
- `secret`

补充说明：

- 上面是当前主线里最常见的生效入口，不是唯一的内部实现枚举

使用前需要在 `nps.conf` 中启用 `allow_connection_num_limit=true`。

## 客户端最大隧道数限制

支持限制单个客户端可创建的隧道数量。

如需开启，请在 `nps.conf` 中设置 `allow_tunnel_num_limit=true`。

## 相关页面

- 需要服务端 ACL 和运行开关：看 [访问控制与运行](/reference/server-config-runtime)
- 需要管理接口里的 ACL 相关字段：看 [资源接口](/reference/management-api-http-resources)
