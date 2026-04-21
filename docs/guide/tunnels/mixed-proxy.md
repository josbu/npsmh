# 混合代理

混合代理用于把“访问内网的能力”暴露给外部，而不是只暴露一个固定业务端口。

这一类模式包含三种常见用法：

- HTTP 代理
- Socks5 代理
- 一个端口同时提供 HTTP 和 Socks5 的混合代理

需要先明确的一点是：它更像“远程代理入口”，不是自动接管整台机器所有流量的 VPN。
只有你显式设置为走代理的浏览器、命令行工具或应用流量，才会经过 NPS。

## 什么时候选它

- 你希望通过代理方式访问多个内网目标
- 你希望浏览器、开发工具、系统代理统一走一个入口
- 你希望对可访问目标做黑白名单限制

## 三种模式的区别

| 模式 | 适合什么场景 |
| --- | --- |
| HTTP 代理 | 浏览器或只支持 HTTP 代理的程序 |
| Socks5 代理 | 更通用的代理能力 |
| 混合代理 | 一个端口同时提供 HTTP 和 Socks5 |

## Web 管理界面最常用字段

| 字段 | 作用 |
| --- | --- |
| 监听端口 | 代理服务的公网端口 |
| HTTP 代理 | 是否启用 HTTP 代理能力 |
| Socks5 代理 | 是否启用 Socks5 代理能力 |
| 认证信息 | 代理访问用户名和密码 |
| 目标 ACL | 限制允许访问的目标地址或域名 |

## 最小示例

当前 Web 管理界面通常是创建一条混合代理规则，再按需勾选：

- 只启用 HTTP 代理
- 只启用 SOCKS5 代理
- 同时启用 HTTP 和 SOCKS5

旧版界面或旧数据里，你也可能看到单独的 HTTP 代理、SOCKS5 类型。

### Socks5 代理

假设你希望把 `1.1.1.1:8003` 作为 Socks5 代理：

1. 创建一条混合代理规则，并只启用 SOCKS5
2. 监听端口填写 `8003`
3. 在外部设备上把 SOCKS5 代理指向 `1.1.1.1:8003`

常见用法：

- 浏览器手动代理
- `curl --proxy socks5://1.1.1.1:8003`
- Git、包管理器、IDE 代理设置

### HTTP 代理

假设你希望把 `1.1.1.1:8004` 作为 HTTP 代理：

1. 创建一条混合代理规则，并只启用 HTTP
2. 监听端口填写 `8004`
3. 在外部设备上把 HTTP 代理指向 `1.1.1.1:8004`

## `npc.conf` 示例

### 混合代理

```ini
[mix]
mode=mixProxy
server_port=19009
http_proxy=true
socks5_proxy=true
multi_account=conf/multi_account.conf
```

### 仅 SOCKS5

```ini
[socks5]
mode=mixProxy
server_port=19009
http_proxy=false
socks5_proxy=true
multi_account=conf/multi_account.conf
```

### 仅 HTTP 代理

```ini
[http]
mode=mixProxy
server_port=19004
http_proxy=true
socks5_proxy=false
```

补充说明：

- 当前更推荐统一使用 `mode=mixProxy`，再通过 `http_proxy` / `socks5_proxy` 控制能力
- 旧写法 `mode=httpProxy`、`mode=socks5` 当前仍可解析，但保存后通常会统一显示为 `mixProxy`

## 目标 ACL

混合代理支持目标黑白名单，适合限制“代理可以访问哪里”。

示例条目：

- `1.2.3.4`
- `10.0.0.0/8`
- `full:example.com`
- `*.example.org`
- `keyword:intranet`
- `geoip:private`
- `geosite:cn`

说明：

- IP 规则始终生效
- 域名规则、`geosite:xx`、域名通配只在混合代理里生效
- 用户目标 ACL 与隧道目标 ACL 会叠加
- 只要启用任一目标 ACL，Socks5 的 UDP ASSOCIATE 就会被直接拒绝

## 注意事项

- 为了安全，建议开启用户名密码认证
- 这类代理不是透明组网；没有配置代理的应用不会自动走这条链路
- 使用 SOCKS5 时，端口扫描表现可能和普通业务端口不同
- 如果你只需要暴露单个业务服务，优先考虑 TCP、UDP 或域名转发
