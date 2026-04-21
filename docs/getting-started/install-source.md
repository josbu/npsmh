# 源码安装

适合需要自己编译、做二次开发或验证源码修改的场景。

当前仓库 `go.mod` 声明为 `go 1.26`，建议使用 Go `1.26` 或更高版本。

## 获取代码

```bash
git clone https://github.com/djylb/nps.git
cd nps
```

## 编译

```bash
go build -o nps cmd/nps/nps.go
go build -o npc cmd/npc/npc.go
```

## 运行前目录说明

只编译二进制还不够。`nps` 运行时还会读取同级目录下的 `conf/` 和 `web/` 资源。

如果你在仓库根目录直接运行，目录结构已经满足要求：

```bash
./nps
```

如果你把 `nps` 复制到其他目录运行，请同时复制以下目录：

- `conf/`
- `web/`

如果你修改了 `frontend/` 下的资源或需要重新生成嵌入静态资源，再补一次前端构建：

```bash
make frontend-build-embedded
```

或：

```bash
pnpm --dir frontend install
pnpm --dir frontend build:embedded
```

## 下一步

- 启动服务端：看 [启动 NPS 服务端](/getting-started/start-server)
- 启动客户端：看 [启动 NPC 客户端](/getting-started/start-client)
