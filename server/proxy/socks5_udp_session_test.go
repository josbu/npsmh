package proxy

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
)

type socks5UDPRemoteConnStub struct {
	net.Conn
	remote net.Addr
}

func (c socks5UDPRemoteConnStub) RemoteAddr() net.Addr {
	return c.remote
}

func TestTunnelModeServerSocks5UDPAssociateAllowsSinglePendingPortMismatch(t *testing.T) {
	port := freeTCPPort(t)
	task := newTestSocks5Task(port, "mixProxy")
	bridge := &testUDP5Bridge{peers: make(chan net.Conn, 1)}
	server, errCh := startTestTunnelModeServer(t, task, bridge)
	defer stopTestTunnelModeServer(t, server, errCh)

	tcpConn := dialSocks5ImplicitNoAuth(t, port)
	defer func() { _ = tcpConn.Close() }()

	udpConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP() error = %v", err)
	}
	defer func() { _ = udpConn.Close() }()

	replyHost, replyPort := requestUDPAssociateFromIPv4(t, tcpConn, net.ParseIP("10.0.0.2"), 54321)
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
}

func TestSocks5UDPSessionControlRemoteAddrFallsBackToControl(t *testing.T) {
	session := &socks5UDPSession{
		control: socks5UDPRemoteConnStub{
			remote: &net.TCPAddr{IP: net.ParseIP("203.0.113.10"), Port: 4000},
		},
	}

	if got := session.controlRemoteAddr(); got != "203.0.113.10:4000" {
		t.Fatalf("controlRemoteAddr() = %q, want %q", got, "203.0.113.10:4000")
	}
}

func TestTunnelModeServerSocks5UDPBindsPendingSessionFromActualSource(t *testing.T) {
	port := freeTCPPort(t)
	task := newTestSocks5Task(port, "mixProxy")
	bridge := &testUDP5Bridge{peers: make(chan net.Conn, 2)}
	server, errCh := startTestTunnelModeServer(t, task, bridge)
	defer stopTestTunnelModeServer(t, server, errCh)

	tcpConn1 := dialSocks5ImplicitNoAuth(t, port)
	defer func() { _ = tcpConn1.Close() }()
	tcpConn2 := dialSocks5ImplicitNoAuth(t, port)
	defer func() { _ = tcpConn2.Close() }()

	udpConn1, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP() first socket error = %v", err)
	}
	defer func() { _ = udpConn1.Close() }()

	udpConn2, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP() second socket error = %v", err)
	}
	defer func() { _ = udpConn2.Close() }()

	_, replyPort1 := requestUDPAssociateFromIPv4(t, tcpConn1, net.ParseIP("127.0.0.1"), 54321)
	if replyPort1 != port {
		t.Fatalf("udp associate port = %d, want fixed port %d", replyPort1, port)
	}

	var peerConn1 net.Conn
	select {
	case peerConn1 = <-bridge.peers:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first udp5 tunnel")
	}
	defer func() { _ = peerConn1.Close() }()
	peerFramed1 := conn.WrapFramed(peerConn1)
	frameBuf1 := make([]byte, conn.MaxFramePayload)

	targetAddr1, _ := net.ResolveUDPAddr("udp4", "8.8.8.8:53")
	var outbound1 bytes.Buffer
	if err := common.NewUDPDatagram(common.NewUDPHeader(0, 0, common.ToSocksAddr(targetAddr1)), []byte("bind1")).Write(&outbound1); err != nil {
		t.Fatalf("build first outbound udp datagram: %v", err)
	}
	if _, err := udpConn1.WriteToUDP(outbound1.Bytes(), &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}); err != nil {
		t.Fatalf("WriteToUDP() first socket error = %v", err)
	}

	_ = peerConn1.SetReadDeadline(time.Now().Add(time.Second))
	n1, err := peerFramed1.Read(frameBuf1)
	if err != nil {
		t.Fatalf("read first framed udp payload: %v", err)
	}
	tunneled1, err := common.ReadUDPDatagram(bytes.NewReader(frameBuf1[:n1]))
	if err != nil {
		t.Fatalf("parse first tunneled udp datagram: %v", err)
	}
	if tunneled1.Header.Addr.String() != "8.8.8.8:53" || string(tunneled1.Data) != "bind1" {
		t.Fatalf("unexpected first tunneled payload addr=%s data=%q", tunneled1.Header.Addr.String(), tunneled1.Data)
	}

	_, replyPort2 := requestUDPAssociateFromIPv4(t, tcpConn2, net.ParseIP("127.0.0.1"), 1)
	if replyPort2 != port {
		t.Fatalf("udp associate port = %d, want fixed port %d", replyPort2, port)
	}

	var peerConn2 net.Conn
	select {
	case peerConn2 = <-bridge.peers:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for second udp5 tunnel")
	}
	defer func() { _ = peerConn2.Close() }()

	peerFramed2 := conn.WrapFramed(peerConn2)
	frameBuf2 := make([]byte, conn.MaxFramePayload)

	targetAddr2, _ := net.ResolveUDPAddr("udp4", "9.9.9.9:9999")
	var outbound2 bytes.Buffer
	if err := common.NewUDPDatagram(common.NewUDPHeader(0, 0, common.ToSocksAddr(targetAddr2)), []byte("bind2")).Write(&outbound2); err != nil {
		t.Fatalf("build second outbound udp datagram: %v", err)
	}
	if _, err := udpConn2.WriteToUDP(outbound2.Bytes(), &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}); err != nil {
		t.Fatalf("WriteToUDP() second socket error = %v", err)
	}

	_ = peerConn1.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
	if _, err := peerFramed1.Read(frameBuf1); err == nil {
		t.Fatal("second source packet should not be routed to first tunnel")
	}

	_ = peerConn2.SetReadDeadline(time.Now().Add(time.Second))
	n2, err := peerFramed2.Read(frameBuf2)
	if err != nil {
		t.Fatalf("read second framed udp payload: %v", err)
	}
	tunneled2, err := common.ReadUDPDatagram(bytes.NewReader(frameBuf2[:n2]))
	if err != nil {
		t.Fatalf("parse second tunneled udp datagram: %v", err)
	}
	if tunneled2.Header.Addr.String() != "9.9.9.9:9999" || string(tunneled2.Data) != "bind2" {
		t.Fatalf("unexpected second tunneled payload addr=%s data=%q", tunneled2.Header.Addr.String(), tunneled2.Data)
	}
}

func TestTunnelModeServerSocks5UDPAmbiguousPendingSessionsDropDatagram(t *testing.T) {
	port := freeTCPPort(t)
	task := newTestSocks5Task(port, "mixProxy")
	bridge := &testUDP5Bridge{peers: make(chan net.Conn, 2)}
	server, errCh := startTestTunnelModeServer(t, task, bridge)
	defer stopTestTunnelModeServer(t, server, errCh)

	tcpConn1 := dialSocks5ImplicitNoAuth(t, port)
	defer func() { _ = tcpConn1.Close() }()
	tcpConn2 := dialSocks5ImplicitNoAuth(t, port)
	defer func() { _ = tcpConn2.Close() }()

	udpConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP() error = %v", err)
	}
	defer func() { _ = udpConn.Close() }()

	_, replyPort1 := requestUDPAssociateFromIPv4(t, tcpConn1, net.ParseIP("127.0.0.1"), 11111)
	_, replyPort2 := requestUDPAssociateFromIPv4(t, tcpConn2, net.ParseIP("127.0.0.1"), 22222)
	if replyPort1 != port || replyPort2 != port {
		t.Fatalf("udp associate ports = (%d, %d), want fixed port %d", replyPort1, replyPort2, port)
	}

	var peerConn1, peerConn2 net.Conn
	select {
	case peerConn1 = <-bridge.peers:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first udp5 tunnel")
	}
	select {
	case peerConn2 = <-bridge.peers:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for second udp5 tunnel")
	}
	defer func() { _ = peerConn1.Close() }()
	defer func() { _ = peerConn2.Close() }()

	peerFramed1 := conn.WrapFramed(peerConn1)
	peerFramed2 := conn.WrapFramed(peerConn2)
	frameBuf1 := make([]byte, conn.MaxFramePayload)
	frameBuf2 := make([]byte, conn.MaxFramePayload)

	targetAddr, _ := net.ResolveUDPAddr("udp4", "9.9.9.9:9999")
	var outbound bytes.Buffer
	if err := common.NewUDPDatagram(common.NewUDPHeader(0, 0, common.ToSocksAddr(targetAddr)), []byte("pending")).Write(&outbound); err != nil {
		t.Fatalf("build outbound udp datagram: %v", err)
	}
	if _, err := udpConn.WriteToUDP(outbound.Bytes(), &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}); err != nil {
		t.Fatalf("WriteToUDP() error = %v", err)
	}

	_ = peerConn1.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
	if _, err := peerFramed1.Read(frameBuf1); err == nil {
		t.Fatal("ambiguous pending datagram should not be routed to first tunnel")
	}
	_ = peerConn2.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
	if _, err := peerFramed2.Read(frameBuf2); err == nil {
		t.Fatal("ambiguous pending datagram should not be routed to second tunnel")
	}
}

func TestTunnelModeServerSocks5UDPRejectsSourceRebind(t *testing.T) {
	port := freeTCPPort(t)
	task := newTestSocks5Task(port, "mixProxy")
	bridge := &testUDP5Bridge{peers: make(chan net.Conn, 1)}
	server, errCh := startTestTunnelModeServer(t, task, bridge)
	defer stopTestTunnelModeServer(t, server, errCh)

	tcpConn := dialSocks5ImplicitNoAuth(t, port)
	defer func() { _ = tcpConn.Close() }()

	udpConn1, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP() first socket error = %v", err)
	}
	defer func() { _ = udpConn1.Close() }()

	udpConn1b, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP() rebound socket error = %v", err)
	}
	defer func() { _ = udpConn1b.Close() }()

	udpConn2, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP() second socket error = %v", err)
	}
	defer func() { _ = udpConn2.Close() }()

	_, replyPort := requestUDPAssociateFromIPv4(t, tcpConn, net.ParseIP("127.0.0.1"), udpConn1b.LocalAddr().(*net.UDPAddr).Port)
	if replyPort != port {
		t.Fatalf("udp associate port = %d, want fixed port %d", replyPort, port)
	}

	var peerConn net.Conn
	select {
	case peerConn = <-bridge.peers:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for udp5 tunnel")
	}
	defer func() { _ = peerConn.Close() }()

	peerFramed := conn.WrapFramed(peerConn)
	frameBuf := make([]byte, conn.MaxFramePayload)

	targetAddr1, _ := net.ResolveUDPAddr("udp4", "8.8.8.8:53")
	var outbound1 bytes.Buffer
	if err := common.NewUDPDatagram(common.NewUDPHeader(0, 0, common.ToSocksAddr(targetAddr1)), []byte("bind1")).Write(&outbound1); err != nil {
		t.Fatalf("build first outbound udp datagram: %v", err)
	}
	if _, err := udpConn1.WriteToUDP(outbound1.Bytes(), &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}); err != nil {
		t.Fatalf("WriteToUDP() first socket error = %v", err)
	}
	_ = peerConn.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := peerFramed.Read(frameBuf); err != nil {
		t.Fatalf("read first framed udp payload: %v", err)
	}

	targetAddrRebind, _ := net.ResolveUDPAddr("udp4", "1.1.1.1:53")
	var outboundRebind bytes.Buffer
	if err := common.NewUDPDatagram(common.NewUDPHeader(0, 0, common.ToSocksAddr(targetAddrRebind)), []byte("rebind")).Write(&outboundRebind); err != nil {
		t.Fatalf("build rebound outbound udp datagram: %v", err)
	}
	if _, err := udpConn1b.WriteToUDP(outboundRebind.Bytes(), &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}); err != nil {
		t.Fatalf("WriteToUDP() rebound socket error = %v", err)
	}

	_ = peerConn.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
	if _, err := peerFramed.Read(frameBuf); err == nil {
		t.Fatal("rebound source should not be routed once a session is bound")
	}
}

func TestSocks5UDPSessionWriteToTunnelUsesBoundNodeRuntimeWhenAvailable(t *testing.T) {
	client := &file.Client{
		Id:   7,
		Cnf:  &file.Config{},
		Flow: &file.Flow{},
	}
	file.InitializeClientRuntime(client)
	task := &file.Tunnel{
		Id:     9,
		Client: client,
		Flow:   &file.Flow{},
		Target: &file.Target{TargetStr: "8.8.8.8:53"},
	}
	file.InitializeTunnelRuntime(task)

	targetAddr, _ := net.ResolveUDPAddr("udp4", "8.8.8.8:53")
	edgePacket, err := marshalSocks5UDPEdgePacket(common.NewUDPDatagram(common.NewUDPHeader(0, 0, common.ToSocksAddr(targetAddr)), []byte("ping")))
	if err != nil {
		t.Fatalf("marshalSocks5UDPEdgePacket() error = %v", err)
	}

	packetBuf := common.BufPoolUdp.Get()
	copy(packetBuf, edgePacket)

	collector := &proxyRouteRuntimeCollectorStub{}
	boundNode := &proxyBoundNodeRuntimeStub{}
	session := &socks5UDPSession{
		registry:   &socks5UDPRegistry{task: task},
		framed:     conn.WrapFramed(&recordingConn{}),
		routeStats: collector,
		boundNode:  boundNode,
		routeUUID:  "node-a",
		packetCh:   make(chan socks5UDPPacket, 1),
		done:       make(chan struct{}),
	}

	session.wg.Add(1)
	go session.writeToTunnelLoop()
	session.packetCh <- socks5UDPPacket{buf: packetBuf, n: len(edgePacket)}
	close(session.packetCh)
	session.wg.Wait()

	if collector.serviceTotals != [2]int64{} {
		t.Fatalf("collector service totals = %#v, want zero when bound node runtime exists", collector.serviceTotals)
	}
	if boundNode.serviceTotals != [2]int64{int64(len(edgePacket)), 0} {
		t.Fatalf("bound node service totals = %#v, want %#v", boundNode.serviceTotals, [2]int64{int64(len(edgePacket)), 0})
	}
	in, out, total := task.ServiceTrafficTotals()
	if in != int64(len(edgePacket)) || out != 0 || total != int64(len(edgePacket)) {
		t.Fatalf("task service traffic = (%d, %d, %d), want (%d, 0, %d)", in, out, total, len(edgePacket), len(edgePacket))
	}
}

func TestSocks5UDPSessionReadFromTunnelUsesBoundNodeRuntimeWhenAvailable(t *testing.T) {
	client := &file.Client{
		Id:   7,
		Cnf:  &file.Config{},
		Flow: &file.Flow{},
	}
	file.InitializeClientRuntime(client)
	task := &file.Tunnel{
		Id:     9,
		Client: client,
		Flow:   &file.Flow{},
		Target: &file.Target{TargetStr: "8.8.8.8:53"},
	}
	file.InitializeTunnelRuntime(task)

	listener, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP(listener) error = %v", err)
	}
	defer func() { _ = listener.Close() }()

	clientConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP(client) error = %v", err)
	}
	defer func() { _ = clientConn.Close() }()

	serverSide, peerSide := net.Pipe()
	defer func() { _ = peerSide.Close() }()

	collector := &proxyRouteRuntimeCollectorStub{}
	boundNode := &proxyBoundNodeRuntimeStub{}
	session := &socks5UDPSession{
		registry: &socks5UDPRegistry{
			task:           task,
			listener:       listener,
			sessions:       make(map[*socks5UDPSession]struct{}),
			sessionsByAddr: make(map[string]*socks5UDPSession),
			sessionsByIP:   make(map[string]map[*socks5UDPSession]struct{}),
			pendingByIP:    make(map[string][]*socks5UDPSession),
		},
		framed:     conn.WrapFramed(serverSide),
		routeStats: collector,
		boundNode:  boundNode,
		routeUUID:  "node-a",
		packetCh:   make(chan socks5UDPPacket),
		done:       make(chan struct{}),
	}
	session.bindClientAddr(clientConn.LocalAddr().(*net.UDPAddr))

	targetAddr, _ := net.ResolveUDPAddr("udp4", "9.9.9.9:9999")
	datagram := common.NewUDPDatagram(common.NewUDPHeader(0, 0, common.ToSocksAddr(targetAddr)), []byte("pong"))
	var payload bytes.Buffer
	if err := datagram.Write(&payload); err != nil {
		t.Fatalf("datagram.Write() error = %v", err)
	}
	expectedEdge, err := marshalSocks5UDPEdgePacket(datagram)
	if err != nil {
		t.Fatalf("marshalSocks5UDPEdgePacket() error = %v", err)
	}

	session.wg.Add(1)
	go session.readFromTunnelLoop()

	peerFramed := conn.WrapFramed(peerSide)
	if _, err := peerFramed.Write(payload.Bytes()); err != nil {
		t.Fatalf("peerFramed.Write() error = %v", err)
	}
	_ = peerSide.Close()

	udpBuf := make([]byte, 2048)
	_ = clientConn.SetReadDeadline(time.Now().Add(time.Second))
	n, _, err := clientConn.ReadFromUDP(udpBuf)
	if err != nil {
		t.Fatalf("ReadFromUDP() error = %v", err)
	}
	if n != len(expectedEdge) {
		t.Fatalf("client udp payload length = %d, want %d", n, len(expectedEdge))
	}

	session.wg.Wait()

	if collector.serviceTotals != [2]int64{} {
		t.Fatalf("collector service totals = %#v, want zero when bound node runtime exists", collector.serviceTotals)
	}
	if boundNode.serviceTotals != [2]int64{0, int64(len(expectedEdge))} {
		t.Fatalf("bound node service totals = %#v, want %#v", boundNode.serviceTotals, [2]int64{0, int64(len(expectedEdge))})
	}
	in, out, total := task.ServiceTrafficTotals()
	if in != 0 || out != int64(len(expectedEdge)) || total != int64(len(expectedEdge)) {
		t.Fatalf("task service traffic = (%d, %d, %d), want (0, %d, %d)", in, out, total, len(expectedEdge), len(expectedEdge))
	}
}

func TestTunnelModeServerSocks5UDPInboundIgnoresReservedFieldLengthExtension(t *testing.T) {
	port := freeTCPPort(t)
	task := newTestSocks5Task(port, "mixProxy")
	bridge := &testUDP5Bridge{peers: make(chan net.Conn, 1)}
	server, errCh := startTestTunnelModeServer(t, task, bridge)
	defer stopTestTunnelModeServer(t, server, errCh)

	tcpConn := dialSocks5ImplicitNoAuth(t, port)
	defer func() { _ = tcpConn.Close() }()

	_, replyPort := requestUDPAssociate(t, tcpConn)
	if replyPort != port {
		t.Fatalf("udp associate port = %d, want fixed port %d", replyPort, port)
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
	if err := common.NewUDPDatagram(common.NewUDPHeader(7, 0, common.ToSocksAddr(targetAddr)), []byte("ping")).Write(&outbound); err != nil {
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
	if tunneled.Header.Rsv != 0 {
		t.Fatalf("tunneled rsv = %d, want 0", tunneled.Header.Rsv)
	}
	if tunneled.Header.Addr.String() != "8.8.8.8:53" {
		t.Fatalf("tunneled addr = %s, want 8.8.8.8:53", tunneled.Header.Addr.String())
	}
	if string(tunneled.Data) != "ping" {
		t.Fatalf("tunneled payload = %q, want ping", tunneled.Data)
	}
}

func TestTunnelModeServerSocks5UDPIdleSessionExpires(t *testing.T) {
	oldIdleTimeout := socks5UDPAssociateIdleTimeout
	oldSweepInterval := socks5UDPSweepInterval
	socks5UDPAssociateIdleTimeout = 200 * time.Millisecond
	socks5UDPSweepInterval = 50 * time.Millisecond
	defer func() {
		socks5UDPAssociateIdleTimeout = oldIdleTimeout
		socks5UDPSweepInterval = oldSweepInterval
	}()

	port := freeTCPPort(t)
	task := newTestSocks5Task(port, "mixProxy")
	bridge := &testUDP5Bridge{peers: make(chan net.Conn, 1)}
	server, errCh := startTestTunnelModeServer(t, task, bridge)
	defer stopTestTunnelModeServer(t, server, errCh)

	tcpConn := dialSocks5NoAuth(t, port)
	defer func() { _ = tcpConn.Close() }()

	_, replyPort := requestUDPAssociate(t, tcpConn)
	if replyPort != port {
		t.Fatalf("udp associate port = %d, want fixed port %d", replyPort, port)
	}

	select {
	case peerConn := <-bridge.peers:
		defer func() { _ = peerConn.Close() }()
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for udp5 tunnel")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if server.socks5UDP != nil && server.socks5UDP.sessionCount() == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("idle socks5 udp session was not cleaned up in time")
}

func TestParseSocks5UDPEdgePacketRejectsInvalidInputs(t *testing.T) {
	tests := []struct {
		name   string
		packet []byte
		want   error
	}{
		{
			name:   "short header",
			packet: []byte{0x00, 0x00, 0x00},
			want:   errSocks5UDPShortPacket,
		},
		{
			name:   "fragment not supported",
			packet: []byte{0x00, 0x00, 0x01, ipV4, 127, 0, 0, 1, 0, 53},
			want:   errSocks5UDPFragmentNotSupported,
		},
		{
			name:   "unsupported atyp",
			packet: []byte{0x00, 0x00, 0x00, 0x09, 0x00, 0x35},
			want:   errSocks5UDPUnsupportedAddrType,
		},
		{
			name:   "truncated ipv4",
			packet: []byte{0x00, 0x00, 0x00, ipV4, 127, 0, 0},
			want:   errSocks5UDPShortPacket,
		},
		{
			name:   "truncated domain length payload",
			packet: []byte{0x00, 0x00, 0x00, domainName, 0x03, 'a', 'b', 0x00},
			want:   errSocks5UDPShortPacket,
		},
		{
			name:   "zero-length domain",
			packet: []byte{0x00, 0x00, 0x00, domainName, 0x00, 0x00, 0x35},
			want:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			datagram, err := parseSocks5UDPEdgePacket(tt.packet)
			if tt.want == nil {
				if err != nil {
					t.Fatalf("parseSocks5UDPEdgePacket() error = %v, want nil", err)
				}
				if datagram == nil || datagram.Header == nil || datagram.Header.Addr == nil {
					t.Fatal("parseSocks5UDPEdgePacket() returned nil datagram")
				}
				if datagram.Header.Addr.Host != "" {
					t.Fatalf("datagram host = %q, want empty domain host", datagram.Header.Addr.Host)
				}
				if datagram.Header.Addr.Port != 53 {
					t.Fatalf("datagram port = %d, want 53", datagram.Header.Addr.Port)
				}
				return
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("parseSocks5UDPEdgePacket() error = %v, want %v", err, tt.want)
			}
		})
	}
}

type socks5UDPSourcePolicyStub struct {
	called bool
	denied bool
}

func (s *socks5UDPSourcePolicyStub) IsClientSourceAccessDenied(*file.Client, *file.Tunnel, string) bool {
	s.called = true
	return s.denied
}

func TestSocks5UDPRegistryShouldDropClientDatagramUsesInjectedPolicy(t *testing.T) {
	task := &file.Tunnel{
		Id:     8,
		Client: &file.Client{Id: 7, Flow: &file.Flow{}, Cnf: &file.Config{}},
		Flow:   &file.Flow{},
	}
	policy := &socks5UDPSourcePolicyStub{denied: true}
	registry := &socks5UDPRegistry{
		task:       task,
		serverRole: bridgeServerRoleStub{isServer: true},
		policyRoot: func() socks5UDPSourcePolicy { return policy },
	}

	drop := registry.shouldDropClientDatagram(&net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 12345})
	if !policy.called {
		t.Fatal("shouldDropClientDatagram() should call injected policy")
	}
	if !drop {
		t.Fatal("shouldDropClientDatagram() = false, want true")
	}
}

func TestNewSocks5UDPRegistryWithPolicyUsesInjectedPolicy(t *testing.T) {
	task := newTestSocks5Task(1081, "mixProxy")
	policy := &socks5UDPSourcePolicyStub{denied: true}

	registry, err := newSocks5UDPRegistryWithPolicy(nil, bridgeServerRoleStub{isServer: false}, policy, task, false, "127.0.0.1:0", nil, nil)
	if err != nil {
		t.Fatalf("newSocks5UDPRegistryWithPolicy() error = %v", err)
	}
	defer func() { _ = registry.Close() }()

	if registry.policyRoot == nil || registry.policyRoot() != policy {
		t.Fatalf("registry.policyRoot() = %#v, want injected policy %#v", registry.policyRoot(), policy)
	}
}

func TestNewSocks5UDPRegistryWithPolicyRootUsesCurrentPolicy(t *testing.T) {
	task := newTestSocks5Task(1081, "mixProxy")
	policy := &socks5UDPSourcePolicyStub{denied: false}

	registry, err := newSocks5UDPRegistryWithPolicyRoot(nil, bridgeServerRoleStub{isServer: true}, func() socks5UDPSourcePolicy { return policy }, task, false, "127.0.0.1:0", nil, nil)
	if err != nil {
		t.Fatalf("newSocks5UDPRegistryWithPolicyRoot() error = %v", err)
	}
	defer func() { _ = registry.Close() }()

	addr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 12345}
	if registry.shouldDropClientDatagram(addr) {
		t.Fatal("shouldDropClientDatagram() = true, want false from initial policy")
	}

	policy = &socks5UDPSourcePolicyStub{denied: true}
	if !registry.shouldDropClientDatagram(addr) {
		t.Fatal("shouldDropClientDatagram() = false, want true after provider policy change")
	}
}

func TestIsClosedPacketConnErrorOnWrappedClosedError(t *testing.T) {
	err := fmt.Errorf("wrapped packet close: %w", net.ErrClosed)
	if !isClosedPacketConnError(err) {
		t.Fatalf("isClosedPacketConnError(%v) = false, want true", err)
	}
	if isClosedPacketConnError(errors.New("other")) {
		t.Fatal("isClosedPacketConnError(other) = true, want false")
	}
}

func TestSocks5UDPRegistryNewSessionRejectsAfterClose(t *testing.T) {
	task := newTestSocks5Task(1080, "mixProxy")
	bridge := &testUDP5Bridge{peers: make(chan net.Conn, 1)}
	registry, err := newSocks5UDPRegistry(bridge, bridgeServerRoleStub{isServer: false}, task, false, "127.0.0.1:0", nil, nil)
	if err != nil {
		t.Fatalf("newSocks5UDPRegistry() error = %v", err)
	}
	defer func() { _ = registry.Close() }()

	if err := registry.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer func() { _ = listener.Close() }()

	controlCh := make(chan net.Conn, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			controlCh <- conn
		}
	}()

	clientConn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer func() { _ = clientConn.Close() }()

	controlConn := <-controlCh
	defer func() { _ = controlConn.Close() }()

	session, err := registry.newSession(controlConn)
	if !errors.Is(err, net.ErrClosed) {
		if session != nil {
			session.Close()
		}
		t.Fatalf("newSession() error = %v, want %v", err, net.ErrClosed)
	}
	if session != nil {
		session.Close()
		t.Fatal("newSession() returned a session after registry close")
	}
	if got := registry.sessionCount(); got != 0 {
		t.Fatalf("sessionCount() = %d, want 0 after rejected late session", got)
	}

	select {
	case peer := <-bridge.peers:
		_ = peer.Close()
		t.Fatal("newSession() opened a late tunnel after registry close")
	default:
	}
}

func TestSocks5UDPRegistryCollectIdleSessionsUsesSessionSnapshot(t *testing.T) {
	now := time.Now()
	idle := &socks5UDPSession{done: make(chan struct{})}
	idle.lastActiveNS.Store(now.Add(-2 * time.Minute).UnixNano())
	active := &socks5UDPSession{done: make(chan struct{})}
	active.lastActiveNS.Store(now.Add(-5 * time.Second).UnixNano())

	registry := &socks5UDPRegistry{
		sessions: map[*socks5UDPSession]struct{}{
			idle:   {},
			active: {},
		},
	}

	idleSessions := registry.collectIdleSessions(now, time.Minute)
	if len(idleSessions) != 1 || idleSessions[0] != idle {
		t.Fatalf("collectIdleSessions() = %#v, want only idle session %#v", idleSessions, idle)
	}

	delete(registry.sessions, idle)
	if len(idleSessions) != 1 || idleSessions[0] != idle {
		t.Fatalf("collectIdleSessions() should return detached snapshot, got %#v", idleSessions)
	}
}

func TestSocks5UDPRegistryPickSessionPrefersBoundSessionFastPath(t *testing.T) {
	addr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 32000}
	bound := &socks5UDPSession{
		clientIPKey: normalizeUDPIPKey(addr.IP),
		done:        make(chan struct{}),
	}
	bound.bindClientAddr(addr)
	pending := &socks5UDPSession{
		clientIPKey: normalizeUDPIPKey(addr.IP),
		done:        make(chan struct{}),
	}
	registry := &socks5UDPRegistry{
		sessions: map[*socks5UDPSession]struct{}{
			bound:   {},
			pending: {},
		},
		sessionsByAddr: map[string]*socks5UDPSession{
			addr.String(): bound,
		},
		sessionsByIP: map[string]map[*socks5UDPSession]struct{}{
			bound.clientIPKey: {
				bound:   {},
				pending: {},
			},
		},
		pendingByIP: map[string][]*socks5UDPSession{
			bound.clientIPKey: {pending},
		},
	}

	if got := registry.pickSession(addr); got != bound {
		t.Fatalf("pickSession() = %#v, want bound session %#v", got, bound)
	}
	if got := len(registry.pendingByIP[bound.clientIPKey]); got != 1 {
		t.Fatalf("pickSession() should not touch pending list on bound fast path, got %d pending entries", got)
	}
}

type bridgeServerRoleStub struct {
	isServer bool
}

func (s bridgeServerRoleStub) IsServer() bool { return s.isServer }
