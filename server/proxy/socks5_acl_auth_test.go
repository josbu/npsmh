package proxy

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/djylb/nps/lib/file"
)

func TestTunnelModeServerSocks5ConnectACLDenyReturnsNotAllowed(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	defer func() { _ = clientSide.Close() }()

	task := newTestSocks5Task(1080, "mixProxy")
	task.DestAclMode = file.AclWhitelist
	task.DestAclRules = "1.1.1.1/32:80"
	task.CompileDestACL()

	server := NewTunnelModeServer(ProcessMix, failBridge{}, task)

	done := make(chan struct{})
	go func() {
		server.handleConn(serverSide)
		close(done)
	}()

	if _, err := clientSide.Write([]byte{5, 1, 0}); err != nil {
		t.Fatalf("write greeting: %v", err)
	}
	negotiation := make([]byte, 2)
	if _, err := io.ReadFull(clientSide, negotiation); err != nil {
		t.Fatalf("read negotiation reply: %v", err)
	}
	if !bytes.Equal(negotiation, []byte{5, 0}) {
		t.Fatalf("unexpected negotiation reply %v", negotiation)
	}

	if _, err := clientSide.Write([]byte{5, connectMethod, 0, ipV4, 8, 8, 8, 8, 0, 53}); err != nil {
		t.Fatalf("write connect request: %v", err)
	}
	if replyCode := readSocks5ReplyCode(t, clientSide); replyCode != notAllowed {
		t.Fatalf("reply code = %d, want %d", replyCode, notAllowed)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not finish ACL denied connect handling in time")
	}
}

func TestTunnelModeServerNegotiateSocks5MethodRejectsMalformedRuntimeTask(t *testing.T) {
	server := NewTunnelModeServer(ProcessMix, &noCallServerBridge{}, &file.Tunnel{})
	serverSide, clientSide := net.Pipe()
	defer func() { _ = clientSide.Close() }()

	errCh := make(chan error, 1)
	go func() {
		defer func() { _ = serverSide.Close() }()
		errCh <- server.negotiateSocks5Method(serverSide, []byte{noAuthMethod})
	}()

	reply := make([]byte, 2)
	if _, err := io.ReadFull(clientSide, reply); err != nil {
		t.Fatalf("read negotiation reply: %v", err)
	}
	if !bytes.Equal(reply, []byte{5, noAcceptableAuth}) {
		t.Fatalf("negotiation reply = %v, want %v", reply, []byte{5, noAcceptableAuth})
	}

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("negotiateSocks5Method() error = nil, want malformed runtime error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("negotiateSocks5Method() did not finish in time")
	}
}

func TestTunnelModeServerSocksAuthRejectsMalformedRuntimeTask(t *testing.T) {
	server := NewTunnelModeServer(ProcessMix, &noCallServerBridge{}, &file.Tunnel{})
	serverSide, clientSide := net.Pipe()
	defer func() { _ = clientSide.Close() }()

	errCh := make(chan error, 1)
	go func() {
		defer func() { _ = serverSide.Close() }()
		errCh <- server.SocksAuth(serverSide)
	}()

	reply := make([]byte, 2)
	if _, err := io.ReadFull(clientSide, reply); err != nil {
		t.Fatalf("read auth reply: %v", err)
	}
	if !bytes.Equal(reply, []byte{userAuthVersion, authFailure}) {
		t.Fatalf("auth reply = %v, want %v", reply, []byte{userAuthVersion, authFailure})
	}

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("SocksAuth() error = nil, want malformed runtime error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SocksAuth() did not finish in time")
	}
}

func TestTunnelModeServerSocks5UDPAssociateUserACLDenyReturnsNotAllowed(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	defer func() { _ = clientSide.Close() }()

	task := newTestSocks5Task(1080, "mixProxy")
	user := &file.User{
		Id:           7,
		Status:       1,
		TotalFlow:    &file.Flow{},
		DestAclMode:  file.AclBlacklist,
		DestAclRules: "10.0.0.0/8",
	}
	file.InitializeUserRuntime(user)
	task.Client.UserId = user.Id
	task.Client.BindOwnerUser(user)

	server := NewTunnelModeServer(ProcessMix, failBridge{}, task)

	done := make(chan struct{})
	go func() {
		server.handleConn(serverSide)
		close(done)
	}()

	if _, err := clientSide.Write([]byte{5, 1, 0}); err != nil {
		t.Fatalf("write greeting: %v", err)
	}
	negotiation := make([]byte, 2)
	if _, err := io.ReadFull(clientSide, negotiation); err != nil {
		t.Fatalf("read negotiation reply: %v", err)
	}
	if !bytes.Equal(negotiation, []byte{5, 0}) {
		t.Fatalf("unexpected negotiation reply %v", negotiation)
	}

	if _, err := clientSide.Write([]byte{5, associateMethod, 0, ipV4, 0, 0, 0, 0, 0, 0}); err != nil {
		t.Fatalf("write udp associate: %v", err)
	}
	if replyCode := readSocks5ReplyCode(t, clientSide); replyCode != notAllowed {
		t.Fatalf("reply code = %d, want %d", replyCode, notAllowed)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not finish ACL denied udp associate handling in time")
	}
}

func TestTunnelModeServerSocks5NoAuthAcceptsEmptyMethodList(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	defer func() { _ = clientSide.Close() }()

	task := newTestSocks5Task(1080, "mixProxy")
	server := NewTunnelModeServer(ProcessMix, failBridge{}, task)

	done := make(chan struct{})
	go func() {
		server.handleConn(serverSide)
		close(done)
	}()

	if _, err := clientSide.Write([]byte{5, 0}); err != nil {
		t.Fatalf("write greeting: %v", err)
	}
	negotiation := make([]byte, 2)
	if _, err := io.ReadFull(clientSide, negotiation); err != nil {
		t.Fatalf("read negotiation reply: %v", err)
	}
	if !bytes.Equal(negotiation, []byte{5, 0}) {
		t.Fatalf("unexpected negotiation reply %v", negotiation)
	}

	if _, err := clientSide.Write([]byte{5, connectMethod, 0, ipV4, 8, 8, 8, 8, 0, 53}); err != nil {
		t.Fatalf("write connect request: %v", err)
	}
	if replyCode := readSocks5ReplyCode(t, clientSide); replyCode != serverFailure {
		t.Fatalf("reply code = %d, want %d", replyCode, serverFailure)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not finish implicit no-auth connect handling in time")
	}
}

func TestTunnelModeServerSocks5NoAuthAcceptsOutOfOrderMethodList(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	defer func() { _ = clientSide.Close() }()

	task := newTestSocks5Task(1080, "mixProxy")
	server := NewTunnelModeServer(ProcessMix, failBridge{}, task)

	done := make(chan struct{})
	go func() {
		server.handleConn(serverSide)
		close(done)
	}()

	if _, err := clientSide.Write([]byte{5, 2, UserPassAuth, noAuthMethod}); err != nil {
		t.Fatalf("write greeting: %v", err)
	}
	negotiation := make([]byte, 2)
	if _, err := io.ReadFull(clientSide, negotiation); err != nil {
		t.Fatalf("read negotiation reply: %v", err)
	}
	if !bytes.Equal(negotiation, []byte{5, noAuthMethod}) {
		t.Fatalf("unexpected negotiation reply %v", negotiation)
	}

	if _, err := clientSide.Write([]byte{5, connectMethod, 0, ipV4, 8, 8, 8, 8, 0, 53}); err != nil {
		t.Fatalf("write connect request: %v", err)
	}
	if replyCode := readSocks5ReplyCode(t, clientSide); replyCode != serverFailure {
		t.Fatalf("reply code = %d, want %d", replyCode, serverFailure)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not finish out-of-order method handling in time")
	}
}

func TestTunnelModeServerSocks5NoAuthRejectsUnsupportedMethodOnly(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	defer func() { _ = clientSide.Close() }()

	task := newTestSocks5Task(1080, "mixProxy")
	server := NewTunnelModeServer(ProcessMix, failBridge{}, task)

	done := make(chan struct{})
	go func() {
		server.handleConn(serverSide)
		close(done)
	}()

	if _, err := clientSide.Write([]byte{5, 1, UserPassAuth}); err != nil {
		t.Fatalf("write greeting: %v", err)
	}
	negotiation := make([]byte, 2)
	if _, err := io.ReadFull(clientSide, negotiation); err != nil {
		t.Fatalf("read negotiation reply: %v", err)
	}
	if !bytes.Equal(negotiation, []byte{5, noAcceptableAuth}) {
		t.Fatalf("unexpected negotiation reply %v", negotiation)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not finish unsupported-method handling in time")
	}
}

func TestSocks5NeedsAuthWhenEitherBasicCredentialConfigured(t *testing.T) {
	cases := []struct {
		name string
		user string
		pass string
		want bool
	}{
		{name: "none", want: false},
		{name: "username-only", user: "demo", want: true},
		{name: "password-only", pass: "secret", want: true},
		{name: "both", user: "demo", pass: "secret", want: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task := newTestSocks5Task(1080, "mixProxy")
			task.Client.Cnf.U = tc.user
			task.Client.Cnf.P = tc.pass
			if got := socks5NeedsAuth(task); got != tc.want {
				t.Fatalf("socks5NeedsAuth() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTunnelModeServerSocks5BasicAuthAcceptsOnlyUserPassMethod(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	defer func() { _ = clientSide.Close() }()

	task := newTestSocks5Task(1080, "mixProxy")
	task.Client.Cnf.U = "demo"
	task.Client.Cnf.P = "secret"
	server := NewTunnelModeServer(ProcessMix, failBridge{}, task)

	done := make(chan struct{})
	go func() {
		server.handleConn(serverSide)
		close(done)
	}()

	if _, err := clientSide.Write([]byte{5, 1, UserPassAuth}); err != nil {
		t.Fatalf("write greeting: %v", err)
	}
	negotiation := make([]byte, 2)
	if _, err := io.ReadFull(clientSide, negotiation); err != nil {
		t.Fatalf("read negotiation reply: %v", err)
	}
	if !bytes.Equal(negotiation, []byte{5, UserPassAuth}) {
		t.Fatalf("unexpected negotiation reply %v", negotiation)
	}

	authPacket := []byte{userAuthVersion, 4, 'd', 'e', 'm', 'o', 6, 's', 'e', 'c', 'r', 'e', 't'}
	if _, err := clientSide.Write(authPacket); err != nil {
		t.Fatalf("write auth packet: %v", err)
	}
	authReply := make([]byte, 2)
	if _, err := io.ReadFull(clientSide, authReply); err != nil {
		t.Fatalf("read auth reply: %v", err)
	}
	if !bytes.Equal(authReply, []byte{userAuthVersion, authSuccess}) {
		t.Fatalf("unexpected auth reply %v", authReply)
	}

	if _, err := clientSide.Write([]byte{5, connectMethod, 0, ipV4, 8, 8, 8, 8, 0, 53}); err != nil {
		t.Fatalf("write connect request: %v", err)
	}
	if replyCode := readSocks5ReplyCode(t, clientSide); replyCode != serverFailure {
		t.Fatalf("reply code = %d, want %d", replyCode, serverFailure)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not finish user-pass-only auth handling in time")
	}
}

func TestTunnelModeServerSocks5BasicAuthRejectsZeroLengthCredentials(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	defer func() { _ = clientSide.Close() }()

	task := newTestSocks5Task(1080, "mixProxy")
	task.Client.Cnf.U = "demo"
	task.Client.Cnf.P = "secret"
	server := NewTunnelModeServer(ProcessMix, failBridge{}, task)

	done := make(chan struct{})
	go func() {
		server.handleConn(serverSide)
		close(done)
	}()

	if _, err := clientSide.Write([]byte{5, 1, UserPassAuth}); err != nil {
		t.Fatalf("write greeting: %v", err)
	}
	negotiation := make([]byte, 2)
	if _, err := io.ReadFull(clientSide, negotiation); err != nil {
		t.Fatalf("read negotiation reply: %v", err)
	}
	if !bytes.Equal(negotiation, []byte{5, UserPassAuth}) {
		t.Fatalf("unexpected negotiation reply %v", negotiation)
	}

	if _, err := clientSide.Write([]byte{userAuthVersion, 0, 0}); err != nil {
		t.Fatalf("write auth packet: %v", err)
	}
	authReply := make([]byte, 2)
	if _, err := io.ReadFull(clientSide, authReply); err != nil {
		t.Fatalf("read auth reply: %v", err)
	}
	if !bytes.Equal(authReply, []byte{userAuthVersion, authFailure}) {
		t.Fatalf("unexpected auth reply %v", authReply)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not finish zero-length credential handling in time")
	}
}

func TestTunnelModeServerSocks5HalfConfiguredBasicAuthRejectsNoAuth(t *testing.T) {
	cases := []struct {
		name string
		user string
		pass string
	}{
		{name: "username-only", user: "demo"},
		{name: "password-only", pass: "secret"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			serverSide, clientSide := net.Pipe()
			defer func() { _ = clientSide.Close() }()

			task := newTestSocks5Task(1080, "mixProxy")
			task.Client.Cnf.U = tc.user
			task.Client.Cnf.P = tc.pass
			server := NewTunnelModeServer(ProcessMix, failBridge{}, task)

			done := make(chan struct{})
			go func() {
				server.handleConn(serverSide)
				close(done)
			}()

			if _, err := clientSide.Write([]byte{5, 1, noAuthMethod}); err != nil {
				t.Fatalf("write greeting: %v", err)
			}
			negotiation := make([]byte, 2)
			if _, err := io.ReadFull(clientSide, negotiation); err != nil {
				t.Fatalf("read negotiation reply: %v", err)
			}
			if !bytes.Equal(negotiation, []byte{5, noAcceptableAuth}) {
				t.Fatalf("unexpected negotiation reply %v", negotiation)
			}

			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("server did not finish half-configured auth handling in time")
			}
		})
	}
}

func TestTunnelModeServerSocks5HalfConfiguredBasicAuthAcceptsUserPass(t *testing.T) {
	cases := []struct {
		name       string
		user       string
		pass       string
		authPacket []byte
	}{
		{
			name:       "username-only",
			user:       "demo",
			authPacket: []byte{userAuthVersion, 4, 'd', 'e', 'm', 'o', 0},
		},
		{
			name:       "password-only",
			pass:       "secret",
			authPacket: []byte{userAuthVersion, 0, 6, 's', 'e', 'c', 'r', 'e', 't'},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			serverSide, clientSide := net.Pipe()
			defer func() { _ = clientSide.Close() }()

			task := newTestSocks5Task(1080, "mixProxy")
			task.Client.Cnf.U = tc.user
			task.Client.Cnf.P = tc.pass
			server := NewTunnelModeServer(ProcessMix, failBridge{}, task)

			done := make(chan struct{})
			go func() {
				server.handleConn(serverSide)
				close(done)
			}()

			if _, err := clientSide.Write([]byte{5, 1, UserPassAuth}); err != nil {
				t.Fatalf("write greeting: %v", err)
			}
			negotiation := make([]byte, 2)
			if _, err := io.ReadFull(clientSide, negotiation); err != nil {
				t.Fatalf("read negotiation reply: %v", err)
			}
			if !bytes.Equal(negotiation, []byte{5, UserPassAuth}) {
				t.Fatalf("unexpected negotiation reply %v", negotiation)
			}

			if _, err := clientSide.Write(tc.authPacket); err != nil {
				t.Fatalf("write auth packet: %v", err)
			}
			authReply := make([]byte, 2)
			if _, err := io.ReadFull(clientSide, authReply); err != nil {
				t.Fatalf("read auth reply: %v", err)
			}
			if !bytes.Equal(authReply, []byte{userAuthVersion, authSuccess}) {
				t.Fatalf("unexpected auth reply %v", authReply)
			}

			if _, err := clientSide.Write([]byte{5, connectMethod, 0, ipV4, 8, 8, 8, 8, 0, 53}); err != nil {
				t.Fatalf("write connect request: %v", err)
			}
			if replyCode := readSocks5ReplyCode(t, clientSide); replyCode != serverFailure {
				t.Fatalf("reply code = %d, want %d", replyCode, serverFailure)
			}

			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("server did not finish half-configured auth connect handling in time")
			}
		})
	}
}

func TestTunnelModeServerSocks5ConnectMissingRemoteResultReturnsServerFailure(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	defer func() { _ = clientSide.Close() }()

	bridge := &connectResultBridge{
		peers: make(chan net.Conn, 1),
	}
	task := newTestSocks5Task(1080, "mixProxy")
	server := NewTunnelModeServer(ProcessMix, bridge, task)

	done := make(chan struct{})
	go func() {
		server.handleConn(serverSide)
		close(done)
	}()

	if _, err := clientSide.Write([]byte{5, 1, 0}); err != nil {
		t.Fatalf("write greeting: %v", err)
	}
	negotiation := make([]byte, 2)
	if _, err := io.ReadFull(clientSide, negotiation); err != nil {
		t.Fatalf("read negotiation reply: %v", err)
	}
	if !bytes.Equal(negotiation, []byte{5, 0}) {
		t.Fatalf("unexpected negotiation reply %v", negotiation)
	}

	if _, err := clientSide.Write([]byte{5, connectMethod, 0, ipV4, 8, 8, 8, 8, 0, 53}); err != nil {
		t.Fatalf("write connect request: %v", err)
	}

	var peerConn net.Conn
	select {
	case peerConn = <-bridge.peers:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for connect tunnel")
	}
	defer func() { _ = peerConn.Close() }()

	_ = peerConn.Close()

	if replyCode := readSocks5ReplyCode(t, clientSide); replyCode != serverFailure {
		t.Fatalf("reply code = %d, want %d", replyCode, serverFailure)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not finish missing connect result handling in time")
	}
}

type wrappedTimeoutNetError struct{}

func (wrappedTimeoutNetError) Error() string   { return "timeout" }
func (wrappedTimeoutNetError) Timeout() bool   { return true }
func (wrappedTimeoutNetError) Temporary() bool { return true }

type wrappedTimeoutControlConn struct{}

func (wrappedTimeoutControlConn) Read([]byte) (int, error) {
	return 0, fmt.Errorf("wrapped timeout: %w", wrappedTimeoutNetError{})
}

func (wrappedTimeoutControlConn) Write([]byte) (int, error) { return 0, io.EOF }
func (wrappedTimeoutControlConn) Close() error              { return nil }
func (wrappedTimeoutControlConn) LocalAddr() net.Addr       { return &net.TCPAddr{} }
func (wrappedTimeoutControlConn) RemoteAddr() net.Addr      { return &net.TCPAddr{} }
func (wrappedTimeoutControlConn) SetDeadline(time.Time) error {
	return nil
}
func (wrappedTimeoutControlConn) SetReadDeadline(time.Time) error {
	return nil
}
func (wrappedTimeoutControlConn) SetWriteDeadline(time.Time) error {
	return nil
}

func TestWaitForSocks5AssociateControlTreatsWrappedTimeoutAsPoll(t *testing.T) {
	session := &socks5UDPSession{
		done:     make(chan struct{}),
		packetCh: make(chan socks5UDPPacket),
	}
	session.Close()

	waitDone := make(chan struct{})
	go func() {
		defer close(waitDone)
		(&TunnelModeServer{}).waitForSocks5AssociateControl(wrappedTimeoutControlConn{}, session)
	}()

	select {
	case <-waitDone:
	case <-time.After(2 * time.Second):
		t.Fatal("waitForSocks5AssociateControl() should return after wrapped timeout when session is done")
	}
}

func TestWaitForSocks5AssociateControlWakesPromptlyWhenSessionEnds(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	defer func() { _ = clientSide.Close() }()
	defer func() { _ = serverSide.Close() }()

	done := make(chan struct{})
	session := &socks5UDPSession{
		done:     done,
		packetCh: make(chan socks5UDPPacket),
	}

	waitDone := make(chan struct{})
	go func() {
		defer close(waitDone)
		(&TunnelModeServer{}).waitForSocks5AssociateControl(serverSide, session)
	}()

	time.Sleep(20 * time.Millisecond)
	session.Close()

	select {
	case <-waitDone:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("waitForSocks5AssociateControl() should wake promptly after session.Done closes")
	}
}

func TestSocks5MetricsSnapshotTracksObservabilityEvents(t *testing.T) {
	resetSocks5Metrics()
	defer resetSocks5Metrics()

	logSocks5ConnectRemoteResultTimeout(nil, "example.com:443", errors.New("timeout"))
	logSocks5ConnectRemoteFailure(nil, "example.com:443", connectionRefused)
	logSocks5ConnectRemoteFailure(nil, "example.com:443", hostUnreachable)
	logSocks5AuthMethod(nil, "userpass", "selected")
	logSocks5AuthMethod(nil, "userpass", "accepted")
	logSocks5AuthMethod(nil, "userpass", "rejected")
	logSocks5AuthMethod(nil, "userpass", "failed")
	logSocks5AuthMethod(nil, "noauth", "selected")
	logSocks5AuthMethod(nil, "noauth", "rejected")
	logSocks5AuthMethod(nil, "custom", "unexpected")
	logSocks5UDPAmbiguousDrop(nil, "127.0.0.1", 53, 2, 1)

	snapshot := Socks5MetricsSnapshot()
	assertSocks5Metric(t, snapshot, "connect", "remote_result_timeout", 1)
	assertSocks5Metric(t, snapshot, "connect", "remote_refused", 1)
	assertSocks5Metric(t, snapshot, "connect", "remote_failed", 1)
	assertSocks5Metric(t, snapshot, "auth", "userpass_selected", 1)
	assertSocks5Metric(t, snapshot, "auth", "userpass_accepted", 1)
	assertSocks5Metric(t, snapshot, "auth", "userpass_rejected", 1)
	assertSocks5Metric(t, snapshot, "auth", "userpass_failed", 1)
	assertSocks5Metric(t, snapshot, "auth", "noauth_selected", 1)
	assertSocks5Metric(t, snapshot, "auth", "noauth_rejected", 1)
	assertSocks5Metric(t, snapshot, "auth", "other", 1)
	assertSocks5Metric(t, snapshot, "udp", "ambiguous_drop", 1)
}

func assertSocks5Metric(t *testing.T, snapshot map[string]interface{}, section, key string, want uint64) {
	t.Helper()

	group, ok := snapshot[section].(map[string]interface{})
	if !ok {
		t.Fatalf("snapshot[%q] type = %T, want map[string]interface{}", section, snapshot[section])
	}
	got, ok := group[key].(uint64)
	if !ok {
		t.Fatalf("snapshot[%q][%q] type = %T, want uint64", section, key, group[key])
	}
	if got != want {
		t.Fatalf("snapshot[%q][%q] = %d, want %d", section, key, got, want)
	}
}
