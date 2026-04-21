//go:build windows
// +build windows

package mux

import "net"

func socketReceiveBufferSize(c net.Conn) (uint32, bool) {
	_ = c
	return 0, false
}
