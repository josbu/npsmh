# 功能清单：运维与调试

这一页集中放环境变量渲染、健康检查、日志和调试相关能力。

如果你要看 ACL 和配额，去看 [访问控制与限制](/reference/features-access)。

## HTTP 缓存（已弃用）

旧版本曾尝试在域名转发场景中直接缓存静态内容。

该方案会把响应内容常驻内存，收益有限，但内存开销和一致性问题明显，因此已经不再推荐使用。当前如需缓存能力，更建议在 NPS 前面接入 Nginx 或其他专门的缓存层。

## 环境变量渲染

NPC 支持在启动时读取环境变量，适合容器、CI 或批量部署场景。

在无配置文件启动模式下，先设置环境变量：

```bash
export NPC_SERVER_ADDR=1.1.1.1:8024
export NPC_SERVER_VKEY=xxxxx
```

然后直接执行 `npc` 即可运行。

在配置文件启动模式下：

```ini
[common]
server_addr={{.NPC_SERVER_ADDR}}
conn_type=tcp
vkey={{.NPC_SERVER_VKEY}}
auto_reconnection=true
[web]
host={{.NPC_WEB_HOST}}
target_addr={{.NPC_WEB_TARGET}}
```

在配置文件中填入相应变量名后，NPC 会在启动时自动替换。

## 健康检查

当客户端以配置文件模式启动时，支持对多目标做健康检查。配置示例如下：

```ini
[health_check_test1]
health_check_timeout=1
health_check_max_failed=3
health_check_interval=1
health_http_url=/
health_check_type=http
health_check_target=127.0.0.1:8083,127.0.0.1:8082

[health_check_test2]
health_check_timeout=1
health_check_max_failed=3
health_check_interval=1
health_check_type=tcp
health_check_target=127.0.0.1:8083,127.0.0.1:8082
```

规则：

- section 名必须以 `health` 开头
- `http` 模式会请求目标加上 `health_http_url`，返回 `200` 视为成功
- `tcp` 模式会直接尝试建立 TCP 连接，连接成功视为成功
- 当失败次数超过 `health_check_max_failed` 时，NPS 会临时移除该目标
- 目标恢复后，NPS 会自动重新加入

| 项 | 含义 |
| --- | --- |
| `health_check_timeout` | 健康检查超时时间 |
| `health_check_max_failed` | 健康检查允许失败次数 |
| `health_check_interval` | 健康检查间隔 |
| `health_check_target` | 健康检查目标，多个以逗号（`,`）分隔 |
| `health_check_type` | 健康检查类型 |
| `health_http_url` | 健康检查 URL，仅 `http` 模式适用 |

## 日志输出

日志级别：

`trace | debug | info | warn | error | fatal | panic | off`

NPC 命令行示例：

```text
-log_level=info -log_path=npc.log
```

NPS 则在 `nps.conf` 中设置对应日志参数。

## pprof 性能分析与调试

可在服务端与客户端配置中开启 pprof 地址，用于性能分析和调试。

留空或注释掉对应参数时为关闭状态。

## 相关页面

- 需要服务端日志和 pprof 配置：看 [访问控制与运行](/reference/server-config-runtime)
- 需要客户端运行参数：看 [NPC 命令行参数](/reference/npc-cli)
