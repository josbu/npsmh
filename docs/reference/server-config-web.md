# 服务端配置：Web、HTTP 与安全

这一页集中放 Web 管理后台、登录保护、真实 IP、前置代理和 HTTP 安全相关配置。

本页涉及端口、初始账号密码等“默认/示例值”时，优先指仓库当前自带 `conf/nps.conf`。

如果你要找桥接端口、代理端口和 P2P 端口，去看 [入口端口与桥接](/reference/server-config-ports)。

## 1. Web 管理面板相关

| 名称 | 说明 |
| --- | --- |
| `web_port` | Web 管理端口（仓库示例配置常见为 `8081`） |
| `web_ip` | Web 管理界面监听地址（默认 `0.0.0.0`，监听所有 IP） |
| `web_host` | Web 界面域名（仓库示例配置写为 `a.o.com`，端口复用时访问管理页面的地址） |
| `web_username` | Web 管理员账号（仓库示例配置初始值为 `admin`） |
| `web_password` | Web 管理员密码（仓库示例配置初始值为 `123`，建议首次登录后立即修改） |
| `web_open_ssl` | 是否启用 Web 面板 HTTPS（默认 `false`，启用需配置证书） |
| `web_cert_file` | Web HTTPS 证书文件路径 |
| `web_key_file` | Web HTTPS 证书密钥文件路径 |
| `web_base_url` | Web 管理主路径；留空表示根路径，配置 `/nps` 时表示挂到子路径；它会作用于整套 Web 路由，包括 `/api/` 前缀下的管理接口、`/captcha/` 路由、静态资源，以及 session cookie 的 `Path`；如果写成 `nps`、`/nps/`、`ops/platform/admin/` 或带多余斜杠，会自动规范化成 canonical 前缀 |
| `web_close_on_not_found` | 未命中 Web 路径时尽量直接断开连接，而不是返回普通页面 |
| `head_custom_code` | 插入到管理页面头部的自定义代码 |
| `open_captcha` | 是否启用验证码 |
| `force_pow` | 是否对 session 登录强制要求 PoW |
| `pow_bits` | PoW 验证位数（默认 `20`） |
| `login_ban_time` | 两次登录请求最小间隔（秒，默认 `5`） |
| `login_ip_ban_time` | IP 维度失败次数重置周期（秒，默认 `180`） |
| `login_user_ban_time` | 用户名维度失败次数重置周期（秒，默认 `3600`） |
| `login_max_fail_times` | 最大允许登录失败次数（默认 `10`） |
| `login_max_body` | 登录请求体最大大小（字节，默认 `1024`） |
| `login_max_skew` | 时间戳偏移容忍度（毫秒，默认 `300000`，即 5 分钟） |
| `login_acl_mode` | 登录静态来源 ACL 模式（`0` 关闭，`1` 白名单，`2` 黑名单） |
| `login_acl_rules` | 登录静态来源 ACL 规则；支持逗号或换行分隔 |
| `totp_secret` | 两步验证密钥；启用后需要提供有效 6 位 TOTP。默认仍会校验 `web_password`；如果 `web_password` 为空，则可仅用 TOTP 登录 |
| `allow_x_real_ip` | 允许从受信代理转发的真实来源 IP 头获取真实 IP；当前会优先读取 `X-Forwarded-For`，再回退 `X-Real-IP` |
| `trusted_proxy_ips` | 受信任的代理服务器 IP 地址（多个用逗号分隔） |

补充说明：

- `login_acl_rules` 支持精确 IP、CIDR、IPv4 前缀通配符（如 `10.*`）、`geoip:xx`、`geoip:private`
- `login_acl_mode=1` 表示仅允许命中规则的来源访问登录入口，`login_acl_mode=2` 表示命中规则的来源会被拒绝
- 登录静态来源 ACL 与登录失败次数封禁是两套独立机制，前者先判定，后者再根据失败记录生效；`POST /api/auth/session`、`POST /api/auth/token` 和 `POST /api/auth/register` 都会走这条静态来源 ACL
- 启用 `totp_secret` 后，可以单独提交 TOTP，也可以把 6 位 TOTP 直接追加到密码末尾
- 只有启用了 `totp_secret` 的账号才允许空密码登录；未启用 TOTP 时，纯空密码会被拒绝
- `pow_bits` / `force_pow` 当前只作用于 `POST /api/auth/session`
- `force_pow=false` 时，PoW 只会在 `secure_mode=true` 且命中登录失败封禁状态时要求提交
- 如果 Web 管理后台放在反向代理的子路径下，`web_base_url` 需要和代理路径保持一致；服务端会自动补齐起始 `/` 并去掉末尾 `/`
- 当 `web_base_url` 不为空时，session cookie 也会自动收口到同一前缀，避免同域不同前缀实例互相冲突
- 当请求来自 `trusted_proxy_ips` 中的代理，或携带了合法 `X-NPS-Http-Only` 口令时，管理认证链会优先使用 `X-Forwarded-For` 的首个有效 IP；若没有，再使用 `X-Real-IP`

## 2. 反向代理与安全

| 名称 | 说明 |
| --- | --- |
| `http_add_origin_header` | 是否添加真实 IP 头（`true` 或 `false`） |
| `x_nps_http_only` | 前置代理和 NPS 之间的共享口令，用于识别可信 `X-NPS-Http-Only` 请求 |

补充说明：

- `http_add_origin_header` 主要影响业务 HTTP / HTTPS 反向代理，不是管理后台登录开关
- `x_nps_http_only` 常用于前置 Nginx / Caddy 之后，避免 NPS 再对 HTTP 请求重复做 HTTPS 跳转

### Nginx 代理示例

```nginx
server {
    listen 443 ssl;
    server_name example.com;

    ssl_certificate /path/to/cert.pem;
    ssl_certificate_key /path/to/key.pem;

    location / {
        proxy_pass http://127.0.0.1:80;
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

## 3. 独立前端相关

| 名称 | 说明 |
| --- | --- |
| `web_standalone_allowed_origins` / `standalone_allowed_origins` | 独立前端允许访问的来源列表，逗号分隔，支持 `*` |
| `web_standalone_allow_credentials` / `standalone_allow_credentials` | 独立前端是否允许浏览器携带凭据 |
| `web_standalone_token_secret` / `standalone_token_secret` | 独立前端访问 token 的签名密钥 |
| `web_standalone_token_ttl_seconds` / `standalone_token_ttl_seconds` | 独立前端访问 token 的有效期（秒） |

这些配置只在你把前端和服务端分开部署时才需要关注。

## 4. 相关页面

- 需要 HTTPS、证书、真实 IP 和前置代理的操作步骤：看 [HTTPS 与反向代理](/guide/server/https-and-proxy)
- 需要当前管理接口的认证入口：看 [管理接入入口](/reference/integration/management-api-entrypoints)
- 需要基础项与密钥：看 [基础项与密钥](/reference/server-config-basics)
