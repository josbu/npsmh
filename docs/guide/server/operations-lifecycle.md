# 重载、重启与更新

这一页聚焦已经部署完成后的服务生命周期操作：

- 配置重载
- 停止和重启
- 标准更新
- 手动覆盖可执行文件

## 1. 配置重载

`nps reload` 适合“重新读取配置后，只需要更新运行时状态，不需要重建监听端口和主运行引擎”的变更。

根据当前实现：

- 只有非 Windows 平台支持 `reload`
- `reload` 会重新加载配置文件，并重应用一批运行时配置，同时替换管理运行时
- 它不等同于完整热更新，不会替你重建所有监听器、共享端口复用器和网络入口

当前更适合使用重载的项目包括：

- 日志相关：`log`、`log_level`、`log_path`、`log_max_*`、`log_compress`
- 系统相关：`timezone`、`dns_server`、`ntp_server`、`ntp_interval`
- 访问控制相关：GeoIP / GeoSite 路径、登录 ACL、访问策略编译结果
- Bridge 运行态相关：`secure_mode`、桥接客户端选择策略、Bridge TLS 证书加载
- 运行限制相关：`allow_ports`、`allow_local_proxy`、`allow_secret_local`、`flow_store_interval`
- 运行时客户端相关：`public_vkey`、`visitor_vkey`，以及自动创建或移除的本地代理客户端
- 管理面相关：大多数 Web 管理、登录、鉴权和节点管理运行时配置
- 运行时维护相关：允许端口列表重建、系统信息采集启动、HTTP 代理缓存清理、管理运行时替换

如果你修改了下面这些内容，仍建议直接重启服务：

- `run_mode`
- Web 监听器相关：`web_ip`、`web_port`、`web_open_ssl`、`web_cert_file`、`web_key_file`
- 共享路由相关：`web_host`、`bridge_host`
- 公网监听器相关：`http_proxy_ip`、`http_proxy_port`、`https_proxy_port`、`http3_proxy_port`
- Bridge 监听器相关：各类 `bridge_*_ip`、`bridge_*_port` 以及对应启用开关
- Bridge WebSocket 网关相关：`bridge_path`、`bridge_trusted_ips`、`bridge_real_ip_header`
- P2P 监听相关：`p2p_ip`、`p2p_port`
- QUIC 传输相关：`quic_alpn`、`quic_keep_alive_period`、`quic_max_idle_timeout`、`quic_max_incoming_streams`
- `pprof_*`

可以把它简单理解为：

- 改“规则、限制、日志、运行态对象”时，优先考虑 `reload`
- 改“监听地址、监听端口、共享入口、协议入口”时，优先考虑 `restart`

Linux 或 macOS：

```bash
sudo nps reload
```

Windows：

```powershell
nps.exe restart
```

Windows 当前不支持 `reload`。

## 2. 停止与重启

Linux 或 macOS：

```bash
sudo nps stop
sudo nps restart
```

Windows：

```powershell
nps.exe stop
nps.exe restart
```

## 3. 更新

标准更新步骤：

1. 停止服务
2. 执行更新
3. 启动服务

Linux 或 macOS：

```bash
sudo nps stop
sudo nps update
sudo nps start
```

也兼容旧的更新入口：

```bash
sudo nps stop
sudo nps-update update
sudo nps start
```

Windows：

```powershell
nps.exe stop
nps.exe update
nps.exe start
```

也兼容旧的更新入口：

```powershell
nps.exe stop
nps-update.exe update
nps.exe start
```

如果更新失败，请从 [GitHub Releases](https://github.com/djylb/nps/releases/latest) 手动下载，并同时替换二进制文件和 `web` 目录。

## 4. 手动覆盖可执行文件

适用于：

- 自动更新失败
- 需要回滚或切换到指定版本

Linux 或 macOS：

```bash
sudo systemctl stop nps
whereis nps
sudo cp nps /usr/bin/nps
sudo chmod +x /usr/bin/nps
sudo systemctl start nps
```

Windows：

```powershell
Stop-Service nps
Copy-Item -Path "PATH_TO_NEW_NPS_EXE" -Destination "PATH_TO_OLD_NPS_EXE_DIR" -Force
Start-Service nps
```

## 下一步

- 需要确认运行目录和日志：看 [目录、面板与日志](/guide/server/operations-basics)
- 需要精确配置项：看 [服务端配置文件](/reference/server-config)
