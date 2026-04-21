# NPC Launch：远程源与兼容性

这一页聚焦远程 URL 取源、状态语义、兼容边界和推荐实践。

## 1. 远程源语义

如果 `-launch` 的来源是远程 URL，或者 JSON `config.source` 指向远程 URL，当前实现按以下规则工作：

- `http://` / `https://` 获取默认支持服务端重定向（遵循 Go `http.Client` 默认行为）
- 每个命令行 `-launch` 都会形成一个独立的 launch supervisor
- supervisor 会保存“最近一次成功解析并构建完成的 last-good bundle”
- 当该 bundle 中任意一个连接单元断开时，会先尝试重新获取远程源，再决定是否重启整个 bundle
- 重复传入多个 `-launch` 时，它们之间互相独立；一个输入触发重载，不会强制重启别的输入

当前把远程 HTTP 响应按以下规则解释：

- `200`：正常 payload
- `429` / `503`：临时故障，优先读取 `Retry-After`
- `410`：配置已作废或已撤销，进入 `revoked/paused` 状态
- 其他 `4xx`：按硬错误处理，不继续使用该次响应内容
- 网络错误 / 超时 / `5xx`：按临时故障处理

当前默认行为：

- `429` / `503` / 网络错误：记录 `source_retry`，按 `Retry-After` 或默认 30 秒安排下一次取源；如果已有 last-good，则本次先继续使用 last-good
- `410`：记录 `source_revoked`，并输出 `source_paused`；不会继续使用 last-good，会进入挂起后再周期性重试
- 其他 `4xx`：记录 `source_paused`，不会继续使用该次响应内容，也不会继续使用 last-good
- 无效 payload：记录 `source_invalid`；如果有 last-good，则保留 last-good，不直接清空
- 当远程源恢复正常后，输出 `source_ok`

当前会输出这些状态日志：

- `source_ok`
- `source_retry`
- `source_invalid`
- `source_revoked`
- `source_paused`

## 2. 远程刷新时的约束

- `runtime` 仍然是**进程级参数**，只在进程启动前的首次成功解析结果上生效一次
- 后续远程源刷新主要更新连接参数和配置内容，不会在运行中动态改日志、时区、keepalive 这类全局参数
- 如果进程启动时远程源还不可用，那么本次进程生命周期内仍按 CLI/默认 runtime 运行；新的 runtime 在下次重启后生效
- 如果需要“不同 bundle 完全独立地热更新”，优先拆成多个 `-launch`，而不是把所有 profile 堆进同一个输入

## 3. 兼容与优先级

- 显式 CLI 参数优先，不会被 `-launch` 覆盖
- `status/register` 只支持单一 launch 目标
- `status/register` 不支持多 profile launch
- `start/stop/restart/install/uninstall/update` 不会主动解析远程 launch，避免服务控制被外部网络状态影响
- 顶层明文 JSON 仍兼容，但命令行分发更推荐 base64url(JSON)、`@path` 或远程 URL
- 超长 payload 推荐 `@path`，尤其是 Windows 环境
- 命令行多实例优先使用重复 `-launch`，而不是文本分隔符

## 4. 推荐实践

- 长期存储与服务端生成：用 JSON
- 命令行分发：优先用 `base64url(JSON)`，也可按需包成 `npc://BASE64URL_PAYLOAD`
- 二维码、聊天或短链分发：用 `npc://BASE64(...)`
- 需要多开：命令行用重复 `-launch`，长期格式用 `profiles`
- 需要表达 `ws path` / `quic alpn`：继续写在 `server/server_addr` 后缀里
- 如果 launch 很长，优先改成 `@path` 或远程 URL，不要硬塞命令行参数

## 相关页面

- 需要 `npc://` 协议细节：看 [`npc://` 协议](/reference/npc-launch-uri)
- 需要 JSON 字段规范：看 [JSON 启动描述](/reference/npc-launch-json)
- 需要快速上手：看 [快速启动与远程源](/guide/client/launch)
