# 快速启动与远程源

本页只回答“怎么开始用 `-launch`”，不展开完整协议细节。

本页聚焦实际使用方法。精确规范、JSON 结构、`npc://` 语法和远程源状态语义，统一放在 [NPC Launch 规范](/reference/npc-launch) 和 [接口与集成](/reference/integration/README)。

## 什么时候用 `-launch`

适合：

- 你想把启动参数打包成一条更容易分发的字符串
- 你要从远程地址获取启动描述
- 你要在一条命令里启动多个实例
- 你希望把命令行直连、本地模式和配置文件模式统一成一种入口

不适合：

- 只是一条最普通的 `-server/-vkey/-type` 直连命令
- 长期手工维护静态隧道配置时，更适合直接使用 `npc.conf`

## 最常用的四种输入

### 1. 直接写 `npc://`

```bash
./npc -launch="npc://demo-vkey@127.0.0.1:8024?type=tcp"
```

适合短链接、聊天分发和人工复制。

### 2. 直接传 base64url payload

```bash
./npc --launch=BASE64URL_PAYLOAD
```

适合跨 shell 分发。相比明文 URL 或 JSON，更不容易被引号、`&`、`|` 等字符影响。

### 3. 从本地文件读取

```bash
./npc -launch="@./npc-launch.txt"
```

适合：

- payload 很长
- Windows 命令行长度受限
- 想把启动描述单独存档

### 4. 从远程 URL 获取

```bash
./npc -launch="https://example.com/npc-launch"
```

适合：

- 服务端统一下发启动描述
- 需要远程更新连接参数
- 想避免把完整配置直接放在命令行里

## 三个最常见场景

### 场景一：替代普通直连命令

```bash
./npc -launch="npc://demo-vkey@127.0.0.1:8024/ws?type=ws"
```

### 场景二：启动本地访问模式

```bash
./npc -launch="npc://local?server=127.0.0.1:8024&vkey=demo-vkey&password=secret&local_type=p2p&target=node-b"
```

### 场景三：一次启动多个实例

```bash
./npc --launch="npc://edge-a@10.0.0.1:8024?type=tcp" --launch="npc://edge-b@10.0.0.2:8024/ws?type=ws"
```

推荐直接重复传 `-launch`，而不是自己用文本分隔符拼接。

## 远程源需要先知道什么

如果 `-launch` 使用远程 URL，当前行为可以先这样理解：

- 每个 `-launch` 都是独立的取源和重试边界
- 远程源临时失败时，会优先保留最近一次成功的结果
- 远程源恢复正常后，会重新拉取并更新连接内容
- 进程级 `runtime` 参数不是运行中热更新的主要对象，通常仍以进程启动时首次成功解析的结果为准

如果你要精确状态语义，例如 `source_retry`、`source_revoked`、`last-good bundle`，看 [NPC Launch 规范](/reference/npc-launch)。

## 使用建议

- 想让不同 shell 更稳：优先使用 `base64url`
- payload 很长：优先使用 `@path` 或远程 URL
- 需要多实例：直接重复 `-launch`
- 需要长期存储和服务端生成：优先使用 JSON 结构，再按需编码分发

## 下一步

- 需要精确协议与字段规范：看 [NPC Launch 规范](/reference/npc-launch)
- 需要客户端状态检查、更新和代理接入：看 [维护与更新](/guide/client/maintenance)
- 需要客户端运行参数：看 [NPC 命令行参数](/reference/npc-cli)
- 需要普通连接和 `npc.conf`：看 [客户端连接与配置](/guide/client/connect)
