# 部署与兼容补充

这一页只保留不适合放在入门流程里的补充事项。

## 旧版兼容

如果需要兼容旧版客户端：

- 服务端可设置 `secure_mode=false`
- 旧版客户端连接时可添加 `-proto_version=0`

新部署建议显式设置 `secure_mode=true`，只在确实需要兼容旧环境时再调整。

更适合客户端阅读的说明见 [维护与更新](/guide/client/maintenance) 和 [NPC 命令行参数](/reference/npc-cli)。

## Linux 系统限制

在高连接数场景下，Linux 默认系统参数可能成为瓶颈。常见需要关注的参数包括：

- `tcp_max_syn_backlog`
- `somaxconn`

使用 QUIC 时还可能看到 UDP 缓冲区相关警告。参考 [quic-go Wiki](https://github.com/quic-go/quic-go/wiki/UDP-Buffer-Sizes)，可以通过下面的方式增大缓冲区：

```bash
echo -e "\nnet.core.rmem_max = 7500000\nnet.core.wmem_max = 7500000" | sudo tee -a /etc/sysctl.conf
sudo sysctl -p
```

## Web 管理保护

Web 管理后台支持多层登录保护，包括：

- 登录失败封禁
- 图形验证码
- TOTP 双因素认证
- PoW 校验

这类配置项主要在 [Web、HTTP 与安全](/reference/server-config-web) 中查看。
