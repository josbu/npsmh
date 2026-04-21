//go:build !windows

package proxy

import (
	"testing"

	"github.com/djylb/nps/lib/conn"
)

func TestHandleTransRejectsMalformedSelectedRuntimeTask(t *testing.T) {
	task := bindMalformedTunnelRuntimeTarget(newTestSocks5Task(1080, "p2pt"))
	server := NewTunnelModeServer(ProcessTunnel, &noCallServerBridge{}, task)
	if err := HandleTrans(conn.NewConn(newUDPCloseSpyConn()), server); err == nil {
		t.Fatal("HandleTrans() should reject malformed selected runtime task")
	}
}
