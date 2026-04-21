package mux

import (
	"context"
	"net"
)

// ConnCapabilities carries optional hints about the wrapped transport.
type ConnCapabilities struct {
	RawConn   net.Conn
	CloseChan <-chan struct{}
	Context   context.Context
}

// AdaptedConn wraps a net.Conn and exposes additional transport capabilities to
// mux helpers without changing the wire protocol.
type AdaptedConn struct {
	net.Conn
	rawConn   net.Conn
	closeChan <-chan struct{}
	ctx       context.Context
}

// AdaptConn attaches optional raw-connection and lifecycle capabilities to conn.
func AdaptConn(conn net.Conn, caps ConnCapabilities) net.Conn {
	if conn == nil {
		return nil
	}
	if caps.RawConn == nil && caps.CloseChan == nil && caps.Context == nil {
		return conn
	}
	return &AdaptedConn{
		Conn:      conn,
		rawConn:   caps.RawConn,
		closeChan: caps.CloseChan,
		ctx:       caps.Context,
	}
}

func (c *AdaptedConn) GetRawConn() net.Conn {
	if c == nil {
		return nil
	}
	return c.rawConn
}

func (c *AdaptedConn) CloseChan() <-chan struct{} {
	if c == nil {
		return nil
	}
	return c.closeChan
}

func (c *AdaptedConn) Context() context.Context {
	if c == nil {
		return nil
	}
	return c.ctx
}

func (c *AdaptedConn) WrappedConn() net.Conn {
	if c == nil {
		return nil
	}
	return c.Conn
}
