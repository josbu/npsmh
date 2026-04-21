package pmux

import (
	"io"
	"net"
	"sync"
	"time"
)

type PortListener struct {
	net.Listener
	route *portRoute
}

type PortConn struct {
	Conn net.Conn
	rs   []byte
}

type portRoute struct {
	conns     chan *PortConn
	closed    chan struct{}
	addr      net.Addr
	closeOnce sync.Once
}

func newPortRoute(addr net.Addr) *portRoute {
	return &portRoute{
		conns:  make(chan *PortConn, 1024),
		closed: make(chan struct{}),
		addr:   addr,
	}
}

func NewPortListener(route *portRoute) *PortListener {
	return &PortListener{
		route: route,
	}
}

func newPortConn(conn net.Conn, rs []byte) *PortConn {
	return &PortConn{
		Conn: conn,
		rs:   rs,
	}
}

func (pConn *PortConn) Read(b []byte) (n int, err error) {
	if len(pConn.rs) > 0 {
		n = copy(b, pConn.rs)
		pConn.rs = pConn.rs[n:]
		if len(pConn.rs) == 0 {
			pConn.rs = nil
		}
		if n == len(b) {
			return n, nil
		}
	}
	m, err := pConn.Conn.Read(b[n:])
	n += m
	if n > 0 && err == io.EOF {
		err = nil
	}
	return n, err
}

func (pConn *PortConn) Write(b []byte) (n int, err error) {
	return pConn.Conn.Write(b)
}

func (pConn *PortConn) Close() error {
	return pConn.Conn.Close()
}

func (pConn *PortConn) LocalAddr() net.Addr {
	return pConn.Conn.LocalAddr()
}

func (pConn *PortConn) RemoteAddr() net.Addr {
	return pConn.Conn.RemoteAddr()
}

func (pConn *PortConn) SetDeadline(t time.Time) error {
	return pConn.Conn.SetDeadline(t)
}

func (pConn *PortConn) SetReadDeadline(t time.Time) error {
	return pConn.Conn.SetReadDeadline(t)
}

func (pConn *PortConn) SetWriteDeadline(t time.Time) error {
	return pConn.Conn.SetWriteDeadline(t)
}

func (pListener *PortListener) Accept() (net.Conn, error) {
	if pListener == nil || pListener.route == nil {
		return nil, net.ErrClosed
	}
	select {
	case <-pListener.route.closed:
		return nil, net.ErrClosed
	default:
	}
	select {
	case conn := <-pListener.route.conns:
		if conn != nil {
			return conn, nil
		}
		return nil, net.ErrClosed
	case <-pListener.route.closed:
		return nil, net.ErrClosed
	}
}

func (pListener *PortListener) Close() error {
	if pListener == nil || pListener.route == nil {
		return nil
	}
	pListener.route.close()
	return nil
}

func (pListener *PortListener) Addr() net.Addr {
	if pListener == nil || pListener.route == nil {
		return nil
	}
	return pListener.route.addr
}

func (r *portRoute) close() {
	if r == nil {
		return
	}
	r.closeOnce.Do(func() {
		close(r.closed)
		for {
			select {
			case conn := <-r.conns:
				if conn != nil {
					_ = conn.Close()
				}
			default:
				return
			}
		}
	})
}

func (r *portRoute) dispatch(conn *PortConn, parentClosed <-chan struct{}) {
	if r == nil || conn == nil {
		if conn != nil {
			_ = conn.Close()
		}
		return
	}
	select {
	case <-parentClosed:
		_ = conn.Close()
	case <-r.closed:
		_ = conn.Close()
	case r.conns <- conn:
	default:
		_ = conn.Close()
	}
}
