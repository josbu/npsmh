# 参考资料

这一部分适合“已经知道自己要查什么，只需要准确答案”的场景。

这里主要存放四类内容：

1. 精确配置字段
2. 功能行为与限制说明
3. 兼容、补充和排查信息
4. 需要长期稳定维护的事实型文档

如果你还没有完成首次部署和首次连接验证，建议先回到 [开始使用](/getting-started/README)。

如果你需要 API、SDK、启动协议或平台接入，请直接进入 [接口与集成](/reference/integration/README)。

## 配置与运行参考

- [NPC 命令行参数](/reference/npc-cli)：`npc` 常用参数、默认值和命令组合
- [服务端配置文件](/reference/server-config)：`nps.conf` 主题入口和阅读顺序
- [基础项与密钥](/reference/server-config-basics)：基础配置、密钥和路径规则
- [Web、HTTP 与安全](/reference/server-config-web)：Web 管理端、登录保护、真实 IP 与前置代理
- [入口端口与桥接](/reference/server-config-ports)：`bridge_*`、HTTP / HTTPS 入口和 P2P 入口
- [节点与平台对接](/reference/server-config-node)：`run_mode=node`、多平台、reverse 和 callback
- [访问控制与运行](/reference/server-config-runtime)：ACL、日志、限制开关与调试

## 功能能力参考

- [功能清单与扩展能力](/reference/features)：能力总入口
- [传输与连接](/reference/features-transport)：压缩、加密、KCP、多路复用和断线判定
- [站点与 HTTP](/reference/features-http)：站点能力总入口
- [证书、TLS 与站点保护](/reference/features-http-tls)：自动证书、自动 HTTPS、TLS 直通和 TLS 终止
- [Header、重定向与 CORS](/reference/features-http-headers)：Header 修改、重定向和 CORS
- [URL 路由、重写与 404](/reference/features-http-routing)：泛域名、路径分流、路径改写和错误页
- [代理、转发与路由](/reference/features-routing)：嵌套转发、端口映射和端口复用
- [访问控制与限制](/reference/features-access)：ACL、流量、带宽、连接数和 IP 限制
- [运维与调试](/reference/features-ops)：环境变量、健康检查、日志和 pprof

## 排查与补充

- [FAQ](/reference/faq)
- [补充说明](/reference/notes)

## 分类边界

- 需要步骤化操作说明：看 [操作指南](/guide/README)
- 需要选择规则类型：看 [选型与规则](/guide/design/README)
- 需要接口、SDK 或启动协议：看 [接口与集成](/reference/integration/README)
