//go:build windows
// +build windows

package proxy

import (
	"testing"

	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
)

func TestHandleTransRejectsMalformedRuntimeTaskWindows(t *testing.T) {
	server := NewTunnelModeServer(ProcessTunnel, &noCallServerBridge{}, &file.Tunnel{})
	if err := HandleTrans(conn.NewConn(newUDPCloseSpyConn()), server); err == nil {
		t.Fatal("HandleTrans() should reject malformed runtime task on Windows")
	}
}
