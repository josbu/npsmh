package routers

import (
	"context"
	"net/http"
	"testing"

	webapi "github.com/djylb/nps/web/api"
	webservice "github.com/djylb/nps/web/service"
)

type wsSessionResolver struct{}

func (wsSessionResolver) NormalizePrincipal(principal webservice.Principal) webservice.Principal {
	return webservice.DefaultPermissionResolver().NormalizePrincipal(principal)
}

func (wsSessionResolver) NormalizeIdentity(identity *webservice.SessionIdentity) *webservice.SessionIdentity {
	if identity == nil {
		return nil
	}
	cloned := *identity
	if cloned.Username == "mapped" {
		cloned.ClientIDs = []int{42}
		cloned.Attributes = map[string]string{"mapped": "true"}
	}
	return webservice.DefaultPermissionResolver().NormalizeIdentity(&cloned)
}

func (wsSessionResolver) KnownRoles() []string {
	return webservice.DefaultPermissionResolver().KnownRoles()
}

func (wsSessionResolver) KnownPermissions() []string {
	return webservice.DefaultPermissionResolver().KnownPermissions()
}

func (wsSessionResolver) PermissionCatalog() map[string][]string {
	return webservice.DefaultPermissionResolver().PermissionCatalog()
}

func TestWSAPIContextHandlesNilState(t *testing.T) {
	var nilCtx *wsAPIContext
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
	nilCtx.MutateSession(func(editor webapi.SessionEditor) {
		editor.Set("k", "v")
		editor.Delete("k")
	})
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

func TestWSAPIContextHandlesZeroValueState(t *testing.T) {
	ctx := &wsAPIContext{}
	if got := ctx.BaseContext(); got == nil {
		t.Fatal("BaseContext() should fall back to non-nil background context")
	}
	if got := ctx.Method(); got != http.MethodGet {
		t.Fatalf("Method() = %q, want %q", got, http.MethodGet)
	}

	ctx.SetSessionValue("answer", 42)
	if got := ctx.SessionValue("answer"); got != 42 {
		t.Fatalf("SessionValue() = %v, want 42", got)
	}
	ctx.MutateSession(func(editor webapi.SessionEditor) {
		editor.Set("mutated", "ok")
	})
	if got := ctx.SessionValue("mutated"); got != "ok" {
		t.Fatalf("mutated SessionValue() = %v, want ok", got)
	}
	ctx.DeleteSessionValue("answer")
	if got := ctx.SessionValue("answer"); got != nil {
		t.Fatalf("deleted SessionValue() = %v, want nil", got)
	}

	ctx.SetParam(" client_id ", " 7 ")
	if got := ctx.String("client_id"); got != "7" {
		t.Fatalf("String(client_id) = %q, want 7", got)
	}

	ctx.SetResponseHeader("X-Test", "1")
	if got := ctx.responseHeader["X-Test"]; got != "1" {
		t.Fatalf("responseHeader[X-Test] = %q, want 1", got)
	}

	ctx.SetActor(webapi.UserActor("alice", []int{3, 5}))
	assertSessionIdentityActor(t, ctx, "alice", "user", []int{3, 5})
	assertNoLegacyWSActorSessionFields(t, ctx)

	ctx.RespondString(http.StatusAccepted, "ok")
	if !ctx.IsWritten() {
		t.Fatal("IsWritten() = false, want true")
	}
	if ctx.status != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", ctx.status, http.StatusAccepted)
	}
	if got := ctx.contentType; got != "text/plain; charset=utf-8" {
		t.Fatalf("contentType = %q, want text/plain; charset=utf-8", got)
	}
	if got := ctx.responseHeader["Content-Type"]; got != "text/plain; charset=utf-8" {
		t.Fatalf("responseHeader[Content-Type] = %q, want text/plain; charset=utf-8", got)
	}
}

func TestWSAPIContextUsesProvidedBaseContext(t *testing.T) {
	parent := context.WithValue(context.Background(), "k", "v")
	ctx, err := newWSAPIContextFromBase(
		nodeWSDispatchBase{Context: parent},
		nil,
		nodeWSFrame{Method: "POST"},
	)
	if err != nil {
		t.Fatalf("newWSAPIContextFromBase() error = %v", err)
	}
	if got := ctx.BaseContext().Value("k"); got != "v" {
		t.Fatalf("BaseContext().Value() = %v, want v", got)
	}
	if got := ctx.Method(); got != http.MethodPost {
		t.Fatalf("Method() = %q, want %q", got, http.MethodPost)
	}
}

func TestWSAPIContextSetActorSyncsSessionIdentityFields(t *testing.T) {
	ctx, err := newWSAPIContextFromBase(
		nodeWSDispatchBase{},
		webapi.UserActor("alice", []int{7, 9}),
		nodeWSFrame{Method: "GET"},
	)
	if err != nil {
		t.Fatalf("newWSAPIContextFromBase() error = %v", err)
	}

	assertSessionIdentityActor(t, ctx, "alice", "user", []int{7, 9})
	assertNoLegacyWSActorSessionFields(t, ctx)

	ctx.SetActor(webapi.AdminActor("root"))
	assertSessionIdentityActor(t, ctx, "root", "admin", nil)
	assertNoLegacyWSActorSessionFields(t, ctx)

	ctx.SetActor(webapi.AnonymousActor())
	if got := ctx.SessionValue(webservice.SessionIdentityKey); got != nil {
		t.Fatalf("anonymous session_identity = %#v, want nil", got)
	}
	assertNoLegacyWSActorSessionFields(t, ctx)
}

func TestWSAPIContextUsesResolverForSessionIdentityPersistence(t *testing.T) {
	ctx, err := newWSAPIContextFromBase(
		nodeWSDispatchBase{Resolver: wsSessionResolver{}},
		webapi.UserActor("mapped", []int{7, 9}),
		nodeWSFrame{Method: "GET"},
	)
	if err != nil {
		t.Fatalf("newWSAPIContextFromBase() error = %v", err)
	}

	assertSessionIdentityActor(t, ctx, "mapped", "user", []int{42})
	if raw, _ := ctx.SessionValue(webservice.SessionIdentityKey).(string); raw == "" {
		t.Fatal("session_identity should be present")
	}
	ctx.SetActor(webapi.UserActor("mapped", []int{1}))
	assertSessionIdentityActor(t, ctx, "mapped", "user", []int{42})
}

func TestBuildNodeWSResponseFrameUsesTrustedJSONResponseBody(t *testing.T) {
	ctx := &wsAPIContext{}
	ctx.RespondJSON(http.StatusAccepted, struct {
		OK string `json:"ok"`
	}{OK: "1"})

	frame := buildNodeWSResponseFrame(nodeWSFrame{Type: "response"}, ctx)
	if frame.Status != http.StatusAccepted {
		t.Fatalf("buildNodeWSResponseFrame() status = %d, want %d", frame.Status, http.StatusAccepted)
	}
	if frame.Encoding != "" {
		t.Fatalf("buildNodeWSResponseFrame() encoding = %q, want empty", frame.Encoding)
	}
	if got := string(frame.Body); got != `{"ok":"1"}` {
		t.Fatalf("buildNodeWSResponseFrame() body = %s, want %s", got, `{"ok":"1"}`)
	}
	if got := frame.Headers["Content-Type"]; got != "application/json; charset=utf-8" {
		t.Fatalf("buildNodeWSResponseFrame() Content-Type = %q, want application/json; charset=utf-8", got)
	}
}

func TestBuildNodeWSResponseFrameEncodesTextAndBinaryResponseBodies(t *testing.T) {
	textCtx := &wsAPIContext{}
	textCtx.RespondData(http.StatusOK, "text/plain", []byte("ok"))
	textFrame := buildNodeWSResponseFrame(nodeWSFrame{Type: "response"}, textCtx)
	if textFrame.Encoding != "text" {
		t.Fatalf("text frame encoding = %q, want text", textFrame.Encoding)
	}
	if got := string(textFrame.Body); got != `"ok"` {
		t.Fatalf("text frame body = %s, want %s", got, `"ok"`)
	}

	binaryCtx := &wsAPIContext{}
	binaryCtx.RespondData(http.StatusOK, "image/png", []byte{0x01, 0x02})
	binaryFrame := buildNodeWSResponseFrame(nodeWSFrame{Type: "response"}, binaryCtx)
	if binaryFrame.Encoding != "base64" {
		t.Fatalf("binary frame encoding = %q, want base64", binaryFrame.Encoding)
	}
	if got := string(binaryFrame.Body); got != `"AQI="` {
		t.Fatalf("binary frame body = %s, want %s", got, `"AQI="`)
	}
}

func TestBuildNodeWSResponseFramePreservesValidJSONRespondDataFallback(t *testing.T) {
	ctx := &wsAPIContext{}
	ctx.RespondData(http.StatusOK, "application/json", []byte(`{"ok":true}`))

	frame := buildNodeWSResponseFrame(nodeWSFrame{Type: "response"}, ctx)
	if frame.Encoding != "" {
		t.Fatalf("fallback JSON frame encoding = %q, want empty", frame.Encoding)
	}
	if got := string(frame.Body); got != `{"ok":true}` {
		t.Fatalf("fallback JSON frame body = %s, want %s", got, `{"ok":true}`)
	}
}

func TestBuildNodeWSRenderedFrameUsesTrustedJSONPayloadMode(t *testing.T) {
	frame := buildNodeWSRenderedFrame(nodeWSFrame{Headers: map[string]string{}}, nodeRenderedEventSinkPayload{
		ContentType: "application/json",
		Body:        []byte(`{"ok":true}`),
		BodyMode:    wsResponseBodyModeJSON,
	})
	if frame.Encoding != "" {
		t.Fatalf("trusted payload encoding = %q, want empty", frame.Encoding)
	}
	if got := string(frame.Body); got != `{"ok":true}` {
		t.Fatalf("trusted payload body = %s, want %s", got, `{"ok":true}`)
	}
}

func TestBuildNodeWSRenderedFramePreservesCustomJSONFallback(t *testing.T) {
	frame := buildNodeWSRenderedFrame(nodeWSFrame{Headers: map[string]string{}}, nodeRenderedEventSinkPayload{
		ContentType: "application/json",
		Body:        []byte(`not-json`),
		BodyMode:    wsResponseBodyModeUnknown,
	})
	if frame.Encoding != "text" {
		t.Fatalf("custom fallback encoding = %q, want text", frame.Encoding)
	}
	if got := string(frame.Body); got != `"not-json"` {
		t.Fatalf("custom fallback body = %s, want %s", got, `"not-json"`)
	}
}

func assertSessionIdentityActor(t *testing.T, ctx *wsAPIContext, username, kind string, clientIDs []int) {
	t.Helper()
	raw, _ := ctx.SessionValue(webservice.SessionIdentityKey).(string)
	if raw == "" {
		t.Fatal("session_identity should be present")
	}
	identity, err := webservice.ParseSessionIdentityWithResolver(raw, webservice.DefaultPermissionResolver())
	if err != nil {
		t.Fatalf("ParseSessionIdentityWithResolver() error = %v", err)
	}
	if identity == nil || !identity.Authenticated {
		t.Fatalf("parsed identity = %+v, want authenticated identity", identity)
	}
	if identity.Username != username || identity.Kind != kind {
		t.Fatalf("parsed identity core = %+v, want username=%s kind=%s", identity, username, kind)
	}
	if identity.Provider != "ws" {
		t.Fatalf("parsed identity provider = %q, want ws", identity.Provider)
	}
	if len(identity.ClientIDs) != len(clientIDs) {
		t.Fatalf("parsed identity client ids = %#v, want %#v", identity.ClientIDs, clientIDs)
	}
	for index, clientID := range clientIDs {
		if identity.ClientIDs[index] != clientID {
			t.Fatalf("parsed identity client ids = %#v, want %#v", identity.ClientIDs, clientIDs)
		}
	}
}

func assertNoLegacyWSActorSessionFields(t *testing.T, ctx *wsAPIContext) {
	t.Helper()
	for _, key := range []string{"auth", "isAdmin", "username", "clientId", "clientIds"} {
		if got := ctx.SessionValue(key); got != nil {
			t.Fatalf("%s = %#v, want nil", key, got)
		}
	}
}
