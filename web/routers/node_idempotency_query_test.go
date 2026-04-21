package routers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/djylb/nps/web/framework"
	"github.com/gin-gonic/gin"
)

func TestNodeHTTPIdempotencyTargetIncludesNormalizedQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/tools/qrcode?b=2&a=1", nil)

	if got := nodeHTTPIdempotencyTarget(ctx); got != "/api/tools/qrcode?a=1&b=2" {
		t.Fatalf("nodeHTTPIdempotencyTarget() = %q, want /api/tools/qrcode?a=1&b=2", got)
	}
}

func TestNodeHTTPIdempotencyFingerprintDependsOnQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)

	recA := httptest.NewRecorder()
	ctxA, _ := gin.CreateTestContext(recA)
	ctxA.Request = httptest.NewRequest(http.MethodPost, "/api/tools/qrcode?text=alpha", nil)

	recB := httptest.NewRecorder()
	ctxB, _ := gin.CreateTestContext(recB)
	ctxB.Request = httptest.NewRequest(http.MethodPost, "/api/tools/qrcode?text=beta", nil)

	if nodeHTTPIdempotencyFingerprint(ctxA) == nodeHTTPIdempotencyFingerprint(ctxB) {
		t.Fatal("nodeHTTPIdempotencyFingerprint() should differ when query differs")
	}
}

func TestResolveNodeWSRequestPreservesNormalizedQueryForIdempotency(t *testing.T) {
	resolved, err := resolveNodeWSRequest(nil, http.MethodPost, "/api/tools/qrcode?b=2&a=1")
	if err != nil {
		t.Fatalf("resolveNodeWSRequest() error = %v", err)
	}
	if resolved.CanonicalPath != "/tools/qrcode" {
		t.Fatalf("resolveNodeWSRequest().CanonicalPath = %q, want /tools/qrcode", resolved.CanonicalPath)
	}
	if resolved.CanonicalQuery != "a=1&b=2" {
		t.Fatalf("resolveNodeWSRequest().CanonicalQuery = %q, want a=1&b=2", resolved.CanonicalQuery)
	}
}

func TestNodeHTTPRequestFingerprintBodyUsesOnlyCapturedRawBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/auth/session", bytes.NewBufferString("username=alice&password=secret"))
	ctx.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	if got := nodeHTTPRequestFingerprintBody(ctx); got != nil {
		t.Fatalf("nodeHTTPRequestFingerprintBody() with uncaptured form body = %q, want nil", string(got))
	}

	framework.SetRequestRawBody(ctx, []byte(`{"username":"alice"}`))
	if got := string(nodeHTTPRequestFingerprintBody(ctx)); got != `{"username":"alice"}` {
		t.Fatalf("nodeHTTPRequestFingerprintBody() with captured raw body = %q, want captured json body", got)
	}
}
