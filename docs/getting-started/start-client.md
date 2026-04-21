# 启动 NPC 客户端

这一页只做一件事：让 NPC 第一次连上 NPS。

如果服务端还没有起来，先看 [启动 NPS 服务端](/getting-started/start-server)。

如果你是第一次启动 NPC，建议优先直接使用 Web 管理界面“客户端列表”提供的快捷启动命令，暂时不要先使用 `npc.conf`。

源码里的实际行为是：当你直接执行 `npc`，且没有传 `-server`、`-vkey`、`-launch` 也没有子命令时，程序会回退尝试读取默认 `conf/npc.conf`。这对第一次使用的用户来说，容易造成启动来源判断错误。

## 最推荐的路径

第一次连接建议按这个顺序做：

1. 登录 Web 管理界面
2. 进入“客户端列表”
3. 找到对应客户端并展开详情
4. 直接复制页面里的 TCP 或 TLS 快捷启动命令

客户端列表会按当前服务端可用的桥接方式生成 `./npc ... -server=... -vkey=... -type=...` 命令。第一次连接时，建议直接复制该命令，以减少参数误配。

## 第一次前台运行

如果二进制已经安装到系统路径，直接使用 `npc`。
如果你是从发布包目录直接运行，请保持命令里的 `./npc` 或 `npc.exe`。

普通 TCP 连接：

```bash
npc -server=<server-ip>:8024 -vkey=<client-vkey> -type=tcp
```

```powershell
npc.exe -server="<server-ip>:8024" -vkey="<client-vkey>" -type="tcp"
```

TLS 连接：

```bash
npc -server=<server-ip>:8025 -vkey=<client-vkey> -type=tls
```

```powershell
npc.exe -server="<server-ip>:8025" -vkey="<client-vkey>" -type="tls"
```

第一次连接时，记住这几点：

- 默认普通桥接端口通常是 `8024`
- 默认 TLS 桥接端口通常是 `8025`
- `-type` 虽然默认是 `tcp`，第一次仍建议显式写出，排查更直接
- 第一次连接时，不建议直接执行不带参数的 `npc`，否则程序可能回退去读默认 `conf/npc.conf`

## 连通后再注册为系统服务

第一次前台连接确认成功后，再决定是否安装为系统服务。

如果你是从发布包目录直接操作，而二进制还没有进入系统路径，请把下面命令里的 `npc` 改成 `./npc`。
Windows 注册服务时，建议使用管理员 PowerShell。

Linux 或 macOS：

```bash
sudo npc install -server=<server-ip>:8024 -vkey=<client-vkey> -type=tcp -log=off
sudo npc start
```

Windows：

```powershell
npc.exe install -server="<server-ip>:8024" -vkey="<client-vkey>" -type="tcp" -log="off"
npc.exe start
```

如果后续需要更换连接参数，先卸载再重新安装服务。

常用命令：

```bash
sudo npc start
sudo npc stop
sudo npc restart
sudo npc uninstall
```

```powershell
npc.exe start
npc.exe stop
npc.exe restart
npc.exe uninstall
```

## 最小检查项

客户端启动后，至少确认这些结果：

1. Web 管理界面里客户端状态显示为在线
2. 你复制的 `server`、`vkey` 和连接协议与服务端配置一致
3. NPC 所在机器可以访问它要转发的内网目标

## 进阶入口

- 需要 `npc.conf`、多份配置、文件隧道或本地访问模式：看 [客户端连接与配置](/guide/client/connect)
- 需要 `-launch`、远程源或多实例：看 [快速启动与远程源](/guide/client/launch)
- 需要服务注册、多实例和手动服务：看 [服务与多实例](/guide/service-instances)
