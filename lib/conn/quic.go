package conn

import (
	"context"
	"crypto/tls"
	"net"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/quic-go/quic-go"
)

var listenQUICPacketConn = func(localIP string) (net.PacketConn, error) {
	return net.ListenUDP("udp", common.BuildUDPBindAddr(localIP))
}

type QuicStreamConn struct {
	stream *quic.Stream
	sess   *quic.Conn
}

func NewQuicStreamConn(stream *quic.Stream, sess *quic.Conn) *QuicStreamConn {
	return &QuicStreamConn{stream: stream, sess: sess}
}

func (q *QuicStreamConn) GetSession() *quic.Conn {
	if q == nil {
		return nil
	}
	return q.sess
}

func (q *QuicStreamConn) Read(p []byte) (int, error) {
	if q == nil || q.stream == nil {
		return 0, net.ErrClosed
	}
	return q.stream.Read(p)
}

func (q *QuicStreamConn) Write(p []byte) (int, error) {
	if q == nil || q.stream == nil {
		return 0, net.ErrClosed
	}
	return q.stream.Write(p)
}

func (q *QuicStreamConn) Close() error {
	if q == nil || q.stream == nil {
		return nil
	}
	return q.stream.Close()
}

func (q *QuicStreamConn) LocalAddr() net.Addr {
	if q == nil || q.sess == nil {
		return nil
	}
	return q.sess.LocalAddr()
}

func (q *QuicStreamConn) RemoteAddr() net.Addr {
	if q == nil || q.sess == nil {
		return nil
	}
	return q.sess.RemoteAddr()
}

func (q *QuicStreamConn) SetDeadline(t time.Time) error {
	if q == nil || q.stream == nil {
		return net.ErrClosed
	}
	return q.stream.SetDeadline(t)
}

func (q *QuicStreamConn) SetReadDeadline(t time.Time) error {
	if q == nil || q.stream == nil {
		return net.ErrClosed
	}
	return q.stream.SetReadDeadline(t)
}

func (q *QuicStreamConn) SetWriteDeadline(t time.Time) error {
	if q == nil || q.stream == nil {
		return net.ErrClosed
	}
	return q.stream.SetWriteDeadline(t)
}

type QuicAutoCloseConn struct{ *QuicStreamConn }

func NewQuicAutoCloseConn(stream *quic.Stream, sess *quic.Conn) *QuicAutoCloseConn {
	return &QuicAutoCloseConn{NewQuicStreamConn(stream, sess)}
}

func (q *QuicAutoCloseConn) Close() error {
	if q == nil || q.QuicStreamConn == nil {
		return nil
	}
	if q.QuicStreamConn.stream != nil {
		q.QuicStreamConn.stream.CancelRead(0)
		_ = q.QuicStreamConn.Close()
	}
	if q.sess == nil {
		return nil
	}
	return q.sess.CloseWithError(0, "close")
}

func DialQuicWithLocalIP(ctx context.Context, server string, tlsCfg *tls.Config, quicCfg *quic.Config, localIP string) (*quic.Conn, error) {
	bindAddr := common.BuildUDPBindAddr(localIP)
	if bindAddr == nil {
		return quic.DialAddr(ctx, server, tlsCfg, quicCfg)
	}
	rAddr, err := net.ResolveUDPAddr("udp", server)
	if err != nil {
		return nil, err
	}
	packetConn, err := listenQUICPacketConn(localIP)
	if err != nil {
		return nil, err
	}
	sess, err := quic.Dial(ctx, packetConn, rAddr, tlsCfg, quicCfg)
	if err != nil {
		_ = packetConn.Close()
		return nil, err
	}
	go closePacketConnOnDone(sess.Context().Done(), packetConn)
	return sess, nil
}

func closePacketConnOnDone(done <-chan struct{}, packetConn net.PacketConn) {
	if done == nil || packetConn == nil {
		return
	}
	<-done
	_ = packetConn.Close()
}
