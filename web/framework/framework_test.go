package framework

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dchest/captcha"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/gin-gonic/gin"
)

func TestSetSessionValuePropagatesSaveErrors(t *testing.T) {
	restoreSessionGlobals(t, func() *servercfg.Snapshot { return &servercfg.Snapshot{} })

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	if err := SetSessionValue(ctx, "bad", make(chan int)); err == nil {
		t.Fatal("SetSessionValue() error = nil, want save failure for unsupported session value")
	}
}

func TestDeleteSessionValuePropagatesSaveErrors(t *testing.T) {
	restoreSessionGlobals(t, func() *servercfg.Snapshot { return &servercfg.Snapshot{} })

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	if err := EnsureSession(ctx); err != nil {
		t.Fatalf("EnsureSession() error = %v", err)
	}
	state := sessionStateFromContext(ctx)
	if state == nil || state.session == nil {
		t.Fatal("sessionStateFromContext() returned nil state")
	}
	state.session.Values["bad"] = make(chan int)

	if err := DeleteSessionValue(ctx, "missing"); err == nil {
		t.Fatal("DeleteSessionValue() error = nil, want save failure for unsupported session value")
	}
}

func TestRequestContextHelpersCloneStoredValues(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	SetRequestData(ctx, "request_id", "req-1")
	SetRequestParam(ctx, "client_id", "7")

	if got, ok := ctx.Get("request_id"); !ok || got != "req-1" {
		t.Fatalf("request data mirror = %v, %v, want req-1, true", got, ok)
	}
	if got, ok := RequestParam(ctx, "client_id"); !ok || got != "7" {
		t.Fatalf("RequestParam(client_id) = %q, %v, want 7, true", got, ok)
	}

	raw := []byte("payload")
	SetRequestRawBody(ctx, raw)
	raw[0] = 'X'

	if view := RequestRawBodyView(ctx); string(view) != "payload" {
		t.Fatalf("RequestRawBodyView() = %q, want payload", string(view))
	}

	first := RequestRawBody(ctx)
	if string(first) != "payload" {
		t.Fatalf("RequestRawBody() = %q, want payload", string(first))
	}
	first[0] = 'Y'
	second := RequestRawBody(ctx)
	if string(second) != "payload" {
		t.Fatalf("RequestRawBody() second read = %q, want payload", string(second))
	}
}

func TestCaptchaHelpers(t *testing.T) {
	if got := CaptchaImageURL("/base", "captcha-id"); got != "/base/captcha/captcha-id.png" {
		t.Fatalf("CaptchaImageURL() = %q, want /base/captcha/captcha-id.png", got)
	}
	if got := CaptchaImageURL("ops/platform/admin/", "captcha-id"); got != "/ops/platform/admin/captcha/captcha-id.png" {
		t.Fatalf("CaptchaImageURL() normalized = %q, want /ops/platform/admin/captcha/captcha-id.png", got)
	}
	if html := string(NewCaptchaHTML("/base")); !strings.Contains(html, `name="captcha_id"`) ||
		!strings.Contains(html, "/base/captcha/new") ||
		!strings.Contains(html, "/base/captcha/") {
		t.Fatalf("NewCaptchaHTML() missing expected captcha markup: %s", html)
	}
	if html := string(NewCaptchaHTML("ops/platform/admin/")); !strings.Contains(html, "/ops/platform/admin/captcha/new") ||
		!strings.Contains(html, "/ops/platform/admin/captcha/") {
		t.Fatalf("NewCaptchaHTML() normalized base missing expected captcha markup: %s", html)
	}
	if VerifyCaptcha("", "") {
		t.Fatal("VerifyCaptcha(empty) = true, want false")
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/captcha/new", CaptchaNewHandler("/base"))
	router.GET("/captcha/:id", CaptchaImageHandler())

	newResp := httptest.NewRecorder()
	router.ServeHTTP(newResp, httptest.NewRequest(http.MethodGet, "/captcha/new", nil))
	if newResp.Code != http.StatusOK {
		t.Fatalf("GET /captcha/new status = %d, want 200", newResp.Code)
	}
	var payload struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	if err := json.Unmarshal(newResp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal(captcha new) error = %v body=%s", err, newResp.Body.String())
	}
	if payload.ID == "" || !strings.HasPrefix(payload.URL, "/base/captcha/") {
		t.Fatalf("captcha new payload = %+v", payload)
	}
	if cacheControl := newResp.Header().Get("Cache-Control"); cacheControl != "no-store, no-cache, must-revalidate" {
		t.Fatalf("GET /captcha/new Cache-Control = %q, want no-store, no-cache, must-revalidate", cacheControl)
	}
	if pragma := newResp.Header().Get("Pragma"); pragma != "no-cache" {
		t.Fatalf("GET /captcha/new Pragma = %q, want no-cache", pragma)
	}

	imageResp := httptest.NewRecorder()
	router.ServeHTTP(imageResp, httptest.NewRequest(http.MethodGet, payload.URL[len("/base"):], nil))
	if imageResp.Code != http.StatusOK || imageResp.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("GET %s status=%d content-type=%q", payload.URL, imageResp.Code, imageResp.Header().Get("Content-Type"))
	}
	if cacheControl := imageResp.Header().Get("Cache-Control"); cacheControl != "no-store, no-cache, must-revalidate" {
		t.Fatalf("GET %s Cache-Control = %q, want no-store, no-cache, must-revalidate", payload.URL, cacheControl)
	}

	emptyResp := httptest.NewRecorder()
	router.ServeHTTP(emptyResp, httptest.NewRequest(http.MethodGet, "/captcha/.png", nil))
	if emptyResp.Code != http.StatusNotFound {
		t.Fatalf("GET /captcha/.png status = %d, want 404", emptyResp.Code)
	}
}

func restoreSessionGlobals(t *testing.T, provider func() *servercfg.Snapshot) {
	t.Helper()
	sessionMu.Lock()
	oldStore := sessionStore
	oldProvider := sessionConfigProvider
	sessionStore = nil
	sessionConfigProvider = provider
	sessionMu.Unlock()
	t.Cleanup(func() {
		sessionMu.Lock()
		sessionStore = oldStore
		sessionConfigProvider = oldProvider
		sessionMu.Unlock()
	})
}

func TestCaptchaImageHandlerServesKnownCaptcha(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/captcha/:id", CaptchaImageHandler())

	id := captcha.NewLen(4)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/captcha/"+id+".png", nil))
	if resp.Code != http.StatusOK || resp.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("GET generated captcha status=%d content-type=%q", resp.Code, resp.Header().Get("Content-Type"))
	}
}
