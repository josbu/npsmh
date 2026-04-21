package conn

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestWsConnReadStreamsAcrossCalls(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	msg := []byte("0123456789")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade failed: %v", err)
		}
		defer func() { _ = ws.Close() }()
		_ = ws.SetWriteDeadline(time.Now().Add(15 * time.Second))
		if err = ws.WriteMessage(websocket.BinaryMessage, msg); err != nil {
			t.Fatalf("write message failed: %v", err)
		}
		_ = ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	}))
	defer server.Close()
	dialer := websocket.Dialer{}
	url := "ws" + server.URL[len("http"):]
	client, _, err := dialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer func() { _ = client.Close() }()
	conn := NewWsConn(client)
	buf := make([]byte, 4)

	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("first read failed: %v", err)
	}
	if got := string(buf[:n]); got != "0123" {
		t.Fatalf("first read mismatch: got %q", got)
	}

	n, err = conn.Read(buf)
	if err != nil {
		t.Fatalf("second read failed: %v", err)
	}
	if got := string(buf[:n]); got != "4567" {
		t.Fatalf("second read mismatch: got %q", got)
	}

	n, err = conn.Read(buf)
	if err != nil {
		t.Fatalf("third read failed: %v", err)
	}
	if got := string(buf[:n]); got != "89" {
		t.Fatalf("third read mismatch: got %q", got)
	}

	_, err = conn.Read(buf)
	if err == nil {
		t.Fatal("expected EOF after close frame")
	}
	if err != io.EOF && !websocket.IsCloseError(err, websocket.CloseNormalClosure) {
		t.Fatalf("expected EOF or close-normal error, got: %v", err)
	}
}

func TestWsConnReadNilConnReturnsClosed(t *testing.T) {
	var conn *WsConn
	if _, err := conn.Read(make([]byte, 1)); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Read() error = %v, want %v", err, net.ErrClosed)
	}
}

func TestWsConnReadStreamsSingleLargeMessage(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	payload := strings.Repeat("z", maxWebsocketMessageSize+123)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade failed: %v", err)
		}
		defer func() { _ = ws.Close() }()
		_ = ws.SetWriteDeadline(time.Now().Add(15 * time.Second))
		if err = ws.WriteMessage(websocket.BinaryMessage, []byte(payload)); err != nil {
			t.Fatalf("write message failed: %v", err)
		}
		_ = ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	}))
	defer server.Close()

	client, _, err := websocket.DefaultDialer.Dial("ws"+server.URL[len("http"):], nil)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer func() { _ = client.Close() }()

	conn := NewWsConn(client)
	buf := make([]byte, 32<<10)
	var got strings.Builder
	for got.Len() < len(payload) {
		n, err := conn.Read(buf)
		if err != nil {
			t.Fatalf("Read() error before payload complete: %v", err)
		}
		got.Write(buf[:n])
	}
	if got.String() != payload {
		t.Fatalf("read payload len = %d, want %d", got.Len(), len(payload))
	}
}

func TestWsConnWriteStreamsOversizedSingleMessage(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	payload := strings.Repeat("x", maxWebsocketMessageSize+123)
	serverReceived := make(chan string, 1)
	serverMessages := make(chan int, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade failed: %v", err)
		}
		defer func() { _ = ws.Close() }()

		mt, reader, err := ws.NextReader()
		if err != nil {
			t.Fatalf("NextReader() error = %v", err)
		}
		if mt != websocket.BinaryMessage {
			t.Fatalf("message type = %d, want %d", mt, websocket.BinaryMessage)
		}
		got, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("ReadAll() error = %v", err)
		}
		serverReceived <- string(got)
		serverMessages <- 1
	}))
	defer server.Close()

	client, _, err := websocket.DefaultDialer.Dial("ws"+server.URL[len("http"):], nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer func() { _ = client.Close() }()

	conn := NewWsConn(client)
	n, err := conn.Write([]byte(payload))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if n != len(payload) {
		t.Fatalf("Write() n = %d, want %d", n, len(payload))
	}

	select {
	case got := <-serverReceived:
		if got != payload {
			t.Fatalf("server payload len = %d, want %d", len(got), len(payload))
		}
		if count := <-serverMessages; count != 1 {
			t.Fatalf("server message count = %d, want 1", count)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("server did not receive websocket payload")
	}
}

func TestHTTPListenerAcceptReturnsClosedErrorAfterClose(t *testing.T) {
	hl := &httpListener{
		acceptCh: make(chan net.Conn, 1),
		closeCh:  make(chan struct{}),
		addr:     LocalTCPAddr,
	}

	if err := hl.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	conn, err := hl.Accept()
	if conn != nil {
		_ = conn.Close()
		t.Fatal("Accept() should not return a connection after close")
	}
	if err == nil || !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Accept() error = %v, want %v", err, net.ErrClosed)
	}
}

func TestHTTPListenerCloseDrainsQueuedConn(t *testing.T) {
	hl := &httpListener{
		acceptCh: make(chan net.Conn, 1),
		closeCh:  make(chan struct{}),
		addr:     LocalTCPAddr,
	}
	serverConn, clientConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()

	if err := hl.enqueue(serverConn); err != nil {
		t.Fatalf("enqueue() error = %v", err)
	}
	if err := hl.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	buf := make([]byte, 1)
	if _, err := clientConn.Read(buf); err == nil {
		t.Fatal("queued conn should be closed when listener closes")
	}
	conn, err := hl.Accept()
	if conn != nil {
		_ = conn.Close()
		t.Fatal("Accept() should not return queued conn after close")
	}
	if err == nil || !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Accept() error = %v, want %v", err, net.ErrClosed)
	}
}

func TestHTTPListenerEnqueueRejectsWhenBufferFull(t *testing.T) {
	withVirtualListenerEnqueueTimeout(t, 25*time.Millisecond)
	hl := &httpListener{
		acceptCh: make(chan net.Conn, 1),
		closeCh:  make(chan struct{}),
		addr:     LocalTCPAddr,
	}
	firstServer, firstClient := net.Pipe()
	defer func() { _ = firstClient.Close() }()
	if err := hl.enqueue(firstServer); err != nil {
		t.Fatalf("enqueue(first) error = %v", err)
	}

	secondServer, secondClient := net.Pipe()
	defer func() { _ = secondClient.Close() }()
	err := hl.enqueue(secondServer)
	if err == nil || !errors.Is(err, ErrVirtualListenerFull) {
		t.Fatalf("enqueue(second) error = %v, want %v", err, ErrVirtualListenerFull)
	}
	if _, readErr := secondClient.Read(make([]byte, 1)); readErr == nil {
		t.Fatal("overflow conn should be closed when listener buffer is full")
	}
	_ = hl.Close()
}

func TestHTTPListenerEnqueueWaitsForCapacity(t *testing.T) {
	withVirtualListenerEnqueueTimeout(t, 200*time.Millisecond)
	hl := &httpListener{
		acceptCh: make(chan net.Conn, 1),
		closeCh:  make(chan struct{}),
		addr:     LocalTCPAddr,
	}
	defer func() { _ = hl.Close() }()

	firstServer, firstClient := net.Pipe()
	defer func() { _ = firstClient.Close() }()
	if err := hl.enqueue(firstServer); err != nil {
		t.Fatalf("enqueue(first) error = %v", err)
	}

	secondServer, secondClient := net.Pipe()
	defer func() { _ = secondClient.Close() }()
	errCh := make(chan error, 1)
	go func() {
		errCh <- hl.enqueue(secondServer)
	}()

	select {
	case err := <-errCh:
		t.Fatalf("enqueue(second) returned early while queue was still full: %v", err)
	case <-time.After(40 * time.Millisecond):
	}

	firstAccepted, err := hl.Accept()
	if err != nil {
		t.Fatalf("Accept(first) error = %v", err)
	}
	_ = firstAccepted.Close()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("enqueue(second) error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("enqueue(second) did not succeed after queue capacity became available")
	}

	secondAccepted, err := hl.Accept()
	if err != nil {
		t.Fatalf("Accept(second) error = %v", err)
	}
	_ = secondAccepted.Close()
}

func TestDialWSContextNormalizesNonPositiveTimeout(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade failed: %v", err)
		}
		defer func() { _ = ws.Close() }()
	}))
	defer server.Close()

	rawConn, err := net.Dial("tcp", strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatalf("net.Dial() error = %v", err)
	}
	defer func() { _ = rawConn.Close() }()

	url := "ws" + strings.TrimPrefix(server.URL, "http")
	wsConn, resp, err := DialWSContext(context.Background(), rawConn, url, "", 0)
	if resp != nil && resp.Body != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if err != nil {
		t.Fatalf("DialWSContext() error = %v", err)
	}
	if wsConn == nil {
		t.Fatal("DialWSContext() returned nil websocket connection")
	}
	_ = wsConn.Close()
}

func TestDialWSContextClosesRawConnOnHandshakeFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	rawConn, err := net.Dial("tcp", strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatalf("net.Dial() error = %v", err)
	}
	spy := &closeSpyConn{Conn: rawConn}

	url := "ws" + strings.TrimPrefix(server.URL, "http")
	if _, resp, err := DialWSContext(context.Background(), spy, url, "", time.Second); err == nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		t.Fatal("DialWSContext() error = nil, want bad handshake")
	} else if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if !spy.isClosed() {
		t.Fatal("DialWSContext() should close raw conn on handshake failure")
	}
}

func TestDialWSSContextNormalizesNonPositiveTimeout(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade failed: %v", err)
		}
		defer func() { _ = ws.Close() }()
	}))
	defer server.Close()

	rawConn, err := net.Dial("tcp", strings.TrimPrefix(server.URL, "https://"))
	if err != nil {
		t.Fatalf("net.Dial() error = %v", err)
	}
	defer func() { _ = rawConn.Close() }()

	url := "wss" + strings.TrimPrefix(server.URL, "https")
	wsConn, resp, err := DialWSSContext(context.Background(), rawConn, url, "", "", 0, false)
	if resp != nil && resp.Body != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if err != nil {
		t.Fatalf("DialWSSContext() error = %v", err)
	}
	if wsConn == nil {
		t.Fatal("DialWSSContext() returned nil websocket connection")
	}
	_ = wsConn.Close()
}

func TestDialWSSContextRejectsUntrustedCertificateWhenVerificationEnabled(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade failed: %v", err)
		}
		defer func() { _ = ws.Close() }()
	}))
	defer server.Close()

	rawConn, err := net.Dial("tcp", strings.TrimPrefix(server.URL, "https://"))
	if err != nil {
		t.Fatalf("net.Dial() error = %v", err)
	}

	url := "wss" + strings.TrimPrefix(server.URL, "https")
	if _, resp, err := DialWSSContext(context.Background(), rawConn, url, "", "", time.Second, true); err == nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		t.Fatal("DialWSSContext() error = nil, want certificate verification failure")
	} else if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
}
