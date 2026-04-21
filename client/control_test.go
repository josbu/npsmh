package client

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/config"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
)

func TestWaitReconnectDelayReturnsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	if waitReconnectDelay(ctx, 5*time.Second) {
		t.Fatal("waitReconnectDelay() = true, want false when context is canceled")
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("waitReconnectDelay() took %v, want under %v", elapsed, 100*time.Millisecond)
	}
}

func TestWaitReconnectDelayCompletesAfterDelay(t *testing.T) {
	ctx := context.Background()

	start := time.Now()
	if !waitReconnectDelay(ctx, 20*time.Millisecond) {
		t.Fatal("waitReconnectDelay() = false, want true when delay completes")
	}
	if elapsed := time.Since(start); elapsed < 15*time.Millisecond {
		t.Fatalf("waitReconnectDelay() took %v, want at least %v", elapsed, 15*time.Millisecond)
	}
}

func TestWaitReconnectDelayHandlesNilContext(t *testing.T) {
	start := time.Now()
	var nilCtx context.Context
	if !waitReconnectDelay(nilCtx, 20*time.Millisecond) {
		t.Fatal("waitReconnectDelay(nil) = false, want true when delay completes")
	}
	if elapsed := time.Since(start); elapsed < 15*time.Millisecond {
		t.Fatalf("waitReconnectDelay(nil) took %v, want at least %v", elapsed, 15*time.Millisecond)
	}
}

func TestReadTaskAddStatusesConsumesExpandedTaskStatuses(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	t.Cleanup(func() { _ = serverSide.Close() })
	t.Cleanup(func() { _ = clientSide.Close() })

	serverConn := conn.NewConn(serverSide)
	clientConn := conn.NewConn(clientSide)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = binary.Write(serverConn, binary.LittleEndian, true)
		_ = binary.Write(serverConn, binary.LittleEndian, false)
		_ = binary.Write(serverConn, binary.LittleEndian, true)
	}()

	task := &file.Tunnel{
		Mode:  "tcp",
		Ports: "12000,12001",
		Target: &file.Target{
			TargetStr: "9000,9001",
		},
	}
	if readTaskAddStatuses(clientConn, task) {
		t.Fatal("readTaskAddStatuses() = true, want false when any expanded port status fails")
	}
	if !clientConn.GetAddStatus() {
		t.Fatal("next GetAddStatus() = false, want the following request status after expanded task statuses are consumed")
	}
	<-done
}

func TestTaskAddStatusCountFallsBackToSingleStatusForInvalidExpandedTCPTask(t *testing.T) {
	task := &file.Tunnel{
		Mode:  "tcp",
		Ports: "12000,12001",
		Target: &file.Target{
			TargetStr: "9000",
		},
	}
	if got := taskAddStatusCount(task); got != 1 {
		t.Fatalf("taskAddStatusCount() = %d, want 1 for an invalid expanded tcp task", got)
	}
}

func TestStartFromConfigRejectsMissingCommonConfig(t *testing.T) {
	err := StartFromConfig(context.Background(), func() {}, &config.Config{}, "memory")
	if err == nil {
		t.Fatal("StartFromConfig() error = nil, want non-nil for missing common section")
	}
}

func TestStartFromFileReturnsConfigLoadError(t *testing.T) {
	err := StartFromFile(context.Background(), func() {}, "D:\\GolandProjects\\nps\\does-not-exist.conf")
	if err == nil {
		t.Fatal("StartFromFile() error = nil, want non-nil for missing config file")
	}
}

func TestNormalizeClientParentContextUsesBackgroundForNil(t *testing.T) {
	var nilCtx context.Context
	ctx := normalizeClientParentContext(nilCtx)
	if ctx == nil {
		t.Fatal("normalizeClientParentContext(nil) = nil, want background context")
	}
	if err := ctx.Err(); err != nil {
		t.Fatalf("normalizeClientParentContext(nil).Err() = %v, want nil", err)
	}
}

func readSentType(c *conn.Conn, want string) error {
	got, err := c.ReadFlag()
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("ReadFlag() = %q, want %q", got, want)
	}
	if Ver > 3 {
		if Ver > 5 {
			if _, err := c.GetShortLenContent(); err != nil {
				return fmt.Errorf("GetShortLenContent(uuid): %w", err)
			}
		}
		if _, err := c.GetShortLenContent(); err != nil {
			return fmt.Errorf("GetShortLenContent(padding): %w", err)
		}
	}
	return nil
}

func TestGetTaskStatusViaConn(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	t.Cleanup(func() { _ = serverSide.Close() })
	t.Cleanup(func() { _ = clientSide.Close() })

	serverConn := conn.NewConn(serverSide)
	clientConn := conn.NewConn(clientSide)
	errCh := make(chan error, 1)

	go func() {
		if err := readSentType(serverConn, common.WORK_CONFIG); err != nil {
			errCh <- err
			return
		}
		flag, err := serverConn.ReadFlag()
		if err != nil {
			errCh <- err
			return
		}
		if flag != common.WORK_STATUS {
			errCh <- fmt.Errorf("status flag = %q, want %q", flag, common.WORK_STATUS)
			return
		}
		auth := make([]byte, len(crypt.Blake2b("demo-key")))
		if _, err := io.ReadFull(serverConn, auth); err != nil {
			errCh <- err
			return
		}
		if !bytes.Equal(auth, []byte(crypt.Blake2b("demo-key"))) {
			errCh <- fmt.Errorf("status auth key mismatch")
			return
		}
		payload := []byte("alpha" + common.CONN_DATA_SEQ + common.CONN_DATA_SEQ)
		if err := binary.Write(serverConn, binary.LittleEndian, false); err != nil {
			errCh <- err
			return
		}
		if err := binary.Write(serverConn, binary.LittleEndian, int32(len(payload))); err != nil {
			errCh <- err
			return
		}
		if err := binary.Write(serverConn, binary.LittleEndian, payload); err != nil {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	got, err := getTaskStatus(clientConn, "demo-uuid", "demo-key")
	if err != nil {
		t.Fatalf("getTaskStatus() error = %v", err)
	}
	if want := []string{"alpha", ""}; !reflect.DeepEqual(got, want) {
		t.Fatalf("getTaskStatus() = %#v, want %#v", got, want)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("server routine error = %v", err)
	}
}

func TestGetTaskStatusRejectsNegativePayloadLength(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	t.Cleanup(func() { _ = serverSide.Close() })
	t.Cleanup(func() { _ = clientSide.Close() })

	serverConn := conn.NewConn(serverSide)
	clientConn := conn.NewConn(clientSide)
	errCh := make(chan error, 1)

	go func() {
		if err := readSentType(serverConn, common.WORK_CONFIG); err != nil {
			errCh <- err
			return
		}
		flag, err := serverConn.ReadFlag()
		if err != nil {
			errCh <- err
			return
		}
		if flag != common.WORK_STATUS {
			errCh <- fmt.Errorf("status flag = %q, want %q", flag, common.WORK_STATUS)
			return
		}
		auth := make([]byte, len(crypt.Blake2b("demo-key")))
		if _, err := io.ReadFull(serverConn, auth); err != nil {
			errCh <- err
			return
		}
		if err := binary.Write(serverConn, binary.LittleEndian, false); err != nil {
			errCh <- err
			return
		}
		if err := binary.Write(serverConn, binary.LittleEndian, int32(-1)); err != nil {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	if _, err := getTaskStatus(clientConn, "demo-uuid", "demo-key"); err == nil || !strings.Contains(err.Error(), "invalid status payload length") {
		t.Fatalf("getTaskStatus() error = %v, want invalid status payload length", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("server routine error = %v", err)
	}
}

func TestGetTaskStatusRejectsOversizedPayloadLength(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	t.Cleanup(func() { _ = serverSide.Close() })
	t.Cleanup(func() { _ = clientSide.Close() })

	serverConn := conn.NewConn(serverSide)
	clientConn := conn.NewConn(clientSide)
	errCh := make(chan error, 1)

	go func() {
		if err := readSentType(serverConn, common.WORK_CONFIG); err != nil {
			errCh <- err
			return
		}
		flag, err := serverConn.ReadFlag()
		if err != nil {
			errCh <- err
			return
		}
		if flag != common.WORK_STATUS {
			errCh <- fmt.Errorf("status flag = %q, want %q", flag, common.WORK_STATUS)
			return
		}
		auth := make([]byte, len(crypt.Blake2b("demo-key")))
		if _, err := io.ReadFull(serverConn, auth); err != nil {
			errCh <- err
			return
		}
		if err := binary.Write(serverConn, binary.LittleEndian, false); err != nil {
			errCh <- err
			return
		}
		if err := binary.Write(serverConn, binary.LittleEndian, int32(maxTaskStatusPayloadSize+1)); err != nil {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	if _, err := getTaskStatus(clientConn, "demo-uuid", "demo-key"); err == nil || !strings.Contains(err.Error(), "invalid status payload length") {
		t.Fatalf("getTaskStatus() error = %v, want invalid status payload length", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("server routine error = %v", err)
	}
}

func TestRegisterLocalIpViaConn(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	t.Cleanup(func() { _ = serverSide.Close() })
	t.Cleanup(func() { _ = clientSide.Close() })

	serverConn := conn.NewConn(serverSide)
	clientConn := conn.NewConn(clientSide)
	errCh := make(chan error, 1)

	go func() {
		if err := readSentType(serverConn, common.WORK_REGISTER); err != nil {
			errCh <- err
			return
		}
		var got int32
		if err := binary.Read(serverConn, binary.LittleEndian, &got); err != nil {
			errCh <- err
			return
		}
		if got != 6 {
			errCh <- fmt.Errorf("registration duration = %d, want 6", got)
			return
		}
		errCh <- nil
	}()

	if err := registerLocalIp(clientConn, "demo-uuid", 6); err != nil {
		t.Fatalf("registerLocalIp() error = %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("server routine error = %v", err)
	}
}

func TestFormatTaskStatus(t *testing.T) {
	got := FormatTaskStatus([]string{"alpha", ""})
	if !strings.Contains(got, "Total active: 2") {
		t.Fatalf("FormatTaskStatus() output %q missing total count", got)
	}
	if !strings.Contains(got, "1. alpha") || !strings.Contains(got, "2. (no remark)") {
		t.Fatalf("FormatTaskStatus() output %q missing formatted entries", got)
	}
}

func TestBuildClientControlConnPlanDefaultsPathAndPort(t *testing.T) {
	oldHasFailed := HasFailed
	HasFailed = false
	t.Cleanup(func() {
		HasFailed = oldHasFailed
	})

	plan, err := buildClientControlConnPlan(context.Background(), "ws", "example.com", "", "", false)
	if err != nil {
		t.Fatalf("buildClientControlConnPlan() error = %v", err)
	}
	if plan.path != "/ws" {
		t.Fatalf("plan.path = %q, want /ws", plan.path)
	}
	if plan.alpn != "nps" {
		t.Fatalf("plan.alpn = %q, want nps", plan.alpn)
	}
	if plan.server != "example.com:80" {
		t.Fatalf("plan.server = %q, want example.com:80", plan.server)
	}
}

func TestBuildClientControlConnPlanUsesExplicitPathAsALPN(t *testing.T) {
	oldHasFailed := HasFailed
	HasFailed = false
	t.Cleanup(func() {
		HasFailed = oldHasFailed
	})

	plan, err := buildClientControlConnPlan(context.Background(), "quic", "bridge.example.com/runtime-main", "", "", false)
	if err != nil {
		t.Fatalf("buildClientControlConnPlan() error = %v", err)
	}
	if plan.path != "/runtime-main" {
		t.Fatalf("plan.path = %q, want /runtime-main", plan.path)
	}
	if plan.alpn != "runtime-main" {
		t.Fatalf("plan.alpn = %q, want runtime-main", plan.alpn)
	}
	if plan.server != "bridge.example.com:8025" {
		t.Fatalf("plan.server = %q, want bridge.example.com:8025", plan.server)
	}
}

func TestBuildClientControlConnPlanRejectsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := buildClientControlConnPlan(ctx, "tcp", "example.com", "", "", false); err == nil {
		t.Fatal("buildClientControlConnPlan() error = nil, want canceled context")
	}
}

func TestBuildClientControlRuntimeInfoRejectsLongTransportType(t *testing.T) {
	oldVer := Ver
	Ver = 3
	t.Cleanup(func() {
		Ver = oldVer
	})

	tp := strings.Repeat("q", 33)
	if _, err := buildClientControlRuntimeInfo(tp, "demo-key"); err == nil || !strings.Contains(err.Error(), "tp too long") {
		t.Fatalf("buildClientControlRuntimeInfo() error = %v, want tp too long", err)
	}
}

func TestEnsurePortAddsDefaultPortWhenMissing(t *testing.T) {
	if got := EnsurePort("bridge.example.com", "tls"); got != "bridge.example.com:8025" {
		t.Fatalf("EnsurePort() = %q, want bridge.example.com:8025", got)
	}
}

func TestEnsurePortKeepsExistingPort(t *testing.T) {
	if got := EnsurePort("bridge.example.com:9443", "tls"); got != "bridge.example.com:9443" {
		t.Fatalf("EnsurePort() = %q, want bridge.example.com:9443", got)
	}
}

func TestLimitClientConnTimeoutUsesContextDeadlineBudget(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()

	timeout := limitClientConnTimeout(ctx, 5*time.Second)
	if timeout <= 0 || timeout > 100*time.Millisecond {
		t.Fatalf("limitClientConnTimeout() = %v, want small deadline-derived timeout", timeout)
	}
}

func TestLimitClientConnTimeoutUsesMinimumForExpiredDeadline(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	if got := limitClientConnTimeout(ctx, 5*time.Second); got != time.Millisecond {
		t.Fatalf("limitClientConnTimeout() = %v, want %v", got, time.Millisecond)
	}
}

func TestCloseConnOnContextDoneClosesAfterCancel(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() { _ = serverConn.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	stop := closeConnOnContextDone(ctx, clientConn)
	defer stop()

	cancel()

	deadline := time.Now().Add(500 * time.Millisecond)
	_ = serverConn.SetReadDeadline(deadline)
	buf := make([]byte, 1)
	if _, err := serverConn.Read(buf); err == nil {
		t.Fatal("serverConn.Read() error = nil, want closed connection after context cancel")
	}
}

func TestCloseConnOnContextDoneStopIsIdempotentForBackgroundContext(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() { _ = serverConn.Close() })
	t.Cleanup(func() { _ = clientConn.Close() })

	stop := closeConnOnContextDone(context.Background(), clientConn)
	stop()
	stop()
}

func TestSendTypeRejectsNilConn(t *testing.T) {
	if err := SendType(nil, common.WORK_CONFIG, "demo-uuid"); err == nil {
		t.Fatal("SendType(nil) error = nil, want non-nil")
	}
}

func TestSendTypeWritesCurrentProtocolPayload(t *testing.T) {
	oldVer := Ver
	Ver = 6
	t.Cleanup(func() {
		Ver = oldVer
	})

	serverSide, clientSide := net.Pipe()
	t.Cleanup(func() { _ = serverSide.Close() })
	t.Cleanup(func() { _ = clientSide.Close() })

	serverConn := conn.NewConn(serverSide)
	clientConn := conn.NewConn(clientSide)
	errCh := make(chan error, 1)

	go func() {
		errCh <- readSentType(serverConn, common.WORK_CONFIG)
	}()

	if err := SendType(clientConn, common.WORK_CONFIG, "demo-uuid"); err != nil {
		t.Fatalf("SendType() error = %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("server routine error = %v", err)
	}
}

func TestBuildClientHTTPProxyConnectRequestSetsBasicAuth(t *testing.T) {
	proxyURL := &url.URL{
		Scheme: "http",
		Host:   "proxy.example.com:8080",
		User:   url.UserPassword("demo-user", "demo-pass"),
	}
	req := buildClientHTTPProxyConnectRequest(proxyURL, "target.example.com:443")

	if req.Method != http.MethodConnect {
		t.Fatalf("request method = %q, want CONNECT", req.Method)
	}
	if req.Host != "target.example.com:443" {
		t.Fatalf("request host = %q, want target.example.com:443", req.Host)
	}
	if req.Header.Get("Authorization") == "" {
		t.Fatal("Authorization header should be populated from proxy credentials")
	}
}

func TestVerifyClientHTTPProxyResponseRejectsNonOK(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() { _ = serverConn.Close() })
	t.Cleanup(func() { _ = clientConn.Close() })

	req := buildClientHTTPProxyConnectRequest(&url.URL{Scheme: "http", Host: "proxy.example.com:8080"}, "target.example.com:443")
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = serverConn.Write([]byte("HTTP/1.1 407 Proxy Authentication Required\r\nContent-Length: 0\r\n\r\n"))
	}()

	if err := verifyClientHTTPProxyResponse(clientConn, req); err == nil || !strings.Contains(err.Error(), "proxy CONNECT failed") {
		t.Fatalf("verifyClientHTTPProxyResponse() error = %v, want proxy CONNECT failed", err)
	}
	<-done
}

func TestResolveClientConfigWebAccessPrefersLegacyImport(t *testing.T) {
	clientInfo := &file.Client{}
	clientInfo.SetLegacyWebLoginImport("legacy-user", "legacy-pass", "")

	username, password := resolveClientConfigWebAccess(&config.CommonConfig{Client: clientInfo}, "demo-vkey")
	if username != "legacy-user" || password != "legacy-pass" {
		t.Fatalf("resolveClientConfigWebAccess() = (%q, %q), want legacy credentials", username, password)
	}
}

func TestResolveClientConfigWebAccessFallsBackToGeneratedKey(t *testing.T) {
	username, password := resolveClientConfigWebAccess(&config.CommonConfig{}, "demo-vkey")
	if username != "user" || password != "demo-vkey" {
		t.Fatalf("resolveClientConfigWebAccess() = (%q, %q), want user/demo-vkey", username, password)
	}
}
