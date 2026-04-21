package proxy

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/djylb/nps/lib/file"
)

type proxyRecordingResponseWriter struct {
	header http.Header
}

func (w *proxyRecordingResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *proxyRecordingResponseWriter) WriteHeader(int) {}

func (w *proxyRecordingResponseWriter) Write(p []byte) (int, error) {
	return len(p), nil
}

func TestAccountedWrappersHandleNilState(t *testing.T) {
	var nilReader *accountedReader
	if _, err := nilReader.Read(make([]byte, 1)); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("nil accountedReader.Read() error = %v, want %v", err, net.ErrClosed)
	}

	var nilCloser *accountedReadCloser
	if _, err := nilCloser.Read(make([]byte, 1)); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("nil accountedReadCloser.Read() error = %v, want %v", err, net.ErrClosed)
	}
	if err := nilCloser.Close(); err != nil {
		t.Fatalf("nil accountedReadCloser.Close() error = %v, want nil", err)
	}

	malformedReader := &accountedReader{}
	if _, err := malformedReader.Read(make([]byte, 1)); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("malformed accountedReader.Read() error = %v, want %v", err, net.ErrClosed)
	}

	malformedCloser := &accountedReadCloser{}
	if _, err := malformedCloser.Read(make([]byte, 1)); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("malformed accountedReadCloser.Read() error = %v, want %v", err, net.ErrClosed)
	}
	if err := malformedCloser.Close(); err != nil {
		t.Fatalf("malformed accountedReadCloser.Close() error = %v, want nil", err)
	}

	var nilWriter *accountedResponseWriter
	if _, err := nilWriter.Write([]byte("x")); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("nil accountedResponseWriter.Write() error = %v, want %v", err, net.ErrClosed)
	}
	nilWriter.Flush()
	if err := nilWriter.Push("/x", nil); !errors.Is(err, http.ErrNotSupported) {
		t.Fatalf("nil accountedResponseWriter.Push() error = %v, want %v", err, http.ErrNotSupported)
	}

	malformedWriter := &accountedResponseWriter{}
	if _, err := malformedWriter.Write([]byte("x")); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("malformed accountedResponseWriter.Write() error = %v, want %v", err, net.ErrClosed)
	}
	malformedWriter.Flush()
	if err := malformedWriter.Push("/x", nil); !errors.Is(err, http.ErrNotSupported) {
		t.Fatalf("malformed accountedResponseWriter.Push() error = %v, want %v", err, http.ErrNotSupported)
	}
	if _, err := malformedWriter.ReadFrom(nil); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("malformed accountedResponseWriter.ReadFrom() error = %v, want %v", err, net.ErrClosed)
	}

	if _, err := (responseWriterOnly{}).Write([]byte("x")); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("responseWriterOnly{}.Write() error = %v, want %v", err, net.ErrClosed)
	}

	valid := &accountedResponseWriter{ResponseWriter: &proxyRecordingResponseWriter{}}
	if _, err := valid.Write([]byte("ok")); err != nil {
		t.Fatalf("valid accountedResponseWriter.Write() error = %v", err)
	}
}

func TestAccountedWrappersReturnOriginalWhenAccountingDisabled(t *testing.T) {
	reader := io.NopCloser(strings.NewReader("payload"))
	if got := wrapReadCloserWithAccounting(reader, nil, nil); got != reader {
		t.Fatalf("wrapReadCloserWithAccounting() = %#v, want original reader %#v", got, reader)
	}

	writer := &proxyRecordingResponseWriter{}
	if got := wrapResponseWriterWithAccounting(writer, nil, nil); got != writer {
		t.Fatalf("wrapResponseWriterWithAccounting() = %#v, want original writer %#v", got, writer)
	}
}

func TestAccountedReaderRefundsLimiterOnObserverError(t *testing.T) {
	limiter := &proxyCountingLimiter{}
	wantErr := errors.New("observer failed")
	reader := &accountedReader{
		reader:  strings.NewReader("payload"),
		limiter: limiter,
		observer: func(int64) error {
			return wantErr
		},
	}

	n, err := reader.Read(make([]byte, len("payload")))
	if n != len("payload") {
		t.Fatalf("Read() n = %d, want %d", n, len("payload"))
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("Read() error = %v, want %v", err, wantErr)
	}
	if limiter.getCalls != 1 || limiter.returnCalls != 1 || limiter.bytes != 0 {
		t.Fatalf("limiter state getCalls=%d returnCalls=%d bytes=%d, want 1/1/0", limiter.getCalls, limiter.returnCalls, limiter.bytes)
	}
}

func TestApplyTunnelHTTPProxyAccountingTracksServiceTrafficWithoutLimiter(t *testing.T) {
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
		Target: &file.Target{TargetStr: "example.com:80"},
	}
	file.InitializeTunnelRuntime(task)

	server := NewTunnelModeServer(ProcessTunnel, &noCallServerBridge{}, task)
	resolved := tunnelHTTPProxyResolvedRequest{
		selection: tunnelHTTPBackendSelection{
			routeUUID:  "node-a",
			targetAddr: "example.com:80",
			task:       task,
		},
		routeRuntime: server.NewRouteRuntimeContext(task.Client, "node-a"),
	}

	req := httptest.NewRequest(http.MethodPost, "http://example.com/demo", io.NopCloser(strings.NewReader("payload")))
	recorder := httptest.NewRecorder()

	req, writer := applyTunnelHTTPProxyAccounting(req, recorder, resolved, nil)
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("ReadAll(req.Body) error = %v", err)
	}
	if string(body) != "payload" {
		t.Fatalf("request body = %q, want %q", string(body), "payload")
	}
	if _, err := writer.Write([]byte("pong")); err != nil {
		t.Fatalf("writer.Write() error = %v", err)
	}

	in, out, total := task.ServiceTrafficTotals()
	if in != int64(len("payload")) || out != int64(len("pong")) || total != int64(len("payload")+len("pong")) {
		t.Fatalf("task service traffic = (%d, %d, %d), want (%d, %d, %d)", in, out, total, len("payload"), len("pong"), len("payload")+len("pong"))
	}
	clientIn, clientOut, clientTotal := client.ServiceTrafficTotals()
	if clientIn != int64(len("payload")) || clientOut != int64(len("pong")) || clientTotal != int64(len("payload")+len("pong")) {
		t.Fatalf("client service traffic = (%d, %d, %d), want (%d, %d, %d)", clientIn, clientOut, clientTotal, len("payload"), len("pong"), len("payload")+len("pong"))
	}
}
