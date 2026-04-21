# 域名转发

域名转发是 NPS 的 HTTP/HTTPS 反向代理能力，适合暴露网站、Webhook、管理后台和调试环境。

## 它是什么

- 按域名、协议和路径把请求转发到内网 Web 服务
- 更像 Nginx 或 Caddy 的反向代理能力
- 不是 DNS 服务器

## 适合什么场景

- 内网网站对外发布
- 微信公众号、小程序、本地前端联调
- Webhook 回调
- 按域名区分多个站点

## 使用前准备

1. 公网服务器已经运行 NPS
2. 域名已经解析到 NPS 所在服务器
3. `http_proxy_port` 或 `https_proxy_port` 已启用

## Web 管理界面最常用字段

| 字段 | 作用 |
| --- | --- |
| 域名 | 外部访问使用的域名 |
| 内网目标 | 实际后端地址，可填写多个用于负载均衡 |
| 模式 | 外部访问使用 `http`、`https` 或 `all` |
| 目标类型 | 后端是 `HTTP` 还是 `HTTPS` |
| URL 路由 | 按路径分流 |
| URL 重写 | 调整转发到后端的路径 |
| Host 修改 | 修改转发时的 `Host` 头 |

## 最小示例

假设：

- 公网服务器 IP 是 `1.1.1.1`
- `*.proxy.com` 已解析到 `1.1.1.1`
- 两个内网站点分别是 `127.0.0.1:81` 和 `127.0.0.1:82`

配置两条域名转发：

- `a.proxy.com` -> `127.0.0.1:81`
- `b.proxy.com` -> `127.0.0.1:82`

完成后访问：

- `http://a.proxy.com`
- `http://b.proxy.com`

## `npc.conf` 示例

```ini
[common]
server_addr=1.1.1.1:8024
vkey=123

[web1]
host=a.proxy.com
target_addr=127.0.0.1:8080,127.0.0.1:8082
host_change=www.proxy.com
header_set_proxy=nps
```

## HTTPS 怎么处理

有两种常见方式：

- 由 NPS 持有证书并终止 HTTPS
- 由后端自己处理 HTTPS，NPS 只做转发

如果后端本身是 HTTPS 服务，需要把目标类型设置为 `HTTPS`。

更完整的证书、反代和真实 IP 说明见 [HTTPS 与反向代理](/guide/server/https-and-proxy)。

## 常见能力

- 泛域名
- URL 路由
- URL 重写
- 自定义请求头和响应头
- 自动 HTTPS
- 自动 CORS
- 负载均衡
- 站点保护

这些高级能力在 [站点与 HTTP](/reference/features-http) 中有更完整说明。

## 注意事项

- 域名转发只适合 Web 服务，不适合 SSH、数据库等原始 TCP 服务
- “模式”描述的是外部访问协议，不是后端协议
- 后端是 HTTPS 时，记得设置目标类型为 `HTTPS`
- 如果需要保留真实 IP，建议同时阅读 [HTTPS 与反向代理](/guide/server/https-and-proxy)
