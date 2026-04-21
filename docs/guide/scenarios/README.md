# 常见场景

这一部分按“业务目标”组织，适合已经明确结果，但还没有确定规则类型的用户。

如果你已经知道需要哪一种规则，直接进入 [隧道与转发类型](/guide/tunnels/README)。

## 推荐阅读顺序

1. 先看 [场景总览与选型](/guide/scenarios/common)
2. 再进入对应的 [隧道与转发类型](/guide/tunnels/README)
3. 需要精确参数时，再进入 [参考资料](/reference/README)

## 常见入口

- 发布网站、Webhook 或调试环境：看 [域名转发](/guide/tunnels/domain-forwarding)
- 暴露 SSH、RDP、数据库：看 [TCP 隧道](/guide/tunnels/tcp)
- 暴露 DNS、游戏或实时 UDP 服务：看 [UDP 隧道](/guide/tunnels/udp)
- 从外网通过代理访问整个内网：看 [混合代理](/guide/tunnels/mixed-proxy)
- 不直接开放公网业务端口：看 [私密代理](/guide/tunnels/secret)
- 优先建立直连，减少中转带宽：看 [P2P 隧道](/guide/tunnels/p2p)
- 暴露本地目录或下载文件：看 [文件隧道](/guide/tunnels/file)
