//go:build windows

package transport

import (
	"errors"
	"net"
)

func ListenTCP(address string, transparent bool) (net.Listener, error) {
	if transparent {
		return nil, errors.New("transparent tcp listener is not supported on Windows")
	}
	return net.Listen("tcp", address)
}
