//go:build !windows

package proxy

import (
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/transport"
)

func HandleTrans(c *conn.Conn, s *TunnelModeServer) error {
	task, err := selectValidatedTunnelRuntimeTask(s.CurrentTask())
	if err != nil {
		return err
	}
	addr, err := transport.GetAddress(c.Conn)
	if err != nil {
		logs.Warn("resolve transparent destination failed: task=%d client=%d remote=%v err=%v", task.Id, task.Client.Id, c.RemoteAddr(), err)
		return err
	}
	if err := s.DealClient(c, task.Client, addr, nil, common.CONN_TCP, nil, []*file.Flow{task.Flow, task.Client.Flow}, task.Target.ProxyProtocol, task.Target.LocalProxy, task); err != nil {
		logs.Warn("transparent proxy forward failed: task=%d client=%d target=%s err=%v", task.Id, task.Client.Id, addr, err)
		return err
	}
	return nil
}
