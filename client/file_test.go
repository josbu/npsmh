package client

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
)

func TestBasicAuth(t *testing.T) {
	t.Parallel()

	users := map[string]string{"demo": "secret"}
	h := basicAuth(users, "WebDAV", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	tests := []struct {
		name           string
		authHeader     string
		wantStatusCode int
		wantChallenge  bool
	}{
		{name: "missing auth", wantStatusCode: http.StatusUnauthorized, wantChallenge: true},
		{name: "invalid base64", authHeader: "Basic !!!", wantStatusCode: http.StatusUnauthorized},
		{name: "wrong password", authHeader: "Basic " + base64.StdEncoding.EncodeToString([]byte("demo:bad")), wantStatusCode: http.StatusUnauthorized, wantChallenge: true},
		{name: "valid credential", authHeader: "Basic " + base64.StdEncoding.EncodeToString([]byte("demo:secret")), wantStatusCode: http.StatusNoContent},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req, err := http.NewRequest(http.MethodGet, "/", nil)
			if err != nil {
				t.Fatalf("new request failed: %v", err)
			}
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			rr := newTestResponseRecorder()
			h.ServeHTTP(rr, req)
			if rr.statusCode != tt.wantStatusCode {
				t.Fatalf("status code mismatch, got=%d want=%d", rr.statusCode, tt.wantStatusCode)
			}
			if got := rr.header.Get("WWW-Authenticate"); (got != "") != tt.wantChallenge {
				t.Fatalf("WWW-Authenticate mismatch, got=%q wantChallenge=%v", got, tt.wantChallenge)
			}
		})
	}
}

func TestReadOnly(t *testing.T) {
	t.Parallel()

	called := false
	h := readOnly(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, method := range []string{http.MethodGet, http.MethodHead, "PROPFIND"} {
		req, err := http.NewRequest(method, "/", nil)
		if err != nil {
			t.Fatalf("new request failed: %v", err)
		}
		rr := newTestResponseRecorder()
		h.ServeHTTP(rr, req)
		if rr.statusCode != http.StatusNoContent {
			t.Fatalf("method=%s got status=%d", method, rr.statusCode)
		}
	}
	if !called {
		t.Fatal("expected next handler to be called for allowed methods")
	}

	req, err := http.NewRequest(http.MethodPost, "/", nil)
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}
	rr := newTestResponseRecorder()
	h.ServeHTTP(rr, req)
	if rr.statusCode != http.StatusMethodNotAllowed {
		t.Fatalf("post should be rejected, got status=%d", rr.statusCode)
	}
	if got := rr.header.Get("Allow"); got != "GET, HEAD, PROPFIND" {
		t.Fatalf("allow header mismatch, got=%q", got)
	}
}

func TestFileServerManagerStartAndCloseAll(t *testing.T) {
	root := t.TempDir()
	filePath := filepath.Join(root, "hello.txt")
	if err := os.WriteFile(filePath, []byte("hello nps"), 0o600); err != nil {
		t.Fatalf("write file failed: %v", err)
	}

	fsm := NewFileServerManager(context.Background())
	tunnel := &file.Tunnel{
		ServerIp:  "127.0.0.1",
		Port:      18080,
		Ports:     "18080",
		Mode:      "file",
		LocalPath: root,
		StripPre:  "/files",
		ReadOnly:  true,
	}
	fsm.StartFileServer(tunnel, "vkey")
	t.Cleanup(fsm.CloseAll)

	listener, ok := waitListener(fsm, 2*time.Second)
	if !ok {
		t.Fatal("file server listener was not registered")
	}

	resp := doVirtualRequest(t, listener, "GET /files/hello.txt HTTP/1.1\r\nHost: local\r\n\r\n")
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET status mismatch, got=%d", resp.StatusCode)
	}
	if strings.TrimSpace(string(body)) != "hello nps" {
		t.Fatalf("GET body mismatch, got=%q", string(body))
	}

	resp = doVirtualRequest(t, listener, "POST /files/hello.txt HTTP/1.1\r\nHost: local\r\nContent-Length: 0\r\n\r\n")
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST status mismatch, got=%d", resp.StatusCode)
	}

	fsm.CloseAll()
	if _, err := listener.DialVirtual("127.0.0.1:12345"); err == nil {
		t.Fatal("expected listener dial to fail after CloseAll")
	}
}

func TestFileServerManagerReplacesExistingServerForSameKey(t *testing.T) {
	root := t.TempDir()
	firstPath := filepath.Join(root, "first.txt")
	if err := os.WriteFile(firstPath, []byte("first"), 0o600); err != nil {
		t.Fatalf("write first file failed: %v", err)
	}

	fsm := NewFileServerManager(context.Background())
	t.Cleanup(fsm.CloseAll)

	tunnel := &file.Tunnel{
		ServerIp:  "127.0.0.1",
		Port:      18081,
		Ports:     "18081",
		Mode:      "file",
		LocalPath: root,
		StripPre:  "/files",
		ReadOnly:  true,
	}
	vkey := "replace-vkey"
	fsm.StartFileServer(tunnel, vkey)

	listener, ok := waitListener(fsm, 2*time.Second)
	if !ok {
		t.Fatal("initial file server listener was not registered")
	}

	key := file.FileTunnelRuntimeKey(vkey, tunnel)

	updated := &file.Tunnel{
		ServerIp:     "127.0.0.1",
		Port:         18081,
		Ports:        "18081",
		Mode:         "file",
		LocalPath:    root,
		StripPre:     "/files",
		ReadOnly:     true,
		MultiAccount: &file.MultiAccount{},
		UserAuth:     &file.MultiAccount{},
	}
	fsm.StartFileServer(updated, vkey)

	var replacement *conn.VirtualListener
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		current, ok := fsm.GetListenerByKey(key)
		if ok && current != listener {
			replacement = current
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if replacement == nil {
		t.Fatal("replacement file server listener was not installed")
	}
	if _, err := listener.DialVirtual("127.0.0.1:12345"); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("old listener dial error = %v, want %v", err, net.ErrClosed)
	}

	resp := doVirtualRequest(t, replacement, "GET /files/first.txt HTTP/1.1\r\nHost: local\r\n\r\n")
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read replacement body failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("replacement GET status mismatch, got=%d", resp.StatusCode)
	}
	if strings.TrimSpace(string(body)) != "first" {
		t.Fatalf("replacement GET body mismatch, got=%q", string(body))
	}
}

func TestFileServerManagerUsesContentOnlyAuthFallback(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "auth.txt"), []byte("secure"), 0o600); err != nil {
		t.Fatalf("write file failed: %v", err)
	}

	fsm := NewFileServerManager(context.Background())
	t.Cleanup(fsm.CloseAll)

	tunnel := &file.Tunnel{
		ServerIp:  "127.0.0.1",
		Port:      18083,
		Ports:     "18083",
		Mode:      "file",
		LocalPath: root,
		StripPre:  "/files",
		UserAuth: &file.MultiAccount{
			Content: "ops=token\n",
		},
	}
	fsm.StartFileServer(tunnel, "content-auth-vkey")

	listener, ok := waitListener(fsm, 2*time.Second)
	if !ok {
		t.Fatal("file server listener was not registered")
	}

	unauthorized := doVirtualRequest(t, listener, "GET /files/auth.txt HTTP/1.1\r\nHost: local\r\n\r\n")
	_ = unauthorized.Body.Close()
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want %d", unauthorized.StatusCode, http.StatusUnauthorized)
	}

	authHeader := base64.StdEncoding.EncodeToString([]byte("ops:token"))
	authorized := doVirtualRequest(t, listener, "GET /files/auth.txt HTTP/1.1\r\nHost: local\r\nAuthorization: Basic "+authHeader+"\r\n\r\n")
	body, err := io.ReadAll(authorized.Body)
	if err != nil {
		t.Fatalf("read authorized body failed: %v", err)
	}
	_ = authorized.Body.Close()
	if authorized.StatusCode != http.StatusOK {
		t.Fatalf("authorized status = %d, want %d", authorized.StatusCode, http.StatusOK)
	}
	if strings.TrimSpace(string(body)) != "secure" {
		t.Fatalf("authorized body = %q, want %q", string(body), "secure")
	}
}

func TestFileServerManagerRemovesEntryWhenServeLoopExits(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "gone.txt"), []byte("gone"), 0o600); err != nil {
		t.Fatalf("write file failed: %v", err)
	}

	fsm := NewFileServerManager(context.Background())
	t.Cleanup(fsm.CloseAll)

	tunnel := &file.Tunnel{
		ServerIp:  "127.0.0.1",
		Port:      18082,
		Ports:     "18082",
		Mode:      "file",
		LocalPath: root,
		StripPre:  "/files",
		ReadOnly:  true,
	}
	vkey := "remove-on-exit"
	fsm.StartFileServer(tunnel, vkey)

	listener, ok := waitListener(fsm, 2*time.Second)
	if !ok {
		t.Fatal("file server listener was not registered")
	}
	key := file.FileTunnelRuntimeKey(vkey, tunnel)

	if err := listener.Close(); err != nil {
		t.Fatalf("listener.Close() error = %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := fsm.GetListenerByKey(key); !ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("file server entry should be removed after serve loop exit")
}

func TestNewFileServerManagerDoesNotLeakWatcherForBackgroundContext(t *testing.T) {
	baseline := runtime.NumGoroutine()
	managers := make([]*FileServerManager, 0, 32)
	for i := 0; i < 32; i++ {
		managers = append(managers, NewFileServerManager(context.Background()))
	}
	for _, fsm := range managers {
		fsm.CloseAll()
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runtime.GC()
		time.Sleep(20 * time.Millisecond)
		if delta := runtime.NumGoroutine() - baseline; delta <= 4 {
			return
		}
	}

	if delta := runtime.NumGoroutine() - baseline; delta > 4 {
		t.Fatalf("goroutine delta = %d, want <= 4 after closing background-context managers", delta)
	}
}

func TestNewFileServerManagerDoesNotLeakWatcherAfterManualCloseWithCancelableParent(t *testing.T) {
	baseline := runtime.NumGoroutine()
	managers := make([]*FileServerManager, 0, 32)
	cancels := make([]context.CancelFunc, 0, 32)
	for i := 0; i < 32; i++ {
		parentCtx, cancel := context.WithCancel(context.Background())
		managers = append(managers, NewFileServerManager(parentCtx))
		cancels = append(cancels, cancel)
	}
	t.Cleanup(func() {
		for _, cancel := range cancels {
			cancel()
		}
	})

	for _, fsm := range managers {
		fsm.CloseAll()
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runtime.GC()
		time.Sleep(20 * time.Millisecond)
		if delta := runtime.NumGoroutine() - baseline; delta <= 4 {
			return
		}
	}

	if delta := runtime.NumGoroutine() - baseline; delta > 4 {
		t.Fatalf("goroutine delta = %d, want <= 4 after manual close with cancelable parents", delta)
	}
}

func doVirtualRequest(t *testing.T, listener *conn.VirtualListener, raw string) *http.Response {
	t.Helper()
	c, err := listener.DialVirtual("127.0.0.1:9000")
	if err != nil {
		t.Fatalf("dial virtual failed: %v", err)
	}
	defer func() { _ = c.Close() }()

	if _, err = c.Write([]byte(raw)); err != nil {
		t.Fatalf("write request failed: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatalf("read response failed: %v", err)
	}
	return resp
}

func waitListener(fsm *FileServerManager, timeout time.Duration) (*conn.VirtualListener, bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		fsm.mu.Lock()
		for _, server := range fsm.servers {
			listener := server.listener
			fsm.mu.Unlock()
			return listener, true
		}
		fsm.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	return nil, false
}

type testResponseRecorder struct {
	header     http.Header
	statusCode int
}

func newTestResponseRecorder() *testResponseRecorder {
	return &testResponseRecorder{header: make(http.Header)}
}

func (r *testResponseRecorder) Header() http.Header { return r.header }

func (r *testResponseRecorder) Write(body []byte) (int, error) {
	if r.statusCode == 0 {
		r.statusCode = http.StatusOK
	}
	return len(body), nil
}

func (r *testResponseRecorder) WriteHeader(statusCode int) { r.statusCode = statusCode }
