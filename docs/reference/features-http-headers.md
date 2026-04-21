# Header、重定向与 CORS

这一页聚焦域名转发里的 Header 改写、自定义跳转和跨域兼容能力。

## 自动修复 CORS

当浏览器访问服务存在跨域问题时，启用该功能会把当前请求的 `Origin` 写入返回头，减少浏览器侧的 CORS 报错。

根据当前实现：

- 只有请求里带 `Origin` 时才会处理
- 如果后端已经返回 `Access-Control-Allow-Origin`，NPS 不会强制覆盖
- 同时会补上 `Access-Control-Allow-Credentials: true`

这更适合作为临时兼容方案。长期仍建议由后端自己返回精确的 CORS 头。

## Host 修改

当内网站点期望的 `Host` 与公网访问域名不一致时，可以在域名转发里改写请求头中的 `Host` 字段。

这比手工在请求 Header 里改 `Host` 更直接，也更不容易出错。

## 自定义重定向地址

支持对请求进行 `307` 临时重定向。

使用示例：

```text
https://xxx.com${request_uri}
```

### 自定义重定向可用占位符

| 占位符 | 含义 |
| --- | --- |
| `${scheme}` | 请求协议，`http` 或 `https` |
| `${ssl}` | TLS 状态，`on`（HTTPS）或 `off`（HTTP） |
| `${forwarded_ssl}` | 同 `${ssl}` |
| `${host}` | 不带端口的主机名 |
| `${http_host}` | 原始 `Host` 头值 |
| `${server_port}` | 当前入口监听端口 |
| `${remote_addr}` | 客户端真实地址（含端口） |
| `${remote_ip}` | 客户端真实 IP |
| `${remote_port}` | 客户端源端口 |
| `${proxy_add_x_forwarded_for}` | 完整的 `X-Forwarded-For` 链（已追加当前客户端 IP） |
| `${request_uri}` | 完整请求路径和查询字符串 |
| `${uri}` | 请求路径，不含查询字符串 |
| `${args}` | 查询字符串，不含前导 `?` |
| `${query_string}` | 同 `${args}` |
| `${scheme_host}` | 协议和主机拼接，如 `https://example.com` |

## 自定义请求 Header

支持对请求 Header 进行新增或修改，以配合后端服务的需要。

使用示例：

```text
X-Original-URL: ${scheme_host}${request_uri}
X-Client-IP: ${remote_ip}
X-Client-Port: ${remote_port}
X-Forwarded-Proto: ${scheme}
X-Forwarded-Ssl: ${ssl}
```

### 请求 Header 额外支持

| 占位符 | 含义 |
| --- | --- |
| `${http_upgrade}` | 原始请求的 `Upgrade` 头 |
| `${http_connection}` | 原始请求的 `Connection` 头 |
| `${http_range}` | 原始请求的 `Range` 头 |
| `${http_if_range}` | 原始请求的 `If-Range` 头 |
| `${unset}` | 删除该头 |

说明：

- 上一节“自定义重定向可用占位符”里的变量，这里也可以使用
- 如果你要改 `Host`，优先使用独立的“Host 修改”字段

## 自定义响应 Header

支持对 HTTP 响应头进行新增或修改，以配合服务的需要。

使用示例：

```text
Access-Control-Allow-Origin: ${origin}
Access-Control-Allow-Credentials: true
```

### 响应 Header 可用占位符

| 占位符 | 含义 |
| --- | --- |
| `${scheme}` | 请求协议，值为 `http` 或 `https` |
| `${ssl}` | TLS 状态，值为 `on`（HTTPS）或 `off`（HTTP） |
| `${server_port}` | 当前入口监听端口 |
| `${server_port_http}` | HTTP 监听端口 |
| `${server_port_https}` | HTTPS 监听端口 |
| `${server_port_http3}` | HTTP/3 监听端口 |
| `${host}` | 不含端口的原始主机名 |
| `${http_host}` | 原始 `Host` 头部内容 |
| `${remote_addr}` | 客户端 IP 和端口 |
| `${remote_ip}` | 客户端 IP 地址 |
| `${remote_port}` | 客户端源端口 |
| `${request_method}` | 请求方法，例如 `GET`、`POST` |
| `${request_host}` | 请求的 Host |
| `${request_uri}` | 完整的请求 URI，包含查询字符串 |
| `${request_path}` | 请求路径，不含查询字符串 |
| `${uri}` | 与 `${request_path}` 相同 |
| `${query_string}` | 查询字符串，不含 `?` |
| `${args}` | 同 `${query_string}` |
| `${origin}` | 请求头 `Origin` 的值 |
| `${user_agent}` | 请求头 `User-Agent` 的值 |
| `${http_referer}` | 请求头 `Referer` 的值 |
| `${scheme_host}` | 协议和主机拼接 |
| `${status}` | 后端响应状态行，例如 `200 OK` |
| `${status_code}` | 后端响应状态码，例如 `200` |
| `${content_length}` | 响应体长度，未知时为 `-1` |
| `${content_type}` | 响应头 `Content-Type` 的值 |
| `${via}` | 响应头 `Via` 的值 |
| `${date}` | 当前 UTC 时间，格式符合 HTTP Date（RFC 1123） |
| `${timestamp}` | 当前时间戳（秒） |
| `${timestamp_ms}` | 当前时间戳（毫秒） |
| `${unset}` | 删除该头 |

## 相关页面

- 需要域名转发的配置步骤：看 [域名转发](/guide/tunnels/domain-forwarding)
- 需要证书、TLS 或站点保护：看 [证书、TLS 与站点保护](/reference/features-http-tls)
- 需要路径分流和重写：看 [URL 路由、重写与 404](/reference/features-http-routing)
