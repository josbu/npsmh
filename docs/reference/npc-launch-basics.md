# NPC Launch：基础规则与解析顺序

这一页聚焦 `-launch` 的最小心智模型、冻结规则、递归解析顺序和多 payload 规则。

## 1. 快速理解 `-launch`

`-launch` 是 NPC 的统一启动描述入口。它不会引入新的执行引擎，而是把外部输入统一解析成内部 spec，再落到现有启动链路：

- `direct`：等价于 `-server/-vkey/-type/...`
- `config`：等价于一份内存版 `npc.conf`
- `local`：等价于 `-password/-local_type/-local_port/...`
- `profiles`：一份 launch 中启动多个 `direct/config/local` 实例

## 2. 冻结规则

这部分建议作为协议冻结下来的约束：

- **JSON** 是完整规范格式，负责表达全部启动参数
- **`npc://`** 是分发协议，只负责轻量直连或承载一个 **base64 payload**
- **`http://` / `https://`** 始终表示远程获取，不承担本地包装语义
- **`@path`** 表示从本地文件读取 launch payload，用于规避命令行长度限制
- **`--launch <base64url>`** 本身就是正式输入，不需要额外包成 URL
- **命令行多实例** 优先使用重复 `-launch`
- `version` 可省略，默认按当前实现视为 `1`

也就是说，`npc://` 后面如果不是明文直连参数，就必须是 base64/base64url/raw-base64 一类可解码字符串；**不定义明文嵌套**。
另外，明文 JSON 虽然仍兼容，但**不作为命令行主推荐格式**。

## 3. 解析顺序

NPC 会递归解析 launch payload，最大嵌套深度为 6。当前顺序是：

1. 明文 JSON
2. `@path` 本地文件
3. `http://` / `https://` / `npc://`
4. 可解密字符串 / base64
5. 失败

补充说明：

- `http/https` 会先拉远程内容，再继续递归解析返回体
- 远程返回体可以是 JSON、URL、base64、`npc://...`
- `@path` 读取到的文件内容会继续走同一套递归解析逻辑
- 顶层仍兼容直接传 JSON，但命令行场景更推荐传 **base64url（无填充）**、`@path` 或远程 URL
- `--launch BASE64URL_PAYLOAD` 与 `--launch npc://BASE64URL_PAYLOAD` 都是正式支持的输入
- `npc://` 嵌套 payload 时只认 base64，不认明文 URL/JSON
- Windows、PowerShell、bash、cmd 对 `|`、`&`、引号等字符处理不同，命令行建议优先使用 **base64url（无填充）**
- Windows 命令行长度较紧，长 payload 建议优先用 `@path` 或远程 URL

## 4. 多 payload 规则

当需要“命令行同时启动多个实例”时，推荐直接重复 `-launch`：

```bash
./npc --launch="npc://edge-a@10.0.0.1:8024?type=tcp" --launch="npc://edge-b@10.0.0.2:8024/ws?type=ws"
```

更稳妥的跨 shell 写法是使用 base64url：

```bash
./npc --launch=BASE64URL_PAYLOAD_A --launch=BASE64URL_PAYLOAD_B
./npc --launch=npc://BASE64URL_PAYLOAD_A --launch=npc://BASE64URL_PAYLOAD_B
```

规则如下：

- 每个 `-launch` 都按完整 launch payload 独立解析
- 每个 `-launch` 也是运行时的独立 supervisor 边界
- 最终自动归并成一个 `profiles` 启动描述
- 多个 payload 的 `runtime` 只能叠加，不能冲突
- 如果存在冲突 runtime，例如一个写 `log=off`、另一个写 `log=stdout`，则直接报错

如果整体 payload 太长，也可以先写入文件：

```bash
./npc -launch="@./npc-launch.txt"
```

## 相关页面

- 需要 `npc://` 语法：看 [`npc://` 协议](/reference/npc-launch-uri)
- 需要 JSON 字段规范：看 [JSON 启动描述](/reference/npc-launch-json)
- 需要远程取源状态：看 [远程源与兼容性](/reference/npc-launch-remote)
