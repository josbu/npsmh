package routers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	webapi "github.com/djylb/nps/web/api"
	"github.com/gin-gonic/gin"
)

func TestGinAPIContextHandlesNilState(t *testing.T) {
	var nilCtx *ginAPIContext
	if got := nilCtx.BaseContext(); got == nil {
		t.Fatal("BaseContext() should fall back to non-nil background context")
	}
	if got := nilCtx.String("k"); got != "" {
		t.Fatalf("String() = %q, want empty", got)
	}
	if got, ok := nilCtx.LookupString("k"); got != "" || ok {
		t.Fatalf("LookupString() = (%q,%v), want empty/false", got, ok)
	}
	if got := nilCtx.Method(); got != http.MethodGet {
		t.Fatalf("Method() = %q, want %q", got, http.MethodGet)
	}
	if got := nilCtx.Host(); got != "" {
		t.Fatalf("Host() = %q, want empty", got)
	}
	if got := nilCtx.RemoteAddr(); got != "" {
		t.Fatalf("RemoteAddr() = %q, want empty", got)
	}
	if got := nilCtx.ClientIP(); got != "" {
		t.Fatalf("ClientIP() = %q, want empty", got)
	}
	if got := nilCtx.RequestHeader("X-Test"); got != "" {
		t.Fatalf("RequestHeader() = %q, want empty", got)
	}
	if got := nilCtx.RawBody(); got != nil {
		t.Fatalf("RawBody() = %v, want nil", got)
	}
	if got := nilCtx.SessionValue("k"); got != nil {
		t.Fatalf("SessionValue() = %v, want nil", got)
	}
	nilCtx.SetSessionValue("k", "v")
	nilCtx.DeleteSessionValue("k")
	nilCtx.MutateSession(nil)
	nilCtx.SetParam("client_id", "1")
	nilCtx.RespondJSON(http.StatusOK, map[string]string{"ok": "1"})
	nilCtx.RespondString(http.StatusOK, "ok")
	nilCtx.RespondData(http.StatusOK, "text/plain", []byte("ok"))
	nilCtx.Redirect(http.StatusTemporaryRedirect, "/x")
	nilCtx.SetResponseHeader("X-Test", "1")
	if nilCtx.IsWritten() {
		t.Fatal("IsWritten() = true, want false")
	}
	if got := nilCtx.Actor(); got != nil {
		t.Fatalf("Actor() = %v, want nil", got)
	}
	nilCtx.SetActor(nil)
	if got := nilCtx.Metadata(); got != (webapi.RequestMetadata{}) {
		t.Fatalf("Metadata() = %+v, want zero value", got)
	}
}

func TestGinAPIContextHandlesMissingRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = nil
	apiCtx := &ginAPIContext{gin: ctx}

	if got := apiCtx.BaseContext(); got == nil {
		t.Fatal("BaseContext() should fall back to non-nil background context")
	}
	if got := apiCtx.Method(); got != http.MethodGet {
		t.Fatalf("Method() = %q, want %q", got, http.MethodGet)
	}
	if got := apiCtx.String("k"); got != "" {
		t.Fatalf("String() = %q, want empty", got)
	}
	if got := apiCtx.Host(); got != "" {
		t.Fatalf("Host() = %q, want empty", got)
	}
	if got := apiCtx.RemoteAddr(); got != "" {
		t.Fatalf("RemoteAddr() = %q, want empty", got)
	}
}

func TestGinAPIContextUsesRequestContextWhenPresent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/api/health", nil).WithContext(context.WithValue(context.Background(), "k", "v"))
	req.Host = "example.test"
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("X-Test", "ok")
	ctx.Request = req

	apiCtx := &ginAPIContext{gin: ctx}
	if got := apiCtx.BaseContext().Value("k"); got != "v" {
		t.Fatalf("BaseContext().Value() = %v, want v", got)
	}
	if got := apiCtx.Method(); got != http.MethodPost {
		t.Fatalf("Method() = %q, want %q", got, http.MethodPost)
	}
	if got := apiCtx.Host(); got != "example.test" {
		t.Fatalf("Host() = %q, want %q", got, "example.test")
	}
	if got := apiCtx.RemoteAddr(); got != "127.0.0.1:12345" {
		t.Fatalf("RemoteAddr() = %q, want %q", got, "127.0.0.1:12345")
	}
	if got := apiCtx.RequestHeader("X-Test"); got != "ok" {
		t.Fatalf("RequestHeader() = %q, want %q", got, "ok")
	}
}
