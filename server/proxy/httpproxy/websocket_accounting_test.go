package httpproxy

import (
	"bufio"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	serverproxy "github.com/djylb/nps/server/proxy"
)

type websocketBufferedIngressBridgeStub struct {
	serviceClientID int
	serviceUUID     string
	serviceIn       int64
	serviceOut      int64
}

func (s *websocketBufferedIngressBridgeStub) SendLinkInfo(int, *conn.Link, *file.Tunnel) (net.Conn, error) {
	return nil, nil
}

func (s *websocketBufferedIngressBridgeStub) IsServer() bool { return false }

func (s *websocketBufferedIngressBridgeStub) CliProcess(*conn.Conn, string) {}

func (s *websocketBufferedIngressBridgeStub) AddClientNodeConn(int, string) {}

func (s *websocketBufferedIngressBridgeStub) CutClientNodeConn(int, string) {}

func (s *websocketBufferedIngressBridgeStub) ObserveClientNodeBridgeTraffic(int, string, int64, int64) {
}

func (s *websocketBufferedIngressBridgeStub) ObserveClientNodeServiceTraffic(clientID int, uuid string, in, out int64) {
	s.serviceClientID = clientID
	s.serviceUUID = uuid
	s.serviceIn += in
	s.serviceOut += out
}

type websocketBufferedIngressLimiter struct {
	got int64
}

func (l *websocketBufferedIngressLimiter) Get(size int64) {
	l.got += size
}

func (l *websocketBufferedIngressLimiter) ReturnBucket(int64) {}

func TestObserveWebsocketBufferedClientIngressCountsServiceTrafficAndLimiter(t *testing.T) {
	bridge := &websocketBufferedIngressBridgeStub{}
	server := &HttpServer{
		HttpProxy: &HttpProxy{
			BaseServer: serverproxy.NewBaseServer(bridge, nil),
		},
	}
	host := &file.Host{
		Id:   1,
		Flow: &file.Flow{},
		Client: &file.Client{
			Id:   22,
			Cnf:  &file.Config{},
			Flow: &file.Flow{},
		},
	}
	routeRuntime := server.NewRouteRuntimeContext(host.Client, "node-ws")
	limiter := &websocketBufferedIngressLimiter{}

	if err := observeWebsocketBufferedClientIngress(routeRuntime, host, limiter, []byte("hello")); err != nil {
		t.Fatalf("observeWebsocketBufferedClientIngress() error = %v", err)
	}

	if limiter.got != 5 {
		t.Fatalf("limiter got = %d, want 5", limiter.got)
	}
	if bridge.serviceClientID != host.Client.Id {
		t.Fatalf("collector client id = %d, want %d", bridge.serviceClientID, host.Client.Id)
	}
	if bridge.serviceUUID != "node-ws" {
		t.Fatalf("collector route uuid = %q, want %q", bridge.serviceUUID, "node-ws")
	}
	if bridge.serviceIn != 5 || bridge.serviceOut != 0 {
		t.Fatalf("collector service traffic = %d/%d, want 5/0", bridge.serviceIn, bridge.serviceOut)
	}
	if in, out, total := host.ServiceTrafficTotals(); in != 5 || out != 0 || total != 5 {
		t.Fatalf("host service totals = %d/%d/%d, want 5/0/5", in, out, total)
	}
	if in, out, total := host.Client.ServiceTrafficTotals(); in != 5 || out != 0 || total != 5 {
		t.Fatalf("client service totals = %d/%d/%d, want 5/0/5", in, out, total)
	}
}

func TestObserveWebsocketBufferedClientIngressSkipsEmptyBuffer(t *testing.T) {
	bridge := &websocketBufferedIngressBridgeStub{}
	server := &HttpServer{
		HttpProxy: &HttpProxy{
			BaseServer: serverproxy.NewBaseServer(bridge, nil),
		},
	}
	host := &file.Host{
		Id:   2,
		Flow: &file.Flow{},
		Client: &file.Client{
			Id:   23,
			Cnf:  &file.Config{},
			Flow: &file.Flow{},
		},
	}
	routeRuntime := server.NewRouteRuntimeContext(host.Client, "node-ws-empty")
	limiter := &websocketBufferedIngressLimiter{}

	if err := observeWebsocketBufferedClientIngress(routeRuntime, host, limiter, nil); err != nil {
		t.Fatalf("observeWebsocketBufferedClientIngress(nil) error = %v", err)
	}

	if limiter.got != 0 {
		t.Fatalf("limiter got = %d, want 0", limiter.got)
	}
	if bridge.serviceIn != 0 || bridge.serviceOut != 0 {
		t.Fatalf("collector service traffic = %d/%d, want 0/0", bridge.serviceIn, bridge.serviceOut)
	}
	if in, out, total := host.ServiceTrafficTotals(); in != 0 || out != 0 || total != 0 {
		t.Fatalf("host service totals = %d/%d/%d, want 0/0/0", in, out, total)
	}
}

func TestAttachBufferedWebsocketBackendDataReturnsOriginalConnWithoutBufferedBytes(t *testing.T) {
	clientConn, peerConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()
	defer func() { _ = peerConn.Close() }()

	reader := bufio.NewReader(strings.NewReader(""))
	got, err := attachBufferedWebsocketBackendData(reader, clientConn)
	if err != nil {
		t.Fatalf("attachBufferedWebsocketBackendData() error = %v", err)
	}
	if got != clientConn {
		t.Fatalf("attachBufferedWebsocketBackendData() = %#v, want original conn %#v", got, clientConn)
	}
}

func TestAttachBufferedWebsocketBackendDataReplaysBufferedBytes(t *testing.T) {
	clientConn, peerConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()
	defer func() { _ = peerConn.Close() }()
	clientConn = serverproxy.WrapRuntimeRouteConn(clientConn, "node-ws-backend")

	reader := bufio.NewReader(strings.NewReader("backend"))
	if _, err := reader.Peek(len("backend")); err != nil {
		t.Fatalf("reader.Peek() error = %v", err)
	}

	got, err := attachBufferedWebsocketBackendData(reader, clientConn)
	if err != nil {
		t.Fatalf("attachBufferedWebsocketBackendData() error = %v", err)
	}
	if got == clientConn {
		t.Fatalf("attachBufferedWebsocketBackendData() returned original conn, want wrapped conn")
	}

	buf := make([]byte, len("backend"))
	if _, err := io.ReadFull(got, buf); err != nil {
		t.Fatalf("wrapped conn ReadFull() error = %v", err)
	}
	if string(buf) != "backend" {
		t.Fatalf("wrapped conn bytes = %q, want %q", string(buf), "backend")
	}
	if got := serverproxy.RuntimeRouteUUIDFromConn(got); got != "node-ws-backend" {
		t.Fatalf("RuntimeRouteUUIDFromConn() = %q, want %q", got, "node-ws-backend")
	}
}

func TestAttachBufferedWebsocketClientIngressReturnsOriginalConnWithoutBufferedBytes(t *testing.T) {
	clientConn, peerConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()
	defer func() { _ = peerConn.Close() }()

	bridge := &websocketBufferedIngressBridgeStub{}
	server := &HttpServer{
		HttpProxy: &HttpProxy{
			BaseServer: serverproxy.NewBaseServer(bridge, nil),
		},
	}
	host := &file.Host{
		Id:   3,
		Flow: &file.Flow{},
		Client: &file.Client{
			Id:   24,
			Cnf:  &file.Config{},
			Flow: &file.Flow{},
		},
	}
	routeRuntime := server.NewRouteRuntimeContext(host.Client, "node-ws-empty-attach")
	limiter := &websocketBufferedIngressLimiter{}

	reader := bufio.NewReader(strings.NewReader(""))
	got, err := attachBufferedWebsocketClientIngress(reader, clientConn, routeRuntime, host, limiter)
	if err != nil {
		t.Fatalf("attachBufferedWebsocketClientIngress() error = %v", err)
	}
	if got != clientConn {
		t.Fatalf("attachBufferedWebsocketClientIngress() = %#v, want original conn %#v", got, clientConn)
	}
	if limiter.got != 0 {
		t.Fatalf("limiter got = %d, want 0", limiter.got)
	}
	if bridge.serviceIn != 0 || bridge.serviceOut != 0 {
		t.Fatalf("collector service traffic = %d/%d, want 0/0", bridge.serviceIn, bridge.serviceOut)
	}
}

func TestAttachBufferedWebsocketClientIngressReplaysBufferedBytesAndAccountsTraffic(t *testing.T) {
	clientConn, peerConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()
	defer func() { _ = peerConn.Close() }()
	clientConn = serverproxy.WrapRuntimeRouteConn(clientConn, "node-ws-client")

	bridge := &websocketBufferedIngressBridgeStub{}
	server := &HttpServer{
		HttpProxy: &HttpProxy{
			BaseServer: serverproxy.NewBaseServer(bridge, nil),
		},
	}
	host := &file.Host{
		Id:   4,
		Flow: &file.Flow{},
		Client: &file.Client{
			Id:   25,
			Cnf:  &file.Config{},
			Flow: &file.Flow{},
		},
	}
	routeRuntime := server.NewRouteRuntimeContext(host.Client, "node-ws-attach")
	limiter := &websocketBufferedIngressLimiter{}

	reader := bufio.NewReader(strings.NewReader("client"))
	if _, err := reader.Peek(len("client")); err != nil {
		t.Fatalf("reader.Peek() error = %v", err)
	}

	got, err := attachBufferedWebsocketClientIngress(reader, clientConn, routeRuntime, host, limiter)
	if err != nil {
		t.Fatalf("attachBufferedWebsocketClientIngress() error = %v", err)
	}
	if got == clientConn {
		t.Fatalf("attachBufferedWebsocketClientIngress() returned original conn, want wrapped conn")
	}

	buf := make([]byte, len("client"))
	if _, err := io.ReadFull(got, buf); err != nil {
		t.Fatalf("wrapped conn ReadFull() error = %v", err)
	}
	if string(buf) != "client" {
		t.Fatalf("wrapped conn bytes = %q, want %q", string(buf), "client")
	}
	if gotRoute := serverproxy.RuntimeRouteUUIDFromConn(got); gotRoute != "node-ws-client" {
		t.Fatalf("RuntimeRouteUUIDFromConn() = %q, want %q", gotRoute, "node-ws-client")
	}
	if limiter.got != 6 {
		t.Fatalf("limiter got = %d, want 6", limiter.got)
	}
	if bridge.serviceClientID != host.Client.Id {
		t.Fatalf("collector client id = %d, want %d", bridge.serviceClientID, host.Client.Id)
	}
	if bridge.serviceUUID != "node-ws-attach" {
		t.Fatalf("collector route uuid = %q, want %q", bridge.serviceUUID, "node-ws-attach")
	}
	if bridge.serviceIn != 6 || bridge.serviceOut != 0 {
		t.Fatalf("collector service traffic = %d/%d, want 6/0", bridge.serviceIn, bridge.serviceOut)
	}
	if in, out, total := host.ServiceTrafficTotals(); in != 6 || out != 0 || total != 6 {
		t.Fatalf("host service totals = %d/%d/%d, want 6/0/6", in, out, total)
	}
	if in, out, total := host.Client.ServiceTrafficTotals(); in != 6 || out != 0 || total != 6 {
		t.Fatalf("client service totals = %d/%d/%d, want 6/0/6", in, out, total)
	}
}
