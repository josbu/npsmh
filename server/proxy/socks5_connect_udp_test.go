package proxy

import (
	"bytes"
	"io"
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
)

type testUDP5Bridge struct {
	peers chan net.Conn
}

func (b *testUDP5Bridge) SendLinkInfo(clientID int, link *conn.Link, t *file.Tunnel) (net.Conn, error) {
	serverSide, peerSide := net.Pipe()
	b.peers <- peerSide
	return serverSide, nil
}

func (b *testUDP5Bridge) IsServer() bool {
	return false
}

func (b *testUDP5Bridge) CliProcess(*conn.Conn, string) {}

type failBridge struct{}

func (failBridge) SendLinkInfo(clientID int, link *conn.Link, t *file.Tunnel) (net.Conn, error) {
	return nil, io.EOF
}

func (failBridge) IsServer() bool { return false }

func (failBridge) CliProcess(*conn.Conn, string) {}

type connectResultBridge struct {
	peers chan net.Conn
}

func (b *connectResultBridge) SendLinkInfo(clientID int, link *conn.Link, t *file.Tunnel) (net.Conn, error) {
	serverSide, peerSide := net.Pipe()
	b.peers <- peerSide
	return serverSide, nil
}

func (b *connectResultBridge) IsServer() bool { return false }

func (b *connectResultBridge) CliProcess(*conn.Conn, string) {}

type socks5CloseSpyConn struct {
	closed atomic.Bool
}

func (s *socks5CloseSpyConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (s *socks5CloseSpyConn) Write(b []byte) (int, error)      { return len(b), nil }
func (s *socks5CloseSpyConn) Close() error                     { s.closed.Store(true); return nil }
func (s *socks5CloseSpyConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (s *socks5CloseSpyConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (s *socks5CloseSpyConn) SetDeadline(time.Time) error      { return nil }
func (s *socks5CloseSpyConn) SetReadDeadline(time.Time) error  { return nil }
func (s *socks5CloseSpyConn) SetWriteDeadline(time.Time) error { return nil }

type socks5RegistryRouteBridgeStub struct {
	lastLink *conn.Link
}

type socks5TypedNilBridgeServerRoleStub struct{}

func (b *socks5RegistryRouteBridgeStub) SendLinkInfo(clientID int, link *conn.Link, t *file.Tunnel) (net.Conn, error) {
	b.lastLink = link
	serverSide, peerSide := net.Pipe()
	go func() {
		_ = peerSide.Close()
	}()
	return serverSide, nil
}

func (b *socks5RegistryRouteBridgeStub) IsServer() bool { return false }

func (b *socks5RegistryRouteBridgeStub) CliProcess(*conn.Conn, string) {}

func (s *socks5TypedNilBridgeServerRoleStub) IsServer() bool {
	panic("unexpected IsServer call on typed nil server role")
}

func newTestSocks5Task(port int, mode string) *file.Tunnel {
	task := &file.Tunnel{
		Port:     port,
		ServerIp: "127.0.0.1",
		Mode:     mode,
		Client: &file.Client{
			Id:   1,
			Cnf:  &file.Config{},
			Flow: &file.Flow{},
		},
		Flow:   &file.Flow{},
		Target: &file.Target{},
	}
	if mode == "mixProxy" {
		task.Socks5Proxy = true
	}
	return task
}

func TestSocks5UDPRegistryOpenBridgeLinkInheritsTaskRuntimeRouteUUID(t *testing.T) {
	bridge := &socks5RegistryRouteBridgeStub{}
	registry := &socks5UDPRegistry{linkOpener: bridge}
	task := newTestSocks5Task(1080, "mixProxy")
	task.BindRuntimeOwner("node-udp", &file.Tunnel{Target: &file.Target{TargetStr: "udp://owner"}})
	link := conn.NewLink("udp5", "", false, false, "203.0.113.60:8000", false)

	target, err := registry.OpenBridgeLink(task.Client.Id, link, task)
	if err != nil {
		t.Fatalf("OpenBridgeLink() error = %v", err)
	}
	if target == nil {
		t.Fatal("OpenBridgeLink() returned nil target")
	}
	_ = target.Close()
	if bridge.lastLink == nil {
		t.Fatal("OpenBridgeLink() did not pass a link to the bridge opener")
	}
	if bridge.lastLink.Option.RouteUUID != "node-udp" {
		t.Fatalf("OpenBridgeLink() route uuid = %q, want %q", bridge.lastLink.Option.RouteUUID, "node-udp")
	}
}

func TestSocks5UDPRegistryBridgeHelpersIgnoreTypedNilRuntimeValues(t *testing.T) {
	var opener *proxyTypedNilLinkOpenerStub
	var role *socks5TypedNilBridgeServerRoleStub
	registry := &socks5UDPRegistry{
		linkOpener: opener,
		serverRole: role,
	}

	if registry.BridgeIsServer() {
		t.Fatal("BridgeIsServer() = true, want false for typed nil server role")
	}

	target, err := registry.OpenBridgeLink(1, conn.NewLink("udp5", "", false, false, "", false), nil)
	if err != errProxyBridgeUnavailable {
		t.Fatalf("OpenBridgeLink() error = %v, want %v", err, errProxyBridgeUnavailable)
	}
	if target != nil {
		t.Fatalf("OpenBridgeLink() target = %#v, want nil", target)
	}
}

func startTestTunnelModeServer(t *testing.T, task *file.Tunnel, bridge NetBridge) (*TunnelModeServer, chan error) {
	t.Helper()

	server := NewTunnelModeServer(ProcessMix, bridge, task)
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Start()
	}()
	waitForTCP(t, net.JoinHostPort("127.0.0.1", strconv.Itoa(task.Port)))
	return server, errCh
}

func stopTestTunnelModeServer(t *testing.T, server *TunnelModeServer, errCh chan error) {
	t.Helper()

	_ = server.Close()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("server.Start() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop in time")
	}
}

func dialSocks5NoAuth(t *testing.T, port int) net.Conn {
	t.Helper()

	tcpConn, err := net.DialTimeout("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	if err != nil {
		t.Fatalf("DialTimeout() error = %v", err)
	}
	if _, err := tcpConn.Write([]byte{5, 1, 0}); err != nil {
		_ = tcpConn.Close()
		t.Fatalf("write greeting: %v", err)
	}
	negotiation := make([]byte, 2)
	if _, err := io.ReadFull(tcpConn, negotiation); err != nil {
		_ = tcpConn.Close()
		t.Fatalf("read negotiation reply: %v", err)
	}
	if !bytes.Equal(negotiation, []byte{5, 0}) {
		_ = tcpConn.Close()
		t.Fatalf("unexpected negotiation reply %v", negotiation)
	}
	return tcpConn
}

func dialSocks5ImplicitNoAuth(t *testing.T, port int) net.Conn {
	t.Helper()

	tcpConn, err := net.DialTimeout("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	if err != nil {
		t.Fatalf("DialTimeout() error = %v", err)
	}
	if _, err := tcpConn.Write([]byte{5, 0}); err != nil {
		_ = tcpConn.Close()
		t.Fatalf("write greeting: %v", err)
	}
	negotiation := make([]byte, 2)
	if _, err := io.ReadFull(tcpConn, negotiation); err != nil {
		_ = tcpConn.Close()
		t.Fatalf("read negotiation reply: %v", err)
	}
	if !bytes.Equal(negotiation, []byte{5, 0}) {
		_ = tcpConn.Close()
		t.Fatalf("unexpected negotiation reply %v", negotiation)
	}
	return tcpConn
}

func requestUDPAssociate(t *testing.T, c net.Conn) (string, int) {
	t.Helper()

	if _, err := c.Write([]byte{5, associateMethod, 0, ipV4, 0, 0, 0, 0, 0, 0}); err != nil {
		t.Fatalf("write udp associate: %v", err)
	}
	return readSocks5Reply(t, c)
}

func requestUDPAssociateFromIPv4(t *testing.T, c net.Conn, ip net.IP, port int) (string, int) {
	t.Helper()

	ip = ip.To4()
	if ip == nil {
		t.Fatal("requestUDPAssociateFromIPv4 requires IPv4 address")
	}
	req := []byte{5, associateMethod, 0, ipV4, ip[0], ip[1], ip[2], ip[3], byte(port >> 8), byte(port)}
	if _, err := c.Write(req); err != nil {
		t.Fatalf("write udp associate: %v", err)
	}
	return readSocks5Reply(t, c)
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer func() { _ = listener.Close() }()
	return listener.Addr().(*net.TCPAddr).Port
}

func waitForTCP(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp4", addr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("server %s did not start listening in time: %v", addr, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func readSocks5Reply(t *testing.T, c net.Conn) (string, int) {
	t.Helper()
	header := make([]byte, 4)
	if _, err := io.ReadFull(c, header); err != nil {
		t.Fatalf("read socks5 reply header: %v", err)
	}
	if header[0] != 5 || header[1] != succeeded {
		t.Fatalf("unexpected socks5 reply header %v", header)
	}

	var host string
	switch header[3] {
	case ipV4:
		addr := make([]byte, net.IPv4len)
		if _, err := io.ReadFull(c, addr); err != nil {
			t.Fatalf("read socks5 ipv4 addr: %v", err)
		}
		host = net.IP(addr).String()
	case ipV6:
		addr := make([]byte, net.IPv6len)
		if _, err := io.ReadFull(c, addr); err != nil {
			t.Fatalf("read socks5 ipv6 addr: %v", err)
		}
		host = net.IP(addr).String()
	default:
		t.Fatalf("unexpected atyp %d", header[3])
	}

	portBuf := make([]byte, 2)
	if _, err := io.ReadFull(c, portBuf); err != nil {
		t.Fatalf("read socks5 port: %v", err)
	}
	return host, int(portBuf[0])<<8 | int(portBuf[1])
}

func readSocks5ReplyCode(t *testing.T, c net.Conn) byte {
	t.Helper()
	header := make([]byte, 4)
	if _, err := io.ReadFull(c, header); err != nil {
		t.Fatalf("read socks5 reply header: %v", err)
	}
	replyCode := header[1]
	addrLen := 0
	switch header[3] {
	case ipV4:
		addrLen = net.IPv4len
	case ipV6:
		addrLen = net.IPv6len
	case domainName:
		var l [1]byte
		if _, err := io.ReadFull(c, l[:]); err != nil {
			t.Fatalf("read socks5 domain length: %v", err)
		}
		addrLen = int(l[0])
	default:
		t.Fatalf("unexpected atyp %d", header[3])
	}
	skip := make([]byte, addrLen+2)
	if _, err := io.ReadFull(c, skip); err != nil {
		t.Fatalf("read socks5 reply tail: %v", err)
	}
	return replyCode
}

func TestTunnelModeServerSocks5UDPUsesFixedPort(t *testing.T) {
	testTunnelModeServerSocks5UDPUsesFixedPort(t, "mixProxy")
}

func TestTunnelModeServerStandaloneSocks5UDPUsesFixedPort(t *testing.T) {
	testTunnelModeServerSocks5UDPUsesFixedPort(t, "socks5")
}

func testTunnelModeServerSocks5UDPUsesFixedPort(t *testing.T, mode string) {
	port := freeTCPPort(t)
	task := newTestSocks5Task(port, mode)
	bridge := &testUDP5Bridge{peers: make(chan net.Conn, 1)}
	server, errCh := startTestTunnelModeServer(t, task, bridge)
	defer stopTestTunnelModeServer(t, server, errCh)

	tcpConn := dialSocks5NoAuth(t, port)
	defer func() { _ = tcpConn.Close() }()

	replyHost, replyPort := requestUDPAssociate(t, tcpConn)
	if replyPort != port {
		t.Fatalf("udp associate port = %d, want fixed port %d", replyPort, port)
	}
	if replyHost == "" {
		t.Fatal("udp associate host should not be empty")
	}

	var peerConn net.Conn
	select {
	case peerConn = <-bridge.peers:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for udp5 tunnel")
	}
	defer func() { _ = peerConn.Close() }()
	peerFramed := conn.WrapFramed(peerConn)

	udpConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP() error = %v", err)
	}
	defer func() { _ = udpConn.Close() }()

	targetAddr, _ := net.ResolveUDPAddr("udp4", "8.8.8.8:53")
	var outbound bytes.Buffer
	if err := common.NewUDPDatagram(common.NewUDPHeader(0, 0, common.ToSocksAddr(targetAddr)), []byte("ping")).Write(&outbound); err != nil {
		t.Fatalf("build outbound udp datagram: %v", err)
	}
	if _, err := udpConn.WriteToUDP(outbound.Bytes(), &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}); err != nil {
		t.Fatalf("WriteToUDP() error = %v", err)
	}

	frameBuf := make([]byte, conn.MaxFramePayload)
	_ = peerConn.SetReadDeadline(time.Now().Add(time.Second))
	n, err := peerFramed.Read(frameBuf)
	if err != nil {
		t.Fatalf("read framed udp payload: %v", err)
	}
	tunneled, err := common.ReadUDPDatagram(bytes.NewReader(frameBuf[:n]))
	if err != nil {
		t.Fatalf("parse tunneled udp datagram: %v", err)
	}
	if tunneled.Header.Addr.String() != "8.8.8.8:53" {
		t.Fatalf("tunneled addr = %s, want 8.8.8.8:53", tunneled.Header.Addr.String())
	}
	if string(tunneled.Data) != "ping" {
		t.Fatalf("tunneled payload = %q, want ping", tunneled.Data)
	}

	replyAddr, _ := net.ResolveUDPAddr("udp4", "1.1.1.1:53")
	var inbound bytes.Buffer
	if err := common.NewUDPDatagram(common.NewUDPHeader(0, 0, common.ToSocksAddr(replyAddr)), []byte("pong")).Write(&inbound); err != nil {
		t.Fatalf("build inbound udp datagram: %v", err)
	}
	if _, err := peerFramed.Write(inbound.Bytes()); err != nil {
		t.Fatalf("write framed inbound udp payload: %v", err)
	}

	udpBuf := make([]byte, 2048)
	_ = udpConn.SetReadDeadline(time.Now().Add(time.Second))
	n, _, err = udpConn.ReadFromUDP(udpBuf)
	if err != nil {
		t.Fatalf("ReadFromUDP() error = %v", err)
	}
	replyDatagram, err := common.ReadUDPDatagram(bytes.NewReader(udpBuf[:n]))
	if err != nil {
		t.Fatalf("parse udp reply datagram: %v", err)
	}
	if replyDatagram.Header.Addr.String() != "1.1.1.1:53" {
		t.Fatalf("reply addr = %s, want 1.1.1.1:53", replyDatagram.Header.Addr.String())
	}
	if string(replyDatagram.Data) != "pong" {
		t.Fatalf("reply payload = %q, want pong", replyDatagram.Data)
	}

	_ = tcpConn.Close()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if server.socks5UDP != nil && server.socks5UDP.sessionCount() == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("socks5 udp session was not cleaned up after closing control connection")
}

func TestTunnelModeServerSocks5ConnectFailureReturnsReply(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	defer func() { _ = clientSide.Close() }()

	task := newTestSocks5Task(1080, "mixProxy")
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
	if replyCode := readSocks5ReplyCode(t, clientSide); replyCode != serverFailure {
		t.Fatalf("reply code = %d, want %d", replyCode, serverFailure)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not finish failed connect handling in time")
	}
}

func TestTunnelModeServerHandleSocks5ConnectOpenErrorClosesTargetAndReplies(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	defer func() { _ = clientSide.Close() }()

	target := &socks5CloseSpyConn{}
	request := resolvedSocks5ConnectRequest{
		task:       newTestSocks5Task(1080, "mixProxy"),
		targetAddr: "8.8.8.8:53",
	}

	done := make(chan struct{})
	go func() {
		(&TunnelModeServer{}).handleSocks5ConnectOpenError(serverSide, request, target, nil, errProxyAccessDenied)
		close(done)
	}()

	if replyCode := readSocks5ReplyCode(t, clientSide); replyCode != notAllowed {
		t.Fatalf("reply code = %d, want %d", replyCode, notAllowed)
	}
	if !target.closed.Load() {
		t.Fatal("handleSocks5ConnectOpenError() should close leaked target on open error")
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleSocks5ConnectOpenError() did not finish in time")
	}
}

func TestTunnelModeServerHandleSocks5ConnectOpenErrorClosesTargetWhenLinkMissing(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	defer func() { _ = clientSide.Close() }()

	target := &socks5CloseSpyConn{}
	request := resolvedSocks5ConnectRequest{
		task:       newTestSocks5Task(1080, "mixProxy"),
		targetAddr: "8.8.8.8:53",
	}

	done := make(chan struct{})
	go func() {
		(&TunnelModeServer{}).handleSocks5ConnectOpenError(serverSide, request, target, nil, nil)
		close(done)
	}()

	_ = clientSide.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
	var probe [1]byte
	if _, err := clientSide.Read(probe[:]); err != io.EOF {
		t.Fatalf("read after link-missing open error = %v, want EOF", err)
	}
	_ = clientSide.SetReadDeadline(time.Time{})
	if !target.closed.Load() {
		t.Fatal("handleSocks5ConnectOpenError() should close leaked target when link is missing")
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleSocks5ConnectOpenError() did not finish in time")
	}
}

func TestTunnelModeServerSocks5ConnectWaitsForRemoteDialResult(t *testing.T) {
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

	_ = clientSide.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	var probe [1]byte
	if _, err := clientSide.Read(probe[:]); err == nil {
		t.Fatal("socks5 connect reply arrived before remote dial result")
	} else if !conn.IsTimeout(err) {
		t.Fatalf("read before remote dial result error = %v, want timeout", err)
	}
	_ = clientSide.SetReadDeadline(time.Time{})

	go func() {
		_ = conn.WriteConnectResult(peerConn, conn.ConnectResultOK, time.Second)
	}()

	if replyCode := readSocks5ReplyCode(t, clientSide); replyCode != succeeded {
		t.Fatalf("reply code = %d, want %d", replyCode, succeeded)
	}

	_ = clientSide.Close()
	_ = peerConn.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not finish connect success handling in time")
	}
}

func TestTunnelModeServerSocks5ConnectRemoteFailuresReturnMappedReplies(t *testing.T) {
	tests := []struct {
		name      string
		status    conn.ConnectResultStatus
		wantReply uint8
	}{
		{name: "connection refused", status: conn.ConnectResultConnectionRefused, wantReply: connectionRefused},
		{name: "host unreachable", status: conn.ConnectResultHostUnreachable, wantReply: hostUnreachable},
		{name: "network unreachable", status: conn.ConnectResultNetworkUnreachable, wantReply: networkUnreachable},
		{name: "not allowed", status: conn.ConnectResultNotAllowed, wantReply: notAllowed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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

			go func() {
				_ = conn.WriteConnectResult(peerConn, tt.status, time.Second)
				_ = peerConn.Close()
			}()

			if replyCode := readSocks5ReplyCode(t, clientSide); replyCode != tt.wantReply {
				t.Fatalf("reply code = %d, want %d", replyCode, tt.wantReply)
			}

			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("server did not finish connect failure handling in time")
			}
		})
	}
}
