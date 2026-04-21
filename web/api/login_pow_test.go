package api

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/servercfg"
	webservice "github.com/djylb/nps/web/service"
)

type stubLoginPolicyService struct {
	settings    webservice.LoginPolicySettings
	allowIP     bool
	ipBanned    bool
	userBanned  bool
	allowChecks []string
	failures    []string
	removed     []string
}

func (s *stubLoginPolicyService) Settings() webservice.LoginPolicySettings {
	return s.settings
}

func (s *stubLoginPolicyService) AllowsIP(ip string) bool {
	if s == nil {
		return true
	}
	s.allowChecks = append(s.allowChecks, ip)
	if !s.allowIP {
		return false
	}
	return true
}

func (s *stubLoginPolicyService) IsIPBanned(string) bool {
	return s != nil && s.ipBanned
}

func (s *stubLoginPolicyService) IsUserBanned(string) bool {
	return s != nil && s.userBanned
}

func (s *stubLoginPolicyService) RecordFailure(key string, explicit bool) {
	if s == nil || !explicit || key == "" {
		return
	}
	s.failures = append(s.failures, key)
}

func (s *stubLoginPolicyService) RemoveBan(key string) bool {
	if s == nil || key == "" {
		return false
	}
	s.removed = append(s.removed, key)
	return true
}

func (s *stubLoginPolicyService) RemoveAllBans() {}

func (s *stubLoginPolicyService) Clean(bool) {}

func (s *stubLoginPolicyService) BanList() []webservice.LoginBanRecord { return nil }

func TestCreateSessionRequiresPoWWhenSecureModeLoginBanActive(t *testing.T) {
	policy := &stubLoginPolicyService{
		settings: webservice.LoginPolicySettings{MaxLoginBody: 1024},
		allowIP:  true,
		ipBanned: true,
	}
	system := &recordingManagementSystemService{}
	authCalls := 0
	app := NewWithOptions(&servercfg.Snapshot{
		Web: servercfg.WebConfig{Username: "admin"},
		Security: servercfg.SecurityConfig{
			SecureMode: true,
			PoWBits:    4,
		},
	}, Options{
		Services: &webservice.Services{
			Auth: stubManagementAuthService{
				authenticate: func(webservice.AuthenticateInput) (*webservice.SessionIdentity, error) {
					authCalls++
					return nil, errors.New("unexpected authenticate call")
				},
			},
			LoginPolicy: policy,
			System:      system,
		},
	})
	ctx := &stubLoginContext{
		rawBody:    []byte(`{"username":"admin","password":"secret"}`),
		remoteAddr: "198.51.100.5:1234",
		clientIP:   "198.51.100.5",
	}

	app.CreateSession(ctx)

	if ctx.status != 429 {
		t.Fatalf("CreateSession() status = %d, want 429 payload=%#v", ctx.status, ctx.jsonPayload)
	}
	response, ok := ctx.jsonPayload.(ManagementErrorResponse)
	if !ok {
		t.Fatalf("CreateSession() payload type = %T, want ManagementErrorResponse", ctx.jsonPayload)
	}
	if response.Error.Code != "pow_required" {
		t.Fatalf("CreateSession() error code = %q, want pow_required", response.Error.Code)
	}
	if got := response.Error.Details["pow_bits"]; got != 4 {
		t.Fatalf("CreateSession() pow_bits detail = %#v, want 4", got)
	}
	if authCalls != 0 {
		t.Fatalf("Authenticate() calls = %d, want 0 when PoW is missing", authCalls)
	}
	if len(system.registered) != 0 {
		t.Fatalf("RegisterManagementAccess() calls = %v, want none", system.registered)
	}
	if want := []string{"198.51.100.5"}; !reflect.DeepEqual(policy.failures, want) {
		t.Fatalf("RecordFailure() keys = %v, want %v", policy.failures, want)
	}
}

func TestCreateSessionAcceptsPoWWhenSecureModeLoginBanActive(t *testing.T) {
	policy := &stubLoginPolicyService{
		settings:   webservice.LoginPolicySettings{MaxLoginBody: 1024},
		allowIP:    true,
		userBanned: true,
	}
	system := &recordingManagementSystemService{}
	identity := (&webservice.SessionIdentity{
		Authenticated: true,
		Kind:          "user",
		Provider:      "local",
		SubjectID:     "user:tenant",
		Username:      "tenant",
		ClientIDs:     []int{7},
		Roles:         []string{webservice.RoleUser},
		Attributes:    map[string]string{"login_mode": "password", "user_id": "7"},
	}).Normalize()
	authCalls := 0
	app := NewWithOptions(&servercfg.Snapshot{
		Web: servercfg.WebConfig{Username: "admin"},
		Security: servercfg.SecurityConfig{
			SecureMode: true,
			PoWBits:    4,
		},
	}, Options{
		Services: &webservice.Services{
			Auth: stubManagementAuthService{
				authenticate: func(input webservice.AuthenticateInput) (*webservice.SessionIdentity, error) {
					authCalls++
					if input.Username != "tenant" || input.Password != "secret" {
						t.Fatalf("Authenticate() input = %+v, want tenant/secret", input)
					}
					return identity, nil
				},
			},
			LoginPolicy: policy,
			System:      system,
		},
	})
	nonce := findSessionLoginPoWNonce(t, 4, "tenant\nsecret\n")
	ctx := &stubLoginContext{
		rawBody:    []byte(`{"username":"tenant","password":"secret","pow_nonce":"` + nonce + `","pow_bits":4}`),
		remoteAddr: "203.0.113.7:1234",
		clientIP:   "203.0.113.7",
	}

	app.CreateSession(ctx)

	if ctx.status != 200 {
		t.Fatalf("CreateSession() status = %d, want 200 payload=%#v", ctx.status, ctx.jsonPayload)
	}
	if authCalls != 1 {
		t.Fatalf("Authenticate() calls = %d, want 1", authCalls)
	}
	if want := []string{"203.0.113.7"}; !reflect.DeepEqual(system.registered, want) {
		t.Fatalf("RegisterManagementAccess() calls = %v, want %v", system.registered, want)
	}
	if want := []string{"203.0.113.7", "tenant"}; !reflect.DeepEqual(policy.removed, want) {
		t.Fatalf("RemoveBan() keys = %v, want %v", policy.removed, want)
	}
	response, ok := ctx.jsonPayload.(ManagementDataResponse)
	if !ok {
		t.Fatalf("CreateSession() payload type = %T, want ManagementDataResponse", ctx.jsonPayload)
	}
	payload, ok := response.Data.(ManagementAuthSessionPayload)
	if !ok {
		t.Fatalf("CreateSession() data type = %T, want ManagementAuthSessionPayload", response.Data)
	}
	if !payload.Session.Authenticated || payload.Session.Username != "tenant" {
		t.Fatalf("CreateSession() session payload = %+v, want authenticated tenant session", payload.Session)
	}
	if got := ctx.response["Cache-Control"]; got != "no-store" {
		t.Fatalf("CreateSession() Cache-Control = %q, want no-store", got)
	}
	if got := ctx.response["Pragma"]; got != "no-cache" {
		t.Fatalf("CreateSession() Pragma = %q, want no-cache", got)
	}
}

func TestCreateSessionReturnsInternalErrorWhenAuthBackendFails(t *testing.T) {
	policy := &stubLoginPolicyService{
		settings: webservice.LoginPolicySettings{MaxLoginBody: 1024},
		allowIP:  true,
	}
	system := &recordingManagementSystemService{}
	backendErr := errors.New("auth backend failed")
	app := NewWithOptions(&servercfg.Snapshot{
		Web: servercfg.WebConfig{Username: "admin"},
	}, Options{
		Services: &webservice.Services{
			Auth: stubManagementAuthService{
				authenticate: func(webservice.AuthenticateInput) (*webservice.SessionIdentity, error) {
					return nil, backendErr
				},
			},
			LoginPolicy: policy,
			System:      system,
		},
	})
	ctx := &stubLoginContext{
		rawBody:    []byte(`{"username":"tenant","password":"secret"}`),
		remoteAddr: "198.51.100.21:1234",
		clientIP:   "198.51.100.21",
	}

	app.CreateSession(ctx)

	if ctx.status != 500 {
		t.Fatalf("CreateSession() status = %d, want 500 payload=%#v", ctx.status, ctx.jsonPayload)
	}
	response, ok := ctx.jsonPayload.(ManagementErrorResponse)
	if !ok {
		t.Fatalf("CreateSession() payload type = %T, want ManagementErrorResponse", ctx.jsonPayload)
	}
	if response.Error.Code != "request_failed" || response.Error.Message != backendErr.Error() {
		t.Fatalf("CreateSession() error = %+v, want request_failed/%q", response.Error, backendErr.Error())
	}
	if len(policy.failures) != 0 {
		t.Fatalf("RecordFailure() keys = %v, want none for backend error", policy.failures)
	}
	if len(system.registered) != 0 {
		t.Fatalf("RegisterManagementAccess() calls = %v, want none", system.registered)
	}
}

func TestTokenDoesNotRequirePoWWhenSessionPoWIsEnabled(t *testing.T) {
	policy := &stubLoginPolicyService{
		settings:   webservice.LoginPolicySettings{MaxLoginBody: 1024},
		allowIP:    true,
		ipBanned:   true,
		userBanned: true,
	}
	system := &recordingManagementSystemService{}
	identity := (&webservice.SessionIdentity{
		Authenticated: true,
		Kind:          "client",
		Provider:      "local",
		SubjectID:     "client:vkey:9",
		Username:      "vkey-client-9",
		ClientIDs:     []int{9},
		Roles:         []string{webservice.RoleClient},
		Attributes:    map[string]string{"login_mode": "client_vkey", "client_id": "9"},
	}).Normalize()
	app := NewWithOptions(&servercfg.Snapshot{
		Web: servercfg.WebConfig{
			Username:                  "admin",
			StandaloneTokenSecret:     "standalone-secret",
			StandaloneTokenTTLSeconds: 600,
		},
		Security: servercfg.SecurityConfig{
			SecureMode: true,
			PoWBits:    20,
		},
	}, Options{
		Services: &webservice.Services{
			Auth: stubManagementAuthService{
				authenticate: func(webservice.AuthenticateInput) (*webservice.SessionIdentity, error) {
					return identity, nil
				},
			},
			LoginPolicy: policy,
			System:      system,
		},
	})
	ctx := &stubLoginContext{
		rawBody:    []byte(`{"verify_key":"vk-9"}`),
		remoteAddr: "203.0.113.9:3456",
		clientIP:   "203.0.113.9",
	}

	app.Token(ctx)

	if ctx.status != 200 {
		t.Fatalf("Token() status = %d, want 200 payload=%#v", ctx.status, ctx.jsonPayload)
	}
	if want := []string{"203.0.113.9"}; !reflect.DeepEqual(system.registered, want) {
		t.Fatalf("RegisterManagementAccess() calls = %v, want %v", system.registered, want)
	}
}

func TestTokenReturnsInternalErrorWhenAuthBackendFails(t *testing.T) {
	policy := &stubLoginPolicyService{
		settings: webservice.LoginPolicySettings{MaxLoginBody: 1024},
		allowIP:  true,
	}
	system := &recordingManagementSystemService{}
	backendErr := errors.New("auth backend failed")
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
					return nil, backendErr
				},
			},
			LoginPolicy: policy,
			System:      system,
		},
	})
	ctx := &stubLoginContext{
		rawBody:    []byte(`{"verify_key":"vk-9"}`),
		remoteAddr: "198.51.100.22:1234",
		clientIP:   "198.51.100.22",
	}

	app.Token(ctx)

	if ctx.status != 500 {
		t.Fatalf("Token() status = %d, want 500 payload=%#v", ctx.status, ctx.jsonPayload)
	}
	response, ok := ctx.jsonPayload.(ManagementErrorResponse)
	if !ok {
		t.Fatalf("Token() payload type = %T, want ManagementErrorResponse", ctx.jsonPayload)
	}
	if response.Error.Code != "request_failed" || response.Error.Message != backendErr.Error() {
		t.Fatalf("Token() error = %+v, want request_failed/%q", response.Error, backendErr.Error())
	}
	if len(system.registered) != 0 {
		t.Fatalf("RegisterManagementAccess() calls = %v, want none", system.registered)
	}
}

func TestTokenBlockedByStaticLoginACL(t *testing.T) {
	policy := &stubLoginPolicyService{
		settings: webservice.LoginPolicySettings{MaxLoginBody: 1024},
		allowIP:  false,
	}
	system := &recordingManagementSystemService{}
	authCalls := 0
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
					authCalls++
					return nil, errors.New("unexpected authenticate call")
				},
			},
			LoginPolicy: policy,
			System:      system,
		},
	})
	ctx := &stubLoginContext{
		rawBody:    []byte(`{"verify_key":"vk-9"}`),
		remoteAddr: "198.51.100.20:3456",
		clientIP:   "198.51.100.20",
	}

	app.Token(ctx)

	if ctx.status != 403 {
		t.Fatalf("Token() status = %d, want 403 payload=%#v", ctx.status, ctx.jsonPayload)
	}
	response, ok := ctx.jsonPayload.(ManagementErrorResponse)
	if !ok {
		t.Fatalf("Token() payload type = %T, want ManagementErrorResponse", ctx.jsonPayload)
	}
	if response.Error.Code != "login_access_denied" {
		t.Fatalf("Token() error code = %q, want login_access_denied", response.Error.Code)
	}
	if authCalls != 0 {
		t.Fatalf("Authenticate() calls = %d, want 0 when ACL denies token issuance", authCalls)
	}
	if want := []string{"198.51.100.20"}; !reflect.DeepEqual(policy.allowChecks, want) {
		t.Fatalf("AllowsIP() checks = %v, want %v", policy.allowChecks, want)
	}
	if len(system.registered) != 0 {
		t.Fatalf("RegisterManagementAccess() calls = %v, want none", system.registered)
	}
	if got := ctx.response["Cache-Control"]; got != "no-store" {
		t.Fatalf("Token() Cache-Control = %q, want no-store", got)
	}
	if got := ctx.response["Pragma"]; got != "no-cache" {
		t.Fatalf("Token() Pragma = %q, want no-cache", got)
	}
}

func TestTokenChecksStaticLoginACLUsingTrustedForwardedIP(t *testing.T) {
	policy := &stubLoginPolicyService{
		settings: webservice.LoginPolicySettings{MaxLoginBody: 1024},
		allowIP:  true,
	}
	system := &recordingManagementSystemService{}
	identity := (&webservice.SessionIdentity{
		Authenticated: true,
		Kind:          "client",
		Provider:      "local",
		SubjectID:     "client:vkey:9",
		Username:      "vkey-client-9",
		ClientIDs:     []int{9},
		Roles:         []string{webservice.RoleClient},
		Attributes:    map[string]string{"login_mode": "client_vkey", "client_id": "9"},
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
			LoginPolicy: policy,
			System:      system,
		},
	})
	ctx := &stubLoginContext{
		rawBody:    []byte(`{"verify_key":"vk-9"}`),
		remoteAddr: "203.0.113.9:3456",
		clientIP:   "203.0.113.9",
		headers: map[string]string{
			"X-Forwarded-For": "198.51.100.66, 203.0.113.9",
			"X-Real-IP":       "198.51.100.67",
		},
	}

	app.Token(ctx)

	if ctx.status != 200 {
		t.Fatalf("Token() status = %d, want 200 payload=%#v", ctx.status, ctx.jsonPayload)
	}
	if want := []string{"198.51.100.66"}; !reflect.DeepEqual(policy.allowChecks, want) {
		t.Fatalf("AllowsIP() checks = %v, want %v", policy.allowChecks, want)
	}
	if want := []string{"198.51.100.66"}; !reflect.DeepEqual(system.registered, want) {
		t.Fatalf("RegisterManagementAccess() calls = %v, want %v", system.registered, want)
	}
}

func TestRegisterAuthUserBlockedByStaticLoginACL(t *testing.T) {
	policy := &stubLoginPolicyService{
		settings: webservice.LoginPolicySettings{MaxLoginBody: 1024},
		allowIP:  false,
	}
	registerCalls := 0
	app := NewWithOptions(&servercfg.Snapshot{
		Web: servercfg.WebConfig{Username: "admin"},
		Feature: servercfg.FeatureConfig{
			AllowUserRegister: true,
		},
	}, Options{
		Services: &webservice.Services{
			Auth: stubManagementAuthService{
				register: func(webservice.RegisterUserInput) (*webservice.RegisterUserResult, error) {
					registerCalls++
					return &webservice.RegisterUserResult{}, nil
				},
			},
			LoginPolicy: policy,
		},
	})
	ctx := &stubLoginContext{
		rawBody:    []byte(`{"username":"demo","password":"secret"}`),
		remoteAddr: "198.51.100.60:1234",
		clientIP:   "198.51.100.60",
	}

	app.RegisterAuthUser(ctx)

	if ctx.status != 403 {
		t.Fatalf("RegisterAuthUser() status = %d, want 403 payload=%#v", ctx.status, ctx.jsonPayload)
	}
	response, ok := ctx.jsonPayload.(ManagementErrorResponse)
	if !ok {
		t.Fatalf("RegisterAuthUser() payload type = %T, want ManagementErrorResponse", ctx.jsonPayload)
	}
	if response.Error.Code != "registration_access_denied" {
		t.Fatalf("RegisterAuthUser() error code = %q, want registration_access_denied", response.Error.Code)
	}
	if registerCalls != 0 {
		t.Fatalf("RegisterUser() calls = %d, want 0", registerCalls)
	}
	if got := ctx.response["Cache-Control"]; got != "no-store" {
		t.Fatalf("RegisterAuthUser() Cache-Control = %q, want no-store", got)
	}
	if got := ctx.response["Pragma"]; got != "no-cache" {
		t.Fatalf("RegisterAuthUser() Pragma = %q, want no-cache", got)
	}
}

func TestRegisterAuthUserSetsNoStoreHeadersOnSuccess(t *testing.T) {
	app := NewWithOptions(&servercfg.Snapshot{
		Feature: servercfg.FeatureConfig{
			AllowUserRegister: true,
		},
	}, Options{
		Services: &webservice.Services{
			Auth: stubManagementAuthService{
				register: func(input webservice.RegisterUserInput) (*webservice.RegisterUserResult, error) {
					if strings.TrimSpace(input.Username) != "tenant" || input.Password != "secret" {
						t.Fatalf("RegisterUser() input = %+v, want tenant/secret", input)
					}
					return &webservice.RegisterUserResult{
						SubjectID: "user:tenant",
						Username:  "tenant",
						ClientIDs: []int{11},
					}, nil
				},
			},
			LoginPolicy: &stubLoginPolicyService{
				settings: webservice.LoginPolicySettings{MaxLoginBody: 1024},
				allowIP:  true,
			},
		},
	})
	ctx := &stubLoginContext{
		rawBody:    []byte(`{"username":"tenant","password":"secret"}`),
		remoteAddr: "198.51.100.61:1234",
		clientIP:   "198.51.100.61",
	}

	app.RegisterAuthUser(ctx)

	if ctx.status != 200 {
		t.Fatalf("RegisterAuthUser() status = %d, want 200 payload=%#v", ctx.status, ctx.jsonPayload)
	}
	if got := ctx.response["Cache-Control"]; got != "no-store" {
		t.Fatalf("RegisterAuthUser() Cache-Control = %q, want no-store", got)
	}
	if got := ctx.response["Pragma"]; got != "no-cache" {
		t.Fatalf("RegisterAuthUser() Pragma = %q, want no-cache", got)
	}
}

func TestManagementDiscoveryPublishesSessionPoWCapabilityWhenSecureModeEnabled(t *testing.T) {
	app := NewWithOptions(&servercfg.Snapshot{
		App: servercfg.AppConfig{Name: "node-a"},
		Security: servercfg.SecurityConfig{
			SecureMode: true,
			PoWBits:    20,
		},
	}, Options{})

	payload := app.managementDiscoveryPayload(stubAppContext{actor: AnonymousActor()})
	if !payload.Auth.PoWEnabled {
		t.Fatalf("managementDiscoveryPayload().Auth.PoWEnabled = false, want true when secure_mode and pow_bits are enabled")
	}
}

func TestCreateSessionInvalidCaptchaOnlyBansIP(t *testing.T) {
	policy := &stubLoginPolicyService{
		settings: webservice.LoginPolicySettings{MaxLoginBody: 1024},
		allowIP:  true,
	}
	authCalls := 0
	app := NewWithOptions(&servercfg.Snapshot{
		Web: servercfg.WebConfig{Username: "admin"},
		Feature: servercfg.FeatureConfig{
			OpenCaptcha: true,
		},
	}, Options{
		Services: &webservice.Services{
			Auth: stubManagementAuthService{
				authenticate: func(webservice.AuthenticateInput) (*webservice.SessionIdentity, error) {
					authCalls++
					return nil, errors.New("unexpected authenticate call")
				},
			},
			LoginPolicy: policy,
		},
	})
	ctx := &stubLoginContext{
		rawBody:    []byte(`{"username":"tenant","password":"secret","captcha_id":"","captcha_answer":""}`),
		remoteAddr: "198.51.100.8:1234",
		clientIP:   "198.51.100.8",
	}

	app.CreateSession(ctx)

	if ctx.status != 400 {
		t.Fatalf("CreateSession() status = %d, want 400 payload=%#v", ctx.status, ctx.jsonPayload)
	}
	response, ok := ctx.jsonPayload.(ManagementErrorResponse)
	if !ok {
		t.Fatalf("CreateSession() payload type = %T, want ManagementErrorResponse", ctx.jsonPayload)
	}
	if response.Error.Code != "invalid_captcha" {
		t.Fatalf("CreateSession() error code = %q, want invalid_captcha", response.Error.Code)
	}
	if authCalls != 0 {
		t.Fatalf("Authenticate() calls = %d, want 0 when captcha is invalid", authCalls)
	}
	if want := []string{"198.51.100.8"}; !reflect.DeepEqual(policy.failures, want) {
		t.Fatalf("RecordFailure() keys = %v, want %v", policy.failures, want)
	}
}

func TestCreateSessionChecksCaptchaBeforePoWWhenBothApply(t *testing.T) {
	policy := &stubLoginPolicyService{
		settings: webservice.LoginPolicySettings{MaxLoginBody: 1024},
		allowIP:  true,
		ipBanned: true,
	}
	authCalls := 0
	app := NewWithOptions(&servercfg.Snapshot{
		Web: servercfg.WebConfig{Username: "admin"},
		Feature: servercfg.FeatureConfig{
			OpenCaptcha: true,
		},
		Security: servercfg.SecurityConfig{
			SecureMode: true,
			PoWBits:    4,
		},
	}, Options{
		Services: &webservice.Services{
			Auth: stubManagementAuthService{
				authenticate: func(webservice.AuthenticateInput) (*webservice.SessionIdentity, error) {
					authCalls++
					return nil, errors.New("unexpected authenticate call")
				},
			},
			LoginPolicy: policy,
		},
	})
	ctx := &stubLoginContext{
		rawBody:    []byte(`{"username":"tenant","password":"secret","captcha_id":"","captcha_answer":""}`),
		remoteAddr: "198.51.100.18:1234",
		clientIP:   "198.51.100.18",
	}

	app.CreateSession(ctx)

	if ctx.status != 400 {
		t.Fatalf("CreateSession() status = %d, want 400 payload=%#v", ctx.status, ctx.jsonPayload)
	}
	response, ok := ctx.jsonPayload.(ManagementErrorResponse)
	if !ok {
		t.Fatalf("CreateSession() payload type = %T, want ManagementErrorResponse", ctx.jsonPayload)
	}
	if response.Error.Code != "invalid_captcha" {
		t.Fatalf("CreateSession() error code = %q, want invalid_captcha", response.Error.Code)
	}
	if authCalls != 0 {
		t.Fatalf("Authenticate() calls = %d, want 0 when captcha is invalid", authCalls)
	}
	if want := []string{"198.51.100.18"}; !reflect.DeepEqual(policy.failures, want) {
		t.Fatalf("RecordFailure() keys = %v, want %v", policy.failures, want)
	}
}

func TestCreateSessionRejectsMixedCredentialModes(t *testing.T) {
	authCalls := 0
	app := NewWithOptions(&servercfg.Snapshot{
		Web: servercfg.WebConfig{Username: "admin"},
	}, Options{
		Services: &webservice.Services{
			Auth: stubManagementAuthService{
				authenticate: func(webservice.AuthenticateInput) (*webservice.SessionIdentity, error) {
					authCalls++
					return nil, errors.New("unexpected authenticate call")
				},
			},
			LoginPolicy: &stubLoginPolicyService{
				settings: webservice.LoginPolicySettings{MaxLoginBody: 1024},
				allowIP:  true,
			},
		},
	})
	ctx := &stubLoginContext{
		rawBody:    []byte(`{"username":"tenant","password":"secret","verify_key":"vk-1"}`),
		remoteAddr: "198.51.100.9:1234",
		clientIP:   "198.51.100.9",
	}

	app.CreateSession(ctx)

	if ctx.status != 400 {
		t.Fatalf("CreateSession() status = %d, want 400 payload=%#v", ctx.status, ctx.jsonPayload)
	}
	response, ok := ctx.jsonPayload.(ManagementErrorResponse)
	if !ok {
		t.Fatalf("CreateSession() payload type = %T, want ManagementErrorResponse", ctx.jsonPayload)
	}
	if response.Error.Code != "mixed_credentials" {
		t.Fatalf("CreateSession() error code = %q, want mixed_credentials", response.Error.Code)
	}
	if authCalls != 0 {
		t.Fatalf("Authenticate() calls = %d, want 0 when credentials are mixed", authCalls)
	}
}

func findSessionLoginPoWNonce(t *testing.T, bits int, parts ...string) string {
	t.Helper()
	if bits < 1 {
		t.Fatal("findSessionLoginPoWNonce() requires bits >= 1")
	}
	for i := 0; i < 1<<20; i++ {
		nonce := strconv.Itoa(i)
		candidate := append([]string{}, parts...)
		candidate = append(candidate, nonce)
		if common.ValidatePoW(bits, candidate...) {
			return nonce
		}
	}
	t.Fatalf("findSessionLoginPoWNonce() could not find nonce for bits=%d", bits)
	return ""
}
