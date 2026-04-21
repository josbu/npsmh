# URL 路由、重写与 404

这一页聚焦同一个域名下的路径分流、路径改写和默认错误页。

## 泛域名

支持泛域名。

例如把 `host` 设置为 `*.proxy.com` 后，`a.proxy.com`、`b.proxy.com` 等都可以命中同一条规则。

## URL 路由

支持按 URL 路径把同一个域名转发到不同的内网目标。

这项能力可在 Web 管理界面或客户端配置文件中设置。例如：

```ini
[web1]
host=a.proxy.com
target_addr=127.0.0.1:7001
location=/test

[web2]
host=a.proxy.com
target_addr=127.0.0.1:7002
location=/static
```

效果如下：

- `a.proxy.com/test` 转发到 `127.0.0.1:7001/test`
- `a.proxy.com/static/bg.jpg` 转发到 `127.0.0.1:7002/static/bg.jpg`

根据当前实现，匹配顺序更接近下面的规则：

- 路径更长的 `location` 优先
- 当路径长度相同时，更具体的域名优先
- 同等条件下，精确域名优先于泛域名
- 空 `location` 会按 `/` 处理

这意味着：

- `/static/` 会先于 `/`
- `api.example.com` 会先于 `*.example.com`

## URL 重写

填写后会自动替换请求路径里 **URL 路由** 的前缀，适合前端访问路径与后端实际路径不一致的场景。

默认情况下，NPS 会附带 `X-Original-Path` 请求头，方便后端识别浏览器请求的原始路径。
如果你开启了兼容模式，不要依赖这个头。

例如：

- 当 **URL 路由** 配置为 `/path/`，当 **URL 重写** 配置为 `/`。请求 `xx.com/path/index.html` 将返回 `127.0.0.1:80/index.html`
- 当 **URL 路由** 配置为 `/xml`，当 **URL 重写** 配置为 `/path/list.xml`。请求 `xx.com/xml` 将下载 `127.0.0.1:80/path/list.xml`
- 当 **URL 路由** 配置为 `/ws`，当 **URL 重写** 配置为 `/websocket`。请求 `xx.com/ws` 将转发到 `127.0.0.1:80/websocket`

## 404 页面配置

域名转发支持自定义默认错误页。

根据当前实现：

- 默认路径是 `web/static/page/error.html`
- 也可以通过 `error_page` 指向别的文件
- 这里更适合放单文件错误页，不适合继续依赖额外静态资源目录

## 相关页面

- 需要域名转发的创建方式：看 [域名转发](/guide/tunnels/domain-forwarding)
- 需要 Host、Header 和重定向：看 [Header、重定向与 CORS](/reference/features-http-headers)
- 需要证书和 TLS 方式：看 [证书、TLS 与站点保护](/reference/features-http-tls)
