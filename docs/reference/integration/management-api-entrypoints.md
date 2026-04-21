# 管理接入入口

本文档只描述当前已经实现并对外可用的管理接入入口，适合任意前端、控制台、自动化脚本或外部平台使用。

如果你要看完整资源与控制接口，继续看 [管理接口说明](/reference/management-api)。

## 先按接口找

| 方法 | 路径 | 用途 | 建议 |
| --- | --- | --- | --- |
| `GET` | `/api/system/health` | 返回最小健康状态和运行时标识 | 反代/LB/k8s probe、接入前快速可用性检查 |
| `GET` | `/api/system/discovery` | 返回正式管理路由目录、当前会话信息和能力位 | 所有新接入方的首个请求 |
| `GET` | `/api/auth/session` | 读取当前 session 状态 | 浏览器同源接入、调试和会话恢复 |
| `POST` | `/api/auth/session` | 创建当前 session | 同源页面或需要 cookie session 的前端 |
| `POST` | `/api/auth/session/logout` | 清理当前 session | 当前 session 登出入口 |
| `POST` | `/api/auth/token` | 签发独立访问令牌 | 脚本、外部控制面、前后端分离页面优先使用 |
| `POST` | `/api/auth/register` | 执行本地用户注册 | 仅在允许注册时可用 |
| `POST` | `/api/access/ip-limit/actions/register` | 用 `verify_key` 登记 `ip_limit` 白名单 | 不创建 session，不签发 token |

## 推荐接入顺序

1. 可先调用 `GET /api/system/health` 做可用性预检查
2. 先调用 `GET /api/system/discovery`
3. 如果你要无状态调用，优先走 `POST /api/auth/token`
4. 如果你要 cookie session，走 `GET /api/auth/session` 和 `POST /api/auth/session`
5. 后续统一按 discovery 返回的 `data.routes` 和 `data.actions[]` 调用正式管理接口

补充：

- `GET /api/system/health`
- `GET /api/system/discovery`
- `GET /api/auth/session`
- `POST /api/auth/session`
- `POST /api/auth/session/logout`
- `POST /api/auth/token`
- `POST /api/auth/register`

当前正式 `/api/*` 管理接口响应都会显式返回 `Cache-Control: no-store` 和 `Pragma: no-cache`，不应被浏览器或反代缓存。

## `GET /api/system/discovery`

discovery 是当前正式管理接口的发现入口，返回统一 `data/meta` 结构：

```json
{
  "data": {
    "app": {},
    "session": {},
    "auth": {},
    "actor": {},
    "actions": [],
    "features": {},
    "security": {},
    "routes": {},
    "extensions": {}
  },
  "meta": {
    "request_id": "req_xxx",
    "generated_at": 1712345678,
    "config_epoch": "epoch_xxx"
  }
}
```

常用字段：

| 字段 | 说明 |
| --- | --- |
| `data.session` | 当前是否已认证、用户名、作用域客户端 |
| `data.auth` | 当前登录延迟、TOTP 长度、session 登录 PoW 能力，以及 `client_vkey` 登录是否开启 |
| `data.actor` | 当前 actor 的角色、权限和客户端作用域 |
| `data.actions[]` | 当前会话可用的正式管理动作目录，按 actor 权限、作用域和运行时能力过滤；除了资源 CRUD，也会发布诸如 `clients/actions/kick`、`clients/actions/clear`、`callbacks/queue/actions/replay`、`callbacks/queue/actions/clear`、`webhooks`、`webhooks/{id}/actions/update` 这类控制动作 |
| `data.routes` | 当前 discovery 发布的正式管理路径和认证路径目录；公开入口始终存在，受保护路径按当前 actor 权限过滤 |
| `data.extensions.authorization` | 角色、权限和资源权限目录 |
| `data.extensions.cluster` | 节点能力、协议和平台状态 |

当前 discovery 的认证相关路由字段会包含：

- `session`
- `token`
- `register`
- `logout`
- `access_ip_limit_register`

如果你只是接入新的前端、控制台或平台，通常只需要：

- `session`
- `token`
- `register`
- `logout`
- `access_ip_limit_register`

如果当前 actor 已认证，discovery 会发布 WS 事件订阅相关路径：

- `routes.realtime_subscriptions`

如果当前 actor 具备 webhook 管理权限，discovery 还会发布：

- `routes.webhooks`

补充说明：

- 正式管理接口错误语义固定为：未认证返回 `401 unauthorized`，已认证但无权限返回 `403 forbidden`
- `data.routes` 是 actor-aware 的
  - 匿名 discovery 只发布公开认证、验证码、健康检查和发现入口
  - 资源、控制、batch、WS 等受保护路径只有当前 actor 真正可用时才会出现
- `routes.captcha_new` 只有在 `data.features.open_captcha=true` 时才会出现
- `routes.webhooks` 对应普通 HTTP 的持久化 webhook 管理接口
- `routes.realtime_subscriptions` 只用于 `/api/ws` 的 `request.path`
- callback 失败队列接口继续独立位于：
  `GET /api/callbacks/queue`、`POST /api/callbacks/queue/actions/replay`、`POST /api/callbacks/queue/actions/clear`
- callback queue 相关动作和路由只有在当前 actor 至少可见一个 callback-enabled 平台时才会发布

## `POST /api/auth/token`

这是当前推荐的无状态接入方式，适合：

- 前后端分离页面
- 外部平台
- 自动化脚本
- 服务到服务调用

拿到令牌后，可以通过：

- `Authorization: Bearer <token>`

调用 discovery 和正式管理接口路径。

补充说明：

- 当前 `POST /api/auth/token` 既支持本地管理员 / 本地用户密码登录，也支持 `client_vkey` 登录
- token 入口凭证字段和 session 登录共用同一套 canonical 认证字段：`username` + `password`，可选 `totp`；或单独提交 `verify_key`
- 启用 TOTP 的账号既可以单独提交 `totp`，也可以把 6 位 TOTP 直接追加到 `password` 末尾；如果该账号已启用 TOTP 且配置密码为空，则也可以只提交 `username` + `totp`
- `client_vkey` 登录成功后，返回的身份是独立的 `client` principal，不是普通 `user`
- `client` principal 只保留单客户端作用域
- `client_vkey` 使用真实客户端 `verify_key`；如果该客户端绑定到用户，还会额外校验 owner 用户仍然可登录
- `client` principal 可以管理自己作用域下的隧道和域名资源
- `client` principal 不允许创建客户端，也不允许修改、停用、删除自己的客户端配置
- token 入口和 session / register 一样，也会先经过登录静态来源 ACL
- 该接口只签发 token，不写 session cookie
- `POST /api/auth/token` 签发的 standalone token 当前只支持 `Authorization: Bearer <token>`；不支持 `X-Node-Token`
- 如果后续需要浏览器直接连 `GET /api/ws`，浏览器当前也可以把 standalone token 放到 `Sec-WebSocket-Protocol` 里；这是给浏览器 JS 不能直接设置 `Authorization` 头准备的
- 后续 bearer 请求和 `GET /api/ws` 连接内请求不会只看签名快照；本地 `user` / `client` principal 会按当前仓库状态刷新，主体被停用、过期、owner 约束失效或作用域变化后会立即收敛
- 该接口当前不走验证码和 PoW 防破解链
- 请求体只接受 strict JSON object，成功响应统一为 `data/meta`

## `GET /api/auth/session` 与 `POST /api/auth/session`

只有在你明确需要 cookie session 时，才需要这组接口。

`GET /api/auth/session` 当前返回：

- `data.session`
- `data.actor`
- `meta`

`POST /api/auth/session` 当前直接接受 strict JSON object：

- `username` + `password`
- 可选 `totp`
- 或 `verify_key`
- 若启用验证码，还需要 `captcha_id` + `captcha_answer`
- 当 discovery 发布 `data.auth.pow_enable=true` 时，客户端应支持在收到 `pow_required` 后补交 `pow_nonce` + `pow_bits` 并重试

补充说明：

- 当前 session 登录同样支持本地管理员 / 本地用户密码登录和 `client_vkey` 登录
- 启用 TOTP 的账号既可以单独提交 `totp`，也可以把 6 位 TOTP 直接追加到 `password` 末尾
- 如果该账号已启用 TOTP 且配置密码为空，则可以只提交 `username` + `totp`
- 未启用 TOTP 的账号不能用纯空密码登录
- `verify_key` 只能单独提交，不能和 `username`、`password`、`totp` 混用
- 当前 JSON API 不支持把 TOTP 追加到 `captcha_answer` 末尾；验证码和 TOTP 是两个独立字段
- `GET /captcha/new` 与 `GET /captcha/{id}.png` 是非缓存响应；当验证码校验失败，或任何一次带验证码的登录/注册请求失败后，客户端都应重新获取新的 `captcha_id`
- 当同时启用了验证码且当前会话登录命中 PoW 条件时，后端仍会先校验验证码；验证码失败会直接返回 `invalid_captcha`，不会先要求 PoW
- 绑定用户的客户端走 `client_vkey` 登录时，成功后仍然只拿到 `client` principal，不会扩大为 owner 用户视图
- `pow_bits` 必须与 discovery 发布的 `data.security.pow_bits` 一致；PoW 种子固定为当前登录凭证本身，密码登录使用 `username + "\n" + password + "\n" + totp`，`client_vkey` 登录使用 `verify_key`
- `force_pow=true` 时 session 登录总是要求 PoW；否则只有在 `secure_mode=true` 且命中登录失败封禁状态时才会要求 PoW
- 如果 session 登录缺少或提交了无效 PoW，会返回 `429` 和 `error.code=pow_required`，同时在 `error.details.pow_bits` 里返回当前位数
- 当请求来自受信代理时，认证链会优先读取 `X-Forwarded-For` 的首个有效 IP，再回退 `X-Real-IP`；来源 ACL、失败封禁和自动管理白名单续期都基于这个解析后的真实来源 IP
- 认证成功后，当前来源 IP 会自动登记到管理访问白名单，有效期 2 小时
- 后续带有效 session 或 bearer token 的管理请求也会继续续期这条白名单记录
- `POST /api/auth/session/logout` 会清理当前 session，并返回新的匿名 session 视图

## `POST /api/auth/register`

只有在 `allow_user_register=true` 时可用。

当前请求体只接受 strict JSON object：

- `username`
- `password`
- 若启用验证码，还需要 `captcha_id` + `captcha_answer`

成功响应统一为 `data/meta`。

补充说明：

- 注册入口和 session 登录入口一样，也会先经过登录静态来源 ACL
- 当请求来自受信代理时，来源 ACL 使用认证链解析出的真实来源 IP，而不是代理出口 IP

## `POST /api/access/ip-limit/actions/register`

这个接口只做 `ip_limit` 白名单登记，不创建 session，不签发 token。

当前请求体只接受：

- `verify_key`

它支持真实客户端 `verify_key`，也支持访问登记用途的 `public_vkey` / `visitor_vkey`；`localproxy` 和其他内部隐藏运行时客户端不会进入这条登记链路。

## 推荐做法

- 新前端、控制台、SDK 或平台统一从 `GET /api/system/discovery` 起步
- 需要无状态认证时优先用 `POST /api/auth/token`
- 只有在需要 cookie session 时，再使用 `GET /api/auth/session` 和 `POST /api/auth/session`
- 如果 discovery 发布了 `pow_enable=true`，实现 session 登录的前端应同时实现 PoW 计算与重试
- 资源读写和控制操作统一按 [管理接口说明](/reference/management-api) 里列出的正式管理接口路径执行

## 相关页面

- 需要完整正式管理接口目录：看 [管理接口说明](/reference/management-api)
- 需要 discovery、overview、changes 等发现接口：看 [发现与快照接口](/reference/management-api-http-discovery)
- 需要资源 CRUD：看 [资源接口](/reference/management-api-http-resources)
