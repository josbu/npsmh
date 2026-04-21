# HTTPS 与反向代理

这一页只回答四类实际问题：

1. 外部访问 `443` 时，TLS 应该由谁处理
2. 后端本身是 HTTP、HTTPS 还是原始 TLS 服务时，域名转发该怎么选
3. Web 管理后台怎样启用 HTTPS 或放到 Nginx / Caddy 后面
4. 真实 IP 应该通过哪种方式传给后端

这一页聚焦部署选择和操作路径，不展开所有配置字段的精确定义。需要逐项查字段时，转到 [Web、HTTP 与安全](/reference/server-config-web) 和 [入口端口与桥接](/reference/server-config-ports)。

## 1. 先用这张表选模式

| 你想得到的结果 | 域名转发关键开关 | NPS 是否解密 TLS | 后端应该接收什么 |
| --- | --- | --- | --- |
| 后端自己处理证书，NPS 只按域名分流 | `https_just_proxy=true` | 否 | 原始 TLS |
| NPS 处理 TLS 握手，再把明文流量转给后端 | `tls_offload=true` | 是 | 明文 TCP 或明文 HTTP |
| NPS 作为 HTTP/HTTPS 反向代理，后端本身仍是 HTTPS | `target_is_https=true` | 是 | HTTPS |
| 浏览器访问 HTTP 时自动跳到 HTTPS | `auto_https=true` | 仅重定向，不处理业务 TLS | 不涉及 |

需要先分清一点：

- `https_just_proxy`、`tls_offload`、`target_is_https` 解决的是三种不同链路
- 它们不是同一个意思的不同叫法
- `auto_https` 只是 `301` 跳转，不等于证书配置

## 2. 三种常见易混淆的 HTTPS 链路

### 模式一：后端自己处理 TLS

适合：

- 后端已经自己持有证书
- 你只想让 NPS 根据 SNI 做分流
- 你不想在 NPS 前端解密

关键开关：

```text
https_just_proxy=true
```

当前实现下：

- NPS 读取 TLS ClientHello，只拿 `SNI` 做规则匹配
- 匹配到域名后，TLS 数据会原样转发给后端
- `target_is_https` 在这个模式下不参与转发决策

注意：

- 这个模式依赖 `SNI`
- 如果后端启用了 HTTP/2，且浏览器复用连接，可能出现偶发串站风险

### 模式二：NPS 处理 TLS，再转发明文流量

适合：

- 想把证书统一放在 NPS
- 后端不想自己做 TLS
- 后端不是传统 HTTP 反向代理，而是接收原始明文 TCP

关键开关：

```text
tls_offload=true
```

当前实现下：

- NPS 会先完成 TLS 握手
- 再把解密后的明文数据直接转发给后端目标
- 后端此时不能再要求 TLS

这个模式常被误解成“转发到 HTTPS 后端”，其实不是。

### 模式三：NPS 终止 HTTPS，再反向代理到 HTTPS 后端

适合：

- 外部入口由 NPS 统一接 HTTPS
- 内网后端本身仍然是 HTTPS 站点
- 你需要标准 HTTP 反向代理能力，例如路径路由、Header 改写、WebSocket

关键开关：

```text
target_is_https=true
```

当前实现下：

- NPS 先在前端终止 TLS
- 进入 HTTP 反向代理逻辑后，再主动以 HTTPS 连接后端
- 这个模式下，后端看到的是标准 HTTPS 反代请求，不是原始 TLS 透传

## 3. 证书从哪里来

### 手工证书

可以在域名转发里直接填写或上传证书与私钥。

### 自动证书

适合：

- 域名已经解析到当前 NPS
- `80` 或 `443` 可用于证书申请
- 你希望由 NPS 自动申请和续签

关键开关：

```text
auto_ssl=true
```

如果 `80` / `443` 端口条件不标准，但你仍希望强制自动申请，可再配：

```ini
force_auto_ssl=true
```

### 默认证书与证书复用

当前实现下，NPS 在域名未单独提供证书时，会按下面思路寻找：

1. 当前域名自己的手工证书
2. 可复用的已有证书
3. `https_default_cert_file` / `https_default_key_file`
4. 如果仍没有可用证书，则回退到后端 TLS 处理路径

所以：

- 想统一兜底，可以配置默认证书
- 不确定证书来源时，不要只看页面开关，要同时确认实际证书文件和域名设置

## 4. `auto_https` 只负责跳转

```text
auto_https=true
```

作用只有一个：

- 当访问者走 HTTP 进入时，NPS 返回 `301`，把请求跳到 HTTPS

它不负责：

- 申请证书
- 生成证书
- 决定 TLS 由谁终止

也就是说：

- `auto_https` 和 `auto_ssl` 往往一起出现
- 但它们不是互相替代关系

## 5. Web 管理后台启用 HTTPS

如果你希望管理后台本身走 HTTPS，使用：

```ini
web_open_ssl=true
web_cert_file=conf/server.pem
web_key_file=conf/server.key
```

启用后访问：

```text
https://<server-ip>:<web-port>
```

如果管理后台不是挂在站点根路径，而是通过子路径访问，还要同时设置：

```ini
web_base_url=/nps
```

## 6. NPS 放在 Nginx 或 Caddy 后面

适合：

- 前面已有 Nginx / Caddy
- 你希望把公网 `80/443` 先交给前置代理
- 证书、WAF、缓存或统一网关策略在前置层处理

常见做法是：

1. NPS 监听本地端口，例如 `8010`
2. Nginx / Caddy 监听公网 `80/443`
3. 反向代理到 NPS 本地 HTTP 入口

例如：

```ini
http_proxy_port=8010
```

Nginx 示例：

```nginx
server {
    listen 80;
    server_name _;

    location / {
        proxy_pass http://127.0.0.1:8010;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $http_connection;
        proxy_set_header Host $http_host;
        proxy_set_header X-NPS-Http-Only "password";
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_redirect off;
        proxy_buffering off;
    }
}
```

这里的关键点：

- `X-NPS-Http-Only` 需要和 `x_nps_http_only` 配置一致
- 它的作用是告诉 NPS：这次请求来自可信前置代理，不要再按自己的 HTTP 到 HTTPS 跳转逻辑重复处理

如果你希望 NPS 信任前置代理传来的来源地址，再配：

```ini
allow_x_real_ip=true
trusted_proxy_ips=127.0.0.1
```

Caddy 示例：

```Caddyfile
nps.example.com {
    reverse_proxy 127.0.0.1:8010 {
        header_up X-NPS-Http-Only "password"
    }
}
```

## 7. 真实 IP 怎么传

### HTTP 反向代理场景

如果域名转发本质上是 HTTP/HTTPS 反代，优先使用：

```ini
http_add_origin_header=true
```

启用后，NPS 会按当前实现补充常见头，例如：

- `X-Forwarded-Proto`
- `X-Real-IP`
- `X-Forwarded-Host`

如果前面已经有可信代理，并携带了 `X-Forwarded-For`，NPS 也会继续追加链路。

### 后端明确支持 Proxy Protocol

如果你的后端支持 Proxy Protocol，可以在目标上启用它。
这适用于：

- TCP 隧道
- UDP 隧道
- 域名转发

这时后端拿到的是 Proxy Protocol 头，而不是只依赖 HTTP Header。

### 管理后台在反代后面

`allow_x_real_ip` 和 `trusted_proxy_ips` 更适合：

- Web 管理后台登录来源识别
- 审计
- 风控

它们不是业务站点真实 IP 透传的主要开关。

## 8. 代理到服务端本地

如果业务就跑在 NPS 这台机器上，而不是独立的 NPC 客户端上，可以开启：

```ini
allow_local_proxy=true
```

适合：

- NPS 服务器本机就有网站或本地服务
- 你希望域名转发直接连 `127.0.0.1:<port>`

## 9. 相关页面

- 需要具体创建域名转发：看 [域名转发](/guide/tunnels/domain-forwarding)
- 需要查询配置项：看 [Web、HTTP 与安全](/reference/server-config-web) 和 [入口端口与桥接](/reference/server-config-ports)
- 需要看证书、TLS 和功能边界：看 [证书、TLS 与站点保护](/reference/features-http-tls)
