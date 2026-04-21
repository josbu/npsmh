# NPS 管理接口说明

本文档只说明当前节点控制面的正式对外接口，适合外部管理平台、多节点统一控制面和自动化同步程序使用。

如果你只是部署一台普通 NPS 服务端，通常不需要先看这一页。单节点运维优先看 [服务端运维](/guide/server/operations)；新的管理前端、控制台或工具优先看 [管理接入入口](/reference/integration/management-api-entrypoints)。

这一页属于 [接口与集成](/reference/integration/README) 部分。

下文示例默认 `web_base_url` 为空。
如果你在 `nps.conf` 中设置了 `web_base_url=/nps`，实际路径会变成 `/nps/api/` 前缀下的对应管理接口。
这个前缀作用于整套 Web 路由，不只是页面路径，正式 `/api/` 前缀下的管理接口、`/captcha/` 路由、静态资源和 discovery 发布的路径都会整体带上该前缀。
如果配置写成 `nps`、`/nps/`、`ops/platform/admin/` 或带多余斜杠，服务端会自动规范化成 `/nps`、`/ops/platform/admin` 这类 canonical 前缀再注册正式路由和发布 discovery 路径。

## 1. 基本原则

- 节点始终以本地数据为准
- 正式对外管理协议统一使用 `/api/` 前缀
- 节点既可以独立运行，也可以被外部平台管理
- 外部平台写入节点后，以节点返回结果为最终结果
- 新的前后端分离页面或外部前端建议先用 `GET /api/system/discovery` 发现可用路径，再调用 discovery 发布的正式管理路径
- discovery 里的 `routes` 和 `actions` 都是 actor-aware 的；受保护入口只会在当前身份真正可用时出现
- discovery 里的 `data.extensions.cluster.management_platforms` 也是 actor-aware 的；匿名、本地普通用户和本地 `client` principal 不会拿到平台状态清单
- 正式管理错误语义固定为：未认证返回 `401 unauthorized`，已认证但无权限返回 `403 forbidden`

## 2. 认证方式

当前管理接口支持三类访问方式：

- 本地 Web 登录后的 session
- standalone token
- 管理平台 token

本地登录成功后，当前来源 IP 会自动登记到管理访问白名单，有效期 2 小时。后续带有效 session 或 bearer token 的管理请求也会继续续期这条白名单记录。

一般建议这样理解：

- 本地调试、浏览器联调：使用 session
- 前后端分离页面、脚本、服务到服务调用：使用 standalone token
- 外部平台、多节点控制面：使用平台 token
- 存活探针、反代健康检查：使用 `GET /api/system/health`

当前入口形态是：

- 探针入口：`GET /api/system/health`
- 发现入口：`GET /api/system/discovery`
- token 入口：`POST /api/auth/token`
- session 状态与登录入口：`GET /api/auth/session`、`POST /api/auth/session`
- session 登出入口：`POST /api/auth/session/logout`
- 注册入口：`POST /api/auth/register`
- `ip_limit` 白名单登记入口：`POST /api/access/ip-limit/actions/register`

当前正式 `/api/*` 管理接口响应都会显式返回 `Cache-Control: no-store` 和 `Pragma: no-cache`，避免被浏览器或反代缓存成过期视图。

callback / webhook 和 WS 事件回调当前都已经可用：

- 节点会按 `platform_callback_*` 配置向 `callback_url` 投递平台级 callback
- 当前管理 API 也已经提供运行时 webhook 注册、修改、启停、删除接口：
  `GET /api/webhooks`、`GET /api/webhooks/{id}`、`POST /api/webhooks`、`POST /api/webhooks/{id}/actions/update`、`POST /api/webhooks/{id}/actions/status`、`POST /api/webhooks/{id}/actions/delete`
  这组接口只对本地管理员和 `platform_admin` 开放；普通用户、客户端或 delegated `platform_user` 统一使用 `/api/ws` 里的临时订阅
- 当前 `/api/ws` 也已经提供连接内临时事件订阅；正式 HTTP 路由里并不存在 `/api/realtime/subscriptions...` 这一组接口。
  它们只作为 WS `request.path` 的订阅路径使用，见 [实时通道与回调](/reference/management-api-realtime)。
- callback 失败队列仍只针对配置驱动的 `platform_callback`

本地登录身份又分为三类：

- `admin`
- `user`
- `client`

其中 `client` 是 `client_vkey` 登录成功后得到的独立 principal，不再混同为普通 `user`。无论客户端是否绑定用户，登录后都只保留单客户端作用域；如果客户端绑定了用户，还会额外继承 owner 用户的可登录状态约束。

远程认证分两类：

- standalone token
  - 来源：`POST /api/auth/token`
  - 只支持：`Authorization: Bearer <token>`
  - bearer 请求和 `GET /api/ws` 的后续帧分发都会按当前本地仓库状态重新校验本地 `user` / `client` principal；主体被停用、过期、owner 约束失效或作用域变化后，不会等到 token TTL 结束
- platform token
  - 来源：节点管理平台配置
  - 支持：`X-Node-Token: <platform_token>`
  - 也支持：`Authorization: Bearer <platform_token>`
  - `POST` JSON 请求体中的 `token` 或 `node_token`
  - `GET` 请求当前不会读取 URL 查询参数里的 `token`

## 3. 平台 actor 上下文

远程平台可附带这些头：

- `X-Platform-Role: admin|user`
- `X-Platform-Username: <name>`
- `X-Platform-Actor-ID: <id>`
- `X-Platform-Client-IDs: 1,2,3`

同名委托上下文字段也支持：

- `GET` 请求的 URL query
- `POST` JSON body

当前推荐仍然优先使用请求头，避免 query 泄漏到代理日志。

作用域规则：

- 只有 token，没有委托上下文时：默认按平台管理员 actor 处理
- `full` 平台密钥：等同节点管理员
- `account` 平台管理员：可完整管理本平台 service user 作用域下的资源
- 如果带了 `X-Platform-Actor-ID`、`X-Platform-Client-IDs` 等委托上下文，但没有显式角色头，会默认按用户作用域处理
- 平台普通用户：只可访问授权给自己的客户端及其关联资源
- 本地普通用户：可访问自己是 owner 或 manager 的资源镜像，以及自己的 `usage_snapshot`；不能访问 `status`、`config`、`sync` 这类节点级控制接口
- 本地 `client` principal：只可访问单个客户端作用域下的资源；可管理该客户端下的隧道和域名资源，但不能修改、停用、删除客户端自身配置

资源读写规则：

- 资源 CRUD 先判断是否可见
- 资源接口会返回该资源的正式 payload，但敏感写配置字段仍可能按当前 actor 权限做脱敏
- 当前稳定的脱敏规则见资源接口文档；例如只读 actor 读取客户端、隧道或域名资源时，不会拿到 `verify_key`、`password`、`auth`、`cert_file`、`key_file` 这类敏感身份或写配置字段
- `status`、`registration`、`usage_snapshot` 属于节点级状态或统计接口，它们的权限和字段粒度不等同于资源 CRUD
- `overview.usage_snapshot` 与单独的 `usage_snapshot` 也会沿用同一套敏感字段裁剪规则；例如只有具备 `clients:update` 的 actor 才会收到客户端 `verify_key`

## 4. 推荐接入顺序

外部平台首次接入一个节点时，推荐顺序：

1. `GET /api/system/overview`
2. 如果你是本地管理员或 `full` 平台管理员，也可以直接调 `GET /api/system/overview?config=1`
3. 后续同步使用：
   - `GET /api/system/changes`
   - `GET /api/system/usage-snapshot`
   - `GET /api/ws`
   - callback

补充说明：

- `GET /api/system/changes` 和 WS `request.path=/api/system/changes` 都要求当前 actor 已认证；匿名请求返回正式 management error `401 unauthorized`

如果仍需要单独的完整管理员配置快照，再调 `GET /api/system/export`

如果发现以下任意情况，应重新做一次全量同步：

- `boot_id` 变化
- `config_epoch` 变化
- `/api/system/changes` 返回 `gap=true`

## 先按任务找

| 你要做什么 | 建议页面 |
| --- | --- |
| 开发新的本地管理前端、控制台或工具 | [管理接入入口](/reference/integration/management-api-entrypoints) |
| 确认认证方式、作用域和首次接入顺序 | 当前页 |
| 查 HTTP 路径、批量接口、示例和关键字段 | [HTTP 接口目录](/reference/management-api-http) |
| 查 WebSocket、reverse WS 和 callback | [实时通道与回调](/reference/management-api-realtime) |

## 相关页面

- 接口入口总览：看 [接口与集成](/reference/integration/README)
- 通用接入入口：看 [管理接入入口](/reference/integration/management-api-entrypoints)
- 平台接入顺序：看 [平台接入总览](/reference/integration/platform-onboarding)
- 节点配置项：看 [节点与平台对接](/reference/server-config-node)

