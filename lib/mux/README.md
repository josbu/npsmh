# mux

`lib/mux` 是一个跑在 `net.Conn` 之上的多路复用实现。

当前实现保持旧版线协议兼容，适合继续跑在这些传输之上：

- `tcp`
- `tls`
- `websocket` / `wss`
- `kcp`
- 其他最终表现为“可靠、有序、全双工字节流”的包装连接

不适合直接跑在原始 `udp` / `net.PacketConn` 这种无序 datagram 之上。  
如果底层是 `udp`，需要先经过 `kcp`、`quic stream` 之类把它抽象成流。

## 设计目标

- 不改线协议，能和旧版 `mux` 互通
- 尽量适配不同网络环境和不同连接包装层
- 在能拿到底层连接能力时尽量利用 `nodelay`、`keepalive`、close signal 等特性
- 拿不到底层连接时退回应用层探测，不强依赖某一种传输
- 不依赖项目内日志模块，尽量通过更具体的错误原因帮助排查问题

## 推荐用法

优先使用 `NewMuxWithConfig`，并把旧的 `pingCheckThreshold` 传 `0`，让超时逻辑统一由 `MuxConfig` 控制。

```go
cfg := mux.DefaultMuxConfig()
cfg.PingInterval = 5 * time.Second
cfg.PingJitter = 2 * time.Second
cfg.PingTimeout = 30 * time.Second
cfg.MinPingTimeout = 8 * time.Second
cfg.PingTimeoutMultiplier = 4
cfg.ReadTimeout = 0
cfg.WriteTimeout = 0

m := mux.NewMuxWithConfig(conn, "tcp", 0, true, cfg)
defer m.Close()
```

如果继续使用旧接口：

```go
m := mux.NewMux(conn, "tcp", 30, true)
```

那么第三个参数 `pingCheckThreshold` 仍然表示“固定最大 ping 超时秒数”，它会优先于 `MuxConfig.PingTimeout`。

## 连接要求

`mux` 的前提不是“底层一定是 TCP”，而是：

- 可靠
- 有序
- 双向读写
- 最终能暴露为 `net.Conn`

所以这些通常都可以直接用：

- `tcp -> tls -> mux`
- `tcp -> websocket -> mux`
- `udp -> kcp -> mux`
- `tcp -> 压缩/混淆/限速包装 -> mux`

## 包装连接和底层能力透传

`mux` 会尝试沿着包装层往下找底层连接，目前会识别这些接口：

- `GetRawConn() net.Conn`
- `NetConn() net.Conn`
- `UnderlyingConn() net.Conn`
- `WrappedConn() net.Conn`

如果连接本身还能暴露这些活性信号，`mux` 也会自动利用：

- `CloseChan() <-chan struct{}`
- `Done() <-chan struct{}`
- `Context() context.Context`

如果你的包装层把底层能力抹掉了，`mux` 不能可靠“猜”出真实底层连接。  
这种场景建议显式包一层 `AdaptConn(...)`：

```go
wrapped := mux.AdaptConn(conn, mux.ConnCapabilities{
    RawConn:   rawConn,
    CloseChan: closeCh,
    Context:   ctx,
})

m := mux.NewMuxWithConfig(wrapped, "ws", 0, true, cfg)
```

`AdaptConn` 的作用不是改协议，而是把这些额外能力显式告诉 `mux`：

- 真正的底层 `net.Conn`
- 更快的关闭通知
- 外部生命周期 `context`

## 超时和 RTT

`mux` 内部有两层活性判断：

- 如果底层连接自己已经暴露关闭信号，优先立刻关闭
- 如果底层不会主动断开，就依赖应用层 ping 检测

当前 RTT 相关状态可以直接读取：

- `Latency()`：平滑后的 RTT
- `LastLatency()`：最近一次 ping 测得的原始 RTT
- `PeakLatency()`：历史峰值 RTT
- `ResetPeakLatency()`：把历史峰值重置为最近一次 RTT
- `EffectivePingTimeout()`：当前生效的 ping 超时

自适应超时逻辑是：

1. 先取 `PeakLatency() * PingTimeoutMultiplier`
2. 再钳到 `[MinPingTimeout, PingTimeout]` 区间内
3. 如果还没有测到有效 RTT，则退回 `PingTimeout`

这比只看当前平滑 RTT 更适合高抖动链路，比如延迟会在几毫秒到几秒之间跳动的网络。
如果外部已经知道链路条件发生了变化，可以主动调用 `ResetPeakLatency()`，避免一次历史尖峰长期把超时抬得过高。

推荐经验值：

- 普通 TCP / TLS：`PingInterval=5s`，`PingTimeout=20s~30s`
- KCP / 高抖动链路：`PingInterval=5s~8s`，`MinPingTimeout=8s~12s`
- 如果外层已经有稳定心跳，可以适当增大 `PingInterval`

## 重要配置

最常用的是这些字段：

- `PingInterval`：ping 基础周期
- `PingJitter`：ping 抖动，减少固定特征
- `PingTimeout`：允许的最大 ping 超时
- `MinPingTimeout`：自适应超时的最小值
- `PingTimeoutMultiplier`：`PeakLatency` 的放大倍数
- `DisableAdaptivePingTimeout`：关闭 RTT 自适应
- `ReadTimeout` / `WriteTimeout`：底层读写 deadline，`0` 表示跟随 ping 超时，负值表示禁用
- `SocketKeepAlive`：能拿到底层 TCP 时启用 keepalive
- `DisableSocketNoDelay`：默认会尽量开启 `nodelay`
- `CloseTimeout`：关闭时先下发一个 deadline，避免某些连接卡死在 `Close`
- `WriteQueueHighWater` / `WriteQueueLowWater`：发送队列回压阈值
- `DisableTrafficAwarePing`：关闭“最近有流量就少发 ping”的优化
- `DisablePingPadRandom`：关闭 ping padding 随机化

## 关闭语义

- `Mux.Close()` 会关闭传给它的那个 `net.Conn`
- 如果包装链上的 `Close()` 会继续往下传，底层连接也会一起关闭
- 如果中间某一层自己截断了 `Close()`，`mux` 不能绕过它去强关更底层
- `Conn.Close()` 只关闭逻辑流，不会直接关闭整个 `Mux`

`Conn.CloseWrite()` 支持半关闭；如果对端太老，不支持这个能力，会自动退化成普通 `Close()`。

## 兼容性

这份实现保持旧版协议兼容，重点优化的是实现层：

- 更统一的配置入口
- 更快的断连感知
- 对包装连接更友好
- 更稳的队列回压
- 更少的外部依赖
- 更明确的关闭原因

因此旧版 `mux` 的对端一般不需要配套修改，只要双方说的还是同一个线协议即可。
如果要做全新协议设计，请直接看 [lib/zmux/README.md](/D:/GolandProjects/nps/lib/zmux/README.md)。

## 测试

默认测试不依赖外部网络工具、docker、`tc` 或手工环境。

```bash
go test ./lib/mux -count=1 -timeout 120s
```
