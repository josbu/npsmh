# NPC SDK 状态

当前主线仓库 **没有可直接使用的 NPC 嵌入式 SDK 导出实现**。

这是根据当前代码确认后的结论：

- `cmd/npc/npc.go` 使用了 `//go:build !sdk`
- 仓库中没有对应的 `//go:build sdk` 入口文件
- 仓库中也没有 `StartClientByVerifyKey`、`GetClientStatus` 这类 `//export` C 接口实现

所以可以直接这样理解：

- 旧资料里提到的 `npc_sdk.h`、`StartClientByVerifyKey` 等接口，不应再视为当前主线可用能力
- 如果你现在要把 NPC 集成进桌面程序、守护进程或其他宿主应用，当前更适合走“子进程集成”而不是“直接 SDK 嵌入”

## 当前更推荐的集成方式

### 1. 把 `npc` 当作子进程启动

当前更适合的方式是直接启动 `npc` 可执行文件，并使用：

- `-server/-vkey/-type`
- `-config`
- `-launch`

这三种入口之一来管理启动参数。

## 2. 把配置与分发交给现有能力

如果你需要更好分发和远程更新，优先考虑：

- `npc://`
- `-launch` JSON
- 远程 URL 启动描述
- `npc.conf`

相关说明见 [快速启动与远程源](/guide/client/launch) 和 [NPC Launch 规范](/reference/npc-launch)。

## 3. 把状态检查交给现有命令和日志

当前更适合的做法：

- 用 `npc status` 检查一组连接参数是否可用
- 用日志观察连接、重连和错误
- 用 Web 管理界面的客户端在线状态确认服务端视角

## 如果你一定要做嵌入式集成

当前仓库并没有稳定 C ABI 合同。
如果你要继续做程序内集成，通常需要自行完成这些工作：

1. 基于 `client` 包或 `cmd/npc/npc.go` 自定义导出层
2. 自己定义启动、停止、日志、状态与配置边界
3. 自己承担后续兼容性维护

换句话说：

- 现在没有“官方现成 SDK”
- 现在只有“可以继续二次开发的 CLI 与内部运行逻辑”

## 相关页面

- 需要普通启动方式：看 [客户端连接与配置](/guide/client/connect)
- 需要更适合集成分发的启动描述：看 [快速启动与远程源](/guide/client/launch)
- 需要精确 `-launch` 结构：看 [NPC Launch 规范](/reference/npc-launch)
