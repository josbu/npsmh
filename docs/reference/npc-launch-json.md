# NPC Launch：JSON 启动描述

这一页聚焦 launch JSON 的正式结构，以及 `direct`、`config`、`local` 和 `profiles` 的字段语义。

## 1. 顶层结构

规范顶层字段：

- `version`
- `runtime`
- `direct`
- `config`
- `local`
- `profiles`

兼容字段：

- `nodes`

规则：

- `version` 可省略
- 顶层必须且只能出现 `direct/config/local/profiles(nodes)` 其中一种
- `runtime` 是**进程级全局参数**
- `profiles` 是多实例规范字段，`nodes` 只是兼容别名

## 2. `runtime`

`runtime` 对应运行时参数，例如：

- `log`
- `log_level`
- `log_path`
- `debug`
- `proto_version`
- `skip_verify`
- `keepalive`
- `dns_server`
- `ntp_server`
- `ntp_interval`
- `timezone`
- `disable_p2p`
- `p2p_type`
- `local_ip_forward`
- `auto_reconnect`
- `disconnect_timeout`
- `p2p_timeout`

## 3. `direct`

`direct` 用于模拟命令行直连。这里的数组语义沿用旧 CLI，一条 `direct` 可以表达单实例内的多节点连接：

```json
{
  "runtime": {
    "log": "off"
  },
  "direct": {
    "server": ["10.0.0.1:8024/ws", "10.0.0.2:8024/my-alpn"],
    "vkey": ["node-a", "node-b"],
    "type": ["ws", "quic"],
    "local_ip": ["192.168.1.10", "192.168.1.11"]
  }
}
```

## 4. `config`

`config` 用于模拟 `npc.conf`，也是“尽可能操控全部参数”的主格式。推荐主字段：

- `source`
- `common`
- `hosts`
- `tasks`
- `healths`
- `local_servers`

兼容别名：

- `tunnels` -> `tasks`
- `locals` -> `local_servers`
- `common.server` -> `common.server_addr`
- `common.type` -> `common.conn_type`
- `common.proxy` -> `common.proxy_url`

其中 `config.source` 是一个很重要的规范字段：

- 可以是本地 `.conf` / `.ini` / `.yaml` / `.yml` / `.json` 路径
- 也可以是返回这些格式内容的 `http://` / `https://` URL

也就是说，`config` 现在支持两种表达方式：

1. 完全内联，把 `npc.conf` 参数拆成 JSON 字段
2. 先通过 `config.source` 指向一份现有 `npc.conf`，再在 JSON 中做补充或覆盖

当 `config.source` 与内联字段同时存在时，建议冻结以下合并规则：

- `common`：按字段覆盖 `source` 里的 `[common]`
- `hosts` / `tasks` / `healths` / `local_servers`：在 `source` 基础上继续追加

结构化格式（JSON / YAML）里，字段名分隔符按服务端配置风格统一处理：

- `server_addr`
- `server-addr`
- `server.addr`

这三种写法会被视为同一个字段。当前 launch `config` 的结构化字段默认接受 `_` / `-` / `.` 三种分隔方式。

`config` 当前支持：

- `common` 中的大部分 `npc.conf` 公共项
- `hosts` 中的 header/response_header/tls_offload/acl/user_auth 等扩展项
- `tasks` 中的 target_type/local_proxy/acl/user_auth 等扩展项
- `local_servers` 中的 `fallback_secret`、`local_proxy` 等项

示例：

```json
{
  "config": {
    "source": "https://example.com/npc.conf",
    "common": {
      "server_addr": "127.0.0.1:8024/ws",
      "vkey": "demo",
      "conn_type": "ws",
      "proxy_url": "http://127.0.0.1:8080"
    },
    "hosts": [
      {
        "remark": "demo-host",
        "host": "example.com",
        "target_addr": ["127.0.0.1:8080"],
        "headers": {
          "X-Forwarded-Env": "demo"
        },
        "response_headers": {
          "Cache-Control": "no-cache"
        }
      }
    ],
    "tasks": [
      {
        "remark": "demo-tcp",
        "mode": "tcp",
        "server_port": "10080",
        "target_addr": ["127.0.0.1:80"]
      }
    ],
    "local_servers": [
      {
        "local_port": 2001,
        "password": "secret",
        "target_addr": "10.0.0.1:22"
      }
    ]
  }
}
```

## 5. `local`

`local` 用于模拟 `p2p/secret` 本地模式，字段与对应命令行参数一致：

- `server`
- `vkey`
- `type`
- `proxy`
- `local_ip`
- `local_type`
- `local_port`
- `password`
- `target`
- `target_addr`
- `target_type`
- `fallback_secret`
- `local_proxy`

## 6. `profiles`

当需要“一份 launch 启动多个实例”时，使用 `profiles`：

```json
{
  "runtime": {
    "log": "off"
  },
  "profiles": [
    {
      "name": "edge-direct",
      "direct": {
        "server": ["10.0.0.1:8024/ws"],
        "vkey": ["edge-a"],
        "type": ["ws"]
      }
    },
    {
      "name": "ops-config",
      "config": {
        "common": {
          "server_addr": "10.0.0.2:8024",
          "vkey": "ops-a",
          "conn_type": "tls"
        },
        "tasks": [
          {
            "remark": "demo",
            "mode": "tcp",
            "server_port": "10080",
            "target_addr": ["127.0.0.1:80"]
          }
        ]
      }
    },
    {
      "name": "secret-local",
      "local": {
        "server": "10.0.0.3:8024",
        "vkey": "local-a",
        "type": "tcp",
        "password": "secret",
        "target": "node-b"
      }
    }
  ]
}
```

也可以直接重复传入多个 `-launch`，让 CLI 自动归并成 `profiles`；但长期存储和服务端生成时，仍建议直接输出规范 JSON。

## 相关页面

- 需要解析和多 payload 规则：看 [基础规则与解析顺序](/reference/npc-launch-basics)
- 需要远程源状态语义：看 [远程源与兼容性](/reference/npc-launch-remote)
