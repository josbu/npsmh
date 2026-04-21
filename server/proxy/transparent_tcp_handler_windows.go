//go:build windows
// +build windows

package proxy

import (
	"errors"

	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/logs"
)

func HandleTrans(c *conn.Conn, s *TunnelModeServer) error {
	if _, validateErr := selectValidatedTunnelRuntimeTask(s.CurrentTask()); validateErr != nil {
		return validateErr
	}
	err := errors.New("transparent proxy is not supported on Windows")
	task := s.CurrentTask()
	logs.Warn("reject transparent proxy request: task=%d client=%d remote=%v err=%v", task.Id, task.Client.Id, c.RemoteAddr(), err)
	return err
}
