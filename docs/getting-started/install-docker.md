# Docker 安装

Docker 更适合第一次验证环境，也适合独立部署。

如果你只想用最短路径完成一条最小链路验证，优先用这一页。

## 安装 NPS 服务端

建议先准备一个本地目录用于持久化配置：

```bash
mkdir -p /opt/nps-conf
```

DockerHub：

```bash
docker pull duan2001/nps
docker run -d \
  --restart=always \
  --name nps \
  --net=host \
  -v /opt/nps-conf:/conf \
  -v /etc/localtime:/etc/localtime:ro \
  duan2001/nps
```

GHCR：

```bash
docker pull ghcr.io/djylb/nps
docker run -d \
  --restart=always \
  --name nps \
  --net=host \
  -v /opt/nps-conf:/conf \
  -v /etc/localtime:/etc/localtime:ro \
  ghcr.io/djylb/nps
```

说明：

- `--net=host` 可以避免额外端口映射配置，最适合 NPS 这类需要监听多种入口端口的服务
- 首次启动时，镜像会把示例配置和地理数据复制到 `/conf`，例如 `/conf/nps.conf`
- `/conf` 用于持久化配置、证书和运行时生成的数据
- 如果不用 `--net=host`，需要自行映射 `web_port`、`bridge_*`、`http_proxy_port`、`https_proxy_port` 和可能用到的其他端口

## 安装 NPC 客户端

DockerHub：

```bash
docker pull duan2001/npc
docker run -d \
  --restart=always \
  --name npc \
  --net=host \
  duan2001/npc \
  -server=<server-ip>:8024 \
  -vkey=<client-vkey> \
  -type=tcp \
  -log=off
```

GHCR：

```bash
docker pull ghcr.io/djylb/npc
docker run -d \
  --restart=always \
  --name npc \
  --net=host \
  ghcr.io/djylb/npc \
  -server=<server-ip>:8024 \
  -vkey=<client-vkey> \
  -type=tcp \
  -log=off
```

补充：

- `npc` 镜像默认直接执行 `/npc`，不会自动帮你生成 `npc.conf`
- 如果你要用配置文件模式，可挂载目录后显式传 `-config`，例如：

```bash
docker run -d \
  --restart=always \
  --name npc \
  --net=host \
  -v /opt/npc-conf:/conf \
  duan2001/npc \
  -config=/conf/npc.conf \
  -log=off
```

如果你想把客户端的隧道定义写进配置文件，再继续看 [客户端连接与配置](/guide/client/connect)。

## 下一步

- 启动和登录管理界面：看 [启动 NPS 服务端](/getting-started/start-server)
- 让客户端连上服务端：看 [启动 NPC 客户端](/getting-started/start-client)
- 只想先完成一条最小链路验证：看 [10 分钟快速开始](/getting-started/quick-start)
