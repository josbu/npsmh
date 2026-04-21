package routers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	webapi "github.com/djylb/nps/web/api"
	"github.com/gin-gonic/gin"
)

func TestNodeIdempotencyAcquireContextStopsWaitingWhenCanceled(t *testing.T) {
	store := newNodeIdempotencyStore(time.Minute, "")
	first := store.acquire("scope", "key", "fingerprint")
	if first.entry == nil {
		t.Fatal("first acquire should create inflight entry")
	}

	ctx, cancel := context.WithCancel(context.Background())
	resultCh := make(chan nodeIdempotencyAcquireResult, 1)
	start := time.Now()
	go func() {
		resultCh <- store.acquireContext(ctx, "scope", "key", "fingerprint")
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case result := <-resultCh:
		if !errors.Is(result.err, context.Canceled) {
			t.Fatalf("acquireContext() err = %v, want context.Canceled", result.err)
		}
		if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
			t.Fatalf("acquireContext() elapsed = %v, want prompt cancellation", elapsed)
		}
		if result.entry != nil || result.httpResp != nil || result.wsResp != nil || result.conflict {
			t.Fatalf("acquireContext() returned unexpected payload on cancel: %+v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("acquireContext() did not stop waiting after context cancellation")
	}
}

func TestNodeIdempotencyResetReleasesInflightWaiters(t *testing.T) {
	store := newNodeIdempotencyStore(time.Minute, "")
	first := store.acquire("scope", "key", "fingerprint")
	if first.entry == nil {
		t.Fatal("first acquire should create inflight entry")
	}

	resultCh := make(chan nodeIdempotencyAcquireResult, 1)
	start := time.Now()
	go func() {
		resultCh <- store.acquire("scope", "key", "fingerprint")
	}()

	time.Sleep(20 * time.Millisecond)
	store.Reset()

	select {
	case result := <-resultCh:
		if result.entry == nil {
			t.Fatalf("acquire() after reset should create replacement entry, got %+v", result)
		}
		if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
			t.Fatalf("acquire() elapsed after reset = %v, want prompt release", elapsed)
		}
	case <-time.After(time.Second):
		t.Fatal("acquire() did not stop waiting after Reset()")
	}
}

func TestNodeIdempotencyMiddlewareReturnsTimeoutWhenWaitIsCanceled(t *testing.T) {
	gin.SetMode(gin.TestMode)

	store := newNodeIdempotencyStore(time.Minute, "")
	seedRec := httptest.NewRecorder()
	seedCtx, _ := gin.CreateTestContext(seedRec)
	seedCtx.Request = httptest.NewRequest(http.MethodPost, "/demo", nil)
	scope := nodeHTTPIdempotencyScope(seedCtx, nil)
	fingerprint := nodeHTTPIdempotencyFingerprint(seedCtx)
	first := store.acquire(scope, "demo-key", fingerprint)
	if first.entry == nil {
		t.Fatal("first acquire should create inflight entry")
	}

	state := &State{Idempotency: store}
	engine := gin.New()
	var handled int32
	engine.Use(nodeIdempotencyMiddleware(state))
	engine.POST("/demo", func(c *gin.Context) {
		atomic.AddInt32(&handled, 1)
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	reqCtx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodPost, "/demo", nil).WithContext(reqCtx)
	req.Header.Set("Idempotency-Key", "demo-key")
	resp := httptest.NewRecorder()
	engine.ServeHTTP(resp, req)

	if resp.Code != http.StatusRequestTimeout {
		t.Fatalf("ServeHTTP() status = %d, want %d body=%s", resp.Code, http.StatusRequestTimeout, resp.Body.String())
	}
	if atomic.LoadInt32(&handled) != 0 {
		t.Fatal("request handler should not run after idempotency wait cancellation")
	}
	if body := resp.Body.String(); !strings.Contains(body, "\"code\":\"idempotency_wait_canceled\"") {
		t.Fatalf("timeout response should expose idempotency wait cancel code, got %s", body)
	}
}

func TestNodeIdempotencyMiddlewareReplaysMultiValueHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)

	store := newNodeIdempotencyStore(time.Minute, "")
	state := &State{Idempotency: store}
	engine := gin.New()
	var handled int32
	engine.Use(nodeIdempotencyMiddleware(state))
	engine.POST("/demo", func(c *gin.Context) {
		atomic.AddInt32(&handled, 1)
		c.Header("Set-Cookie", "session=a; Path=/")
		c.Writer.Header().Add("Set-Cookie", "refresh=b; Path=/")
		c.Writer.Header().Add("Cache-Control", "no-cache")
		c.Writer.Header().Add("Cache-Control", "private")
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	makeRequest := func(requestID string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/demo", nil)
		req.Header.Set("Idempotency-Key", "demo-key")
		req.Header.Set("X-Request-ID", requestID)
		resp := httptest.NewRecorder()
		engine.ServeHTTP(resp, req)
		return resp
	}

	first := makeRequest("req-1")
	if first.Code != http.StatusOK {
		t.Fatalf("first ServeHTTP() status = %d, want %d body=%s", first.Code, http.StatusOK, first.Body.String())
	}

	second := makeRequest("req-2")
	if second.Code != http.StatusOK {
		t.Fatalf("replayed ServeHTTP() status = %d, want %d body=%s", second.Code, http.StatusOK, second.Body.String())
	}
	if got := atomic.LoadInt32(&handled); got != 1 {
		t.Fatalf("handler call count = %d, want 1 after replay", got)
	}
	if replay := second.Result().Header.Get("X-Idempotent-Replay"); replay != "true" {
		t.Fatalf("replayed response X-Idempotent-Replay = %q, want true", replay)
	}
	if requestID := second.Result().Header.Get("X-Request-ID"); requestID != "req-2" {
		t.Fatalf("replayed response X-Request-ID = %q, want req-2", requestID)
	}
	if cookies := second.Result().Header.Values("Set-Cookie"); len(cookies) != 2 {
		t.Fatalf("replayed response Set-Cookie values = %v, want 2 entries", cookies)
	}
	if cacheControl := second.Result().Header.Values("Cache-Control"); len(cacheControl) != 2 {
		t.Fatalf("replayed response Cache-Control values = %v, want 2 entries", cacheControl)
	}
}

func TestDispatchNodeWSRequestReturnsTimeoutWhenIdempotencyWaitIsCanceled(t *testing.T) {
	state := NewStateWithApp(webapi.New(nil))
	defer state.Close()

	store := newNodeIdempotencyStore(time.Minute, "")
	state.Idempotency = store

	resolved, err := resolveNodeWSRequest(state.CurrentConfig(), http.MethodPost, "/api/tools/qrcode")
	if err != nil {
		t.Fatalf("resolveNodeWSRequest() error = %v", err)
	}
	first := store.acquire(
		nodeWSIdempotencyScope(nil, resolved.CanonicalMethod, resolved.CanonicalPath, resolved.CanonicalQuery),
		"ws-key",
		nodeWSIdempotencyFingerprint(resolved.CanonicalMethod, resolved.CanonicalPath, resolved.CanonicalQuery, nil),
	)
	if first.entry == nil {
		t.Fatal("first acquire should create inflight entry")
	}

	baseCtx, cancel := context.WithCancel(context.Background())
	cancel()
	response := dispatchNodeWSRequestWithBase(state, nodeWSDispatchBase{
		Context: baseCtx,
		Metadata: webapi.RequestMetadata{
			RequestID: "ws-idempotency-timeout",
		},
	}, nil, nodeWSFrame{
		ID:     "req-1",
		Type:   "request",
		Method: http.MethodPost,
		Path:   "/api/tools/qrcode",
		Headers: map[string]string{
			"Idempotency-Key": "ws-key",
			"X-Request-ID":    "ws-request-42",
		},
	})

	if response.Status != http.StatusRequestTimeout {
		t.Fatalf("dispatchNodeWSRequestWithBase() status = %d, want %d body=%s", response.Status, http.StatusRequestTimeout, string(response.Body))
	}
	if response.Error != "request canceled while waiting for the in-flight idempotent request" {
		t.Fatalf("dispatchNodeWSRequestWithBase() error = %q", response.Error)
	}
	if response.Headers["X-Request-ID"] != "ws-request-42" {
		t.Fatalf("response request id = %q, want ws-request-42", response.Headers["X-Request-ID"])
	}
}
