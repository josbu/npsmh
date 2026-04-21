package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/djylb/nps/lib/config"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/mux"
	"github.com/quic-go/quic-go"
)

type Closer interface{ Close() error }

var p2pOpenQUICStreamSync = func(ctx context.Context, qConn *quic.Conn) (*quic.Stream, error) {
	return qConn.OpenStreamSync(ctx)
}
var p2pSecretSendType = SendType
var p2pWorkSendType = SendType
var p2pSecretSendLinkInfo = func(c net.Conn, link *conn.Link) error {
	if c == nil {
		return nil
	}
	_, err := conn.NewConn(c).SendInfo(link, "")
	return err
}
var p2pSecretReadACK = conn.ReadACK

var errP2PBridgeUnavailable = errors.New("p2p bridge is unavailable")
var errP2PManagerUnavailable = errors.New("p2p manager is unavailable")
var errP2PQUICTransportUnavailable = errors.New("p2p quic transport is unavailable")
var errP2PKCPTransportUnavailable = errors.New("p2p kcp transport is unavailable")

type P2pBridge struct {
	mgr     *P2PManager
	local   *config.LocalServer
	p2p     bool
	secret  bool
	timeout time.Duration
}

func NewP2pBridge(mgr *P2PManager, l *config.LocalServer) *P2pBridge {
	var useP2P, secret bool
	timeout := time.Second * 5
	if l.Type != "secret" && !DisableP2P {
		useP2P = true
		secret = l.Fallback
	} else {
		secret = true
	}
	if secret && useP2P {
		timeout = 3 * time.Second
	}
	return &P2pBridge{
		mgr:     mgr,
		local:   l,
		p2p:     useP2P,
		secret:  secret,
		timeout: timeout,
	}
}

func (b *P2pBridge) manager() (*P2PManager, error) {
	if b == nil {
		return nil, errP2PBridgeUnavailable
	}
	if b.mgr == nil {
		return nil, errP2PManagerUnavailable
	}
	return b.mgr, nil
}

func (b *P2pBridge) SendLinkInfo(_ int, link *conn.Link, _ *file.Tunnel) (net.Conn, error) {
	if link == nil {
		return nil, errors.New("link is nil")
	}
	mgr, err := b.manager()
	if err != nil {
		return nil, err
	}
	var lastErr error
	waitTimeout := b.timeout
	if waitTimeout <= 0 {
		waitTimeout = time.Second
	}
	ctx, cancel := context.WithTimeout(normalizeClientParentContext(mgr.ctx), waitTimeout)
	defer cancel()
	if b.p2p && b.local != nil && strings.TrimSpace(b.local.Password) != "" {
		_, peer, err := mgr.waitOrEstablishPeer(ctx, b.local)
		if err == nil && peer != nil {
			if quicConn := normalizeP2PQUICConn(peer.transport.quicConn); quicConn != nil {
				logs.Trace("using P2P[QUIC] for peer %s", peer.peer.UUID)
				viaQUIC, sendErr := b.sendViaQUIC(ctx, link, quicConn, time.Since(peer.lastActive), peer)
				if sendErr == nil {
					return viaQUIC, nil
				}
				lastErr = sendErr
			}
			if muxSession := normalizeP2PMuxSession(peer.transport.muxSession); muxSession != nil {
				logs.Trace("using P2P[KCP] for peer %s", peer.peer.UUID)
				viaKCP, sendErr := b.sendViaKCP(link, muxSession, peer)
				if sendErr == nil {
					return viaKCP, nil
				}
				lastErr = sendErr
			}
		} else if err != nil {
			lastErr = err
		}
		if b.secret {
			logs.Warn("P2P not ready, fallback to secret")
			viaSecret, err := b.sendViaSecret(ctx, link)
			if err == nil {
				return viaSecret, nil
			}
			lastErr = err
		}
		if lastErr != nil {
			if errors.Is(lastErr, context.DeadlineExceeded) {
				return nil, fmt.Errorf("timeout waiting P2P tunnel; last error: %w", lastErr)
			}
			return nil, lastErr
		}
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	first := true
	for {
		var tick <-chan time.Time
		if first {
			first = false
			ch := make(chan time.Time, 1)
			ch <- time.Time{}
			tick = ch
		} else {
			tick = ticker.C
		}
		select {
		case <-ctx.Done():
			mgr.resetStatus(false)
			if lastErr != nil {
				return nil, fmt.Errorf("timeout waiting P2P tunnel; last error: %w", lastErr)
			}
			return nil, errors.New("timeout waiting P2P tunnel")
		case <-tick:
			if b.p2p {
				mgr.mu.Lock()
				qConn := normalizeP2PQUICConn(mgr.quicConn)
				session := normalizeP2PMuxSession(mgr.muxSession)
				idle := time.Since(mgr.lastActive)
				mgr.mu.Unlock()
				if qConn != nil {
					logs.Trace("using P2P[QUIC] for connection")
					viaQUIC, err := b.sendViaQUIC(ctx, link, qConn, idle, nil)
					if err == nil {
						return viaQUIC, nil
					}
					lastErr = err
				}
				if session != nil {
					logs.Trace("using P2P[KCP] for connection")
					viaKCP, err := b.sendViaKCP(link, session, nil)
					if err == nil {
						return viaKCP, nil
					}
					lastErr = err
				}
			}
			if b.secret {
				if b.p2p {
					logs.Warn("P2P not ready, fallback to secret")
				} else {
					logs.Trace("using Secret for connection")
				}
				viaSecret, err := b.sendViaSecret(ctx, link)
				if err == nil {
					return viaSecret, nil
				}
				lastErr = err
			}
		}
	}
}

func (b *P2pBridge) sendViaQUIC(ctx context.Context, link *conn.Link, qConn *quic.Conn, idle time.Duration, peer *p2pPeerState) (net.Conn, error) {
	mgr, err := b.manager()
	if err != nil {
		return nil, err
	}
	if qConn == nil {
		return nil, errP2PQUICTransportUnavailable
	}
	sendLink := quicLinkForP2PSend(link, idle > b.timeout)
	if sendLink == nil {
		return nil, errors.New("link is nil")
	}
	if sendLink.Option.NeedAck {
		logs.Trace("sent ACK before proceeding")
	}
	if ctx == nil {
		ctx = mgr.ctx
	}
	ctx = normalizeClientParentContext(ctx)
	stream, err := p2pOpenQUICStreamSync(ctx, qConn)
	if err != nil {
		logs.Trace("QUIC OpenStreamSync failed, retrying: %v", err)
		if peer != nil {
			mgr.mu.Lock()
			peer.statusOK = false
			mgr.mu.Unlock()
		} else {
			mgr.resetStatus(false)
		}
		return nil, err
	}
	nc := conn.NewQuicStreamConn(stream, qConn)
	sendOK := false
	defer func() {
		if !sendOK {
			_ = nc.Close()
		}
	}()
	if _, err := conn.NewConn(nc).SendInfo(sendLink, ""); err != nil {
		logs.Trace("QUIC SendInfo failed, retrying: %v", err)
		if peer != nil {
			mgr.mu.Lock()
			peer.statusOK = false
			mgr.mu.Unlock()
		} else {
			mgr.resetStatus(false)
		}
		return nil, err
	}
	if sendLink.Option.NeedAck {
		if err := conn.ReadACK(nc, b.timeout); err != nil {
			logs.Trace("QUIC ReadACK failed, retrying: %v", err)
			if peer != nil {
				mgr.mu.Lock()
				peer.statusOK = false
				mgr.mu.Unlock()
			} else {
				mgr.resetStatus(false)
			}
			return nil, err
		}
		now := time.Now()
		mgr.mu.Lock()
		if peer != nil {
			peer.lastActive = now
			peer.statusOK = true
		} else {
			mgr.lastActive = now
		}
		mgr.mu.Unlock()
	}
	if peer != nil {
		mgr.mu.Lock()
		peer.statusOK = true
		peer.lastActive = time.Now()
		mgr.mu.Unlock()
	} else {
		mgr.resetStatus(true)
	}
	sendOK = true
	return nc, nil
}

func (b *P2pBridge) sendViaKCP(link *conn.Link, session *mux.Mux, peer *p2pPeerState) (net.Conn, error) {
	mgr, err := b.manager()
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, errP2PKCPTransportUnavailable
	}
	sendLink := kcpLinkForP2PSend(link)
	if sendLink == nil {
		return nil, errors.New("link is nil")
	}
	nowConn, err := session.NewConn()
	if err != nil {
		logs.Trace("KCP NewConn failed, retrying: %v", err)
		if peer != nil {
			mgr.mu.Lock()
			peer.statusOK = false
			mgr.mu.Unlock()
		} else {
			mgr.resetStatus(false)
		}
		return nil, err
	}
	sendOK := false
	defer func() {
		if !sendOK {
			_ = nowConn.Close()
		}
	}()
	if _, err := conn.NewConn(nowConn).SendInfo(sendLink, ""); err != nil {
		logs.Trace("KCP SendInfo failed, retrying: %v", err)
		if peer != nil {
			mgr.mu.Lock()
			peer.statusOK = false
			mgr.mu.Unlock()
		} else {
			mgr.resetStatus(false)
		}
		return nil, err
	}
	if peer != nil {
		mgr.mu.Lock()
		peer.statusOK = true
		peer.lastActive = time.Now()
		mgr.mu.Unlock()
	} else {
		mgr.resetStatus(true)
	}
	sendOK = true
	return nowConn, nil
}

func (b *P2pBridge) sendViaSecret(ctx context.Context, link *conn.Link) (net.Conn, error) {
	mgr, err := b.manager()
	if err != nil {
		return nil, err
	}
	sendLink := cloneLinkForP2PSend(link)
	if sendLink == nil {
		return nil, errors.New("link is nil")
	}
	sc, err := mgr.getSecretConnContext(ctx)
	if err != nil {
		if AutoReconnect {
			logs.Trace("getSecretConn failed, retrying: %v", err)
		} else {
			logs.Trace("getSecretConn failed: %v", err)
			mgr.pCancel()
		}
		return nil, err
	}
	secretConn := mgr.snapshotSecretConn()
	sendOK := false
	defer func() {
		if !sendOK {
			_ = sc.Close()
		}
	}()
	if _, err := sc.Write([]byte(crypt.Md5(b.local.Password))); err != nil {
		logs.Error("secret write password failed: %v", err)
		mgr.discardSecretConnIfCurrent(secretConn, "secret password write failed")
		return nil, err
	}
	if err := p2pSecretSendLinkInfo(sc, sendLink); err != nil {
		logs.Trace("Secret SendInfo failed, retrying: %v", err)
		mgr.discardSecretConnIfCurrent(secretConn, "secret link send failed")
		return nil, err
	}
	if sendLink.Option.NeedAck {
		if err := p2pSecretReadACK(sc, b.timeout); err != nil {
			logs.Trace("Secret ReadACK failed, retrying: %v", err)
			mgr.discardSecretConnIfCurrent(secretConn, "secret ack failed")
			return nil, err
		}
	}
	sendOK = true
	return sc, nil
}

func (b *P2pBridge) IsServer() bool {
	return false
}

func (b *P2pBridge) CliProcess(*conn.Conn, string) {
}

func cloneLinkForP2PSend(link *conn.Link) *conn.Link {
	if link == nil {
		return nil
	}
	cloned := *link
	cloned.Option = link.Option
	return &cloned
}

func quicLinkForP2PSend(link *conn.Link, needAck bool) *conn.Link {
	cloned := cloneLinkForP2PSend(link)
	if cloned != nil && needAck {
		cloned.Option.NeedAck = true
	}
	return cloned
}

func kcpLinkForP2PSend(link *conn.Link) *conn.Link {
	cloned := cloneLinkForP2PSend(link)
	if cloned != nil {
		cloned.Option.NeedAck = false
	}
	return cloned
}
