# 证书、TLS 与站点保护

这一页聚焦域名转发里和证书、TLS 处理方式、站点基础保护有关的能力。

## 站点保护

域名转发支持用 HTTP Basic Auth 保护站点。

适合：

- 测试环境
- 临时演示环境
- 只需要一层简单密码保护的管理页面

可在 Web 管理界面或客户端配置文件中设置。

注意：

- 这更适合“挡一层门”，不适合替代正式身份系统
- 当前更适合保护普通 HTTP 请求，不建议把它当作 WebSocket 等场景的唯一保护手段

## 自动证书

启用后，如果 NPS 可以正常使用 `80` 或 `443` 端口，就能自动申请并续签证书。

更适合这些前提：

- 域名已经解析到 NPS 所在服务器
- `80` 或 `443` 没有被别的程序占用
- 你希望由 NPS 统一管理证书

补充：

- `auto_ssl` 负责“证书从哪里来”
- `auto_https` 负责“HTTP 是否跳到 HTTPS”
- 这两个开关彼此独立

## 自动 HTTPS（301）

启用后，浏览器访问 HTTP 时会自动永久跳转到 HTTPS。

这适合：

- 站点已经能稳定提供 HTTPS
- 希望外部入口始终统一为 HTTPS

注意：

- `auto_https` 只做重定向
- 它不会自动替你决定 TLS 由谁终止

## TLS 直通（由后端解密）

NPS 会根据首个 TLS 握手里的 **SNI** 选择目标，但不会在前端解密，数据会原样转发到后端，由后端自己处理证书和 TLS 握手。

适合：

- 后端程序自己管理证书
- 需要把 TLS 原样交给后端
- 你只想让 NPS 负责按域名分流

注意：

- 这种方式依赖 SNI
- 对应的核心开关是 `https_just_proxy`
- 当后端开启 HTTP/2，且多个站点共享同一证书或同一 IP 时，浏览器可能复用连接，导致站点内容错位

## TLS 终止（解密转发）

NPS 会根据 **SNI** 选择目标，在前端完成 TLS 握手和证书处理，再把解密后的明文流量转发给后端。

适合：

- 你希望证书统一放在 NPS
- 后端只想处理明文 HTTP 或明文 TCP
- 你要在前端统一做 HTTPS 接入

注意：

- 对应的核心开关是 `tls_offload`
- 开启后，后端不再接收 TLS 原始流量
- 如果后端本身还要求 HTTPS，请不要把它当成 TLS 直通使用

## HTTPS 后端反向代理

如果你希望：

- 入口由 NPS 统一接收 HTTP / HTTPS
- 但后端网站本身仍然是 HTTPS

那么应该使用：

```text
target_is_https=true
```

这和 `https_just_proxy`、`tls_offload` 都不是一回事。

当前实现下：

- NPS 先处理前端请求
- 再主动以 HTTPS 连接后端
- 适合标准 HTTP 反向代理和 WebSocket 场景

## 相关页面

- 需要证书部署、真实 IP 或前置反向代理：看 [HTTPS 与反向代理](/guide/server/https-and-proxy)
- 需要域名转发的创建方式：看 [域名转发](/guide/tunnels/domain-forwarding)
- 需要 URL 路由和错误页：看 [URL 路由、重写与 404](/reference/features-http-routing)
