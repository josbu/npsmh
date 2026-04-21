# 隧道与转发类型

这一部分按具体规则类型拆分说明。每一页只关注一种模式的用途、最小配置、常见字段和注意事项。

如果 NPS 和 NPC 还没有先完成首次连接验证，建议先回到 [10 分钟快速开始](/getting-started/quick-start)。

## 这一部分适合什么场景

- 你已经知道要创建哪一种规则
- 你需要查看某一类规则的最小配置
- 你需要比较两个相近规则的差异

如果你还没有确定应该选择哪种规则，先看 [选型与规则](/guide/design/README)。

## 选择入口

| 类型 | 适合什么服务 | 说明页 |
| --- | --- | --- |
| 域名转发 | 网站、Webhook、管理后台、小程序调试 | [域名转发](/guide/tunnels/domain-forwarding) |
| TCP 隧道 | SSH、RDP、数据库、任意 TCP 服务 | [TCP 隧道](/guide/tunnels/tcp) |
| UDP 隧道 | DNS、游戏、音视频流、任意 UDP 服务 | [UDP 隧道](/guide/tunnels/udp) |
| 混合代理 | HTTP 代理、Socks5 代理、一个端口同时提供两者 | [混合代理](/guide/tunnels/mixed-proxy) |
| 私密代理 | 不希望直接公开公网业务端口的内网服务 | [私密代理](/guide/tunnels/secret) |
| P2P 隧道 | 大流量、低延迟、尽量直连的点对点访问 | [P2P 隧道](/guide/tunnels/p2p) |
| 文件隧道 | 暴露本地目录或文件访问 | [文件隧道](/guide/tunnels/file) |

## 与其他部分的边界

- 还不清楚该选哪种规则：看 [规则选型总览](/guide/design/tunnel-selection) 或 [场景总览与选型](/guide/scenarios/common)
- 需要精确配置项和行为限制：看 [参考资料](/reference/README)
