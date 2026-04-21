# NPS 仓库可维护性地图

## 1. 文档目的

这份文档保留为项目全貌地图和历史上下文，不再作为当前 Go 目录重构的施工计划。

当前 Go 目录重构完成状态以 `LEAN_REFACTOR_PLAN.md` 为准；本文只用于快速理解仓库结构、主运行链路、历史高风险点和后续实现级演进方向。

- 人工整理时可以先用本文建立模块地图，再读取当前源码确认细节。
- 其他 AI 接手时可以先读这份文档，再按阶段拆改。
- 后续修改优先围绕四个原则：单一职责、清晰接口、可组合、可替换。
- 本文中旧文件名或旧拆分建议只作为历史背景，不构成限制性要求。

## 1.1 重构约束与代码风格原则

后续重构不以“抽象纯度”作为目标，而以“实现可读、行为可审计、依赖显式、扩展成本低”为目标。具体约束如下：

- 不要为了抽象而抽象。若一个新增层只会制造跳转，而不能明确职责、隔离变化点或降低测试成本，就不应引入。
- 优先写容易对照规范、协议、配置语义的代码。输入解析、规则判断、状态变更应尽量贴近真实业务阶段，而不是被过早打散到难追踪的小对象里。
- 优先显式依赖，少用隐式耦合。能通过构造参数、provider、root getter 明确表达的依赖，不要继续埋在包级变量、隐藏 fallback 或跨层全局读取里。
- 默认行为必须保持可理解。对于本来就应该随全局配置、全局状态变化而变化的行为，默认实现应继续动态读取，而不是在重构时被意外冻结成快照。
- 抽象层数要受控。新引入的 seam 最好能直接服务于一个明确目标：可替换、可测试、可注入、可复用。避免 root -> facet -> adapter -> wrapper 多层转发。
- 代码应优先方便审计。重构后应能让人快速回答“配置从哪里来”“策略在哪里判断”“连接在哪里建立”“状态在哪里更新”“错误往哪里返回”这几个问题。
- 扩展点应围绕真实变化面设置。比如协议差异、运行时来源、存储来源、策略来源适合做 seam；纯数据搬运或只包一层调用的不适合单独抽象。
- 若“更抽象”和“更易读”冲突，默认优先更易读；只有在明确降低耦合或测试成本时，才接受额外抽象。

## 2. 本次分析范围

本文当前只保留 Go 运行结构地图，目标是帮助后续实现级修改快速定位模块边界、主链路和历史风险点。

已覆盖并重点阅读：

- 根目录构建/安装/配置文件
- `cmd`
- `bridge`
- `client`
- `server`
- `server/connection`
- `server/proxy`
- `server/p2pstate`
- `server/tool`
- `web/api`
- `web/routers`
- `web/service`
- `web/framework`
- `web/ui`
- `lib/file`
- `lib/servercfg`
- `lib/p2p`
- `lib/conn`
- `lib/common`
- `lib/config`
- `lib/policy`
- `lib/transport`
- `lib/cache`
- `lib/crypt`
- `lib/logs`
- `lib/daemon`
- `lib/index`
- `lib/rate`
- `lib/mux`
- `lib/pmux`
- `lib/goroutine`
- `lib/pool`
- `lib/serverreload`

本次不逐项展开的内容：

- `*_test.go`
- `.history`
- `coverage_*`
- `docs` 说明文档正文
- `image`
- `web/views`
- `web/static` 下第三方压缩产物
- `frontend`
- `frontend/node_modules`
- `frontend/dist`

这些文件不决定主运行时结构，后续如需补测试或发版整理，可再单独出文档。

## 2.1 文档维护方式

这个仓库体量很大，本文不再按历史轮次追加施工记录。后续维护原则如下：

- 只在真实实现或架构边界发生变化时更新相关模块说明。
- 更新时写当前状态、当前风险和后续方向，不追加流水账。
- 如果本文和源码不一致，以源码、测试和 `LEAN_REFACTOR_PLAN.md` 的最终状态为准。
- 前端计划仍由 `WEB_FRONTEND_REFACTOR_PLAN.md` 单独维护。

## 2.2 当前执行摘要（简化版）

这份文档已经从“结构整理施工日志”切换成“当前仓库说明 + 后续实现级建议”。

阅读顺序建议：

1. 先读 `2.2` 到 `2.5`
2. 再看 `10.x`、`11.x` 了解主链和管理面
3. 最后看 `14`、`15` 获取当前结构状态和下一阶段执行顺序

当前总判断：

- Go 代码的“结构整理 / 过散回并”阶段已经完成，详细完成状态见 `LEAN_REFACTOR_PLAN.md`
- 后续工作的主目标不再是继续拆/并文件，而是按主链做实现级演进、兼容债删除和数据库持久化扩展
- 少量文件级建议如果与当前源码不一致，应以当前源码和根计划状态为准

## 2.3 下一阶段真正值得处理的地方

真正值得继续做的是实现级工作，而不是新一轮目录级文件整理：

- `lib/file` 的非 JSON 数据库持久化 seam、增量写入和旧 JSON 兼容债删除
- `server/proxy`、`bridge`、`client`、`lib/p2p` 的 runtime-owner、P2P 时序、取消和旧客户端兼容删除
- `web/api`、`web/service`、`web/routers` 的 API/docs 一致性和管理面兼容路径继续收紧
- `lib/servercfg` 与运行时配置边界在具体主链改造时继续压实

详细说明见 `14.3`。
## 2.4 明确不再继续做的纯结构整理

明确不再继续硬并的区域：

- `client/launch*`
- `server/runtime_dashboard` 数据源层
- `server/proxy/udp_server.go` / `udp_server_session.go` / `udp_server_io.go`
- `server/proxy/runtime_context.go`
- `web/*`

详细原因见 `14.4`。

## 2.5 剩余尾部项（可选，不阻塞下一阶段）

当前还可能继续清理的，只剩少量尾部项：

- `bridge` / `client` / `server` / `server/proxy` 里极少数仍然很小的测试入口
- 个别 root 邻近 helper 的命名或注释

这些都不阻塞下一阶段。详细说明见 `14.5`。

- 做了会更整洁
- 但不再决定仓库主结构是否清晰
- 因此它们不应阻塞下一阶段的实现级重构

## 2.6 当前实现级重构的执行原则

进入实现级重构后，当前默认执行顺序是：

1. 先把仍然依赖包级全局的默认来源改成“动态 provider / root getter”，避免把原本应随全局变化的行为意外冻结。
2. 再把确实有价值的注入入口补齐，主要面向测试、替换实现、分层解耦。
3. 如果在这个过程中出现“抽象层数增加了，但阅读路径更长、语义更不完整”的情况，优先回调这些薄包装。

当前已确认适合回调的模式：

- root/context 上只有一两行 nil-check 后转发的包装方法
- 顶层 pipeline 被拆成过多仅做中转的方法，导致阅读时必须来回跳转
- 默认来源到处各写一份匿名 closure，导致很难回答“默认行为到底跟随什么全局状态”

当前明确不建议回调的模式：

- 数据源边界仍然真实存在的 provider 层
- 协议差异明显的 transport 层
- 会把配置、策略、连接、状态更新重新搅回一个大函数的“回并”

当前已经验证有效、可继续复用的回调模式：

- 把默认动态来源集中到具名 helper，而不是在多个构造函数里复制匿名 closure
- 去掉只做 nil-check + 转发的 root helper，让主路径直接表达阶段顺序
- 保留测试 seam，但不要让测试 seam 反过来主导生产代码的阅读路径
- 在同包内部，优先直接读 runtime/context 字段；如果 getter 只是重复字段名、没有额外语义或边界价值，就不必保留
- 对 root 上仅供全局入口调用的薄包装优先压平到真正的 `startup/service` 主链里；例如 engine 启动顺序、dashboard cache/build 刷新、provider transport 生命周期这类入口，应保证一眼能看出执行顺序，而不是先跨一层 root 方法再读真实逻辑

## 3. 全局理解

### 3.1 系统角色

- `nps` 服务端同时承担：配置加载、数据持久化、桥接连接、隧道监听、Web 管理端、Node 管理面、回调/事件/幂等状态管理。
- `npc` 客户端同时承担：CLI 解析、服务化运行、连接建立、配置文件解析、launch profile 解析、P2P 直连、secret/local 模式、本地服务代理。
- 管理层 Go 代码还要同时对旧页面体系和新管理台提供接口，但本轮只分析 Go 侧的 schema、handler、service、router 结构，不展开前端实现。
- 数据面当前同时存在多种协议栈：
  - bridge 主控连接
  - tunnel 转发
  - http/https/http3 代理
  - socks5/mix proxy
  - p2p probe / nomination / telemetry / port mapping

### 3.2 当前最重的目录热点

| 目录 | 非测试文件数 | 大致代码量 | 当前问题判断 |
| --- | ---: | ---: | --- |
| `web/routers` | 31 | 7800+ | 路由、鉴权、协议状态、回调、事件、WS、持久化混在一起 |
| `web/service` | 39 | 7700+ | DTO、业务规则、权限、仓储适配、运行时调用集中 |
| `lib/p2p` | 38 | 7500+ | 算法、状态机、端口映射、历史学习、probe、wire 混杂 |
| `web/api` | 36 | 6800+ | handler、response、catalog、node API 拼装耦合 |
| `client` | 28 | 5400+ | 启动入口、连接、P2P、launch 规格解析共处一个包 |
| `bridge` | 19 | 3600+ | 客户端注册、握手、link、health、P2P 会话都在 bridge 包里 |
| `lib/file` | 13 | 2800+ | 领域模型、JSONDB、索引、迁移、ACL/runtime 初始化纠缠 |
| `lib/conn` | 20 | 2500+ | 连接封装过于万能，协议语义和基础 IO 混在一起 |
| `server/proxy` | 19 | 2400+ | 多代理模式共享基础设施不足，入口/协议/ACL/流控散布 |

### 3.3 贯穿全仓库的结构问题

1. **全局单例太多**
   - 典型点：`server.Bridge`、`server.RunList`、`file.GlobalStore`、`file.GetDb()`、`servercfg.Current()`、`connection.*` 全局变量。
   - 结果：依赖方向不清楚，替换实现困难，测试也只能靠全局重置。

2. **同一文件经常同时负责“输入解析 + 业务规则 + 状态修改 + 响应拼装”**
   - 典型点：`cmd/nps/nps.go`、`cmd/npc/npc.go`、`web/api/*handlers.go`、`web/service/node_control_service.go`、`server/runtime.go`。

3. **包名虽然分层了，但真实依赖方向仍然反复跨层**
   - `web/service` 直接碰 `server`、`file`、`connection`
   - `server` 直接碰 `file`、`bridge`、`proxy`
   - `bridge` 直接碰 `file`、`server/tool`
   - `client` 同时持有 CLI/launch/P2P/domain knowledge

4. **管理面 Go 契约源还不够收敛**
   - `web/api/page_catalog.go`、`web/api/action_catalog.go`、`web/api/*handlers.go`、`web/service/*` 之间仍存在页面语义、动作语义、DTO 语义重复定义。

5. **P2P 相关代码已经从“功能”演变成“子系统”，但仍按零散文件挂在多个包**
   - `lib/p2p`
   - `bridge/p2p_*`
   - `client/p2p_*`
   - `server/p2pstate`

6. **旧管理界面和新管理台长期共存，导致每次改字段或行为都可能双改甚至三改**
   - 这是后续维护成本最高的隐性负担之一。

## 4. 建议的重构阶段顺序

### 阶段 A：先冻结边界，不急着换实现

- 建立统一的 `AppContext` / `RuntimeContext`
- 收口全局单例读取点
- 明确 Repository、Runtime、ProtocolState、ShellAssetResolver 等接口

### 阶段 B：拆管理面后端

- `web/api` 只负责输入绑定和响应转换
- `web/service` 只负责 use case
- `web/routers` 只负责 transport / middleware / protocol runtime

### 阶段 C：拆 bridge / client / p2p

- 把 `p2p` 变成真正的独立子系统
- 把 bridge 握手、链路复用、P2P 编排拆成独立服务
- 把 `npc` 的 launch/profile/config/source 解析从连接运行时彻底分开

### 阶段 D：拆持久化和领域模型

- `lib/file` 从“模型 + 仓储 + JSON 序列化 + runtime 初始化”拆开
- 已不再保留单独的 `lib/store`；原本那层本地存储/快照逻辑直接并回 `lib/file`

### 阶段 E：统一管理面 Go 契约来源

- 保留一个权威页面/动作 schema
- 旧兼容入口只保留最薄 Go 适配层
- 所有管理面入口都依赖稳定 contracts，而不是散落的 handler 私有字段约定

## 5. 模块与文件级整理

下面按“上下文 -> 文件 -> 问题 -> 建议”记录。对高风险业务文件逐项写得更细；对职责单一的工具文件，保留简明说明，后续优先级可放低。

---

## 6. 根目录、构建、配置

### 6.1 目录上下文

根目录承担三类职责：

- 项目构建与发布
- 安装/运行入口
- 默认配置和文档入口

问题是很多“运行时约束”散落在脚本、Dockerfile、默认配置和 Go 代码里，没有统一的“部署模型”。

### 6.2 文件记录

- `README.md`, `README.zh.md`: 仓库入口说明已经偏产品化；问题是和代码真实结构的映射不够强；建议后续补“架构总览 + 包依赖图 + 管理面双栈说明”。
- `CHANGELOG.md`: 版本变化说明较完整；问题是无法直接映射到目录/模块；建议以后按模块分组维护 changelog。
- `go.mod`: 依赖面很广，包含 web、quic、upnp、certmagic、gin、viper 等多类能力；建议按子系统重新审视是否需要拆模块或限制依赖扩散。
- `Makefile`: 当前是实际统一入口；问题不大，但仍把前端/后端/发版逻辑并列平铺；建议将 release 逻辑进一步委派给脚本或 task 层。
- `build.sh`: 打包逻辑清楚；问题是 geodata、前端静态资源、Go 构建目标都在一个脚本里；建议拆 `release-core`, `release-assets`, `release-geodata`。
- `build_assets.sh`, `build_assets_legacy.sh`: 体现新旧前端并存；问题是双构建路径会长期制造认知成本；建议尽快确定唯一资产流程。
- `build_android.sh`: 平台特定脚本；建议独立归档到 `scripts/release/`。
- `Dockerfile.nps`: 同时做前端构建、后端编译、geodata 拉取；问题是职责过重；建议拆成更清晰的多阶段镜像职责。
- `Dockerfile.npc`: 客户端镜像；建议和 `nps` 共享部分发布基础镜像脚本，而不是完全平铺。
- `entrypoint_nps.sh`: 容器入口；建议只保留启动前检查，把配置修补逻辑移到专门初始化脚本。
- `install.sh`, `install.ps1`: 安装路径、服务注册、下载更新逻辑分布在脚本和 Go 代码之间；建议把“下载/落盘/服务注册”拆成清晰步骤或复用统一元数据。
- `conf/nps.conf`: 默认配置已经非常丰富，实际上承担了“系统能力总目录”的角色；问题是字段太多，配置域缺乏分组边界；建议后续按 `app/log/web/auth/runtime/network/bridge/proxy/p2p/feature` 分段保持和 `servercfg.Snapshot` 完全一一对应。
- `conf/npc.conf`: 客户端配置默认模板；建议和 `client/launch*` 的 schema 做严格对齐，避免“文件模式”和“launch 模式”出现字段分叉。
- `conf/multi_account.conf`: 多账号示例；建议明确它只属于某几种 tunnel/host 模式，不要让调用点分散自解析。
- `DOCS_RULES.md`, `GO_CODE_AUDIT_CHECKLIST.md`, `SECRET_P2P_ACCESS_REFACTOR_PLAN.md`, `WEB_FRONTEND_REFACTOR_PLAN.md`: 这些是已经存在的整理型文档；建议后续把跨主题文档统一纳入 `docs/architecture/` 或 `docs/plans/`。

---

## 7. 命令行入口 `cmd`

### 7.1 上下文

`cmd/nps` 和 `cmd/npc` 是整个系统的外壳层，本该只做：

- 参数解析
- 配置加载
- 依赖装配
- 运行模式分发

当前实际还承担了大量运行时细节和兼容逻辑。

### 7.2 文件记录

- `cmd/nps/nps.go`: 同时负责 flag 兼容、配置加载、日志初始化、service 安装控制、daemon reload、runtime 启动；是典型多职责文件；建议拆成 `cli`, `servicecmd`, `bootstrap`, `runtimeentry`。
- `cmd/npc/npc.go`: 同时负责 flag 定义、launch 自动解析、日志/DNS/NTP 初始化、service 命令、连接入口；问题和 `cmd/nps/nps.go` 一样明显；建议拆出 `cli/options`, `cli/commands`, `runtime/bootstrap`。
- `cmd/npc/launch_flags.go`: 负责把 launch payload 反投影到传统 flags；上下文重要但位置尴尬；建议作为 `launch/compat_flags`，不要继续放在 `main` 包。
- `cmd/npc/launch_supervisor.go`: 已经形成独立的“launch 监督器”；问题是仍放在 `main` 包且直接驱动 client runtime；建议升级成 `client/launchsupervisor` 或 `internal/launch/runtime`。
- `cmd/npc/service_runtime.go`: service 生命周期和日志配置耦合；建议把 `configureLogging` 与 `Npc` service wrapper 分开。
- `cmd/npc/launch_legacy.go`: 兼容旧参数/旧行为；建议明确寿命，如果保留就单独标记 compatibility 层。
- `cmd/npc/sdk.go`: 构建标签下的 SDK 入口；建议在包结构上明确“cli 版”和“sdk 版”的共享边界。

---

## 8. 配置、领域模型、持久化核心

### 8.1 `lib/servercfg`

- `lib/servercfg/config.go`: 当前是服务端配置注册、格式解析、候选路径加载、全局存储；问题是“格式注册器 + loader + singleton store”混在一起；建议拆 `registry`, `loader`, `store`。
- `lib/servercfg/config_builders.go`: 配置组装逻辑很重，是真正的 schema builder；建议拆成按配置域的 builder 文件。
- `lib/servercfg/snapshot.go`: `Snapshot` 作为权威配置模型是正确方向；问题是字段太大且 accessor/derived value 不够内聚；建议给每个 config 域增加方法对象。
- `lib/servercfg/management_platform_parsing.go`: 多平台配置解析属于独立子域；建议独立成 `runtime/managementplatform` 包。
- `lib/servercfg/runtime_capabilities.go`, `runtime_management.go`, `runtime_platforms.go`, `p2p_accessors.go`, `accessors.go`, `web_standalone.go`: 这些 accessor/derived helper 散而多；建议围绕 `Snapshot` 子结构做 method 收口。

总体建议：`servercfg` 应成为“纯配置域”，不要再承担任何运行时副作用。

### 8.2 `lib/file`

当前状态：`lib/file` 已经完成本轮结构收口，旧 `file.go` / `store.go` 已收敛为 `jsondb.go` / `local_store.go`，实体 runtime、DB owner、legacy import、access policy、route runtime、snapshot import/export 已按真实边界重新分组。

保留边界：

- `DbUtils` 是当前运行时/CRUD owner，外部包应继续通过 typed owner methods 访问用户、客户端、隧道、域名和索引视图。
- `LocalStore`、`Store`、`ConfigExporter`、`ConfigImporter` 是对外 store/snapshot seam。
- `JsonDb` 的 `persistenceBackend` 是当前 JSON 文件持久化 seam，后续非 JSON 数据库实现应接在这里或同等 owner 边界后面。
- 旧 JSON 配置导入只作为过渡导入/迁移路径保留，不应扩展成第二套长期 live model。

后续重点：不要再围绕文件名做纯结构整理；如果接数据库，应保持热读 runtime index 和 typed mutation owner，不要把 SQL/KV/ORM 查询散到 `web`、`server`、`bridge` 或 `client`。

### 8.3 旧 `lib/store` 层

- 独立 `lib/store` 包已经移除，职责并入 `lib/file`。
- 原先的 `IStore` 命名债已经收口成更符合 Go 习惯的 `file.Store`。
- 本地存储实现、`GlobalStore`、配置快照导入导出现在集中在 `lib/file/local_store.go`，减少了一层重复抽象和跨包跳转。
- 后续数据库接入不应复活旧 `lib/store` 包；应优先扩展 `lib/file` 内的 store/backend seam，并逐步把全量 JSON flush 迁移为 owner mutation 下的增量持久化。

### 8.4 `lib/config`

- `lib/config/config.go`: 客户端配置解析器承担了 section 解析、兼容规则、实体构造；文件很重；建议拆 `parser`, `section/common`, `section/tunnel`, `section/host`, `section/local`。

---

## 9. 公共基础设施与网络层

### 9.1 `lib/common`

- `lib/common/addr.go`, `addr_network.go`: 地址工具非常多，已经接近“网络策略杂物间”；建议拆 `addr_parse`, `addr_select`, `addr_external`, `udp_bind`。
- `lib/common/dns.go`: DNS 和自定义解析逻辑应保持独立，避免继续被 runtime 直接改全局状态。
- `lib/common/file.go`, `run.go`, `time.go`, `security.go`, `util.go`, `const.go`: 通用工具太集中，建议按主题归档，避免继续增长成超大“万能包”。
- `lib/common/proxyacl.go`: 访问控制规则与 `lib/policy` 有概念重叠，建议逐步统一。
- `lib/common/netpackager.go`, `pool.go`, `pprof.go`: 工具职责相对单一，可保留，但建议减少被高层直接依赖。

### 9.2 `lib/conn`

- `lib/conn/conn.go`: 当前是“超级连接对象”，同时处理 flag、长度帧、对象序列化、健康包、link/config/task 读写；过于中心化；建议拆协议层 codec。
- `lib/conn/util.go`: 既做 ACK、copy、UDP5、Proxy Protocol、真实 IP、TLS helper；明显违背单一职责；建议至少拆成 `copy`, `ack`, `proxyproto`, `realip`, `udp5`。
- `lib/conn/listener.go`, `websocket.go`, `quic.go`, `kcp.go`, `tls.go`, `udp.go`, `udp_bind.go`, `udp_other.go`, `udp_windows.go`: 适合作为 transport adapter 层；建议不要再混入高层 link 语义。
- `lib/conn/link.go`, `connect_result.go`, `flow.go`, `wrapper.go`, `teeconn.go`, `framed.go`, `timeout.go`, `snappy.go`, `neterr.go`: 建议围绕“protocol model / io decorator / error taxonomy”再分层。

### 9.3 `lib/transport`

- `listen_linux.go`, `listen_freebsd.go`, `listen_windows.go`, `listen_other.go`: 平台透明监听适配，职责清晰。
- `transport_linux.go`, `transport_freebsd.go`, `transport_windows.go`, `transport_darwin.go`, `transport.go`, `keepalive.go`: 当前是获取真实目标地址和 keepalive 参数；职责相对单一，可保留。

### 9.4 `lib/mux`, `lib/pmux`, `lib/rate`, `lib/goroutine`, `lib/pool`

- `lib/mux/*`: 已经是完整子系统，建议后续单独整理成“多路复用层”；当前问题主要是内部文件很多但外部接口边界不够明显。
- `lib/pmux/*`: 共享端口分流层，建议明确只暴露少量 facade。
- `lib/rate/rate.go`, `conn.go`: 流控本身职责清晰；建议不要再由业务层直接持有内部状态。
- `lib/goroutine/pool.go`, `lib/pool/pool.go`: 并发复制/池化工具相对独立；后续只需补边界命名和依赖反转。

---

## 10. P2P、bridge、client、server 主运行链路

### 10.1 `lib/p2p`

`lib/p2p` 现在已经不是工具包，而是完整 P2P domain/runtime。建议未来独立成清晰子系统。

- `candidate.go`, `candidate_rank.go`: 候选对与提名策略核心。当前 `CandidateManager` 的只读查询已经收成 `RWMutex + HasConfirmed/HasNominated/HasConfirmedOrNominated` 这类显式状态接口，喷洒/提名热路径不再为了判断 bool 反复 clone pair；但 runtime session 对候选管理的访问仍然偏直接，后续仍建议引入更窄接口。
- `session_negotiation.go`, `session_probe.go`, `session_runner.go`, `session_runtime_flow.go`, `session_state.go`, `session_events.go`, `session_effects.go`, `session_handshake_handlers.go`, `session_packet_dispatch.go`, `runtime_session.go`, `runtime_session_io.go`: 这些文件已经接近状态机实现；建议整理成显式阶段和 effect 驱动。
- `punch_plan.go`, `punch_targets.go`, `port_prediction.go`, `history.go`, `policy.go`: 这些是算法/历史学习层；建议和 runtime 分离，不直接触碰网络对象。
- `probe_config.go`, `probe_inventory.go`, `protocol_probe_sample.go`, `nat_observation.go`, `peer_family.go`, `network_family.go`: 是 probe 与环境建模层，适合沉淀为 domain model。
- `protocol.go`, `protocol_bridge.go`, `protocol_udp_packet.go`, `wire_codec.go`, `handshake.go`, `errors.go`: 协议层边界已出现，但还不够纯；建议彻底做 codec/model 层。
- `portmap.go`, `portmap_coordinator.go`: 端口映射复杂度高；建议再单独成 `portmap` 子包。
- `spray.go`, `pacing.go`: 打洞发送策略。当前 `spray` 主循环已经改成直接依赖 `CandidateManager` 的显式状态查询，而不是反复 `ConfirmedPair()/NominatedPair()`；这条热路径的锁竞争和语义都更清楚。后续仍建议与 session orchestration 进一步解耦。
- `telemetry_export.go`, `telemetry_sink.go`, `replay.go`, `util.go`: 周边配套能力；建议统一挂到 runtime observer 层。

### 10.2 `bridge`

- `bridge/bridge.go`: bridge 根对象本身合理，但仍持有太多 channel/global 状态；建议把 client registry、task event、p2p managers 拆成独立依赖。
- `bridge/client.go`: `Node` 和 `Client` 两个运行时实体同处一文件，且负责在线状态、信号/隧道切换、重试、文件绑定；建议拆分。
- `bridge/handshake.go`, `handshake_runtime.go`: 握手认证和注册后运行时行为在两个超重文件里；建议拆 `auth`, `register`, `node_attach`, `version_compat`.
- `bridge/link.go`: bridge 到 client/local 的 link 建立逻辑很核心，当前同时处理 IP 校验、本地 scheme、mux/quic stream、NeedAck；建议抽出 `LinkDialer`。
- `bridge/listener.go`, `tls_gateway.go`: 监听接入和 TLS gateway 可独立成 transport adapter。
- `bridge/health.go`, `idle.go`, `replay.go`: health 和清理逻辑不应该直接扫全局 DB；建议通过更窄的 runtime registry 做状态同步。
- `bridge/p2p_session.go`, `p2p_session_flow.go`, `p2p_session_lifecycle.go`, `p2p_session_probe.go`, `p2p_session_telemetry.go`, `p2p_session_telemetry_sink.go`, `p2p_association.go`, `p2p_resolve.go`: 说明 bridge 已经介入 P2P orchestration；建议未来把它缩成 `P2PBridgeCoordinator` 适配层，真正逻辑下沉到专门包。
- `bridge/config.go`: bridge 配置辅助应尽量依赖 `servercfg`，不要自己再维护配置语义。

### 10.3 `client`

- `client/client.go`: `TRPClient` 既做主连接生命周期，也处理 channel、新连接、ping、P2P 加入；建议拆 `control session`, `work channel`, `p2p hook`.
- `client/client_channel.go`, `client_channel_handler.go`: 这是可拆出的工作连接子域。
- `client/control.go`, `control_conn.go`, `control_proxy.go`, `control_status.go`: 连接建立、TLS 校验、端口补全、状态查询、代理建立应进一步分开。
- `client/health.go`: 本地健康检测当前已经收成“health 条目堆 + due pop / reschedule”调度，不再每轮全量扫描 `healths` 再重建 heap；这条路径对可读性和运行期开销都更友好。后续仍建议继续向可插拔 `HealthReporter` 演进。
- `client/launch.go`, `launch_profile.go`, `launch_resolve.go`, `launch_config.go`, `launch_common_build.go`: 这是一个已经成形的 launch 规格层；优点是比旧 CLI 更清晰，问题是仍和 `cmd/npc` 强耦合；建议先继续按功能收口，再决定是否抽成更独立的包。
- `client/p2p_manager.go`, `p2p_manager_helpers.go`, `p2p_manager_monitor.go`, `p2p_manager_secret.go`, `p2p_manager_transport.go`, `p2p_manager_visitor.go`, `p2p_bridge.go`, `p2p_provider_session.go`, `p2p_provider_transport.go`, `p2p_route_state.go`, `p2p_association_runtime.go`: 这些文件共同构成 client 侧 P2P runtime，建议明确 visitor/provider/route/secret 四个子域。

### 10.4 `server`

- `server/server.go`: 组装 bridge、probe server、dashboard、db 初始化、web server 启动；职责过重；建议变成纯 orchestration。
- `server/runtime.go`: dashboard cache、client 生命周期清理、system metrics 全混在一起；建议拆 `dashboard service`, `client lifecycle monitor`, `net sampler`。
- `server/server_flow.go`: 流量持久化 session 管理应该抽成独立后台任务。
- `server/server_tasks.go`: task 启停、mode 分发、runtime 删除是核心 use case；建议和 `server.StartServerEngine` 分离。
- `server/server_runtime_clients.go`, `server_bridge_runtime.go`, `tunnel_list.go`, `tunnel_list_client.go`, `tunnel_list_host.go`, `web_server.go`: 已经按主题拆了一步，但对 `file.GetDb()` 和全局运行时依赖依旧太深。

### 10.5 `server/connection`, `server/p2pstate`, `server/tool`

- `server/connection/connection_config.go`: 这一块已经从“原始大文件”收成更自然的两块：`connection_config.go` 负责 `Config + typed runtime accessors + ApplySnapshot/apply globals + listener getters/shared-mux fallback`，`shared_mux.go` 负责共享端口复用本身。包级变量仍然保留以兼容现有调用方，但配置提取逻辑已集中到 `Config` 对象与 `applyConnectionConfig`。后续仍建议继续向下推进：
  - `ConnectionConfig`: 继续补全为真正的权威配置对象，逐步减少包级变量直读
  - `ListenerFactory`: 把 bridge/http/web listener 创建再收口到更统一的工厂层
  - `SharedMuxManager`: 继续维持在单独文件，避免复用决策重新混回 listener 层
  - `RuntimePorts` 或 `Endpoints`: 对外暴露只读派生信息
  - 当前已补上 reload 链路中的 `connection.ApplySnapshot(current)`，避免配置文件重载后 `BridgePath`、`BridgeHost`、`WebPort` 等运行时字段仍停留旧值；但监听器端口和桥接 WS/WSS listener 自身仍属于重启生效范围。
- `server/connection/shared_mux.go`: 共享端口复用应作为 connection manager 内部实现，不该被太多包感知。
- `server/p2pstate/registry.go`: P2P 会话探测态存储，职责清楚；当前读路径已经收成 `RLock + 复制快照后锁外排序`，避免 `GetObservations(...)` 这类读操作长时间占着会话锁。这一轮又把 `ValidateToken/LookupSession/RecordObservation/AcceptPacket/GetObservations` 的过期判断和快照提取收成了 session 级 helper，并统一到单一 `currentProbeTime` 时钟来源，避免在热路径里重复手写“取时间 -> 加锁 -> 判过期 -> 复制快照”模板。后续仍建议注入时钟/存储以便替换。
- `server/tool/utils.go`: 现在同时有端口检查、生成、统计抽样，以及 tunnel lookup / web virtual listener helper；职责仍偏杂，后续可按 `portpolicy`、`metricshelper`、`virtual dialing helper` 再收口。

### 10.6 `server/proxy`

- `server/proxy/base.go`: 目前是所有代理模式的中心基类，负责 ACL、认证、流控、连接建立、限速；过于关键；建议拆为可组合 middleware。
- `server/proxy/auth_policy.go`: 方向正确，建议把认证策略全部收口到这里，不再散落在 `BaseServer.Auth` 和 HTTP 代理逻辑里。
- `server/proxy/http_proxy_handler.go`, `tunnel_server.go`, `udp_server.go`, `udp_server_session.go`, `udp_server_io.go`, `transparent_tcp_handler_nonwindows.go`, `transparent_tcp_handler_windows.go`, `secret_server.go`, `p2p.go`: 每种模式都在长出自己的行为分支；建议统一入口模板。
- `server/proxy/socks5_handler.go`, `socks5_auth.go`, `socks5_udp_registry.go`, `socks5_udp_session.go`: SOCKS5 已经是完整子域；建议分成独立子包而不是继续挂平级。
- `server/proxy/httpproxy/httpproxy.go`: HTTP proxy 总协调器同时初始化 ACME、监听器、错误页、HTTP/3；职责过多。
- `server/proxy/httpproxy/http.go`: 这是 HTTP 反向代理主逻辑，路径重写、限流、认证、转发、CORS、H3 广告全部在一处；建议按 request pipeline 拆。
- `server/proxy/httpproxy/https.go`: HTTPS 与 HTTP/3 现在共享同一条 secure listener/runtime 语义；后续仍应保持成协议适配层，不要继续承载更多业务规则。

---

## 11. 管理面后端：`web/service`、`web/api`、`web/routers`

### 11.1 总体判断

这是当前最值得先整理的区域。好消息是代码已经开始朝“services + app + state + catalog”的方向演进；坏消息是边界还没有真正封住。

### 11.2 `web/service`

- `web/service/services.go`: `Services` 聚合根是正确方向；建议继续强化，使 transport 不再直接访问 `file`/`server` 全局。
- `web/service/backend.go`: 已复核并已完成第一轮拆分。当前 `backend.go` 只保留 `Repository` / `Runtime` 契约和 `Backend` 聚合，默认实现已移到 `backend_repository.go`、`backend_runtime.go`、`backend_clone.go`。这一轮先解决“单文件多职责”，未改变现有接口和行为。后续仍建议继续把 `defaultRepository/defaultRuntime` 再细分为读写接口与更窄的组合接口，避免一个 service 因为只需要 2 个方法却依赖整个 `Repository`。
- `web/service/support.go`: 这一类“通用业务规则聚集地”已经开始拆散。当前相关辅助逻辑已按 `auth_key`、`bridge_support`、`acl_support`、`resource_status_support` 分文件收纳，后续应继续避免再回到单文件杂糅模式。
- `web/service/errors.go`: 可保留，建议统一错误分类并附带错误码。
- `web/service/auth_service.go`, `authorization_service.go`, `permission_resolver.go`, `principal_context.go`, `protected_action_access.go`, `http_auth.go`, `login_policy.go`, `standalone_token.go`: 认证授权已形成子域；建议进一步收口 session、token、platform principal 的不同入口。
- `web/service/client_service.go`, `client_input.go`: 客户端 use case 相对清晰，但仍夹杂 config/二维码/owner 绑定逻辑；建议拆 command/query。
- `web/service/user_service.go`: 用户逻辑较集中，但也负责资源统计和限制生效；建议补 `UserQuotaService`。
- `web/service/global_service.go`, `system_service.go`: 职责较清晰，可保留。
- `web/service/index_service.go`, `index_service_tunnel.go`, `index_service_host.go`, `index_service_support.go`, `index_input.go`, `mutation_clone.go`, `quota_store.go`: tunnel/host 相关 use case 已经拆了一步；建议继续把“输入规范化”“复制/克隆”“额度判断”分离。
- `web/service/node_control_service.go`: 已复核并已完成第一轮拆分。当前 `node_control_service.go` 只保留 `NodeControlService` 与 `DefaultNodeControlService`，其余类型已经分散到 `node_control_payload_status.go`、`node_control_payload_usage.go`、`node_control_payload_callback.go`、`node_control_input.go`、`node_control_internal.go`。这一轮先把“契约 / 输出模型 / 输入模型 / 内部视图”分开，后续仍建议继续把 callback queue、traffic、kick、sync 等 mutation 场景抽成更独立的子服务。
- `web/service/node_control_helpers.go`: 当前主要承载 node control 的共享辅助逻辑。`node_control_read.go`、`node_control_mutation.go` 已经在后续轮次中继续拆散为按场景划分的文件，这个方向应继续保持。
- `web/service/node_batch_service.go`: 批处理协议独立性较好，建议保留，但避免直接依赖上层 DTO。
- `web/service/node_access.go`, `node_descriptor.go`, `node_runtime_identity.go`, `node_runtime_status.go`, `node_operation_store.go`, `node_storage.go`, `node_traffic_reporter.go`, `management_platform_runtime.go`, `management_platform_store.go`: 这是 node control plane 的支撑层，未来可继续独立成 `web/nodecontrol` 子包。

### 11.3 `web/api`

- `web/api/app.go`: `App` 已经是管理 API 组合根；方向正确，但还保留很多惰性初始化和默认注入逻辑；建议让装配更显式。
- `web/api/request_context.go`, `session_identity.go`, `principal.go`, `actor.go`, `node_actor_context.go`: 这些上下文对象可以沉淀为 transport-neutral model。
- `web/api/request_value.go`, `response_payload.go`, `bootstrap_payload.go`, `page_model.go`, `page_rendering.go`, `native_helpers.go`, `event_fields.go`: 建议围绕“输入绑定 / 输出 DTO / page model / event model”分开。
- `web/api/action_catalog.go`, `page_catalog.go`: 当前是新管理台和旧模板的关键桥接文件；很有价值；问题是 catalog 与 handler/controller 名称直接绑死；建议成为唯一 schema 来源。
- `web/api/auth_handlers.go`, `login_handlers.go`, `client_handlers.go`, `user_handlers.go`, `index_handlers.go`, `global_handlers.go`: 都存在“绑定参数 + 调 service + 文本消息翻译 + event 发射”的混合职责；建议统一 handler 模板。
- `web/api/node_handlers.go`, `node_routes.go`, `node_resource_handlers.go`, `node_resource_mutation_helpers.go`, `node_client_*`, `node_host_*`, `node_tunnel_*`, `node_user_mutation_handlers.go`, `node_global_handlers.go`: node API 资源化方向明确，但文件数已经很多，建议进一步按 resource 拆目录，而不是继续堆在一个包。

### 11.4 `web/routers`

- `web/routers/router.go`: 新管理面主路由；职责还算合理，但注册逻辑很多；建议抽 route modules。
- `web/routers/router_state.go`: `State` 是非常关键的整理成果；建议后续所有 router 依赖都从这里进入。
- `web/routers/middleware.go`: 中间件过多且种类混杂，包含 session、request data、权限、ownership、machine API 判断；建议拆 `auth_mw`, `ownership_mw`, `node_mw`。
- `web/routers/request_state.go`, `api_context.go`, `request_auth.go`, `cors.go`, `managed_runtime.go`: 适合作为 request/runtime 装配层。
- `web/routers/node.go`: node 路由注册、token 访问、platform actor 解析都在一个大文件里；建议拆 `node_auth`, `node_platform_actor`, `node_route_register`.
- `web/routers/node_ws.go`, `node_ws_dispatch.go`, `node_ws_dispatch_helpers.go`, `node_ws_events.go`, `node_ws_reverse.go`, `node_ws_api_context.go`, `node_ws_resource_dispatch.go`, `node_ws_legacy_dispatch.go`: 这是完整 WS 子系统；建议单独成目录。
- `web/routers/node_callbacks.go`, `node_callback_admin.go`, `node_callback_queue.go`: callback runtime 复杂度已经足够高，应当成为独立模块。
- `web/routers/node_event_log.go`, `node_runtime_storage.go`, `node_operation_recording.go`, `node_operations_store.go`, `node_idempotency.go`, `node_realtime_sessions.go`: 协议状态持久化、事件日志、幂等、会话失效都在 router 层，说明 transport 和 protocol runtime 还没分开。
- `web/routers/node_batch.go`, `node_changes.go`, `node_config_import.go`, `node_compat_routes.go`: 是 node 协议兼容层，建议统一挂在 `nodeprotocol` 目录。

---

## 12. 旧 Web 兼容相关 Go 适配层

当前过渡版的兼容边界需要按新的重构目标收紧：

- 保留：
  旧 JSON 配置导入
  旧版客户端的最薄连接兼容，但只服务于 TCP 隧道和域名转发
- 不保留：
  任何为了兼容而继续扩散的 Web/会话/控制器壳层
  与 TCP 隧道/域名转发无关的旧版客户端行为兼容
  导入完成后仍长期并行存在的 legacy 运行时字段或兼容模型

后续阅读本文件时，凡是提到 legacy/compat 的地方，都应默认按上面的收紧边界理解，而不是按“尽量保留旧行为”理解。

另外再补两条解释口径：

- 兼容判断基准是已发布/已提交的旧版本，不是当前工作区里尚未提交的大量草稿改动。
- 旧 `lib/store` 原本就属于后加的新层，没有旧版本兼容包袱；现在已经并回 `lib/file`。旧 JSON 导入只需要在仓库里保留一个清晰 owner，不要求额外保留单独 store 包。

### 12.1 `web/framework`

- `web/framework/session.go`: session store 初始化、cookie 安全策略、gin 绑定都在这里；建议保持，但减少全局 provider。
- `web/framework/request_context.go`: 适合作为旧模板渲染上下文适配层。
- `web/framework/captcha.go`: 独立能力，职责清晰。

### 12.2 `web/ui`

- `web/ui/render.go`: 旧模板渲染层，可保留为兼容层。
- `web/ui/viewdata.go`: 页面公共数据对象，职责明确。
- `web/ui/shell.go`, `shell_assets.go`: 新 React 管理台挂载壳层，当前是双栈过渡的关键点；建议未来明确它只是“资产装载器”，不要再承担 fallback 业务。

### 12.3 已移除的 `web/controllers`

- 该目录已在当前重构轮次删除。
- 原先的兼容包装职责已经直接并回 `web/service`，`web/routers` 的剩余测试调用点也已改为直接使用 `web/service`。
- 后续不要重新引入这类中间兼容层；如果需要新逻辑，应直接落在实际拥有行为的包里。

---

## 13. 其余支撑包

### 13.1 安全、日志、缓存、索引

- `lib/crypt/crypt.go`, `otp.go`, `tls.go`, `client_hello.go`: 当前把加密工具、TOTP、TLS 证书、ClientHello 读取放在一个包里，范围略大；建议再细分但优先级低于业务层。
- `lib/logs/logger.go`: 日志系统统一入口，职责清晰；建议后续减少全局初始化副作用。
- `lib/cache/lru.go`, `cert.go`: 缓存层整体可用；证书缓存更像 SSL 子系统的一部分。
- `lib/index/index.go`, `host.go`: 索引能力职责清晰，但与 `file` 包过度绑定。

### 13.2 ACL 与策略

- `lib/policy/mode.go`, `source.go`, `destination.go`, `domain.go`, `ip.go`, `geoip.go`, `geosite.go`: 这是相对完整的规则引擎，建议作为独立 domain 保持。

### 13.3 运行辅助

- `lib/daemon/daemon.go`, `reload.go`: 服务化控制逻辑独立，可保留。
- `lib/serverreload/runtime.go`: 运行时重载逻辑目前会触发日志、系统配置、bridge 配置、副作用用户创建；问题是职责过多；建议变成显式 reload pipeline。
- `lib/version/version.go`: 版本语义清晰。
- `lib/sheap/heap.go`: 小工具，优先级低。

---


## 14. 当前结构状态（替代历史变更记录）

从这里开始，文档不再保留按轮次堆积的 `14.x` 历史修改流水账。

之前的结构整理记录已经被吸收进当前结论，后续请把这里当作“当前仓库状态说明”，而不是“历史施工日志”。如果后面继续改仓库，应优先更新这里的当前状态和判断，而不是再追加大段时间顺序记录。

### 14.1 现在应如何阅读这个仓库

推荐的理解顺序：

1. 先看 `cmd`，确认服务端和客户端如何启动。
2. 再看 `lib/servercfg`、`lib/file`，理解配置、领域对象、持久化根。
3. 再看 `server/connection`，理解 listener、reload、桥接路径和运行时配置消费。
4. 然后按主链阅读：
   - 服务端运行主链：`server`
   - 协议与桥接：`bridge`
   - 客户端主链：`client`
   - 代理模式实现：`server/proxy`
5. 只有在需要管理面时再看 `web/service`、`web/api`、`web/routers`。

这一顺序能先建立“配置与启动”模型，再理解长生命周期运行对象，最后再进入各协议与模式分支。

### 14.2 当前已稳定的主链结构

以下不是“不会再动”，而是“已经从过度分散状态收口到可读、可继续重构的结构”。

#### A. `client`

当前主入口：

- `client/client.go`
  `TRPClient` 根对象，以及 startup、main signal、main loop、join 等主链入口。
- `client/client_shutdown_lifecycle.go`
  关闭、summary、transport shutdown。
- `client/control.go`
  客户端控制面连接、状态读取、注册、本地 IP 等控制操作。
- `client/control_conn.go`
  control connection 的底层连接与请求发送细节。
- `client/launch.go`
  launch schema、值对象、normalize。
- `client/launch_profile.go`
  profile 读取与 overlay。
- `client/launch_resolve.go`
  source resolve 与输入来源处理。
- `client/p2p_manager.go`
  `P2PManager` 根对象。
- `client/p2p_manager_session.go`
  secret / visitor / provider session 主链。
- `client/client_channel_runtime.go`
  通用 channel 主链，以及 file-channel / WebDAV file server manager。
- `client/client_p2p_provider_root_channel.go`
  provider channel 处理链。
- `client/client_p2p_provider_root_transport.go`
  provider transport 主链，以及底层 QUIC / KCP / stream / mux plumbing。
- `client/client_p2p_state_root.go`
  association / peer policy / ACL / state store。

当前判断：

- `client` 这条线的结构整理已经基本完成。
- 现在继续处理 `client`，应该开始做实现级重构，而不是再做文件拆并。
- 唯一不建议继续硬并的是 `launch.go` 这一组，因为 `schema / profile / resolve` 边界仍然真实存在。
- `client.go` 当前已经把 startup、main dispatch、P2P join、control conn UUID 维护里的那批单点中转 helper压回主流程；后续若继续动 `client`，应优先针对“行为语义不完整”的实现级问题，而不是再为了层次感拆出薄包装。
- `client/client.go` 当前主连接生命周期也已经固定成更容易对照的阶段：`Start() = parent ctx 归一化 -> client ctx/cancel 建立 -> startup runtime -> main loop`，`establishSignalRuntime()` 则是 `legacy main signal` 与 `bridge tunnel main signal` 两条真实协议路径。后续如果继续改，应优先沿这两条主阶段推进，而不是重新塞回一串只有中转价值的 `start*/handle*` 薄包装。
- `client/client.go` 的 main signal / main event 读取当前也已经按协议动作收口：`openTunnelMainSignal()` 只负责按 tunnel type 选择 `mux/quic` 打开路径，再统一走 main signal wrap/announce；`readMainEvent()` 则固定成 `read flag -> decode payload(bind/punch-start)/drain unknown`，`dispatchMainEvent()` 对 punch-start 的处理保持为 `shouldRecord -> record runtime -> optional provider join`。这几段对应的是协议语义本身，可以保留具名 helper，但不要继续抽成新的 facade。
- `client/control_conn.go` 当前也已经把 control connection 主链固定成更容易审计的三段：`connection plan(server/path/alpn/timeout)`、`transport open(tcp/tls/ws/wss/quic/kcp)`、`auth handshake(version -> legacy/runtime auth -> optional fingerprint/uuid)`。这些 helper 对应的是连接协议阶段本身，不应再回退成一个 300 多行的大函数，也不需要再额外拆出新的 facade 包。
- `client/control.go` 当前也已经把一次配置运行收成少量真实语义阶段：`runtime prepare(DNS/NTP/TLS tp)`、`local-server mode or remote config session`、`public config publish`、`host/task publish`、`web access credential resolve`、`runtime client start`。代理建连路径也已固定成 `proxy URL parse -> env/socks5/http dial -> CONNECT request/response` 的显式链路；后续如果继续改，应在这些协议阶段里补语义，而不是重新把配置发布、代理 CONNECT、web login fallback 揉回主函数。

#### B. `bridge`

当前主入口：

- `bridge/bridge.go`
  `Bridge` 根对象、listener bootstrap、reserved TLS gateway、node runtime helper。
- `bridge/handshake.go`
  握手根链与 replay cache。
- `bridge/handshake_runtime.go`
  runtime dispatch root、main/config/visitor/tunnel lifecycle，以及 `register/secret/file/p2p resolve-connect-session` 辅助控制分支。
- `bridge/link.go`
  `SendLinkInfo -> target resolve -> transport open` 主链。
- `bridge/config.go`
  配置流 session root、flag dispatch、`NEW_CONF/WORK_STATUS/NEW_HOST`，以及配置流扫描时复用的 typed DB entry 清洗 helper。
- `bridge/config_task.go`
  `NEW_TASK` 端口展开、file runtime 绑定、variant 发布/回滚。
- `bridge/p2p_resolve.go`
  route payload、provider 选择、association attach、probe/session hint。
- `bridge/p2p_session.go`
  P2P session root、manager、state guards、progress/completion 决议。
- `bridge/p2p_session_control.go`
  session control envelope、summary/go dispatch、abort/completion 副作用。
- `bridge/p2p_session_telemetry.go`
  telemetry model、sink、emit。

当前判断：

- `bridge` 的 root 阅读路径已经明显收平。
- `bridge` 侧默认配置来源应继续集中。`p2p_resolve.go` 的 probe runtime / policy / timeouts，以及 `handshake.go` 的 runtime auth 判定，都应优先通过具名动态 root 读取，而不是各自散落 `connection.CurrentP2PRuntime()` / `servercfg.Current()`；这样既保留“默认跟随当前全局状态”的行为，也让配置来源更容易审计。
- `bridge.go` 的 listener/bootstrap 入口也应遵循同一原则：bridge listener runtime、QUIC runtime、gateway path 判断等默认来源应集中在具名动态 root，而不是在启动链和 gateway 判断里零散直读全局。
- `bridge` 的协议主路径里，DB 来源也应优先集中到同一个动态 root；`handshake`、`handshake_runtime`、`p2p_resolve`、`config` 这类文件不应各自直接散写 `file.GetDb()`，否则很难回答“当前 bridge 协议到底依赖哪份 DB 状态”。
- `bridge/handshake.go` 的 `CliProcess` 主链现在更适合维持成“版本读取 -> client 鉴权 -> runtime payload 解码 -> 进入 typeDeal”这四段，而不是再回退成一个巨型函数把 legacy/runtime 两套协议细节全揉在一起。提炼阶段时应保留在同文件内，避免为了分层再引入一串只会增加跳转的小对象。
- `bridge/handshake.go` 里的 runtime auth 现在也更适合维持为固定的同文件协议阶段：`auth envelope 读取 -> 时间窗校验 -> client 解析 -> runtime info 解密/应用 -> HMAC/replay 校验 -> server 响应写回`。这些阶段适合用协议名 helper 显式表达，但不应拆成跨文件 facade。
- `bridge/handshake_runtime.go` 当前已经固定成更完整的 runtime dispatch 根文件：`runtime work envelope 读取 -> auth gate -> dispatch -> main/config/register/secret/file/tunnel/visitor runtime serve`。`flag/uuid/random` 的 envelope 与 `register/secret/file` 这些控制旁支都属于同一条 runtime 分发链本身，适合同文件内具名表达；但不应再为了所谓分层扩展成新的 facade 对象。
- `bridge/handshake_runtime.go` 当前也已经把 `p2p resolve/connect/session join` 这组三个 runtime 分发分支收回同文件；它们和 `register/secret/file` 一样，本质上都是 `typeDeal()` 下的 work-flag 分支，不需要再拆成邻接薄文件。真正独立的 P2P 实现子域仍然留在 `p2p_resolve.go`、`p2p_probe_runtime.go`、`p2p_session*.go`。
- `bridge/handshake.go` 当前连主链内部也已经收成少量真正有语义的阶段点：`readBridgeHandshakeVersion()` 下分 `min version` 与 `client version` 读取，legacy auth 走 `server version write -> key read -> client lookup -> verify success`，runtime auth 则先构造 `bridgeRuntimeAuthContext`，再统一走 envelope 校验与响应写回。这里保留的是握手协议阶段，不是为了抽象纯度制造新的层。
- `bridge/handshake_runtime.go` 里 `typeDeal()` 现在也已经先解析 `bridgeRuntimeDispatch(addr/work/isPub)`，`handleTunnelWork/handleVisitorWork` 共同复用 `openRuntimeTunnelWork(...)` 这个“首个 runtime channel 才允许打开 mux/quic transport”的阶段点。这样 `auth gate / public-client flag / transport open` 都各自只有一个审计位置，后续不要再把它们散回多个 `switch` 分支里。
- `bridge/handshake.go` 当前也已经把 auth 上下文显式收成了同文件阶段：version 读取现在拆成 `min-version payload -> client version payload`，runtime auth 则走 `read envelope -> validate timestamp -> resolve runtime auth context(id/client/env) -> verify HMAC/replay -> write response`，`CliProcess()` 本身只负责 `validate conn -> read handshake -> authenticate -> hand off to typeDeal`。这类 helper 保留的是握手协议语义，不是再造新的 auth facade。
- `bridge/handshake_runtime.go` 当前也已经把 `register / secret / file / p2p resolve / p2p connect / p2p session join` 这些辅助分发分支全部收回到 runtime 根文件里；这样 `typeDeal()` 的 `flag -> dispatch` 顺序可以在一个文件里看完。后续不要再把这些很短的 runtime work 分支重新拆成邻接薄文件。
- `bridge/p2p_resolve.go` 现在更适合维持为三段：`task/provider route 解析`、`association/access route 绑定`、`probe config 与 summary hints 生成`。其中 provider 选择应显式表达“task -> runtime client -> hinted/default node -> current signal”，probe config 应显式表达“runtime root -> base hosts -> triple-port endpoints”，不要把 route 解析和 probe 生成重新揉成一个大函数。
- `bridge/p2p_resolve.go` 当前已经收回到 500 行以内，并且只保留 route / association 主链：`password -> task selection -> provider runtime(client/node) -> visitor/provider association attach -> current signal 校验`。`resolveP2PRouteByPassword()` 现在虽然还保留兼容性的 tuple 返回，但内部已经先构造具名结果，再由调用方决定是走 `bridgeP2PResolvedRoute` 还是旧接口，后续不要再把这些阶段揉回一串匿名返回值。password 查 task 的路径也应继续保持“当前 db root 优先，索引 miss 时退回当前 db 扫描”，避免全局 password index 在切换 runtime db root 时把另一个 db 的 lookup 污染掉。
- `bridge/p2p_probe_runtime.go` 当前承接了原来混在 `p2p_resolve.go` 里的另一半真实子域：`probe config`、`base host 采集`、`server observation merge`、`summary hints`、`session/go delay`。这样 `p2p_resolve.go` 不再同时背 route 和 probe 两套语义，后续如果继续改 probe policy、family hints、timeout 预算，应优先在这个文件内保持 `runtime root/config root -> probe model -> summary/timing` 的阅读路径。
- `bridge/config.go` 当前已经回到更自然的配置流根文件：保留 `session state`、`read flag -> dispatch by flag`、`NEW_CONF` 注册分支，以及同一条分发链上的 `WORK_STATUS` 与 `NEW_HOST`。这一轮又把 `WORK_STATUS` 与 bridge runtime 扫描共用的 typed DB entry 清洗 helper 一并收回这里，配置流控制面现在可以在一个文件内顺着看完 `flag -> payload -> scan/publish -> ack`；后续不要再把 `status/host` 或这组扫描 helper 为了减函数长度重新拆散。
- `bridge/config_task.go` 当前固定成 `端口展开 -> variant 构造 -> file runtime 安装 -> publish new/no-store task variant -> response ack/rollback` 这条主链。`NEW_TASK` 仍保留“单 variant 处理结果”和“整体 config stream 是否继续”的区分；像端口占用、NoStore publish 失败这类场景，本来就是“写失败状态后继续配置流”，而 file runtime attach 失败、响应写失败则应终止流。这个 continue/stop 语义已经被 focused tests 锁住，后续不要再把它们混成一个布尔返回值。
- `bridge/config_task.go` 里的 file runtime owner 清理当前也应沿单一语义阅读：预处理阶段如果 attach 成功，后续 publish 失败或 response 写失败都只通过“prepared variant rollback / published variant rollback”回收 owner 映射；这样 `NEW_TASK` 的 file runtime cleanup、task rollback、queue publish rollback 不会重新散到多处分支里。
- `bridge/bridge.go` 的 `StartTunnel()` 也更适合维持成同文件内、按协议命名的 bootstrap 阶段：`TCP`、`TLS`、`WS`、`WSS`、`reserved TLS gateway`、`KCP`、`QUIC` 各自明确启动。这里适合保留少量协议名 helper，让人一眼看清监听顺序和每种 transport 的握手方式；但不应再抽象成泛化 listener launcher，否则启动链会重新失去可审计性。
- `bridge/bridge.go` 的 reserved TLS gateway 现在也应保持成 `TLS handshake -> prefix read -> TLS / HTTP prefix 路由 -> websocket request 解析 -> WSS virtual listener` 这条明确路径。这里保留的是协议阶段 helper，不是通用 gateway 抽象；后续不要再把这条链压回一个大函数。
- `bridge/bridge.go` 当前也已经把 listener bootstrap 与 reserved TLS gateway 的上下文收成了同文件、按协议命名的阶段：bootstrap 走 `reserved TLS -> TCP -> TLS -> WS -> WSS` 的显式打开顺序，reserved gateway 则走 `accept+prefix -> prefix dispatch -> HTTP request decode -> websocket handoff`。这些 helper 对应的是 transport 启动和 gateway 协议本身，不是为了抽象纯度再造新的 runtime façade。
- `bridge/bridge.go` 的 runtime health 更新也应继续保持成单一语义点：`GetHealthFromClient(...)` 负责重试与 stale signal 退出，task/host 的健康写回集中到同文件 helper，避免健康状态写回逻辑在 `host/task` 两条扫描里重复展开。
- `bridge/bridge.go` 的 runtime health 更新现在也已经明确拆成三段：`consumeBridgeClientHealth(...)` 负责 health read loop 与 timeout retry，`applyBridgeTaskTargetHealth(...) / applyBridgeHostTargetHealth(...)` 负责写回 task/host runtime target health，`cleanupBridgeClientHealthSignal(...)` 负责 stale signal 关闭与空 runtime client 清理。后续不要再把 read loop、db 写回、signal cleanup 混回一个大函数。
- `bridge/bridge.go` 的 runtime client map 读取这轮也已经固定到 `lookupRuntimeClient/runtimeClientFromValue`：`DelClient`、`loadRuntimeClient`、`loadOrStoreRuntimeClient`、`collectPingClosedClients` 不再各写一份 `Load + type assert + invalid cleanup`。这里要保留两个细节差异：lookup 路径会清理 invalid entry，而 ping 收集对 invalid value 仍只做“加入 closed 列表”；另外 ping 关闭预算现在应继续通过 `client.collectPingHealth()/notePingUnavailable()/resetPingRetry()` 留在 `client.go`，不要再让 bridge 直接改 client 的内部 retry 字段。
- `bridge/link.go` 当前也已经把建链路径固定成更容易审计的阶段：`SendLinkInfo()` 只负责 `request 校验 -> client/runtime target 解析 -> tunnel open -> link info write -> ACK -> legacy UDP warn`，而本地目标则固定成 `udp5 shortcut -> scheme parse(tunnel/bridge) -> local target open -> direct dial fallback`。这里保留的是建链协议阶段，不要再把 `scheme`、`route uuid`、`ACK cleanup` 这些语义点重新散回一堆临时分支里。
- `bridge/link.go` 的 client runtime 失败判定现在也应继续保持成两段显式 helper：`resolveClientRouteNode(...)` 负责把显式 route uuid 统一收成 `ready / retry / missing / offline`，`classifyClientTunnelFailure(...)` 则负责把默认 route 的 tunnel 失败统一收成 `retry-connect / retry-reconnect / offline / unavailable`。这样 `resolveClientUUIDTunnel()`、`clientNodeUnavailable()`、`clientTunnelUnavailable()` 不再各自手写一套 grace/signal/tunnel 分支，后续不要再把这些判定散回多个入口。继续往前，这条链上稳定下来的通用失败面也已经收成 sentinel：`missing route uuid`、`client connecting`、`client connect unavailable`、`tunnel reconnecting`、`client offline`、`tunnel unavailable` 不再依赖裸字符串，focused tests 也已经切到 `errors.Is(...)`。
- `bridge/link.go` 的本地 target 路径现在也应继续保持成 `udp5 shortcut -> scheme parse(tunnel/bridge) -> local target open -> direct dial fallback` 这条阅读路径，并且显式保留两个 runtime 细节：`bridge://...` listener 缺失时返回可审计错误而不是直接 nil panic，direct dial 统一使用 trim 过的 host 再走 `FormatAddress(...)`，避免空格 host 在直连路径上产生隐式地址错误。
- `bridge/client.go` 的 node/client runtime 也应继续维持“typed node 读取与无效 entry 清理共用一条路径”的结构。`AddFile`、`GetNodeByFile`、`GetNodeByUUID`、`HasOnlineNode`、`SnapshotNodes`、`OnlineNodeCount`、`RemoveOfflineNodes` 这些入口如果都各自手写一套 `sync.Map` 类型断言和清理逻辑，后续很容易把 file mapping、nodeList、LastUUID 的一致性重新弄散。
- `bridge/client.go` 现在也已经把 `uuid -> node` 读取固定成 `lookupNodeByUUID()` 这条状态链：调用方能显式区分 `ready / missing / invalid(cleaned)`，`AddFileOwner`、`GetNodeByFile`、`GetNodeByUUID`、`CheckNode` 都已经接到这条路径上。后续不要再在这些入口里手写一份 `sync.Map.Load + type assert + removeNodeEntryIfCurrent/pruneMissingNodeUUID`。
- `bridge/client.go` 当前也已经把“当前节点选择”和“文件 owner 解析”收成少量有语义的同文件 helper：`currentOrNextNodeUUID()` 负责 `LastUUID` 复用/按模式补选，`resolveFileRouteNode(...)` 负责 `file owner -> typed node -> stale owner/missing node 清理`。这类 helper 直接对应运行时语义，后续应保留；不要再回退成在 `CheckNode()/GetNodeByFile()` 里手写一整段 `LastUUID`、`sync.Map`、owner 清理逻辑。
- `bridge/client.go` 的 file mapping 现在也已经统一到 `addFileOwnerToRoute/removeFileOwnerFromRoute` 这组值语义 helper：`single owner string`、`owner pool`、`invalid mapping` 的新增/删除/清理不再在 `AddFileOwner`、`RemoveFileOwner`、`pruneFileMappings`、`GetNodeByFile` 各写一份 switch。`pruneFileMappings(...)` 也改成了锁内单次遍历处理，不再先扫 key 再二次删除。
- `bridge/client.go` 的候选判定现在也已经显式化到 `evaluateNodeCandidate(...)`：`CheckNode()` 与 `GetNodeByFile()` 都统一复用 `ready / defer / prune / missing / invalid` 语义，但 `CheckNode()` 会在 defer 时通过 `nextDistinctNodeUUID(...)` 尽量切到不同候选继续尝试，而 `GetNodeByFile()` 只尊重 client connect grace，不会因为 node join grace 保留离线 file owner。这里属于运行时策略本身，后续可以继续细化，但不要再散回多个入口各写一份 if 链。
- `bridge/client.go` 的 offline 节点清理现在也应继续维持显式语义点：`RemoveOfflineNodes(...)` 先过 `shouldSkipOfflineNodePrune(...)` 的 client-grace gate，再通过 `shouldRemoveOfflineNode(...)` 统一表达 `keepUUID / join grace / offline retry budget / remove`。`Node.deferOfflineRemoval()` 代表的是“这轮先保留离线节点并消耗一次 retry 预算”，不是泛化重试工具；后续不要再把这条规则散回 `collectPingClosedClients()`、`GetNodeByFile()` 或其他 runtime 入口里。
- `bridge/client.go` 的只读运行时观察路径这轮也收了一步：`Node.Snapshot()` 现在先抓取连接/在线态/统计指针，再在锁外填充流量与速率快照，缩短了 node 读锁占用；`SnapshotNodes()`、`HasOnlineNode()`、`OnlineNodeCount()`、`HasMultipleOnlineNodes()` 则统一走 `collectNodeSnapshots/sortNodeSnapshots/onlineNodeCount`，把“当前节点优先 + join 时间倒序 + UUID 兜底”和“在线节点计数上限短路”都固定成显式 helper，避免后续在多个入口各写一份遍历和排序细节。
- `bridge/client.go` 的节点剔除路径现在也应尽量复用同一套锁内状态迁移：`dropNodeStateLocked(...)` 负责 nodeList/LastUUID/file mappings 的一致性维护，`Close()` 则走 `detachNodesForClose -> closeDetachedNodes`，先在锁内脱离 client 状态，再在锁外关闭节点。后续不要再把“持锁清状态”和“节点 Close()”揉回一段。
- `bridge/p2p_session.go` 当前已经只保留 session 对象、manager/create、state guard、`progress -> transport-established completion` 这条主链；原来混在一起的 bridge control envelope、summary/go、abort/completion 副作用，已经集中到 `bridge/p2p_session_control.go`。这样阅读时先看状态机，再看控制面，不需要在同一个文件里反复切换锁内状态和桥接 I/O。
- `bridge/p2p_session_control.go` 的 bridge control 主链应继续保持成“control message 读取 -> flag 对应 payload 解码 -> 状态应用或 abort”这三段。`flag + short payload` 是 session 协议 envelope，本身适合同文件内具名表达；但不应再为了分层引入新的 runtime/facade。
- `bridge/p2p_session_control.go` 里的 `report/ready -> summary/go` 状态推进也应继续按协议阶段组织：`report/ready` 只负责更新 session state 并判断是否进入下一阶段，`summary/go` 负责准备桥接消息并在锁外执行发送/定时器副作用，不要再把锁内状态推进和桥接 IO 混在一起。
- `bridge/p2p_session_control.go` 里的 session 结束路径也应保持成“锁内决议 outcome 与资源快照 -> 锁外执行 close/unregister/association 标记/telemetry”这条顺序；这样 transport-established 和 aborted 两种结束态的副作用都能在一个阅读路径里审计。
- `bridge/p2p_session.go` 的 `progress` 路径应继续沿用同一套 completion 模型：先记录 telemetry 与 transport-established 判定，再统一走 session completion，而不要在 `progress` 分支里单独维护另一套关闭/发 telemetry 副作用。
- `bridge/p2p_session.go` 里像 telemetry 初始化、closed/provider attach 判断、abort gate 这类 session state guard，也应尽量集中成少量具名语义点，而不是在多个入口里重复展开同样的锁内判断。
- `bridge/p2p_session_control.go` 的 `summary` 阶段应继续保留独立的“写 bridge summary -> 安排 post-summary timer -> 注销 pending p2p state”副作用阶段；`p2pstate.Unregister(...)` 的时机已经值得用 focused test 锁住，避免后续整理时把 pending route 清理延后或提前。
- `bridge/p2p_session.go` + `bridge/p2p_session_control.go` 现在应维持一条单一的 completion 阅读路径：`report/ready/progress` 只生成状态推进或 completion 条件，`transport_established/abort` 最终统一落到 `applySessionCompletion(...)`，避免 success/abort 各维护一套副作用。
- 后续如果继续动 `bridge`，重点应是协议边界和角色边界，而不是再合并文件。

#### C. `server`

当前主入口：

- `server/server.go`
  server root、启动入口、顶层运行时状态、客户端连接只读信息。
- `server/engine_startup.go`
  engine 启动链。
- `server/server_task_runtime.go`
  task mode registry、runtime dependencies、Add/Stop/Ping。
- `server/server_runtime_coordination.go`
  managed tasks、orphan clients、special clients、bridge events。
- `server/server_flow_runtime.go`
  flow 汇总、session、持久化刷新。
- `server/runtime_dashboard_context.go`
  dashboard root、cache、service、builder、runtime/static provider、sampler。
- `server/runtime_dashboard_sources.go`
  system metrics 与 db stats 数据源。
- `server/client_host_list.go`
  client / host list 读模型。
- `server/connection/*`
  listener、reload、runtime config accessor。

当前判断：

- `server` 的 root 与 runtime 主链已经收得比较稳。
- 真正还值得深改的，不是“再合并文件”，而是把运行时上下文、配置边界、全局状态进一步显式化。
- `server/server_task_runtime.go` 里的 `WebServer` 现在也已经补上显式 `webRuntime/config roots`；默认构造仍动态跟随 `connection.CurrentWebRuntime()` 与 `servercfg.Current()`，但来源已经可注入、可审计，不再只能在 `Start()` 里隐式散读全局。
- `server/engine_startup.go` 里已经开始回调那种“只有 nil-check 和转发”的小 helper；当前保留的 launcher/factory 更多是为了表达启动阶段，而不是为了制造额外抽象层。
- `server/engine_startup.go` 当前连 `assignRuntimeBridge`、注册清理/flow loop 启动、`httpHost/web` 默认 task 构造这类只在 wiring 处使用的小 helper 也已经压回闭包本身；现在看 `newServerEngineContext()` 时，可以直接对照 `bridge -> backgrounds -> httpHost -> web` 的默认装配。
- `server/engine_startup.go` 的 `startup.Start(...)` 当前也应保持成两段清楚的阶段：`bridge runtime 准备(create/assign/start)` 与 `ancillary runtimes 启动(probe/background/httpHost/web)`。这两段属于真实启动边界，可以保留具名 helper；但不要再拆成一串只重复阶段名的薄包装。
- `server/engine_startup.go` 的 `p2pProbeLauncher.Start(...)` 现在也已固定成 `bootstrap resolve -> required ports check -> background start -> success log` 这条路径；missing start hook 会直接跳过，避免出现“没有真正启动却记录 success”的隐式行为。
- `server/engine_startup.go` 的 `webRuntimeLauncher.Start(...)` 当前也明确要求 `enable -> task factory -> service factory -> service start -> publish` 全链成立；`newTask()` 返回 nil 时应直接停止，不要再让空 task 继续流入 service 构造。
- `server.go` 里包内使用的 `ensureLocalProxyClient`、`syncRuntimeVKeyClients`、`DelClientConnect` 这类转发壳已经移除，初始化与 lifecycle monitor 现在直接落到 `runtimeSpecialClients` / `runtimeState`。
- `server.go` 当前也已把“runtime 特殊 client 同步”收成单一语义点：`syncRuntimeSpecialClients(cfg)` 负责 local proxy + runtime vkey client 的同步，`InitFromDb()` 额外保留 shared public/visitor vkey 的提示日志。后续不要再把这条边界揉回多个入口里各写一份。
- `server.go` 现在也顺手承接了那组跨列表/flow/dashboard/coordination 复用的 `sync.Map` typed helper（如 `rangeClientMapEntries/loadTaskMapEntry`）。这些 helper 本来就是 package 级共享 runtime sanitation 逻辑，单独保留一个很小的 standalone helper 文件收益不高；回到 `server.go` 后，“runtime state + shared entry helper”可以在一处审计。
- `server/connection/connection_config.go` 现在也已经固定成清楚的阶段边界：`ApplySnapshot = snapshot -> Config -> apply globals -> rebuild shared mux`，其中 normalize 继续拆成 `listener ports` 与 `transport runtime` 两段，global apply 则按 `bridge/http/web/p2p/transport` 分组；原先零散的 runtime accessor 也已回收到这里，读取当前 bridge/http/web/p2p/quic 运行时时不需要再跳去另一组小文件。这一轮又把 `bridge/http/web listener getters + reserved TLS gateway + shared-mux fallback + TCP port 校验` 一并收回了这里，所以配置归一化、全局应用、listener 获取已经能在单个 root 文件里顺着看完。这里保留包级变量只是兼容现有调用方，不代表后续要把配置语义重新散回各 listener。
- `server_flow_runtime.go`、`client_host_list.go`、`runtime_dashboard_sources.go` 里原本只剩转发价值的 `dealClientData` / `RefreshClientData` 也已经退场；客户端/仪表盘刷新统一直接表达为 `runtimeFlow.RefreshClients()`。
- `client_host_list.go` 与 `tunnel_list.go` 现在也已经统一到同一套 stable sort helper：字符串/数字/布尔/无限额度比较不再各写一份 `sort.SliceStable` 分支，`NowRate`、`FlowRemain`、`FlowLimit`、`ExpireAt` 等字段都改成显式 comparator。这样列表读模型的排序语义更集中，也避免后续修一侧漏一侧。
- `server_flow_runtime.go` 里 `currentFlowBridgeRuntime.currentBridge()` 这类只在一处做 `resolve()` 转发的小 helper 也已回调；当前 flow bridge 读写点直接表达“先 resolve bridge，再读/写 client runtime”。
- `server_flow_runtime.go` 里的 `RefreshClients()` 现在也已经按阶段稳定成“解析当前 runtime context -> 刷新 client 在线态/地址/version 并归零流量 -> 聚合 host 流量 -> 聚合 tunnel 流量”。后续若继续改这条链，应优先保持这四段顺序，而不是重新把 runtime 状态刷新和流量聚合揉回一段循环。
- `server_flow_runtime.go` 里的单 client 刷新与 flow session 更新，现在也都已经收敛成“先算当前阶段，再执行副作用”的结构：client runtime 走 `bridge display runtime -> virtual local proxy -> offline`，session 则走 `build update plan -> stop old ticker/session -> immediate flush -> run new session`。这里保留的是阶段边界，不是为了抽象纯度再加 façade。
- `server_runtime_coordination.go` 里 `bridgeRuntimeSpecialClients.currentBridge()` 也已回调；删 bridge client 的主路径直接表达“取当前 bridge -> 删除 client”，不再为了同文件内单点调用保留一层包装。
- `server_runtime_coordination.go` 里的 bridge event 主链也更适合保持成显式阶段：`open host -> remove cache`、`open task -> restart runtime`、`close client -> 删除 runtime 资源后清理 NoStore client`、`secret -> 查 task/校验状态 -> 派发 secret task`。这类 event handler 适合同文件按事件语义分段，不适合再抽成多层 coordinator facade。
- `DealBridgeTask()` 现在也直接把当前 bridge event source 接到 `dealBridgeTaskWithSource(...)`，`secret task` 执行则直接落在 `serverBridgeEventRuntime.HandleSecretTask(...)`；这条后台事件链已经不再保留只用一次的中转 wrapper。
- `server_runtime_coordination.go` 里的 secret 分发当前也已经明确成 `resolveActiveSecretTask -> resolveSecretTaskDispatch(cfg/src/task) -> async runtime dispatch` 三段；这类 helper 对应的是 secret 入口的真实阶段，后续可以保留，但不要再拆成只重复字段名的 getter/setter。
- `server_runtime_coordination.go` 里的 client runtime cleanup 现在也已固定成 `collect plan -> apply deletions/cache refresh` 的同文件结构：按 client id 的整体删除和按 node uuid 的 ownerless runtime 清理都走 plan/apply 路径。后续不要再把 task/host 的收集和执行副作用重新揉回一个大函数。
- `server_task_runtime.go` 的 task lifecycle 也已经整理成更直白的阶段：`被动模式注册(secret/p2p) -> active task 启动前检查(port/flow session) -> mode service 构造 -> runtime launch`；停止路径则保持为“先更新持久化状态，再关闭当前 runtime entry”。后续如果继续改 task runtime，应优先沿这条 lifecycle 阅读路径推进，而不是再回到 `AddTask/StopServer` 巨型分支。
- `server_task_runtime.go` 当前连辅助路径也已经尽量按语义收口：`PingClient()` 是 `build ping link -> open runtime bridge target -> measure/close`，`StartTask()` 是 `load stored task -> validate start precondition -> AddTask -> persist started status/rollback`，异步启动失败则固定走 `resolve failure context -> close failed runtime -> delete only-if-current-entry-still-matches -> persist stopped status`。这些 helper 的目的都是保留对照生命周期时需要的检查点，而不是把实现藏到更深层。
- `server.go` 里 `serverRuntimeState.TaskEntries()/ProxyCache()` 这类测试或单点内部使用的字段 getter 也已回调；当前 runtime state 只保留 `Bridge/AssignBridge/OpenBridgeLink` 这类确实表达边界的方法，不再为测试或单文件内部调用维护多余 API。
- `server.go` 里的 client lifecycle 判定现在也统一沿用 `expirationReached(...)` 与 `trafficLimitReached(...)` 这两个语义点，用户态 client 和独立 client 都复用同一套“到期 / 流量上限”门槛；后续不要再把相同判定在两个分支里手写展开。

#### D. `server/proxy`

当前主入口：

- `server/proxy/base.go`
  `BaseServer`、依赖视图、auth/flow、ACL/auth policy helper。
- `server/proxy/base_transport_runtime.go`
  transport pipeline 与 traffic accounting。
- `server/proxy/base_limit_runtime.go`
  limit runtime、limiter charge/refund。
- `server/proxy/runtime_context.go`
  provider 级默认 wiring。
- `server/proxy/tunnel_server.go`
  `TunnelModeServer` 的 runtime root 与 `ProcessMix` / `ProcessTunnel` 协议主链，包含 runtime task 校验、mixed 协议分派、listener lifecycle、virtual dial/serve。
- `server/proxy/http_proxy_handler.go`
  HTTP proxy 主链、CONNECT / reverse proxy 分支，以及 route context / traffic accounting / idle shutdown 包装。
- `server/proxy/socks5_handler.go`
  socks5 request/connect/associate 主链，以及 address / request / reply 协议 helper。
- `server/proxy/socks5_auth.go`
  socks5 method negotiate、userpass auth、observability/metrics。
- `server/proxy/udp_server.go`
  UDP listener、read loop、source ACL、session enqueue。
- `server/proxy/udp_server_session.go`
  UDP session state、worker lifecycle、cleanup。
- `server/proxy/udp_server_io.go`
  UDP bridge I/O、backchannel、payload assembly。
- `server/proxy/socks5_udp_session.go`
  UDP session 与 edge packet。
- `server/proxy/secret_server.go`
  secret route。
- `server/proxy/p2p.go`
  P2P server / probe。

当前判断：

- `server/proxy` 已经从“过度拆散”收回到可读状态。
- `BaseServer` 已开始从隐式全局 `runtimeProxy` 过渡到可注入 runtime root，说明这一层已经适合继续做实现级依赖收缩。
- `BaseServer` 的主路径现在优先直接表达“读哪个字段 / 调哪个 bridge facet / 用哪个 runtime policy / 更新哪段 flow”，而把 `dependencies()/serviceContext()` 更多留给测试 seam 和装配逻辑，避免主代码阅读时反复跨一层薄包装。
- `BaseServer` 的 `LocalProxyAllowed()` 也已经改成显式 provider，但默认仍然通过动态 closure 读取当前全局配置，所以不会破坏“配置重载后行为跟着变”的既有语义。
- `socks5 UDP` 注册表的 source policy 也已改成显式 provider，不再只能在启动时抓取一份全局 policy 快照；默认和注入两种路径都会继续看到当前 policy。
- `TunnelModeServer`、`UdpModeServer`、`SecretServer` 现在都已经具备显式 `runtime root + local proxy provider` 构造入口；默认构造仍通过动态 closure 读取当前 `runtimeProxy` 和 `servercfg.Current().AllowLocalProxyEnabled()`，因此不会破坏那些本来就应随全局状态变化的行为。
- `server/proxy/httpproxy/httpproxy.go` 现在也已经具备显式 `config/db roots`。默认构造仍然通过动态 closure 读取 `servercfg.Current()` 和 `file.GetDb()`，因此不会破坏“全局配置/全局 DB 变化后行为跟着更新”的旧语义。
- `server/proxy/httpproxy/httpproxy.go` 现在还直接承载了 runtime startup 与默认 roots helper，`HttpProxy.Start()` 的“listener 获取 -> server 初始化 -> runtime launch”顺序可以在一个文件内完成审计，不再需要在 root、roots、runtime_start 三个文件之间跳转。
- `server/proxy/httpproxy` 当前保留 `currentConfig()/currentDB()` 作为默认动态来源边界，但像 `currentHTTPOnlyPass/currentErrorAlways/currentForceAutoSSL/currentResponseHeaderTimeout` 这类仅重复配置字段的一层 helper 已经回调；主代码现在直接对照 `cfg.Auth/Proxy` 字段，更便于审计“具体规则来自哪个配置项”。
- `server/proxy` 和 `server/proxy/httpproxy` 里这些“默认动态来源”现在优先集中定义成具名 helper，便于直接审计“默认值从哪里来”，而不是散在多个构造函数里各写一份匿名 closure。
- `server/proxy/runtime_context.go` 现在集中承载 `currentProxyRuntimeRoot`、`currentProxyUserLookupDB`、`currentProxyGlobalAccessRoot`、`currentProxyLocalProxyAllowed` 这组默认动态来源；这比把这些 helper 分散在多个 root 邻近小文件里更利于维护。
- `server/proxy/runtime_context.go` 里 `currentProxyAccessPolicy/currentProxyUserQuota/currentProxyClientLifecycle/currentProxyRateLimiters` 这组仅重复 runtime 字段名的一行 helper 已去掉；同包主流程现在直接显式读取 `currentProxyRuntimeRoot()` 的对应字段。
- `server/proxy/httpproxy/http.go`、`https.go` 已开始把内部 host/cert/config 查询切到这层 root；后续剩下的工作主要是继续把零散直连点收完，而不是再改构造模型。
- `server/proxy/httpproxy/http.go` 的 `handleProxy(...)` 现在也更适合维持成同文件协议阶段：`host lookup/not-found`、`source ACL`、`ACME/http-only/https redirect`、`path rewrite`、`flow+auth+temporary redirect`、`backend context`、`websocket/reverse proxy`。这些阶段适合保留具名 helper，方便对照 HTTP 代理规则，但不应再拆成跨文件 pipeline 对象。
- `server/proxy/httpproxy/https.go` 的 `checkHTTPAndRedirect(...)` 也已整理成“read request -> host/ACL 校验 -> write redirect response”三段，而且这一轮又把 `Http3Server` 一并收回同文件；secure listener / TLS cert / ALPN / bridge QUIC 分流现在可以在单个 secure runtime 文件里顺着看完。这条路径适合同文件内直接表达，不需要再额外引入 facade 层。HTTPS backend 转发这轮又把 `handleHttpsProxy(...) / handleTlsProxy(...)` 共享的 `runtime route -> backend 校验 -> lease -> target -> NewTunnelByHost(...)` 收成了同文件的 `resolveHTTPSProxyBackend(...)`，证书装配也把 `resolveManualCertSource(...) -> host/default cert fallback -> tls.Config` 收成了 `loadHostedCertificate(...) / tlsConfigForCertificate(...)`。继续往前，`HttpsServer` / `Http3Server` 的 unavailable 和 started 状态也已经统一成 sentinel，并把 source remote addr 读取收到了本地 helper，测试不再靠裸字符串比较，secure 入口的 ACL 身份读取也不再散在多个分支里。后续不要再把这两条链复制回不同入口里。
- `server/proxy/base_limit_runtime.go`、`base_transport_runtime.go` 里的默认 provider fallback 现在直接写在额度检查/建链主流程里，不再拆成一组只重复字段名和空值分支的小 getter；而且 `proxyLimitRuntime` 这轮又把“缺省 provider 解析”收成一次完成的 `withDefaults()`，`CheckFlowAndConnNum/ServiceRateLimiter/BridgeRateLimiter` 不再在单次调用里反复取 `currentProxyRuntimeRoot()`。这轮 `BaseServer` 侧又补齐了按需构造的 `limitRuntime()/newProxyTransportRuntime(...)`，不再为了拿 `limit/transport` 一个 facet 先拼整份 `serviceContext`；这样审计“限额从哪来、local proxy 怎么判、bridge/service limiter 何时启用”时可以一眼看完，热路径也少一层无关对象构造。继续往前，`OpenClientLink(...)` 里 client source remote addr 现在也显式收成了单次 `proxyConnRemoteAddr(...)` 解析，再同时喂给 source ACL 和 `conn.NewLink(...)`，不再在同一条建链路径上重复向 `*conn.Conn` 取两次地址。
- `server/proxy/base.go` 里 `BaseServer.dependencies()/serviceContext()` 这层仅服务于测试的薄包装已去掉；生产代码直接用 `newProxyBaseDependencies(...)` / `newProxyServiceContext(...)`，测试也直接对这两个构造函数断言，不再让测试 seam 反向塑造主代码结构。
- `server/proxy/base.go` 里 `proxyBaseDependencies` 那批仅重复字段名的 getter 也已继续回调；当前同包代码和测试直接读取依赖视图字段或断言真实行为，保留 `OpenBridgeLink/ProcessBridgeClient/BridgeIsServer/LocalProxyAllowed` 这类有语义的方法，不再维护无意义的 `Task()/Locker()/LinkOpener()` 包装。
- `server/proxy/base.go` 里 `proxyBaseDependencies` 当前也已从“closure 驱动的动态视图”回调成“构造时快照”。生产代码本来就是每次现取现用，并不会复用旧依赖对象，因此不再为了测试保留 `currentTask/currentErrorBytes` 这类间接层；测试也改成验证“重新构造依赖才会反映 BaseServer 新状态”，与真实生产语义保持一致。
- `server/proxy/base.go` 里的 auth 失败面现在也不再返回裸字符串 `"401 Unauthorized"`；`proxyAuthRuntime.Check(...)` 已统一复用 `errProxyUnauthorized` 和 `writeProxyUnauthorized(...)`。后续如果有其他入口要复用同一条 unauthorized 写回/错误分类，不要再各自拼字节和字符串。
- `server/proxy/base_transport_runtime.go`、`udp_server_io.go` 现在直接从 `LinkOpener` 推导 route stats collector，而不是为了取 `routeStats` 先构造整份 transport runtime；这样 route 观测链更容易审计。
- `server/proxy/base_transport_runtime.go` 这轮也把 transport 缺省 policy 解析收成了 `withDefaults()`，`OpenClientLink/accessDenied` 不再在同一条建链路径上零散取默认 runtime policy；`RouteRuntimeContext` 侧则补了 `currentRouteBinding()`，`TrackConn/BridgeObserver/ServiceObserver/Observe*Traffic` 不再各自重复 `RouteUUID() -> boundNodeRuntime(...)` 这套样板，route binding 的读取点更集中、更好审计。
- `server/proxy/base_transport_runtime.go` 现在也把同一套 `clientID / routeUUID / boundNode` 解析显式收成 `proxyRouteRuntimeBinding`：`PipeClientConn(...)`、`BaseServer.ObserveServiceTraffic(...)`、`BaseServer.BridgeTrafficObserver(...)`、`BaseServer.ServiceTrafficObserver(...)` 都共用 `newProxyRouteRuntimeBinding(...)` 与 `serviceRuntimeRouteUUID()/linkRuntimeRouteUUID()`，后续不要再把 route trim、bound-node lookup、service routeUUID 选择散回四五处各写一份。
- `server/proxy/base_transport_runtime.go` 里 `BaseServer` 侧现在也统一通过 `routeStatsCollector()` 从 `linkOpener` 取 route collector，再把 `NewRouteRuntimeContext(...)`、`transportRuntime()`、`ObserveServiceTraffic(...)`、`BridgeTrafficObserver(...)`、`ServiceTrafficObserver(...)` 接到同一条来源上；后续不要再在这些包装层里各自手写一遍 `linkOpener -> collector`。
- `server/proxy/base.go` 上 `accessPolicy()` 这一层也已去掉；`BaseServer` 与 `TunnelModeServer` 直接读取当前 runtime policy，不再为了拿一份 `proxyAccessPolicyRuntime` 再跨一层同名 helper。
- `server/proxy` 包内主路径现在也不再反复通过 `BaseServer.LinkOpener()/ServerRole()` 取同一个 facet；这些公开方法仍保留给边界使用，但同包内的 tunnel / udp / route-runtime 逻辑已直接读取底层依赖字段，减少无意义跳转。
- `server/proxy/runtime_context.go` 里像 `ResolveUser()` 这类只在同文件里调用、且仅重复 `users.Resolve(...)` 的包装也已去掉；当前 ACL 主链直接表达“从 users 解析 owner，再做 source/destination 判断”。
- `server/proxy/socks5_udp_registry.go` 里 `currentPolicy()` 这层同文件单点包装也已去掉；当前 source policy 读取直接表达为 `policyRoot()`，既保留默认动态行为，也少一层无意义跳转。registry 侧这轮又补齐了和 `BaseServer` 一致的 typed-nil `linkOpener/serverRole` 防护，并把 `sessionCount/isClosed/collectIdleSessions` 收成 `RWMutex + session snapshot`；`pickSession(...)` 也先走 `sessionsByAddr` 的读锁快路径，只有 miss 才进写锁处理 pending bind，避免已绑定 UDP 包继续抢 registry 写锁。再往前，ambiguous pending/bound drop 的日志路径也不再把 `addr.String()` 反拆回 port 并走 `LookupPort`，而是直接沿 source `UDPAddr.Port` 透传，避免这条冲突热路径再做一次字符串往返和端口解析。
- `server/proxy/tunnel_server.go` 当前已经重新收成一个完整的 `TunnelModeServer` 根文件：`runtime task 校验 -> listen addr 解析 -> socks5 UDP sidecar 启动 -> TCP listener 建立 -> accept serve -> active conn / listener cleanup`，以及 `ProcessMix(...)` / `ProcessTunnel(...)` 这两条协议入口。mixed 协议识别(`http/socks5`)与 route target 解析也都在同一阅读路径里，文件规模仍然可控，因此这里不再保留独立的 `tunnel_server_protocol.go`。同时 `httpProxyBackendPool()` 这轮也收成了 `RWMutex + 读快路径 + 写侧初始化`，`Close()` 则改成“锁内摘掉 pool，锁外关闭 idle transports”，避免 tunnel HTTP proxy 请求在取 pool 时继续抢独占锁。继续往前，mixed 入口里原先裸字符串的 `unknown protocol / http disabled / socks5 disabled / invalid socks5 method list` 也已经收成具名 sentinel，`ProcessMix(...) / handleMixedHTTPProxy(...) / handleMixedSocks5(...)` 的失败面现在更容易审计，也方便 focused tests 直接用 `errors.Is(...)` 断言，不再只能匹配文案字符串。
- `server/proxy/http_proxy_handler.go` 当前也更适合维持成 `读 ingress(host/raw buffer/request) -> auth + dest ACL -> CONNECT 分支 / 普通 HTTP proxy 分支` 这条入口结构。普通 HTTP 分支再明确为 `backend selection -> route runtime context -> accounting -> idle shutdown -> reverse proxy`，这样能直接对照代理行为和计量点。这一轮又把原先独立的 accounting/shutdown wrapper 收回到了同文件里，因此 tunnel HTTP proxy 的主链和它依赖的 route context / traffic accounting / idle shutdown 包装现在可以在一个文件内顺着审计；其中 `prepareTunnelHTTPProxyContext(...)` 已回调成单次 route trace 安装，shutdown 则明确落到 `tunnelHTTPProxyShutdown.reset/stop`，不再在主链里来回传 `mutex + **time.Timer`。继续往前，CONNECT 和普通 HTTP 请求现在又共享了 `resolveTunnelHTTPProxyTarget(...)` 这条 resolved request 边界，`selection + routeRuntime` 不再在两个入口里各自拼装；raw 连接侧的 400/403/502/200 写回也收到了静态响应字符串和 `closeTunnelHTTPProxyConn(...) / writeTunnelHTTPProxyConnResponse(...)`，并通过 `tunnelHTTPProxyConnResponseForError(...)` 统一分类，不再把 socket 级响应散回各分支里临时写字节切片。再进一步，handler 自己也已切到 `resolveTunnelHTTPProxyServeRequest(...)` + `tunnelHTTPProxyResolvedRequest` 这条统一状态，context 回填、route trace 安装、service accounting 观察点都以这份 resolved request 为准，不再在 `serveTunnelHTTPProxy(...)` 主链里手工展开 `routeRuntime/remoteAddr/selection`；读 ingress 时的 source remote addr 也已经统一复用 `proxyConnRemoteAddr(...)`，避免 tunnel HTTP、transport、secret 三条 setup 路径再各自手写一份底层连接地址读取。与此同时，这轮又把授权检查收成了 `resolveAuthorizedTunnelHTTPProxyTarget(...) / resolveAuthorizedTunnelHTTPProxyServeRequest(...)`，并让 handler 前置校验与 backend dial 都统一复用 `errTunnelHTTPProxyDestinationDenied`，不再一边返回 403 语义、一边在 backend 路径里掉回裸字符串错误；同时，`missing target -> 400`、`dest ACL -> 403`、其他 backend/route 失败 -> `502` 这条分类也已经在 CONNECT raw response、普通 HTTP resolve 阶段、以及 reverse proxy `ErrorHandler` 三条路径上统一下来。与此同时 tunnel HTTP proxy 的 accounting wrapper 现在也补了 no-op fast path：当 limiter 和 observer 都为空时，`wrapReadCloserWithAccounting(...) / wrapResponseWriterWithAccounting(...)` 直接返回原对象，不再为了“可能的计量”额外包一层无效 wrapper。再往前，这轮也把普通 HTTP 分支的限速语义收正成“socket 层 limiter 一次、body/writer wrapper 只做 service traffic 观测”：`serveTunnelHTTPProxy(...)` 仍保留 client conn 级限速，但 `applyTunnelHTTPProxyAccounting(...)` 在这条路径上不再重复传同一个 limiter，避免请求体和响应体在 wire 层已限速后再次扣桶。
- `server/proxy/http_proxy_backend.go` 当前应保持成 `backend task 选择 -> routeUUID 选择 -> transport pool -> backend dial -> route runtime wrap` 这条单一阅读路径。`dialSelectedHTTPBackend(...)` 已经不再自己串接一整条 `selection / remote addr / routeRuntime` 解析链，而是先统一走 `resolveTunnelHTTPBackendState(...)`，再下沉到 `dialResolvedTunnelHTTPBackend(...)` 做 ACL、link build、bridge open、wrapped flow conn；这样 tunnel HTTP backend 的 direct dial、transport callback、context payload 现在共享一套 normalized backend state，不再把 `resolveTunnelHTTPBackendSelection(...) / resolveTunnelHTTPBackendRemoteAddr(...) / resolveTunnelHTTPRouteRuntime(...)` 三段散在调用方各自拼装。同文件的 `tunnelHTTPBackendTransportPool` 这轮也改成了“锁内检查/清理，锁外创建 transport，回锁复核并收敛到共享缓存”的模式，避免 `GetOrCreate(...)` 在 miss 热路径上长时间持锁跑 build。再往前一步，entry 的 `lastUsed` 也已经改成原子时间戳，cache hit 现在只走读锁和原子更新时间，不再为了刷新命中时间回写抢写锁。现在 `http_proxy_handler.go` 与 `http_proxy_backend.go` 之间也已经通过同一个 `tunnelHTTPProxyResolvedRequest` 对齐，后续不要再让 handler 侧和 dial 侧各自维护不同版本的 `selection/routeRuntime/remoteAddr` 状态拼装。
- `server/proxy/httpproxy/http_backend.go` 这一轮也已经把 `backendTransportPool` 收回同文件；`selection key/cache/prune/capacity` 与 backend dial / TLS dial / context fallback 现在可以在一条阅读路径里完成审计，不再需要在 `http_backend.go` 和邻接 pool 文件之间来回跳。同样地，`backendTransportPool.GetOrCreate(...)` 现在也采用了和 tunnel HTTP proxy 对称的“锁外创建 + 回锁复核”策略，并把 `Size()` 收成读锁路径；entry 命中时间也切到了原子时间戳，避免高频 backend cache hit 为了更新 `lastUsed` 再拿写锁。继续往前，这条路径现在又把 `host + backend selection + route runtime + request data` 统一收成 `resolveHTTPProxyBackendState(...) / dialResolvedBackendContext(...)` 这组 shared state helper，生产代码不再额外保留 `prepareHTTPProxyBackendContext(...)`、`websocketBackendContext(...)` 这类只服务测试的薄包装；后续不要再回退到持锁 build transport、持锁更新时间戳，或把 backend state 解析散回四五个入口重复实现。
- `server/proxy/httpproxy/http.go` 的 HTTP 主链也应继续保持“host lookup/source ACL/redirect/path rewrite -> quota/auth/redirect -> backend context -> accounting -> reverse proxy”这条阶段顺序。service 流量计量现在已收成 `applyHTTPProxyServiceAccounting(...)` / `httpProxyServiceObservers(...)`，后续不要再把 `req.Body` 与 `ResponseWriter` 的 limiter/observer 细节散回 `handleProxy(...)` 主链里。普通 HTTP 与 websocket 现在都对齐到单次 `resolveHTTPProxyServeRequest(...)`，而且 `dest ACL` 拒绝语义已经统一成 `403`，不再出现普通 HTTP 返回 `502`、websocket 返回 `403` 的分叉。websocket buffered replay 这一轮又补齐了 route tag 保持：预读字节通过 `wrapBufferedHTTPProxyConn(...)` 回灌时，原连接上的 runtime route UUID 不会被 `conn.NewConn(...).SetRb(...)` 吃掉。与此同时，`http.go` 本身已经收掉只用一次的 `http-only/redirect/context-required` 薄 helper，并压回到 600 行内；`HttpServer.Start()` 的 unavailable / already-started 也已经切到 sentinel，避免入口测试继续靠文案比较。继续整理时优先保持协议阶段清楚，不要再为了抽象纯度引入只做一层转发的私有包装。`httpproxy/limiter_io.go` 这轮也和 tunnel HTTP proxy 对齐了 no-op fast path：没有 limiter/observer 时直接返回原始 body/writer，避免普通请求在“无计量、无限速”的路径上仍额外套一层空包装。
- `server/proxy/socks5_handler.go` 现在也更适合维持成清楚的协议阶段：`request 读取 -> command 分发`、`connect resolve -> target 打开 -> remote result 等待 -> reply`、`associate 准备 -> session 建立 -> control wait`。这一轮又把 `socks5Address/request/read/write reply` 这组协议 helper 一并收回同文件，所以 SOCKS5 主链和它依赖的 wire-format 解析可以在一个阅读路径里完成审计。这类 helper 应保留 SOCKS5 术语，方便对照 RFC 和现有 focused tests。继续往前，`handleSocks5Connect(...)` 现在也统一走 `resolveSocks5ConnectRequest(...) / handleSocks5ConnectResolveError(...) / handleSocks5ConnectOpenError(...) / completeSocks5ConnectHandshake(...)` 这条边界，ACL deny、runtime task 校验、open 失败、remote result 失败不再散在一个函数里；同时 open 阶段若已拿到 `target` 但 `err/link` 异常，也会统一主动关闭，避免半打开 target 泄漏。
- `server/proxy/socks5_auth.go` 当前单独承接 `method negotiate -> userpass/noauth 分支`，以及 connect/auth/udp 的 observability 计数与日志。这样 `socks5_handler.go` 看的是协议主链，`socks5_auth.go` 看的是认证与观测，不再在同一文件里来回切换。继续往前，这条路径的 `auth failed / no acceptable authentication method` 也已经收成具名 sentinel，method selection 与 auth status 写回则落到 nil-safe helper；后续如果别的入口需要复用 SOCKS5 协商失败面，应直接沿这组 sentinel 和 helper 扩展，不要再回退到裸字符串和散落的 `c.Write([]byte{...})`。
- `server/proxy/socks5_udp_session.go` 当前应保持成 `open tunnel -> framed bridge I/O -> edge packet 编解码 -> bound client writeback` 这条协议边界。这一轮又把 bound client 地址读路径收紧成“bind 时 clone 一次、read loop 复用 session 内私有副本”，不再在每个下行 UDP 包上额外 clone 一次 `UDPAddr`；同时 `control.RemoteAddr().String()` 也固定到了 session state，setup、link 构造和日志不再重复向底层 conn 取值。继续往前，associate setup 现在也把 `controlAddr + clientIP` 身份解析固定成单次完成，再通过 `handleSocks5Associate(...) -> socks5UDPRegistry.newSessionWithClientIdentity(...) -> newSocks5UDPSessionWithClientIdentity(...)` 往下传，不再在 handler、registry、session 构造里各自重复解析；这条链的 remote addr fallback 现在也已经统一到单点 helper，不再让 `resolve identity / session log / link open` 各自直接碰底层 `RemoteAddr()`. 后续如果继续优化，应沿着 bridge I/O / edge packet 语义走，不要再把 registry/source policy 判定揉回 session 热路径。
- `server/proxy/socks5_associate_control.go` 现在也把“session 已结束”的情况显式短路到 read loop 之前和每次 poll 之前；这样 associate control 不会在已完成 session 上再额外起 wake goroutine 或多做一轮 deadline/read 周期，同时保留现有“wrapped timeout 视为 poll、session done 后及时退出”的语义。
- `server/proxy/secret_server.go` 的 `HandleSecret(...)` 现在也已整理成同文件的五段：`限额检查 -> 读 secret ingress(link/buffer/ack) -> route resolve + backend open -> 可选 ACK -> service/bridge copy`。这条路径适合直接对照 secret 协议和 route/ACL 规则，不应再塞回一个大函数。继续往前，`readSecretIngress(...)` 现在也把 source remote addr 固定到 `secretIngress.remoteAddr`，`openSecretBackend(...)` 直接复用这份 setup 身份，不再在 backend 打开时再向底层连接取地址。
- `server/proxy/udp_server.go`、`udp_server_session.go`、`udp_server_io.go` 当前已经固定成三段清楚边界：`udp_server.go` 负责 `listener 打开 -> UDP packet read loop -> source ACL + session lookup/enqueue`，`udp_server_session.go` 负责 `session state -> worker lifecycle -> cleanup/drain`，`udp_server_io.go` 负责 `worker 建链 -> backchannel -> client packet forward -> PROXY header once-only 注入`。这三块对应的是真实变化面，后续不要再回退成单大文件，也不要再额外包一层 facade。继续往前，`UdpModeServer.Start()` 的 runtime 缺失错误也已经收成 `errUDPServerUnavailable`，测试不再靠裸字符串比较 unavailable 语义。
- `server/proxy/p2p.go` 的 probe server 当前也更适合维持成 `listener bootstrap -> packet read -> worker acquire -> async dispatch -> probe decode/accept -> observation -> primary ack -> optional extra reply` 这条协议主链。像 `DecodeUDPPacketWithLookup`、`AcceptPacket`、`RecordObservation`、`WriteToUDP` 之间的顺序已经明确，不要再回退成一个把读包、worker 调度和 probe 业务揉在一起的大循环。继续往前，这条入口的 `server unavailable` 也已经收成 `errP2PServerUnavailable`，`Start()/StartBackground()` 与相关测试不再依赖裸字符串比较；可分类错误和 runtime 可用性判断现在都落在同一个语义点上。
- `server/proxy/tunnel_server.go` 的 mode server root 现在除了 mixed/http/socks5 主链外，连最外层的 runtime 缺失也已经统一成 `errTunnelServerUnavailable`；`Start()` 和 focused tests 不再靠文案匹配，后续如果入口再扩展 background/managed start，也应沿这个 sentinel 继续扩展。
- `client/client_p2p_state_root.go` 现在保留 `state root` 作为外层 API，但去掉了 root 与 state store 之间重复的 `Ensure/Lookup` 中转层；读取 association / peer policy 时已经不需要在 root、store 两层同名方法之间来回跳。
- `client/client_p2p_state_root.go` 里 `clientP2PStateStore.WithLock(...)` 这类仅在同文件内部把字段函数再包一层的 helper 也已回调；当前 association bind / punch start / policy 读取主链直接使用 `state.withLock`，减少同文件内部的跳转层。
- `server/engine_startup.go` 和 `server/runtime_dashboard_context.go` 现在也开始回调 root 上仅供入口调用的一层包装：`StartNewServer/StartServerEngine` 已直接落到 `startup.Start(...)`，dashboard 全局入口也直接落到 `service.Get(...)` 与 sampler 启动，不再先经由 context 上一层同名方法。
- `client/client_p2p_provider_root_transport.go` 顶部那组 `CloseTransportRuntime/normalizeParentContext` 纯中转 helper 已去掉，而且原先零散的 transport adapter / QUIC stream / KCP mux serve runtime 也已收回同文件；provider transport 的“建 runtime -> setup -> accept(promote) -> serve -> close”主链现在可以在一个阅读路径里看完，不再需要在 `root_transport` 和 `transport_flow_runtime` 两个邻接文件之间来回跳。`preConn` 信号则改成具名 helper，避免测试再依赖一个只为零值 runtime 存在的方法。
- `client/client_channel_runtime.go` 现在也已经把 `handleFileChannel(...)` 旁边那块 `FileServerManager/WebDAV/basic-auth/readOnly` 一并收回同文件；这样 `channel link -> special channel(file/udp5) -> file virtual listener / WebDAV server` 这条 file-channel 子域能在一个阅读路径里看完，不再需要在 `client_channel_runtime.go` 和独立 file server 文件之间来回切换。
- `client/client.go` 顶层启动链也继续压平了一轮：`startChannelRuntime/startMonitorRuntime/startHealthRuntime/markClientReady/joinP2PSession/storeOrKeepClientUUID` 这批仅单点中转的方法已回写到启动、事件分发和 control conn 主流程里，保留阶段语义，但减少了同文件内的来回跳转。
- `client/client_p2p_provider_root_channel.go` 里的 `launchChannelAsync/wrapChannelConn` 也已回写到 `dispatchChannel(...)`；测试直接断言 `wrapP2PAssociationConn(...)` 这个真实语义点，不再绑着 root 包装方法。
- `server/runtime_dashboard_context.go` 里的 `memoryDashboardCacheStore.Replace(...)` 已移出生产代码；测试现在通过 `runtime_dashboard_context_test.go` 里的本地 helper 改写 cache snapshot，避免 test-only 能力继续膨胀生产 API。
- 但它仍然是后续最值得做实现级重构的一块，因为 mode server、ACL、流控、transport、quota 仍有大量跨域耦合。

#### E. `web`

当前判断：

- `web/service`、`web/api`、`web/routers` 在这一阶段已经整理过。
- 当前主任务不再是继续整理 `web` 结构。
- 后续只有在做真实功能重构或接口调整时，再回到 `web`。

#### F. `runtime_dashboard`

当前判断：

- `runtime_dashboard` 的 `sources/system/stats` 仍然是有意义的数据源边界，不建议继续为了减文件数硬并。
- 但 `dashboardService` 这种位于 root 内部、只负责 cache/build 刷新的主路径，应优先保持“一眼能读完刷新顺序”的形态；目前这条路径已经回调了过多的薄 helper，阅读时不需要再在 `Get -> loadCache/currentTime/storeCache/buildAndStore` 之间跳转。
- `server/runtime_dashboard_context.go` 的 `dashboardService.Get(...)` 现在应保持成“cache snapshot -> refresh mode 决策 -> full build / light refresh / cached clone 返回”这条固定阅读路径。像 `selectDashboardRefreshMode(...)` 这种只表达刷新语义分叉的 helper 可以保留，但不要再拆成 `loadCache/currentTime/storeCache/buildAndStore` 这类只重复阶段名的一层 getter/setter。
- `dashboardIOSampler` 里 `currentTime()` 这种仅在一个初始化点做 `now()` 转发的 helper 也已移除；采样初始化直接表达“优先用注入时钟，否则用 `time.Now()`”。
- `runtime_dashboard` 当前又进一步稳定成了更直接的阶段结构：`dashboardService.Get(...)` 现在是 `build refresh plan(now/cache/mode) -> resolve full/light/cached data -> on refresh modes store cache -> clone response`；`dashboardBuilder.Build(...)` 则直接表达 `static snapshot -> traffic snapshot -> task-mode snapshot -> runtime/system/chart -> socks metrics`。这类 helper 对应 payload 的真实组成部分，可以保留；不要再回退到多值元组在 builder 里散写字段。
- `server/runtime_dashboard_sources.go` 里的 stats 也已经收成 `dashboardTrafficSnapshot` / `dashboardTaskModeSnapshot` 两类具名快照，`runtime_dashboard_context.go` 只消费快照并应用到 payload；这样后续继续改 dashboard 时，统计采集和 payload 写入不会重新耦合回同一个函数里。`dashboardIOSampler` 现在也固定成 `read current totals -> lock -> compute elapsed/rates -> store baseline` 这条路径，baseline 更新只保留一处。

### 14.3 当前真正值得处理的地方

下面这些是“下一阶段应开始真正改实现”的地方，不再是继续做纯文件整理。

#### A. `server` 运行时上下文与配置边界

重点文件：

- `server/server.go`
- `server/engine_startup.go`
- `server/server_task_runtime.go`
- `server/server_runtime_coordination.go`
- `server/connection/*`
- `lib/servercfg/*`

仍然存在的问题：

- `server` 虽然已有 root 和 coordination 分层，但全局运行时状态仍较强。
- `server/connection` 已经有 accessor，但配置消费仍不是完全按显式依赖注入。
- reload、listener、runtime config 的边界虽然比之前清楚，但还没有统一成“启动层分发配置，运行时对象按窄配置消费”的最终形态。

下一步建议：

- 把“配置源头 / 配置分发 / 运行对象消费”彻底分层。
- 继续减少包级全局值的直接读写。
- 先从 `server` 启动链和 `server/connection` 开始，不要一开始就去碰更深的网络实现。

#### B. `server/proxy` 模式服务与 ACL / 流控 / transport

重点文件：

- `server/proxy/base.go`
- `server/proxy/runtime_context.go`
- `server/proxy/tunnel_server.go`
- `server/proxy/http_proxy_handler.go`
- `server/proxy/socks5_handler.go`
- `server/proxy/udp_server.go`
- `server/proxy/udp_server_session.go`
- `server/proxy/udp_server_io.go`
- `server/proxy/secret_server.go`
- `server/proxy/p2p.go`

仍然存在的问题：

- `BaseServer` 仍然是大量 cross-cutting concern 的中心。
- mode server 之间虽然结构更清楚，但还没有统一成真正可替换的 service contract。
- ACL / auth / flow / quota / transport 依赖已被收口，但很多地方仍是默认 wiring 驱动，不是面向最小接口驱动。

下一步建议：

- 先定义 mode server 的最小契约面。
- 明确 `BaseServer` 只保留真正通用的能力，把模式差异进一步外移。
- 按“认证 / ACL / 流控 / transport”四条横切链逐条拆实现依赖。

#### C. `bridge` / `client` 的 P2P 协议核心

重点文件：

- `bridge/handshake.go`
- `bridge/handshake_runtime.go`
- `bridge/p2p_resolve.go`
- `bridge/p2p_session.go`
- `client/p2p_manager.go`
- `client/p2p_manager_session.go`
- `client/client_p2p_provider_root_channel.go`
- `client/client_p2p_provider_root_transport.go`
- `client/client_p2p_state_root.go`

仍然存在的问题：

- 结构虽然已经可读，但 provider / visitor / secret / tunnel 角色之间的协议边界仍然偏隐式。
- `bridge` 和 `client` 两侧的 P2P 状态推进逻辑仍然较重，很多阶段依然依赖默认 wiring，而不是更强约束的显式契约。

下一步建议：

- 不再继续拆并文件。
- 开始把 P2P 协议里真正的角色契约提出来：
  - provider transport contract
  - provider channel contract
  - association / policy contract
  - bridge-side resolve / session contract
- 先做契约和状态边界，再做实现替换。

#### D. 全局状态与持久化根

重点文件：

- `lib/file/*`
- `lib/servercfg/*`
- `server/server.go`
- `server/connection/*`

仍然存在的问题：

- `file.GetDb()` 链路虽然已明显收口，但仍是高耦合点。
- `file.GlobalStore` 仍然容易让业务层直接越过边界。
- `servercfg.Current()` 和运行时对象之间还不是完全端口化关系。

下一步建议：

- 只有在开始做深层实现重构时再碰这块。
- 不要单独把这块当“整理文件”的目标。
- 正确做法是配合具体主链改造，一起把全局读取往装配层收。

### 14.4 当前明确不再继续做的纯结构整理

以下区域本轮之后不再作为“继续合并文件”的主目标：

- `client/launch*`
  仍有真实边界，继续硬并会伤可读性。
- `server/runtime_dashboard` 的数据源层
  `sources/system/stats` 这些层仍有真实边界。
- `server/proxy/udp_server.go` / `udp_server_session.go` / `udp_server_io.go`
  这组边界现在分别对应 listener/read loop、session/worker lifecycle、bridge I/O/backchannel，继续硬并会把 UDP 主链重新搅回一处。
- `server/proxy/runtime_context.go`
  仍然是 provider 级 wiring 根，不适合继续为了减文件数硬并。
- `bridge/bridge.go`
  已经接近可读性上限，不应再把 bridge 配置流或 DB 扫描 helper 往里回塞。
- `web/*`
  当前阶段不再继续做纯结构整理。

### 14.5 剩余尾部项（可选，不阻塞下一阶段）

还有少量收益较低的尾部项，但都不阻塞下一阶段：

- `bridge`、`client`、`server`、`server/proxy` 里极少数仍然很小的测试入口文件
  最近几轮已经把 `client` / `server` / `server/connection` / `server/proxy` / `server/proxy/httpproxy` / `server/tool` 中大部分过小测试入口回并到对应主测试文件，剩余量已经很低。
- 剩余的小文件大多已经属于应保留的真实边界
  例如 build tag 平台分支、UDP 三段、独立 IO/accounting 测试，以及 `bridge/runtime_owner_pool.go` 这类贴近大根文件、但实际承载本地 owner 轮转与锁语义的 helper。
- 个别 root 邻近 helper 的命名与注释清理
- 少量 focused test 的统一命名

处理原则：

- 只有在顺手改到相关文件时再做。
- 不要再把它们当成主任务。

## 15. 下一阶段实施建议

接下来建议正式从“结构整理阶段”切换到“实现级重构阶段”。

### 15.1 推荐执行顺序

推荐顺序：

1. `server` 运行时上下文与配置边界
2. `server/proxy` 模式服务与 ACL / 流控 / transport
3. `bridge` / `client` 的 P2P 协议契约化
4. 配合具体主链改造，逐步减少 `lib/file` / `servercfg` 全局读取

原因：

- 先做 `server`，风险和收益最平衡。
- 再做 `server/proxy`，可以最大化“单一职责、清晰接口、可组合、可替换”的收益。
- `bridge` / `client` P2P 虽然价值高，但协议复杂度更高，适合放在前两条稳定之后再做深改。

### 15.2 后续文档维护规则

后续更新文档时，不要再恢复成按轮次追加的施工日志。

建议规则：

- 只维护“当前状态、当前问题、下一步建议”。
- 如果一条问题已经被理解并吸收，就直接改写当前结论，不保留完整历史流水账。
- 每次真正改实现时，只更新相关模块的“当前状态”与“待处理问题”。

### 15.3 测试与验证策略

这个仓库当前的 `go test` 噪音仍然较大，后续建议继续使用以下策略：

- 优先用 focused test：
  `go test ./pkg -run TestName`
- 必要时先做编译检查：
  `go test ./pkg -run TestDoesNotExist`
- 当 `PowerShell` 行为不稳定时，用
  `cmd /c "go test ... 2>&1"`
- 不把“无输出超时”直接当作结构问题，先判断是否是当前工作区噪音

### 15.4 下一步建议

下一步不要再继续整理文件。

建议直接从下面两条中选一条开始做真正的实现重构：

1. `server` 运行时上下文与配置边界
   目标是继续减少包级全局与隐式配置读取。
2. `server/proxy` 模式服务契约化
   目标是把 mode server、ACL、流控、transport 的跨域耦合继续降下来。



