//go:build !linux && !freebsd && !windows

package transport

import "net"

func ListenTCP(address string, _ bool) (net.Listener, error) {
	return net.Listen("tcp", address)
}
