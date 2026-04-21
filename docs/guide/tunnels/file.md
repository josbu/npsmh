# 文件隧道

文件隧道用于把本地目录通过 NPS 暴露为公网可访问的文件入口。

这项能力建立在 `npc.conf` 配置文件模式之上，不适合作为第一次连通入口。第一次部署时，先用 Web 管理界面的“客户端列表”复制启动命令，把 NPC 直接连上服务端并确认在线，再回到这页配置文件隧道。

这页需要先明确两点：

- 当前 Web 管理界面保留了文件隧道列表页，但新增入口默认不显示；这个能力主要仍通过 `npc.conf` 配置
- 它更像文件目录或 WebDAV 入口，不是普通网站反向代理

## 适合什么场景

- 临时共享目录
- 公开下载文件
- 只读方式暴露静态目录

## 工作特点

- 主要通过 `npc.conf` 配置
- 由 NPC 直接提供文件访问能力
- 更适合文件和目录，不适合普通业务服务
- `read_only=false` 时可用于 WebDAV 写入；公开下载场景通常更建议 `read_only=true`
- 首次使用这个功能时，优先参考 `conf/npc.conf` 里的示例

## `npc.conf` 示例

```ini
[common]
server_addr=1.1.1.1:8024
vkey=123

[file]
mode=file
server_ip=0.0.0.0
server_port=19008
local_path=/srv/nps-files
strip_pre=/web/
read_only=true
multi_account=conf/multi_account.conf
```

## 常用字段

| 字段 | 作用 |
| --- | --- |
| `server_port` | 对外访问端口 |
| `server_ip` | 文件入口监听地址，常见值为 `0.0.0.0` |
| `local_path` | 本地目录 |
| `strip_pre` | 对外访问时使用的 URL 前缀 |
| `read_only` | 是否只读 |
| `multi_account` | 多账号认证配置 |

## 访问效果

如果：

- `server_port=19008`
- `local_path=/srv/nps-files`
- `strip_pre=/web/`

那么访问：

```text
http://<server-ip>:19008/web/
```

就等价于访问：

```text
/srv/nps-files
```

例如：

```text
http://<server-ip>:19008/web/readme.txt
```

会对应到：

```text
/srv/nps-files/readme.txt
```

## 注意事项

- 文件隧道更适合目录和文件访问，不适合作为普通 Web 应用反代
- 如果需要复杂的网站行为、证书、路径路由等，优先使用 [域名转发](/guide/tunnels/domain-forwarding)
- 如果需要认证，可以配合 `multi_account`
- 如果你只是想发布一个下载目录，公开场景优先考虑 `read_only=true`
