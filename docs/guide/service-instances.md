# 服务与多实例

这一页面向已经完成首次部署和首次连接验证的用户。

这里不展开标准 `install/start/stop/restart` 的基础用法，只说明“内置服务管理之外”的进阶场景。

只有在下面这些情况，才建议自己创建额外的系统服务：

- 同一台机器运行多个 NPS 实例
- 同一台机器运行多个 NPC 实例
- 每个实例需要不同的配置目录或不同的启动参数

## Linux systemd 示例

服务端：

```ini
[Unit]
Description=NPS Server Instance
After=network-online.target

[Service]
ExecStart=/usr/bin/nps -conf_path=/srv/nps-a service
Restart=always

[Install]
WantedBy=multi-user.target
```

客户端：

```ini
[Unit]
Description=NPC Client Instance
After=network-online.target

[Service]
ExecStart=/usr/bin/npc -server=1.1.1.1:8024 -vkey=YOUR_VKEY -type=tcp -debug=false -log=off
Restart=always

[Install]
WantedBy=multi-user.target
```

## Windows `sc` 示例

这类方式只建议在你已经确认“内置 `install` 不够用”时再使用。

如果你只是单实例运行，优先使用 `nps.exe install` 或 `npc.exe install`。

以下命令建议在管理员 PowerShell 中执行。

服务端：

```powershell
sc.exe create Nps1 binPath= "\"D:\NPS-A\nps.exe\" -conf_path=\"D:\NPS-A\"" start= auto DisplayName= "NPS Server 1"
sc.exe start Nps1
```

客户端：

```powershell
sc.exe create Npc1 binPath= "\"D:\NPC-A\npc.exe\" -server=1.1.1.1:8024 -vkey=YOUR_VKEY -type=tcp -log=off -debug=false" start= auto DisplayName= "NPS Client 1"
sc.exe start Npc1
```

说明：

- 每个实例都应该有独立目录和独立服务名
- 服务端多实例通常配合不同的 `-conf_path`
- `-conf_path` 不只影响 `nps.conf`；相对路径资源（例如 `web/`、证书、错误页）也会随这个目录解析
- 客户端多实例通常配合不同的连接参数或不同的 `npc.conf`

推荐每个服务端实例目录至少包含：

- `conf/nps.conf`
- `web/`
- 该实例使用的证书与其他静态资源

## 什么时候不需要看这页

如果你只是：

- 先完成一台 NPS 的基础验证
- 先连上一台 NPC
- 只运行一个实例

那直接用标准 `install` 已经足够，不需要自己手写 systemd 或 `sc` 服务。

## 相关页面

- 服务端基础运行：看 [启动 NPS 服务端](/getting-started/start-server)
- 客户端基础运行：看 [启动 NPC 客户端](/getting-started/start-client)
- 服务端维护：看 [服务端运维](/guide/server/operations)
- 客户端维护：看 [客户端维护与更新](/guide/client/maintenance)
