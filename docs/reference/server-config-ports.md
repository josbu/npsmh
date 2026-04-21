# 服务端配置：入口端口与桥接

这一页集中说明服务端监听端口、桥接入口、证书入口和 P2P 入口。

为了避免“默认值”歧义，下表同时给出两类值：

- 内置默认值：配置缺失时程序使用的值
- 仓库示例值：当前仓库 `conf/nps.conf` 的示例配置

如果你要找登录保护、真实 IP 和前置代理安全，去看 [Web、HTTP 与安全](/reference/server-config-web)。

## 1. 代理端口与桥接相关

| 名称 | 说明 | 内置默认值（缺省） | 仓库示例值（`conf/nps.conf`） |
| --- | --- | --- | --- |
| `bridge_port` | 旧兼容桥接端口基准；`bridge_tcp_port` / `bridge_kcp_port` 未设置时会回退到它 | `0`（不启用） | 未设置 |
| `bridge_ip` | 桥接入口监听地址 | `0.0.0.0` | `0.0.0.0` |
| `bridge_type` | 连接方式（`tcp`、`udp`、`both`）；兼容项，建议用独立 `bridge_*_port` 控制 | `both` | 未设置 |
| `http_proxy_ip` | HTTP 代理监听地址 | `0.0.0.0` | `0.0.0.0` |
| `http_proxy_port` | HTTP 代理监听端口 | `0`（不启用） | `80` |
| `https_proxy_port` | HTTPS 代理监听端口 | `0`（不启用） | `443` |
| `http3_proxy_port` | HTTP/3 监听端口；未设置时回退到 `https_proxy_port` | 跟随 `https_proxy_port` | 未设置（示例注释为 `443`） |
| `http_proxy_response_timeout` | HTTP 后端响应头超时（秒） | `100` | `100` |
| `force_auto_ssl` | 强制自动申请证书（需自行保证 80/443 可用） | `false` | `false` |
| `https_default_cert_file` | HTTPS 默认公钥证书文件（未单独配置证书的域名会使用） | 空 | `conf/server.pem` |
| `https_default_key_file` | HTTPS 默认私钥文件 | 空 | `conf/server.key` |
| `ssl_path` | 自动申请证书保存路径 | `ssl` | `conf/ssl` |
| `ssl_email` | 自动申请证书邮箱 | 空 | `you@yours.com` |
| `ssl_ca` | 自动申请证书 CA（`LetsEncrypt`、`ZeroSSL`、`GoogleTrust`） | `LetsEncrypt` | `LetsEncrypt` |
| `ssl_zerossl_api` | ZeroSSL API 密钥 | 空 | 未设置 |
| `ssl_cache_max` | 证书缓存最大条目数（`0` 不限制） | `0` | `0` |
| `ssl_cache_reload` | 证书缓存重载检查间隔（秒） | `0` | `3600` |
| `ssl_cache_idle` | 证书缓存闲置清理间隔（分钟） | `60` | `60` |
| `bridge_tcp_port` | 桥接 TCP 端口 | 跟随 `bridge_port`（通常是 `0`） | `8024` |
| `bridge_kcp_port` | 桥接 KCP 端口 | 跟随 `bridge_port`（通常是 `0`） | `8024` |
| `bridge_tls_port` | 桥接 TLS 端口 | `0`（不启用） | `8025` |
| `bridge_quic_port` | 桥接 QUIC 端口 | `0`（不启用） | `8025` |
| `bridge_ws_port` | 桥接 WS 端口 | `0`（不启用） | `8026` |
| `bridge_wss_port` | 桥接 WSS 端口 | `0`（不启用） | `8027` |
| `bridge_tcp_ip` | 桥接 TCP 监听 IP（未设置时跟随 `bridge_ip`） | 跟随 `bridge_ip` | 未设置 |
| `bridge_kcp_ip` | 桥接 KCP 监听 IP（未设置时跟随 `bridge_ip`） | 跟随 `bridge_ip` | 未设置 |
| `bridge_quic_ip` | 桥接 QUIC 监听 IP（未设置时跟随 `bridge_ip`） | 跟随 `bridge_ip` | 未设置 |
| `bridge_tls_ip` | 桥接 TLS 监听 IP（未设置时跟随 `bridge_ip`） | 跟随 `bridge_ip` | 未设置 |
| `bridge_ws_ip` | 桥接 WS 监听 IP（未设置时跟随 `bridge_ip`） | 跟随 `bridge_ip` | 未设置 |
| `bridge_wss_ip` | 桥接 WSS 监听 IP（未设置时跟随 `bridge_ip`） | 跟随 `bridge_ip` | 未设置 |
| `bridge_path` | 桥接 WS/WSS 路径 | `/ws` | `/ws` |
| `bridge_real_ip_header` | 桥接 WS/WSS 真实 IP 头名 | 空 | 未设置 |
| `bridge_trusted_ips` | 桥接 WS/WSS 可信代理 IP/CIDR 列表 | 空 | 未设置 |
| `bridge_host` | 桥接复用场景的主机名 | 空 | `xxx.com` |
| `bridge_cert_file` | 桥接 TLS/WSS 证书文件 | 空（空时自动证书） | 未设置 |
| `bridge_key_file` | 桥接 TLS/WSS 私钥文件 | 空（空时自动证书） | 未设置 |
| `bridge_http3` | 桥接是否允许走 HTTP/3 端口 | `true` | 未设置（默认仍是 `true`） |
| `bridge_select_mode` | 相同 `vkey` 客户端选取模式（`Primary` / `RoundRobin` / `Random`） | 空（运行时回退 `Primary`） | `Primary` |
| `bridge_addr` | Web 命令中展示给客户端的连接地址 | 空（自动推导） | 空 |
| `quic_alpn` | QUIC 允许协商的 ALPN（逗号分隔） | `nps` | 未设置 |
| `quic_keep_alive_period` | QUIC 空闲保活周期（秒） | `10` | 未设置 |
| `quic_max_idle_timeout` | QUIC 最大空闲超时（秒） | `30` | 未设置 |
| `quic_max_incoming_streams` | QUIC 最大并发接收流数量 | `100000` | 未设置 |

## 2. P2P 相关

| 名称 | 说明 | 内置默认值（缺省） | 仓库示例值（`conf/nps.conf`） |
| --- | --- | --- | --- |
| `p2p_ip` | P2P 服务端监听地址（可写公网 IP） | `0.0.0.0` | `0.0.0.0` |
| `p2p_port` | P2P 端口 | `0`（不启用） | `6000` |

## 3. 相关页面

- 需要 HTTPS、证书和反向代理的操作步骤：看 [HTTPS 与反向代理](/guide/server/https-and-proxy)
- 需要默认端口和角色关系：看 [架构与核心概念](/getting-started/architecture)
- 需要 P2P 的使用方式：看 [P2P 隧道](/guide/tunnels/p2p)
