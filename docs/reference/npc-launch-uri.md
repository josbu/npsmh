# NPC Launch：`npc://` 协议

这一页聚焦 `npc://` 的明文直连、本地模式、base64 包装和非规范写法。

## 1. 明文直连 / 本地模式

```bash
./npc -launch="npc://demo-vkey@127.0.0.1:8024/ws?type=ws&log=off"
./npc -launch="npc://demo-vkey@127.0.0.1:8024/my-alpn?type=quic"
./npc -launch="npc://demo-vkey@127.0.0.1:8024?password=secret&local_type=p2p&target=node-b"
./npc -launch="npc://connect?server=127.0.0.1:8024/ws&vkey=demo-vkey&type=ws"
./npc -launch="npc://local?server=127.0.0.1:8024&vkey=demo-vkey&password=secret&local_type=p2p&target=node-b"
```

这里的地址表达仍沿用原有 `-server` 语义：

`addr:port[@host_or_sni[:port]][/ws_path_or_quic_alpn]`

也就是说：

- `ws/wss` 时，`/xxx` 仍表示 WebSocket path
- `quic` 时，`/xxx` 仍表示 ALPN
- 未写 path 时，`ws/wss` 默认 `/ws`，`quic` 默认 `nps`

适合直接摊平在 query 中的字段包括：

- `server` / `server_addr`
- `vkey`
- `type` / `conn_type`
- `proxy` / `proxy_url`
- `local_ip`
- `password`
- `local_type`
- `local_port`
- `target` / `target_addr`
- `target_type`
- `fallback_secret`
- `local_proxy`
- 常见 runtime，如 `log`、`debug`、`dns_server`

其中：

- `npc://<vkey>@<server>?...` 适合最短的分享链接
- `npc://connect?...` / `npc://local?...` 适合需要显式写出 `server`、`vkey` 等字段的场景

## 2. base64 包装

规范写法：

```bash
./npc -launch="BASE64_OR_BASE64URL_PAYLOAD"
./npc -launch="npc://BASE64_OR_BASE64URL_PAYLOAD"
```

命令行更推荐 **base64url 且不带 `=` padding**，这样更不容易被不同 shell 误处理。

这里 `BASE64...` 解码后可以是：

- 远程 URL
- JSON 启动描述
- 另一条 `npc://...`

例如把一份 JSON 包起来：

```bash
./npc -launch="eyJwcm9maWxlcyI6W3siZGlyZWN0Ijp7InNlcnZlciI6WyIxMC4wLjAuMTo4MDI0Il0sInZrZXkiOlsiZGVtbyJdLCJ0eXBlIjpbInRjcCJdfX1dfQ"
./npc -launch="npc://eyJwcm9maWxlcyI6W3siZGlyZWN0Ijp7InNlcnZlciI6WyIxMC4wLjAuMTo4MDI0Il0sInZrZXkiOlsiZGVtbyJdLCJ0eXBlIjpbInRjcCJdfX1dfQ"
```

## 3. 非规范写法

以下形式不作为协议规范的一部分：

- `npc://https://example.com/launch`
- `npc://npc://demo@127.0.0.1:8024?type=tcp`
- `npc://base64/BASE64_PAYLOAD`
- `npc://launch?data=BASE64_PAYLOAD`

如果需要嵌套 URL、JSON 或另一条 `npc://`，先 base64，再放到 `npc://` 后面。

## 相关页面

- 需要解析顺序和多 payload：看 [基础规则与解析顺序](/reference/npc-launch-basics)
- 需要 JSON 结构规范：看 [JSON 启动描述](/reference/npc-launch-json)
