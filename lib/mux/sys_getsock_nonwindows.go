//go:build !windows

package mux

import (
	"net"
	"syscall"
)

func socketReceiveBufferSize(c net.Conn) (uint32, bool) {
	base := unwrapSocketConn(c)
	if base == nil {
		return 0, false
	}

	sysConn, ok := base.(syscall.Conn)
	if !ok {
		return 0, false
	}

	rawConn, err := sysConn.SyscallConn()
	if err != nil {
		return 0, false
	}

	var (
		size    int
		sockErr error
	)
	if err := rawConn.Control(func(fd uintptr) {
		size, sockErr = syscall.GetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF)
	}); err != nil || sockErr != nil || size <= 0 {
		return 0, false
	}
	return uint32(size), true
}
