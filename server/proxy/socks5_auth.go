package proxy

import (
	"errors"
	"fmt"
	"net"
	"sync/atomic"

	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
)

var (
	errSocks5AuthFailed             = errors.New("socks5: auth failed")
	errSocks5NoAcceptableAuthMethod = errors.New("socks5: no acceptable authentication method")
)

func (s *TunnelModeServer) SocksAuth(c net.Conn) error {
	task := s.CurrentTask()
	if err := validateTunnelRuntimeTask(task); err != nil {
		_ = writeSocks5AuthStatus(c, authFailure)
		return err
	}
	username, password, err := readSocks5UserPassCredentials(c)
	if err != nil {
		return err
	}

	valid := tunnelProxyAuthPolicy(task).CheckCredentials(username, password)
	status := authFailure
	if valid {
		status = authSuccess
	}
	if err := writeSocks5AuthStatus(c, status); err != nil {
		return err
	}
	if !valid {
		return errSocks5AuthFailed
	}
	return nil
}

func (s *TunnelModeServer) negotiateSocks5Method(c net.Conn, methods []byte) error {
	task := s.CurrentTask()
	if err := validateTunnelRuntimeTask(task); err != nil {
		_ = writeSocks5MethodSelection(c, noAcceptableAuth)
		return err
	}

	if socks5NeedsAuth(task) {
		if !supportsSocks5Method(methods, UserPassAuth) {
			logSocks5AuthMethod(task, "userpass", "rejected")
			_ = writeSocks5MethodSelection(c, noAcceptableAuth)
			return fmt.Errorf("%w: userpass required", errSocks5NoAcceptableAuthMethod)
		}
		logSocks5AuthMethod(task, "userpass", "selected")
		if err := writeSocks5MethodSelection(c, UserPassAuth); err != nil {
			return err
		}
		if err := s.SocksAuth(c); err != nil {
			logSocks5AuthMethod(task, "userpass", "failed")
			logs.Warn("Validation failed: %v", err)
			return err
		}
		logSocks5AuthMethod(task, "userpass", "accepted")
		return nil
	}

	if len(methods) == 0 {
		logSocks5AuthMethod(task, "noauth", "selected")
		return writeSocks5MethodSelection(c, noAuthMethod)
	}

	if !supportsSocks5Method(methods, noAuthMethod) {
		logSocks5AuthMethod(task, "noauth", "rejected")
		_ = writeSocks5MethodSelection(c, noAcceptableAuth)
		return fmt.Errorf("%w: no-auth not offered", errSocks5NoAcceptableAuthMethod)
	}
	logSocks5AuthMethod(task, "noauth", "selected")
	return writeSocks5MethodSelection(c, noAuthMethod)
}

func socks5NeedsAuth(task *file.Tunnel) bool {
	return tunnelProxyAuthPolicy(task).RequiresAuth()
}

func supportsSocks5Method(methods []byte, method byte) bool {
	for _, offered := range methods {
		if offered == method {
			return true
		}
	}
	return false
}

func writeSocks5MethodSelection(c net.Conn, method byte) error {
	if c == nil {
		return nil
	}
	_, err := c.Write([]byte{5, method})
	return err
}

func writeSocks5AuthStatus(c net.Conn, status byte) error {
	if c == nil {
		return nil
	}
	_, err := c.Write([]byte{userAuthVersion, status})
	return err
}

var socks5MetricsState = struct {
	connectRemoteResultTimeout atomic.Uint64
	connectRemoteFailed        atomic.Uint64
	connectRemoteRefused       atomic.Uint64

	authUserPassSelected atomic.Uint64
	authUserPassAccepted atomic.Uint64
	authUserPassRejected atomic.Uint64
	authUserPassFailed   atomic.Uint64
	authNoAuthSelected   atomic.Uint64
	authNoAuthRejected   atomic.Uint64
	authOther            atomic.Uint64

	udpAmbiguousDrop atomic.Uint64
}{}

func recordSocks5ConnectRemoteResultTimeout() {
	socks5MetricsState.connectRemoteResultTimeout.Add(1)
}

func recordSocks5ConnectRemoteFailure(replyCode uint8) {
	switch replyCode {
	case connectionRefused:
		socks5MetricsState.connectRemoteRefused.Add(1)
	default:
		socks5MetricsState.connectRemoteFailed.Add(1)
	}
}

func recordSocks5AuthMethod(method, outcome string) {
	switch method {
	case "userpass":
		switch outcome {
		case "selected":
			socks5MetricsState.authUserPassSelected.Add(1)
		case "accepted":
			socks5MetricsState.authUserPassAccepted.Add(1)
		case "rejected":
			socks5MetricsState.authUserPassRejected.Add(1)
		case "failed":
			socks5MetricsState.authUserPassFailed.Add(1)
		default:
			socks5MetricsState.authOther.Add(1)
		}
	case "noauth":
		switch outcome {
		case "selected":
			socks5MetricsState.authNoAuthSelected.Add(1)
		case "rejected":
			socks5MetricsState.authNoAuthRejected.Add(1)
		default:
			socks5MetricsState.authOther.Add(1)
		}
	default:
		socks5MetricsState.authOther.Add(1)
	}
}

func logSocks5ConnectRemoteResultTimeout(task *file.Tunnel, dest string, err error) {
	recordSocks5ConnectRemoteResultTimeout()
	clientID, taskID := socks5LogIDs(task)
	logs.Warn("event=s5_connect_remote_result_timeout client=%d task=%d dest=%s err=%v", clientID, taskID, dest, err)
}

func logSocks5ConnectRemoteFailure(task *file.Tunnel, dest string, replyCode uint8) {
	recordSocks5ConnectRemoteFailure(replyCode)
	clientID, taskID := socks5LogIDs(task)
	switch replyCode {
	case connectionRefused:
		logs.Warn("event=s5_connect_remote_refused client=%d task=%d dest=%s", clientID, taskID, dest)
	default:
		logs.Warn("event=s5_connect_remote_failed client=%d task=%d dest=%s reply=%d", clientID, taskID, dest, replyCode)
	}
}

func logSocks5AuthMethod(task *file.Tunnel, method, outcome string) {
	recordSocks5AuthMethod(method, outcome)
	clientID, taskID := socks5LogIDs(task)
	logs.Info("event=s5_auth_method_selected client=%d task=%d method=%s outcome=%s", clientID, taskID, method, outcome)
}

func logSocks5UDPAmbiguousDrop(task *file.Tunnel, clientIP string, port, activeCount, pendingCount int) {
	recordSocks5UDPAmbiguousDrop()
	clientID, taskID := socks5LogIDs(task)
	logs.Debug("event=s5_udp_ambiguous_drop client=%d task=%d ip=%s port=%d active=%d pending=%d", clientID, taskID, clientIP, port, activeCount, pendingCount)
}

func socks5LogIDs(task *file.Tunnel) (clientID, taskID int) {
	if task == nil {
		return 0, 0
	}
	taskID = task.Id
	if task.Client != nil {
		clientID = task.Client.Id
	}
	return clientID, taskID
}

func recordSocks5UDPAmbiguousDrop() {
	socks5MetricsState.udpAmbiguousDrop.Add(1)
}

func Socks5MetricsSnapshot() map[string]interface{} {
	return map[string]interface{}{
		"connect": map[string]interface{}{
			"remote_result_timeout": socks5MetricsState.connectRemoteResultTimeout.Load(),
			"remote_failed":         socks5MetricsState.connectRemoteFailed.Load(),
			"remote_refused":        socks5MetricsState.connectRemoteRefused.Load(),
		},
		"auth": map[string]interface{}{
			"userpass_selected": socks5MetricsState.authUserPassSelected.Load(),
			"userpass_accepted": socks5MetricsState.authUserPassAccepted.Load(),
			"userpass_rejected": socks5MetricsState.authUserPassRejected.Load(),
			"userpass_failed":   socks5MetricsState.authUserPassFailed.Load(),
			"noauth_selected":   socks5MetricsState.authNoAuthSelected.Load(),
			"noauth_rejected":   socks5MetricsState.authNoAuthRejected.Load(),
			"other":             socks5MetricsState.authOther.Load(),
		},
		"udp": map[string]interface{}{
			"ambiguous_drop": socks5MetricsState.udpAmbiguousDrop.Load(),
		},
	}
}

func resetSocks5Metrics() {
	socks5MetricsState.connectRemoteResultTimeout.Store(0)
	socks5MetricsState.connectRemoteFailed.Store(0)
	socks5MetricsState.connectRemoteRefused.Store(0)
	socks5MetricsState.authUserPassSelected.Store(0)
	socks5MetricsState.authUserPassAccepted.Store(0)
	socks5MetricsState.authUserPassRejected.Store(0)
	socks5MetricsState.authUserPassFailed.Store(0)
	socks5MetricsState.authNoAuthSelected.Store(0)
	socks5MetricsState.authNoAuthRejected.Store(0)
	socks5MetricsState.authOther.Store(0)
	socks5MetricsState.udpAmbiguousDrop.Store(0)
}
