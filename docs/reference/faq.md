# FAQ

这一页收集最常见的排查入口和简短答案。需要精确配置项时，请同时查看 [服务端配置文件](/reference/server-config)。

## 服务端

### 服务端无法启动

先检查这些点：

1. 端口是否冲突。仓库自带示例配置常会占用 `8024`、`8081`、`80`、`443`、`6000` 等端口。
2. 当前配置目录下是否包含完整的 `conf/` 和 `web/` 资源。
3. 证书路径、日志路径和自定义目录是否有效。

最直接的排查方式是先停止服务，再前台运行一次 `nps` 查看报错。

如果你是 Linux / macOS 标准安装，建议直接这样跑：

```bash
nps -conf_path=/etc/nps
```

如果你是从发布包目录直接运行，再使用 `./nps`。

### 修改配置后没有生效

优先确认你改的是实际生效的配置文件。

- 标准安装通常使用 `/etc/nps/conf/nps.conf`
- Windows 管理员标准安装通常使用 `C:\Program Files\nps\conf\nps.conf`
- Windows 无管理员脚本安装通常使用 `%LOCALAPPDATA%\nps\conf\nps.conf`
- 如果安装时带了 `-conf_path`，应修改那个目录下的配置

补充：

- Linux / macOS 上前台直接排查时，最好显式传 `-conf_path=/etc/nps`
- 否则你可能看到“服务正常，但前台手动运行读到的不是同一份配置”

多数配置修改后需要重启服务。只有少量管理和鉴权相关参数适合 `nps reload`。

补充：

- `nps reload` 当前只适用于非 Windows 平台
- 如果改的是端口、Bridge、HTTPS 入口或运行模式，直接重启更合适

### IPv6 是否需要单独开启

通常不需要。只要机器和网络环境本身支持 IPv6，NPS 可以按监听地址正常工作。

## 客户端

### 客户端无法连接服务端

按下面顺序检查：

1. 服务端防火墙和云安全组是否放行桥接端口
2. `server` 地址和端口是否正确
3. `vkey` 是否对应当前客户端
4. 连接协议是否匹配，例如 `8025` 应搭配 `-type=tls`
5. 客户端和服务端版本是否兼容

### 客户端显示在线，但业务访问不通

“连上服务端”和“业务可访问”是两件事。

继续检查：

1. 是否已经创建了隧道或域名转发规则
2. NPC 所在机器能否访问目标地址
3. 服务端公网入口端口是否开放
4. 目标服务本身是否正常监听

### Docker 场景下隧道端口连不上

如果 NPS 使用 Docker 且没有使用 `--net=host`，要确认容器端口映射是否完整。

需要暴露的是 NPS 容器监听的端口，不是 NPC 容器的端口。

### P2P 直连失败

P2P 成功率与 NAT 类型和网络环境强相关。双方都是 Symmetric NAT 时，通常无法成功直连。

先看 [P2P 隧道](/guide/tunnels/p2p) 中的成功率与 NAT 说明，再决定是否改用私密代理或保留回退方案。

### 一条命令连接多个服务端

支持。多个值用逗号一一对应：

```bash
npc -server=xxx:123,yyy:456,zzz:789 -vkey=key1,key2,key3 -type=tcp,tls,tls
```

如果只写一个 `server`，但写了多个 `vkey`，通常不会得到你期望的结果，建议始终保持数量一致。

## 反向代理和 HTTPS

### 如何把真实 IP 传给后端

HTTP 或 HTTPS 域名转发场景，优先检查：

- `http_add_origin_header=true`
- 后端是否正确读取 `X-Forwarded-For` 或 `X-Real-IP`

如果是 TCP 或 UDP 链路，且后端支持 Proxy Protocol，也可以启用 Proxy Protocol 传递来源地址。

### NPS 放在 Nginx 或 Caddy 后面时怎么配

常见做法是：

1. 让 Nginx 或 Caddy 统一监听公网 `80/443`
2. 把请求反向代理到 NPS 的本地 HTTP 端口
3. 通过 `X-NPS-Http-Only` 避免重复跳转
4. 同时传递 `X-Real-IP` 和 `X-Forwarded-For`

完整示例见 [HTTPS 与反向代理](/guide/server/https-and-proxy)。

### 后端已经自己处理 HTTPS，还要不要让 NPS 配证书

不一定。

如果后端自己持有证书，可以在域名转发里选择“由后端处理 HTTPS”，让 NPS 只做转发。

### 自动 CORS 是否建议长期依赖

不建议。自动 CORS 适合临时排障或简单场景，正式环境更推荐由后端自己返回精确的跨域头。

## 日志与调试

### 默认日志在哪里

NPS：

- Linux / macOS：`/var/log/nps.log`
- Windows：当前运行的 `nps.exe` 所在目录下的 `nps.log`

NPC：

- Linux / macOS：`/var/log/npc.log`
- Windows：当前运行的 `npc.exe` 所在目录下的 `npc.log`

如果配置里显式设置了 `log_path`，以配置值为准。

### 如何快速确认当前运行的是哪个配置目录

推荐做法是：

1. 查看安装或启动命令是否带了 `-conf_path`
2. 查看日志中的配置路径输出
3. 在实际运行目录中确认 `conf/` 和 `web/` 是否一致

### `-conf_path` 应该什么时候传

- 直接运行时：每次启动都可以传
- 安装系统服务时：在 `install` 命令里传一次即可

例如：

```bash
nps install -conf_path=/app/nps
```

安装完成后，后续执行 `nps start` 就可以，不需要再重复传 `-conf_path`。

## 其他

### 旧版客户端如何接入新服务端

如果必须兼容旧环境：

1. 服务端按需设置 `secure_mode=false`
2. 旧客户端连接时增加 `-proto_version=0`

新部署建议显式设置 `secure_mode=true`，只在确实需要兼容旧客户端时再调整。

### 到期时间限制如何工作

前提是服务端允许时间限制能力：

```ini
allow_time_limit=true
```

然后可以在 Web 管理界面为客户端或资源设置到期时间。

### 还查不到答案怎么办

建议按这个顺序继续：

1. 看 [服务端配置文件](/reference/server-config)
2. 看 [功能清单与扩展能力](/reference/features)
3. 看 [补充说明](/reference/notes)
4. 到 [GitHub Issues](https://github.com/djylb/nps/issues) 提交问题
