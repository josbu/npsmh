package api

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
	webservice "github.com/djylb/nps/web/service"
)

type stubManagementAuthService struct {
	authenticate func(webservice.AuthenticateInput) (*webservice.SessionIdentity, error)
	register     func(webservice.RegisterUserInput) (*webservice.RegisterUserResult, error)
}

func (s stubManagementAuthService) Authenticate(input webservice.AuthenticateInput) (*webservice.SessionIdentity, error) {
	if s.authenticate != nil {
		return s.authenticate(input)
	}
	return nil, nil
}

func (s stubManagementAuthService) RegisterUser(input webservice.RegisterUserInput) (*webservice.RegisterUserResult, error) {
	if s.register != nil {
		return s.register(input)
	}
	return nil, nil
}

type recordingManagementSystemService struct {
	registered []string
}

func (s *recordingManagementSystemService) Info() webservice.SystemInfo {
	return webservice.SystemInfo{}
}

func (s *recordingManagementSystemService) BridgeDisplay(*servercfg.Snapshot, string) webservice.BridgeDisplay {
	return webservice.BridgeDisplay{}
}

func (s *recordingManagementSystemService) RegisterManagementAccess(remoteAddr string) {
	s.registered = append(s.registered, remoteAddr)
}

type stubIPLimitVerifyKeyRepository struct {
	webservice.Repository
	getClientByVerifyKey func(string) (*file.Client, error)
	rangeClients         func(func(*file.Client) bool)
}

func (s stubIPLimitVerifyKeyRepository) SupportsGetClientByVerifyKey() bool {
	return s.getClientByVerifyKey != nil
}

func (s stubIPLimitVerifyKeyRepository) GetClientByVerifyKey(vkey string) (*file.Client, error) {
	if s.getClientByVerifyKey != nil {
		return s.getClientByVerifyKey(vkey)
	}
	return nil, file.ErrClientNotFound
}

func (s stubIPLimitVerifyKeyRepository) RangeClients(fn func(*file.Client) bool) {
	if s.rangeClients != nil {
		s.rangeClients(fn)
	}
}

type stubLoginContext struct {
	actor       *Actor
	params      map[string]string
	headers     map[string]string
	response    map[string]string
	session     map[string]interface{}
	rawBody     []byte
	remoteAddr  string
	clientIP    string
	status      int
	jsonPayload interface{}
}

func (c *stubLoginContext) BaseContext() context.Context { return context.Background() }

func (c *stubLoginContext) String(key string) string {
	value, _ := c.LookupString(key)
	return value
}

func (c *stubLoginContext) LookupString(key string) (string, bool) {
	if c == nil || c.params == nil {
		return "", false
	}
	value, ok := c.params[key]
	return value, ok
}

func (c *stubLoginContext) Int(key string, def ...int) int {
	value, ok := c.LookupString(key)
	if ok {
		if parsed, err := strconv.Atoi(value); err == nil {
			return parsed
		}
	}
	if len(def) > 0 {
		return def[0]
	}
	return 0
}

func (c *stubLoginContext) Bool(string, ...bool) bool { return false }
func (c *stubLoginContext) Method() string            { return "POST" }
func (c *stubLoginContext) Host() string              { return "" }
func (c *stubLoginContext) RemoteAddr() string        { return c.remoteAddr }
func (c *stubLoginContext) ClientIP() string          { return c.clientIP }

func (c *stubLoginContext) RequestHeader(key string) string {
	if c == nil || c.headers == nil {
		return ""
	}
	return c.headers[key]
}

func (c *stubLoginContext) RawBody() []byte {
	return append([]byte(nil), c.rawBody...)
}

func (c *stubLoginContext) SessionValue(key string) interface{} {
	if c == nil || c.session == nil {
		return nil
	}
	return c.session[key]
}

func (c *stubLoginContext) SetSessionValue(key string, value interface{}) {
	if c.session == nil {
		c.session = make(map[string]interface{})
	}
	c.session[key] = value
}

func (c *stubLoginContext) DeleteSessionValue(key string) {
	if c.session == nil {
		return
	}
	delete(c.session, key)
}

func (c *stubLoginContext) SetParam(key, value string) {
	if c.params == nil {
		c.params = make(map[string]string)
	}
	c.params[key] = value
}

func (c *stubLoginContext) RespondJSON(status int, payload interface{}) {
	c.status = status
	c.jsonPayload = payload
}

func (c *stubLoginContext) RespondString(int, string)       {}
func (c *stubLoginContext) RespondData(int, string, []byte) {}
func (c *stubLoginContext) Redirect(int, string)            {}
func (c *stubLoginContext) SetResponseHeader(key, value string) {
	if c.response == nil {
		c.response = make(map[string]string)
	}
	c.response[key] = value
}
func (c *stubLoginContext) IsWritten() bool           { return c.status != 0 }
func (c *stubLoginContext) Actor() *Actor             { return c.actor }
func (c *stubLoginContext) SetActor(actor *Actor)     { c.actor = actor }
func (c *stubLoginContext) Metadata() RequestMetadata { return RequestMetadata{} }

func resetAuthWhitelistTestDB(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	confDir := filepath.Join(root, "conf")
	if err := os.MkdirAll(confDir, 0o755); err != nil {
		t.Fatalf("create temp conf dir: %v", err)
	}
	oldDb := file.Db
	oldIndexes := file.SnapshotRuntimeIndexes()
	db := &file.DbUtils{JsonDb: file.NewJsonDb(root)}
	db.JsonDb.Global = &file.Glob{}
	file.ReplaceDb(db)
	file.ReplaceRuntimeIndexes(file.NewRuntimeIndexes())
	t.Cleanup(func() {
		file.ReplaceDb(oldDb)
		file.ReplaceRuntimeIndexes(oldIndexes)
	})
	return root
}

func TestDoLoginRegistersManagementAccessForAuthenticatedIdentities(t *testing.T) {
	testCases := []struct {
		name     string
		identity *webservice.SessionIdentity
	}{
		{
			name: "user",
			identity: (&webservice.SessionIdentity{
				Authenticated: true,
				Kind:          "user",
				SubjectID:     "user:tenant",
				Username:      "tenant",
				ClientIDs:     []int{7},
				Roles:         []string{webservice.RoleUser},
				Attributes:    map[string]string{"user_id": "7"},
			}).Normalize(),
		},
		{
			name: "client",
			identity: (&webservice.SessionIdentity{
				Authenticated: true,
				Kind:          "client",
				SubjectID:     "client:vkey:9",
				Username:      "vkey-client-9",
				ClientIDs:     []int{9},
				Roles:         []string{webservice.RoleClient},
				Attributes:    map[string]string{"client_id": "9", "login_mode": "client_vkey"},
			}).Normalize(),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			system := &recordingManagementSystemService{}
			app := NewWithOptions(&servercfg.Snapshot{
				Web: servercfg.WebConfig{Username: "admin"},
			}, Options{
				Services: &webservice.Services{
					Auth: stubManagementAuthService{
						authenticate: func(webservice.AuthenticateInput) (*webservice.SessionIdentity, error) {
							return testCase.identity, nil
						},
					},
					System: system,
				},
			})
			ctx := &stubLoginContext{
				remoteAddr: "198.51.100.5:1234",
				clientIP:   "198.51.100.5",
				headers: map[string]string{
					"X-Forwarded-For": "203.0.113.41, 198.51.100.5",
				},
			}

			identity := app.doLogin(ctx, testCase.identity.Username, "secret", "", false)
			if identity == nil || !identity.Authenticated {
				t.Fatalf("doLogin() identity = %+v, want authenticated identity", identity)
			}
			if len(system.registered) != 1 || system.registered[0] != "198.51.100.5" {
				t.Fatalf("RegisterManagementAccess() calls = %v, want [198.51.100.5] when proxy forwarding is disabled", system.registered)
			}
			if raw, _ := ctx.SessionValue(webservice.SessionIdentityKey).(string); strings.TrimSpace(raw) == "" {
				t.Fatalf("session identity = %q, want encoded session identity", raw)
			}
		})
	}
}

func TestTokenRegistersManagementAccessForAuthenticatedIdentity(t *testing.T) {
	system := &recordingManagementSystemService{}
	identity := (&webservice.SessionIdentity{
		Authenticated: true,
		Kind:          "client",
		SubjectID:     "client:vkey:9",
		Username:      "vkey-client-9",
		ClientIDs:     []int{9},
		Roles:         []string{webservice.RoleClient},
		Attributes:    map[string]string{"client_id": "9", "login_mode": "client_vkey"},
	}).Normalize()
	app := NewWithOptions(&servercfg.Snapshot{
		Web: servercfg.WebConfig{
			Username:                  "admin",
			StandaloneTokenSecret:     "standalone-secret",
			StandaloneTokenTTLSeconds: 600,
		},
	}, Options{
		Services: &webservice.Services{
			Auth: stubManagementAuthService{
				authenticate: func(webservice.AuthenticateInput) (*webservice.SessionIdentity, error) {
					return identity, nil
				},
			},
			System: system,
		},
	})
	ctx := &stubLoginContext{
		rawBody:    []byte(`{"verify_key":"vk-9"}`),
		remoteAddr: "203.0.113.9:3456",
		clientIP:   "203.0.113.9",
		headers: map[string]string{
			"X-Forwarded-For": "198.51.100.55, 203.0.113.9",
		},
	}

	app.Token(ctx)
	if ctx.status != 200 {
		t.Fatalf("Token() status = %d, want 200 payload=%#v", ctx.status, ctx.jsonPayload)
	}
	if len(system.registered) != 1 || system.registered[0] != "203.0.113.9" {
		t.Fatalf("RegisterManagementAccess() calls = %v, want [203.0.113.9] when proxy forwarding is disabled", system.registered)
	}
	response, ok := ctx.jsonPayload.(ManagementDataResponse)
	if !ok {
		t.Fatalf("Token() payload type = %T, want ManagementDataResponse", ctx.jsonPayload)
	}
	payload, ok := response.Data.(ManagementAuthTokenPayload)
	if !ok {
		t.Fatalf("Token() response data type = %T, want ManagementAuthTokenPayload", response.Data)
	}
	if payload.AccessToken == "" {
		t.Fatalf("Token() payload = %+v, want issued token", payload)
	}
}

func TestTokenRegistersManagementAccessUsingTrustedForwardedIP(t *testing.T) {
	system := &recordingManagementSystemService{}
	identity := (&webservice.SessionIdentity{
		Authenticated: true,
		Kind:          "client",
		SubjectID:     "client:vkey:9",
		Username:      "vkey-client-9",
		ClientIDs:     []int{9},
		Roles:         []string{webservice.RoleClient},
		Attributes:    map[string]string{"client_id": "9", "login_mode": "client_vkey"},
	}).Normalize()
	app := NewWithOptions(&servercfg.Snapshot{
		Web: servercfg.WebConfig{
			Username:                  "admin",
			StandaloneTokenSecret:     "standalone-secret",
			StandaloneTokenTTLSeconds: 600,
		},
		Auth: servercfg.AuthConfig{
			AllowXRealIP:    true,
			TrustedProxyIPs: "203.0.113.9",
		},
	}, Options{
		Services: &webservice.Services{
			Auth: stubManagementAuthService{
				authenticate: func(webservice.AuthenticateInput) (*webservice.SessionIdentity, error) {
					return identity, nil
				},
			},
			System: system,
		},
	})
	ctx := &stubLoginContext{
		rawBody:    []byte(`{"verify_key":"vk-9"}`),
		remoteAddr: "203.0.113.9:3456",
		clientIP:   "203.0.113.9",
		headers: map[string]string{
			"X-Forwarded-For": "198.51.100.44, 203.0.113.9",
			"X-Real-IP":       "198.51.100.45",
		},
	}

	app.Token(ctx)
	if ctx.status != 200 {
		t.Fatalf("Token() status = %d, want 200 payload=%#v", ctx.status, ctx.jsonPayload)
	}
	if len(system.registered) != 1 || system.registered[0] != "198.51.100.44" {
		t.Fatalf("RegisterManagementAccess() calls = %v, want [198.51.100.44]", system.registered)
	}
}

func TestIPLimitRegisterableClientAllowsVisibleAndAccessOnlyRuntimeClients(t *testing.T) {
	resetAuthWhitelistTestDB(t)

	makeClient := func(id int, verifyKey, remark string, noStore, noDisplay bool) *file.Client {
		client := &file.Client{
			Id:        id,
			VerifyKey: verifyKey,
			Remark:    remark,
			Status:    true,
			Cnf:       &file.Config{},
			Flow:      &file.Flow{},
			NoStore:   noStore,
			NoDisplay: noDisplay,
		}
		if err := file.GetDb().NewClient(client); err != nil {
			t.Fatalf("NewClient(%s) error = %v", verifyKey, err)
		}
		return client
	}

	visible := makeClient(1, "vk-visible", "", false, false)
	visitor := makeClient(2, "vk-visitor", "visitor_vkey", true, true)
	public := makeClient(3, "vk-public", "public_vkey", true, true)
	makeClient(4, "localproxy", "localproxy", false, false)
	makeClient(5, "vk-hidden", "", false, true)

	app := NewWithOptions(&servercfg.Snapshot{}, Options{})

	if got := app.ipLimitRegisterableClient(visible.VerifyKey); got == nil || got.Id != visible.Id {
		t.Fatalf("ipLimitRegisterableClient(%q) = %+v, want visible client %d", visible.VerifyKey, got, visible.Id)
	}
	if got := app.ipLimitRegisterableClient(visitor.VerifyKey); got == nil || got.Id != visitor.Id {
		t.Fatalf("ipLimitRegisterableClient(%q) = %+v, want visitor runtime client %d", visitor.VerifyKey, got, visitor.Id)
	}
	if got := app.ipLimitRegisterableClient(public.VerifyKey); got == nil || got.Id != public.Id {
		t.Fatalf("ipLimitRegisterableClient(%q) = %+v, want public runtime client %d", public.VerifyKey, got, public.Id)
	}

	for _, verifyKey := range []string{"localproxy", "vk-hidden", "missing"} {
		if got := app.ipLimitRegisterableClient(verifyKey); got != nil {
			t.Fatalf("ipLimitRegisterableClient(%q) = %+v, want nil", verifyKey, got)
		}
	}

	for idx := 0; idx < 2; idx++ {
		hiddenKey := fmt.Sprintf("vk-hidden-%d", idx)
		makeClient(10+idx, hiddenKey, "", true, false)
		if got := app.ipLimitRegisterableClient(hiddenKey); got != nil {
			t.Fatalf("ipLimitRegisterableClient(%q) = %+v, want nil for NoStore runtime client", hiddenKey, got)
		}
	}
}

func TestIPLimitRegisterableClientUsesVerifyKeyFastPath(t *testing.T) {
	client := &file.Client{
		Id:        7,
		VerifyKey: "vk-fast",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}
	services := webservice.Services{
		Backend: webservice.Backend{
			Repository: stubIPLimitVerifyKeyRepository{
				getClientByVerifyKey: func(vkey string) (*file.Client, error) {
					if vkey != "vk-fast" {
						t.Fatalf("GetClientByVerifyKey(%q), want vk-fast", vkey)
					}
					return client, nil
				},
				rangeClients: func(func(*file.Client) bool) {
					t.Fatal("RangeClients() should not be used when GetClientByVerifyKey is available")
				},
			},
		},
	}
	app := NewWithOptions(&servercfg.Snapshot{}, Options{Services: &services})

	got := app.ipLimitRegisterableClient("vk-fast")
	if got == nil || got.Id != client.Id {
		t.Fatalf("ipLimitRegisterableClient(vk-fast) = %+v, want client %d", got, client.Id)
	}
}

func TestIPLimitRegisterableClientFallsBackToRangeForDuplicateVerifyKeyWhenFastPathHitsBlockedClient(t *testing.T) {
	blocked := &file.Client{
		Id:        7,
		VerifyKey: "vk-shared",
		Status:    true,
		NoStore:   true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}
	allowed := &file.Client{
		Id:        8,
		VerifyKey: "vk-shared",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}
	rangeCalls := 0
	services := webservice.Services{
		Backend: webservice.Backend{
			Repository: stubIPLimitVerifyKeyRepository{
				getClientByVerifyKey: func(vkey string) (*file.Client, error) {
					if vkey != "vk-shared" {
						t.Fatalf("GetClientByVerifyKey(%q), want vk-shared", vkey)
					}
					return blocked, nil
				},
				rangeClients: func(fn func(*file.Client) bool) {
					rangeCalls++
					for _, client := range []*file.Client{blocked, allowed} {
						if !fn(client) {
							return
						}
					}
				},
			},
		},
	}
	app := NewWithOptions(&servercfg.Snapshot{}, Options{Services: &services})

	got := app.ipLimitRegisterableClient("vk-shared")
	if rangeCalls != 1 {
		t.Fatalf("RangeClients() called %d times, want 1 fallback pass", rangeCalls)
	}
	if got == nil || got.Id != allowed.Id {
		t.Fatalf("ipLimitRegisterableClient(vk-shared) = %+v, want client %d", got, allowed.Id)
	}
}

func TestIPLimitRegisterableClientFallsBackAfterVerifyKeyLookupError(t *testing.T) {
	lookupErr := errors.New("verify key lookup failed")
	allowed := &file.Client{
		Id:        9,
		VerifyKey: "vk-fallback",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}
	rangeCalls := 0
	services := webservice.Services{
		Backend: webservice.Backend{
			Repository: stubIPLimitVerifyKeyRepository{
				getClientByVerifyKey: func(vkey string) (*file.Client, error) {
					if vkey != "vk-fallback" {
						t.Fatalf("GetClientByVerifyKey(%q), want vk-fallback", vkey)
					}
					return nil, lookupErr
				},
				rangeClients: func(fn func(*file.Client) bool) {
					rangeCalls++
					fn(allowed)
				},
			},
		},
	}
	app := NewWithOptions(&servercfg.Snapshot{}, Options{Services: &services})

	got := app.ipLimitRegisterableClient("vk-fallback")
	if rangeCalls != 1 {
		t.Fatalf("RangeClients() called %d times, want 1 fallback pass", rangeCalls)
	}
	if got == nil || got.Id != allowed.Id {
		t.Fatalf("ipLimitRegisterableClient(vk-fallback) = %+v, want client %d", got, allowed.Id)
	}
}
