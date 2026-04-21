package service

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
)

func TestSessionIdentityRoundTrip(t *testing.T) {
	raw, err := MarshalSessionIdentity(&SessionIdentity{
		Authenticated: true,
		Kind:          "user",
		Provider:      "local",
		SubjectID:     "user:demo",
		Username:      "demo",
		ClientIDs:     []int{9, 0, 9, 3},
		Roles:         []string{RoleUser},
	})
	if err != nil {
		t.Fatalf("MarshalSessionIdentity() error = %v", err)
	}

	identity, err := ParseSessionIdentity(raw)
	if err != nil {
		t.Fatalf("ParseSessionIdentity() error = %v", err)
	}
	if identity == nil {
		t.Fatal("ParseSessionIdentity() returned nil identity")
	}
	if identity.Version != SessionIdentityVersion {
		t.Fatalf("Version = %d, want %d", identity.Version, SessionIdentityVersion)
	}
	if got, want := len(identity.ClientIDs), 2; got != want {
		t.Fatalf("len(ClientIDs) = %d, want %d", got, want)
	}
	if identity.ClientIDs[0] != 9 || identity.ClientIDs[1] != 3 {
		t.Fatalf("ClientIDs = %v, want [9 3]", identity.ClientIDs)
	}
	if len(identity.Permissions) == 0 {
		t.Fatal("Permissions should be derived from roles")
	}
	found := false
	for _, permission := range identity.Permissions {
		if permission == PermissionClientsRead {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Permissions = %v, expected %q to be present", identity.Permissions, PermissionClientsRead)
	}
}

func TestParseSessionIdentityWithResolver(t *testing.T) {
	raw := `{"version":1,"authenticated":true,"kind":"service","subject_id":"service:demo","roles":["service"]}`
	identity, err := ParseSessionIdentityWithResolver(raw, stubPermissionResolver{
		normalizeIdentity: func(identity *SessionIdentity) *SessionIdentity {
			if identity == nil {
				return nil
			}
			normalized := *identity
			normalized.Permissions = []string{"service:access"}
			return &normalized
		},
	})
	if err != nil {
		t.Fatalf("ParseSessionIdentityWithResolver() error = %v", err)
	}
	if identity == nil {
		t.Fatal("ParseSessionIdentityWithResolver() returned nil identity")
	}
	if len(identity.Permissions) != 1 || identity.Permissions[0] != "service:access" {
		t.Fatalf("Permissions = %v, want [service:access]", identity.Permissions)
	}
}

func TestDefaultAuthServiceAuthenticateUsesConfigProvider(t *testing.T) {
	service := DefaultAuthService{
		ConfigProvider: func() *servercfg.Snapshot {
			return &servercfg.Snapshot{
				Web: servercfg.WebConfig{
					Username: "admin-from-provider",
					Password: "provider-secret",
				},
			}
		},
	}

	identity, err := service.Authenticate(AuthenticateInput{
		Username: "admin-from-provider",
		Password: "provider-secret",
	})
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if identity == nil {
		t.Fatal("Authenticate() returned nil identity")
	}
	if !identity.IsAdmin || identity.Username != "admin-from-provider" {
		t.Fatalf("Authenticate() returned unexpected identity: %+v", identity)
	}
}

func TestDefaultAuthServiceAuthenticateAllowsLocalUserTOTP(t *testing.T) {
	secret, err := crypt.GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("GenerateTOTPSecret() error = %v", err)
	}
	code, _, err := crypt.GetTOTPCode(secret)
	if err != nil {
		t.Fatalf("GetTOTPCode() error = %v", err)
	}
	service := DefaultAuthService{
		ConfigProvider: func() *servercfg.Snapshot {
			return &servercfg.Snapshot{
				Feature: servercfg.FeatureConfig{
					AllowUserLogin: true,
				},
			}
		},
		Backend: Backend{
			Repository: stubRepository{
				getUserByUsername: func(username string) (*file.User, error) {
					return &file.User{
						Id:         7,
						Username:   username,
						Password:   "secret",
						TOTPSecret: secret,
						Kind:       "local",
						Status:     1,
						TotalFlow:  &file.Flow{},
					}, nil
				},
			},
		},
	}

	identity, err := service.Authenticate(AuthenticateInput{
		Username: "tenant",
		Password: "secret",
		TOTP:     code,
	})
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if identity == nil || identity.Kind != "user" || identity.Username != "tenant" {
		t.Fatalf("Authenticate() identity = %+v, want authenticated local user", identity)
	}
}

func TestIssueAndParseStandaloneToken(t *testing.T) {
	cfg := &servercfg.Snapshot{}
	cfg.Web.StandaloneTokenSecret = "standalone-secret"
	cfg.Web.StandaloneTokenTTLSeconds = 600

	token, issuedAt, expiresAt, err := IssueStandaloneToken(cfg, (&SessionIdentity{
		Version:       SessionIdentityVersion,
		Authenticated: true,
		Kind:          "admin",
		Provider:      "local",
		SubjectID:     "admin:test",
		Username:      "admin",
		IsAdmin:       true,
		Roles:         []string{RoleAdmin},
	}).Normalize(), time.Unix(100, 0))
	if err != nil {
		t.Fatalf("IssueStandaloneToken() error = %v", err)
	}
	if token == "" {
		t.Fatal("IssueStandaloneToken() returned empty token")
	}
	if issuedAt != 100 || expiresAt != 700 {
		t.Fatalf("IssueStandaloneToken() timestamps = %d/%d, want 100/700", issuedAt, expiresAt)
	}

	identity, err := ParseStandaloneToken(cfg, token, time.Unix(200, 0))
	if err != nil {
		t.Fatalf("ParseStandaloneToken() error = %v", err)
	}
	if identity == nil || !identity.IsAdmin || identity.Username != "admin" {
		t.Fatalf("ParseStandaloneToken() identity = %+v", identity)
	}
}

func TestParseStandaloneTokenRejectsExpiredToken(t *testing.T) {
	cfg := &servercfg.Snapshot{}
	cfg.Web.StandaloneTokenSecret = "standalone-secret"
	cfg.Web.StandaloneTokenTTLSeconds = 60

	token, _, _, err := IssueStandaloneToken(cfg, (&SessionIdentity{
		Version:       SessionIdentityVersion,
		Authenticated: true,
		Kind:          "user",
		Provider:      "local",
		SubjectID:     "user:test",
		Username:      "user",
		Roles:         []string{RoleUser},
	}).Normalize(), time.Unix(100, 0))
	if err != nil {
		t.Fatalf("IssueStandaloneToken() error = %v", err)
	}
	if _, err := ParseStandaloneToken(cfg, token, time.Unix(160, 0)); !errors.Is(err, ErrStandaloneTokenExpired) {
		t.Fatalf("ParseStandaloneToken() error = %v, want %v", err, ErrStandaloneTokenExpired)
	}
}

func TestResolveStandaloneTokenIdentityRejectsDisabledUser(t *testing.T) {
	cfg := &servercfg.Snapshot{}
	cfg.Web.StandaloneTokenSecret = "standalone-secret"
	cfg.Web.StandaloneTokenTTLSeconds = 600

	token, _, _, err := IssueStandaloneToken(cfg, (&SessionIdentity{
		Version:       SessionIdentityVersion,
		Authenticated: true,
		Kind:          "user",
		Provider:      "local",
		SubjectID:     "user:tenant",
		Username:      "tenant",
		Roles:         []string{RoleUser},
		Attributes: map[string]string{
			"user_id":    "7",
			"login_mode": "password",
		},
	}).Normalize(), time.Unix(100, 0))
	if err != nil {
		t.Fatalf("IssueStandaloneToken() error = %v", err)
	}

	identity, err := ResolveStandaloneTokenIdentity(cfg, DefaultPermissionResolver(), stubRepository{
		getUser: func(id int) (*file.User, error) {
			return &file.User{
				Id:        id,
				Username:  "tenant",
				Kind:      "local",
				Status:    0,
				TotalFlow: &file.Flow{},
			}, nil
		},
	}, token, time.Unix(200, 0))
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("ResolveStandaloneTokenIdentity() error = %v, want %v", err, ErrUnauthenticated)
	}
	if identity != nil {
		t.Fatalf("ResolveStandaloneTokenIdentity() identity = %+v, want nil", identity)
	}
}

func TestResolveStandaloneTokenIdentityTreatsMissingUserAsUnauthenticated(t *testing.T) {
	cfg := &servercfg.Snapshot{}
	cfg.Web.StandaloneTokenSecret = "standalone-secret"
	cfg.Web.StandaloneTokenTTLSeconds = 600

	token, _, _, err := IssueStandaloneToken(cfg, (&SessionIdentity{
		Version:       SessionIdentityVersion,
		Authenticated: true,
		Kind:          "user",
		Provider:      "local",
		SubjectID:     "user:tenant",
		Username:      "tenant",
		Roles:         []string{RoleUser},
		Attributes: map[string]string{
			"user_id":    "7",
			"login_mode": "password",
		},
	}).Normalize(), time.Unix(100, 0))
	if err != nil {
		t.Fatalf("IssueStandaloneToken() error = %v", err)
	}

	identity, err := ResolveStandaloneTokenIdentity(cfg, DefaultPermissionResolver(), stubRepository{
		getUser: func(int) (*file.User, error) {
			return nil, file.ErrUserNotFound
		},
	}, token, time.Unix(200, 0))
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("ResolveStandaloneTokenIdentity() error = %v, want %v", err, ErrUnauthenticated)
	}
	if identity != nil {
		t.Fatalf("ResolveStandaloneTokenIdentity() identity = %+v, want nil", identity)
	}
}

func TestResolveStandaloneTokenIdentityRefreshesClientScope(t *testing.T) {
	cfg := &servercfg.Snapshot{}
	cfg.Web.StandaloneTokenSecret = "standalone-secret"
	cfg.Web.StandaloneTokenTTLSeconds = 600

	token, _, _, err := IssueStandaloneToken(cfg, (&SessionIdentity{
		Version:       SessionIdentityVersion,
		Authenticated: true,
		Kind:          "client",
		Provider:      "local",
		SubjectID:     "client:vkey:9",
		Username:      "vkey-client-stale",
		ClientIDs:     []int{99},
		Roles:         []string{RoleClient},
		Attributes: map[string]string{
			"client_id":  "9",
			"login_mode": "client_vkey",
		},
	}).Normalize(), time.Unix(100, 0))
	if err != nil {
		t.Fatalf("IssueStandaloneToken() error = %v", err)
	}

	identity, err := ResolveStandaloneTokenIdentity(cfg, DefaultPermissionResolver(), stubRepository{
		getClient: func(id int) (*file.Client, error) {
			return &file.Client{
				Id:        id,
				Status:    true,
				VerifyKey: "vk-live",
				Cnf:       &file.Config{},
				Flow:      &file.Flow{},
			}, nil
		},
	}, token, time.Unix(200, 0))
	if err != nil {
		t.Fatalf("ResolveStandaloneTokenIdentity() error = %v", err)
	}
	if identity == nil {
		t.Fatal("ResolveStandaloneTokenIdentity() returned nil identity")
	}
	if identity.Username != "vkey-client-9" || len(identity.ClientIDs) != 1 || identity.ClientIDs[0] != 9 {
		t.Fatalf("ResolveStandaloneTokenIdentity() identity = %+v, want refreshed client scope", identity)
	}
}

func TestResolveStandaloneTokenIdentityPropagatesUnexpectedLookupError(t *testing.T) {
	cfg := &servercfg.Snapshot{}
	cfg.Web.StandaloneTokenSecret = "standalone-secret"
	cfg.Web.StandaloneTokenTTLSeconds = 600

	token, _, _, err := IssueStandaloneToken(cfg, (&SessionIdentity{
		Version:       SessionIdentityVersion,
		Authenticated: true,
		Kind:          "user",
		Provider:      "local",
		SubjectID:     "user:tenant",
		Username:      "tenant",
		Roles:         []string{RoleUser},
		Attributes: map[string]string{
			"user_id":    "7",
			"login_mode": "password",
		},
	}).Normalize(), time.Unix(100, 0))
	if err != nil {
		t.Fatalf("IssueStandaloneToken() error = %v", err)
	}

	lookupErr := errors.New("user lookup failed")
	identity, err := ResolveStandaloneTokenIdentity(cfg, DefaultPermissionResolver(), stubRepository{
		getUser: func(int) (*file.User, error) {
			return nil, lookupErr
		},
	}, token, time.Unix(200, 0))
	if !errors.Is(err, lookupErr) {
		t.Fatalf("ResolveStandaloneTokenIdentity() error = %v, want %v", err, lookupErr)
	}
	if identity != nil {
		t.Fatalf("ResolveStandaloneTokenIdentity() identity = %+v, want nil", identity)
	}
}

func TestDefaultAuthServiceAuthenticateRejectsLocalUserWithoutRequiredTOTP(t *testing.T) {
	secret, err := crypt.GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("GenerateTOTPSecret() error = %v", err)
	}
	service := DefaultAuthService{
		ConfigProvider: func() *servercfg.Snapshot {
			return &servercfg.Snapshot{
				Feature: servercfg.FeatureConfig{
					AllowUserLogin: true,
				},
			}
		},
		Backend: Backend{
			Repository: stubRepository{
				getUserByUsername: func(username string) (*file.User, error) {
					return &file.User{
						Id:         7,
						Username:   username,
						Password:   "secret",
						TOTPSecret: secret,
						Kind:       "local",
						Status:     1,
						TotalFlow:  &file.Flow{},
					}, nil
				},
			},
		},
	}

	identity, err := service.Authenticate(AuthenticateInput{
		Username: "tenant",
		Password: "secret",
	})
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Authenticate() error = %v, want %v", err, ErrInvalidCredentials)
	}
	if identity != nil {
		t.Fatalf("Authenticate() identity = %+v, want nil", identity)
	}
}

func TestDefaultAuthServiceAuthenticateUsesManagedClientIDFastPath(t *testing.T) {
	service := DefaultAuthService{
		ConfigProvider: func() *servercfg.Snapshot {
			return &servercfg.Snapshot{
				Feature: servercfg.FeatureConfig{
					AllowUserLogin: true,
				},
			}
		},
		Backend: Backend{
			Repository: stubRepository{
				getUserByUsername: func(username string) (*file.User, error) {
					return &file.User{
						Id:        7,
						Username:  username,
						Password:  "secret",
						Kind:      "local",
						Status:    1,
						TotalFlow: &file.Flow{},
					}, nil
				},
				rangeClients: func(func(*file.Client) bool) {
					t.Fatal("RangeClients() should not be used when GetManagedClientIDsByUserID is available")
				},
				getManagedClientIDs: func(userID int) ([]int, error) {
					if userID != 7 {
						t.Fatalf("GetManagedClientIDsByUserID(%d), want 7", userID)
					}
					return []int{12, 11, 12, 0}, nil
				},
				authoritativeManagedClientIDs: true,
			},
		},
	}

	identity, err := service.Authenticate(AuthenticateInput{
		Username: "tenant",
		Password: "secret",
	})
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if identity == nil || identity.Kind != "user" || identity.Username != "tenant" {
		t.Fatalf("Authenticate() identity = %+v, want authenticated local user", identity)
	}
	if !reflect.DeepEqual(identity.ClientIDs, []int{11, 12}) {
		t.Fatalf("Authenticate() client ids = %v, want [11 12]", identity.ClientIDs)
	}
}

func TestDefaultAuthServiceAuthenticatePropagatesUnexpectedManagedClientLookupError(t *testing.T) {
	lookupErr := errors.New("managed client lookup failed")
	service := DefaultAuthService{
		ConfigProvider: func() *servercfg.Snapshot {
			return &servercfg.Snapshot{
				Feature: servercfg.FeatureConfig{
					AllowUserLogin: true,
				},
			}
		},
		Backend: Backend{
			Repository: stubRepository{
				getUserByUsername: func(username string) (*file.User, error) {
					return &file.User{
						Id:        7,
						Username:  username,
						Password:  "secret",
						Kind:      "local",
						Status:    1,
						TotalFlow: &file.Flow{},
					}, nil
				},
				rangeClients: func(func(*file.Client) bool) {
					t.Fatal("RangeClients() should not be used after GetManagedClientIDsByUserID backend error")
				},
				getManagedClientIDs: func(userID int) ([]int, error) {
					if userID != 7 {
						t.Fatalf("GetManagedClientIDsByUserID(%d), want 7", userID)
					}
					return nil, lookupErr
				},
				authoritativeManagedClientIDs: true,
			},
		},
	}

	identity, err := service.Authenticate(AuthenticateInput{
		Username: "tenant",
		Password: "secret",
	})
	if !errors.Is(err, lookupErr) {
		t.Fatalf("Authenticate() error = %v, want %v", err, lookupErr)
	}
	if identity != nil {
		t.Fatalf("Authenticate() identity = %+v, want nil", identity)
	}
}

func TestDefaultAuthServiceAuthenticateAllowsLocalUserTOTPWithEmptyPassword(t *testing.T) {
	secret, err := crypt.GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("GenerateTOTPSecret() error = %v", err)
	}
	code, _, err := crypt.GetTOTPCode(secret)
	if err != nil {
		t.Fatalf("GetTOTPCode() error = %v", err)
	}
	service := DefaultAuthService{
		ConfigProvider: func() *servercfg.Snapshot {
			return &servercfg.Snapshot{
				Feature: servercfg.FeatureConfig{
					AllowUserLogin: true,
				},
			}
		},
		Backend: Backend{
			Repository: stubRepository{
				getUserByUsername: func(username string) (*file.User, error) {
					return &file.User{
						Id:         8,
						Username:   username,
						Password:   "",
						TOTPSecret: secret,
						Kind:       "local",
						Status:     1,
						TotalFlow:  &file.Flow{},
					}, nil
				},
			},
		},
	}

	identity, err := service.Authenticate(AuthenticateInput{
		Username: "tenant",
		TOTP:     code,
	})
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if identity == nil || identity.Kind != "user" || identity.Username != "tenant" {
		t.Fatalf("Authenticate() identity = %+v, want authenticated local user", identity)
	}

	identity, err = service.Authenticate(AuthenticateInput{
		Username: "tenant",
		Password: code,
	})
	if err != nil {
		t.Fatalf("Authenticate() password-suffix error = %v", err)
	}
	if identity == nil || identity.Kind != "user" || identity.Username != "tenant" {
		t.Fatalf("Authenticate() password-suffix identity = %+v, want authenticated local user", identity)
	}
}

func TestDefaultAuthServiceAuthenticateRejectsLocalUserWithEmptyPasswordWhenTOTPDisabled(t *testing.T) {
	service := DefaultAuthService{
		ConfigProvider: func() *servercfg.Snapshot {
			return &servercfg.Snapshot{
				Feature: servercfg.FeatureConfig{
					AllowUserLogin: true,
				},
			}
		},
		Backend: Backend{
			Repository: stubRepository{
				getUserByUsername: func(username string) (*file.User, error) {
					return &file.User{
						Id:        8,
						Username:  username,
						Password:  "",
						Kind:      "local",
						Status:    1,
						TotalFlow: &file.Flow{},
					}, nil
				},
			},
		},
	}

	identity, err := service.Authenticate(AuthenticateInput{
		Username: "tenant",
	})
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Authenticate() error = %v, want %v", err, ErrInvalidCredentials)
	}
	if identity != nil {
		t.Fatalf("Authenticate() identity = %+v, want nil", identity)
	}

	identity, err = service.Authenticate(AuthenticateInput{
		Username: "tenant",
		TOTP:     "123456",
	})
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Authenticate() TOTP-only error = %v, want %v", err, ErrInvalidCredentials)
	}
	if identity != nil {
		t.Fatalf("Authenticate() TOTP-only identity = %+v, want nil", identity)
	}
}

func TestDefaultAuthServiceAuthenticatePropagatesUnexpectedUserLookupError(t *testing.T) {
	lookupErr := errors.New("user lookup failed")
	service := DefaultAuthService{
		ConfigProvider: func() *servercfg.Snapshot {
			return &servercfg.Snapshot{
				Feature: servercfg.FeatureConfig{
					AllowUserLogin: true,
				},
			}
		},
		Backend: Backend{
			Repository: stubRepository{
				getUserByUsername: func(string) (*file.User, error) {
					return nil, lookupErr
				},
			},
		},
	}

	identity, err := service.Authenticate(AuthenticateInput{
		Username: "tenant",
		Password: "secret",
	})
	if !errors.Is(err, lookupErr) {
		t.Fatalf("Authenticate() error = %v, want %v", err, lookupErr)
	}
	if identity != nil {
		t.Fatalf("Authenticate() identity = %+v, want nil", identity)
	}
}

func TestDefaultAuthServiceAuthenticateTreatsMissingUserAsInvalidCredentials(t *testing.T) {
	service := DefaultAuthService{
		ConfigProvider: func() *servercfg.Snapshot {
			return &servercfg.Snapshot{
				Feature: servercfg.FeatureConfig{
					AllowUserLogin: true,
				},
			}
		},
		Backend: Backend{
			Repository: stubRepository{
				getUserByUsername: func(string) (*file.User, error) {
					return nil, file.ErrUserNotFound
				},
			},
		},
	}

	identity, err := service.Authenticate(AuthenticateInput{
		Username: "missing",
		Password: "secret",
	})
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Authenticate() error = %v, want %v", err, ErrInvalidCredentials)
	}
	if identity != nil {
		t.Fatalf("Authenticate() identity = %+v, want nil", identity)
	}
}

func TestDefaultAuthServiceAuthenticatePropagatesUnexpectedVerifyKeyOwnerLookupError(t *testing.T) {
	lookupErr := errors.New("owner lookup failed")
	service := DefaultAuthService{
		ConfigProvider: func() *servercfg.Snapshot {
			return &servercfg.Snapshot{
				Feature: servercfg.FeatureConfig{
					AllowUserVkeyLogin: true,
				},
			}
		},
		Backend: Backend{
			Repository: stubRepository{
				getUser: func(int) (*file.User, error) {
					return nil, lookupErr
				},
				rangeClients: func(fn func(*file.Client) bool) {
					fn(&file.Client{
						Id:          9,
						OwnerUserID: 7,
						Status:      true,
						VerifyKey:   "vk-owner-error",
						Cnf:         &file.Config{},
						Flow:        &file.Flow{},
					})
				},
			},
		},
	}

	identity, err := service.Authenticate(AuthenticateInput{
		Password: "vk-owner-error",
	})
	if !errors.Is(err, lookupErr) {
		t.Fatalf("Authenticate() error = %v, want %v", err, lookupErr)
	}
	if identity != nil {
		t.Fatalf("Authenticate() identity = %+v, want nil", identity)
	}
}

func TestDefaultAuthServiceRegisterUserRollsBackCreatedUserOnClientCreateFailure(t *testing.T) {
	createClientErr := errors.New("create client failed")
	deletedUserID := 0
	service := DefaultAuthService{
		ConfigProvider: func() *servercfg.Snapshot {
			return &servercfg.Snapshot{
				Web: servercfg.WebConfig{Username: "admin"},
			}
		},
		Backend: Backend{
			Repository: stubRepository{
				nextUserID:   func() int { return 7 },
				nextClientID: func() int { return 11 },
				createUser:   func(user *file.User) error { return nil },
				createClient: func(client *file.Client) error { return createClientErr },
				deleteUser: func(id int) error {
					deletedUserID = id
					return nil
				},
			},
		},
	}

	result, err := service.RegisterUser(RegisterUserInput{
		Username: "tenant",
		Password: "secret",
	})
	if !errors.Is(err, createClientErr) {
		t.Fatalf("RegisterUser() error = %v, want create client failure", err)
	}
	if result != nil {
		t.Fatalf("RegisterUser() result = %+v, want nil", result)
	}
	if deletedUserID != 7 {
		t.Fatalf("DeleteUser(%d), want 7", deletedUserID)
	}
}

func TestDefaultAuthServiceRegisterUserReturnsRollbackFailure(t *testing.T) {
	createClientErr := errors.New("create client failed")
	deleteUserErr := errors.New("rollback delete failed")
	service := DefaultAuthService{
		ConfigProvider: func() *servercfg.Snapshot {
			return &servercfg.Snapshot{
				Web: servercfg.WebConfig{Username: "admin"},
			}
		},
		Backend: Backend{
			Repository: stubRepository{
				nextUserID:   func() int { return 7 },
				nextClientID: func() int { return 11 },
				createUser: func(user *file.User) error {
					return nil
				},
				createClient: func(client *file.Client) error {
					return createClientErr
				},
				deleteUser: func(id int) error {
					if id != 7 {
						t.Fatalf("DeleteUser(%d), want 7", id)
					}
					return deleteUserErr
				},
			},
		},
	}

	result, err := service.RegisterUser(RegisterUserInput{
		Username: "tenant",
		Password: "secret",
	})
	if result != nil {
		t.Fatalf("RegisterUser() result = %+v, want nil", result)
	}
	if !errors.Is(err, createClientErr) {
		t.Fatalf("RegisterUser() error = %v, want create client failure", err)
	}
	if !errors.Is(err, deleteUserErr) {
		t.Fatalf("RegisterUser() error = %v, want rollback delete failure", err)
	}
}

func TestDefaultAuthServiceAuthenticateAllowsAdminTOTPWithEmptyPassword(t *testing.T) {
	secret, err := crypt.GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("GenerateTOTPSecret() error = %v", err)
	}
	code, _, err := crypt.GetTOTPCode(secret)
	if err != nil {
		t.Fatalf("GetTOTPCode() error = %v", err)
	}
	service := DefaultAuthService{
		ConfigProvider: func() *servercfg.Snapshot {
			return &servercfg.Snapshot{
				Web: servercfg.WebConfig{
					Username:   "admin",
					TOTPSecret: secret,
				},
			}
		},
	}

	identity, err := service.Authenticate(AuthenticateInput{
		Username: "admin",
		TOTP:     code,
	})
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if identity == nil || !identity.IsAdmin {
		t.Fatalf("Authenticate() identity = %+v, want admin", identity)
	}

	identity, err = service.Authenticate(AuthenticateInput{
		Username: "admin",
		Password: code,
	})
	if err != nil {
		t.Fatalf("Authenticate() password-suffix error = %v", err)
	}
	if identity == nil || !identity.IsAdmin {
		t.Fatalf("Authenticate() password-suffix identity = %+v, want admin", identity)
	}
}

func TestDefaultAuthServiceAuthenticateRejectsAdminEmptyPasswordWithoutTOTP(t *testing.T) {
	service := DefaultAuthService{
		ConfigProvider: func() *servercfg.Snapshot {
			return &servercfg.Snapshot{
				Web: servercfg.WebConfig{
					Username: "admin",
				},
			}
		},
	}

	identity, err := service.Authenticate(AuthenticateInput{
		Username: "admin",
	})
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Authenticate() error = %v, want %v", err, ErrInvalidCredentials)
	}
	if identity != nil {
		t.Fatalf("Authenticate() identity = %+v, want nil", identity)
	}
}

func TestAutoAdminIdentity(t *testing.T) {
	identity, ok := AutoAdminIdentity(&servercfg.Snapshot{
		Web: servercfg.WebConfig{},
	})
	if !ok {
		t.Fatal("AutoAdminIdentity() should enable implicit admin login when username and password are empty")
	}
	if identity == nil {
		t.Fatal("AutoAdminIdentity() returned nil identity")
	}
	if !identity.Authenticated || !identity.IsAdmin {
		t.Fatalf("AutoAdminIdentity() returned unexpected identity: %+v", identity)
	}
	if identity.Username != "admin" {
		t.Fatalf("AutoAdminIdentity() username = %q, want admin", identity.Username)
	}
	if identity.Attributes["login_mode"] != "auto" {
		t.Fatalf("AutoAdminIdentity() login mode = %q, want auto", identity.Attributes["login_mode"])
	}

	if disabled, ok := AutoAdminIdentity(&servercfg.Snapshot{
		Web: servercfg.WebConfig{
			TOTPSecret: "totp-secret",
		},
	}); ok || disabled != nil {
		t.Fatalf("AutoAdminIdentity() should stay disabled when TOTP is configured, got %+v, %v", disabled, ok)
	}
}

func TestDefaultAuthServiceAuthenticateAllowsVerifyKeyFallbackForBoundClientWithActiveOwner(t *testing.T) {
	service := DefaultAuthService{
		ConfigProvider: func() *servercfg.Snapshot {
			return &servercfg.Snapshot{
				Feature: servercfg.FeatureConfig{
					AllowUserLogin:     true,
					AllowUserVkeyLogin: true,
				},
			}
		},
		Backend: Backend{
			Repository: stubRepository{
				getUser: func(id int) (*file.User, error) {
					return &file.User{
						Id:        id,
						Username:  "tenant",
						Kind:      "local",
						Status:    1,
						TotalFlow: &file.Flow{},
					}, nil
				},
				rangeClients: func(fn func(*file.Client) bool) {
					fn(&file.Client{
						Id:        7,
						UserId:    3,
						Status:    true,
						VerifyKey: "vk-bound",
						Cnf:       &file.Config{},
						Flow:      &file.Flow{},
					})
				},
			},
		},
	}

	identity, err := service.Authenticate(AuthenticateInput{
		Password: "vk-bound",
	})
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if identity == nil {
		t.Fatal("Authenticate() returned nil identity")
	}
	if identity.Kind != "client" || identity.SubjectID != "client:vkey:7" || identity.Username != "vkey-client-7" {
		t.Fatalf("Authenticate() identity = %+v, want bound client principal", identity)
	}
	if !reflect.DeepEqual(identity.ClientIDs, []int{7}) {
		t.Fatalf("Authenticate() client ids = %v, want [7]", identity.ClientIDs)
	}
	if identity.Attributes["login_mode"] != "client_vkey" || identity.Attributes["client_id"] != "7" {
		t.Fatalf("Authenticate() attributes = %v, want client_vkey client_id=7", identity.Attributes)
	}
}

func TestDefaultAuthServiceAuthenticateRejectsVerifyKeyFallbackForBoundClientWithDisabledOwner(t *testing.T) {
	service := DefaultAuthService{
		ConfigProvider: func() *servercfg.Snapshot {
			return &servercfg.Snapshot{
				Feature: servercfg.FeatureConfig{
					AllowUserVkeyLogin: true,
				},
			}
		},
		Backend: Backend{
			Repository: stubRepository{
				getUser: func(id int) (*file.User, error) {
					return &file.User{
						Id:        id,
						Username:  "tenant",
						Kind:      "local",
						Status:    0,
						TotalFlow: &file.Flow{},
					}, nil
				},
				rangeClients: func(fn func(*file.Client) bool) {
					fn(&file.Client{
						Id:        8,
						UserId:    3,
						Status:    true,
						VerifyKey: "vk-bound-disabled-owner",
						Cnf:       &file.Config{},
						Flow:      &file.Flow{},
					})
				},
			},
		},
	}

	identity, err := service.Authenticate(AuthenticateInput{
		Password: "vk-bound-disabled-owner",
	})
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Authenticate() error = %v, want %v", err, ErrInvalidCredentials)
	}
	if identity != nil {
		t.Fatalf("Authenticate() identity = %+v, want nil", identity)
	}
}

func TestDefaultAuthServiceAuthenticateRejectsVerifyKeyFallbackForBoundClientWithMissingOwner(t *testing.T) {
	service := DefaultAuthService{
		ConfigProvider: func() *servercfg.Snapshot {
			return &servercfg.Snapshot{
				Feature: servercfg.FeatureConfig{
					AllowUserVkeyLogin: true,
				},
			}
		},
		Backend: Backend{
			Repository: stubRepository{
				getUser: func(int) (*file.User, error) {
					return nil, file.ErrUserNotFound
				},
				rangeClients: func(fn func(*file.Client) bool) {
					fn(&file.Client{
						Id:          8,
						OwnerUserID: 3,
						Status:      true,
						VerifyKey:   "vk-bound-missing-owner",
						Cnf:         &file.Config{},
						Flow:        &file.Flow{},
					})
				},
			},
		},
	}

	identity, err := service.Authenticate(AuthenticateInput{
		Password: "vk-bound-missing-owner",
	})
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Authenticate() error = %v, want %v", err, ErrInvalidCredentials)
	}
	if identity != nil {
		t.Fatalf("Authenticate() identity = %+v, want nil", identity)
	}
}

func TestDefaultAuthServiceAuthenticateAllowsVerifyKeyFallbackForRealClient(t *testing.T) {
	service := DefaultAuthService{
		ConfigProvider: func() *servercfg.Snapshot {
			return &servercfg.Snapshot{
				Feature: servercfg.FeatureConfig{
					AllowUserLogin:     false,
					AllowUserVkeyLogin: true,
				},
			}
		},
		Backend: Backend{
			Repository: stubRepository{
				rangeClients: func(fn func(*file.Client) bool) {
					fn(&file.Client{
						Id:        9,
						Status:    true,
						VerifyKey: "vk-unowned",
						Cnf:       &file.Config{},
						Flow:      &file.Flow{},
					})
				},
			},
		},
	}

	identity, err := service.Authenticate(AuthenticateInput{
		Password: "vk-unowned",
	})
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if identity == nil {
		t.Fatal("Authenticate() returned nil identity")
	}
	if identity.Username != "vkey-client-9" || identity.SubjectID != "client:vkey:9" {
		t.Fatalf("Authenticate() identity = %+v, want dedicated vkey identity", identity)
	}
	if identity.Kind != "client" {
		t.Fatalf("Authenticate() kind = %q, want client", identity.Kind)
	}
	if len(identity.ClientIDs) != 1 || identity.ClientIDs[0] != 9 {
		t.Fatalf("Authenticate() client ids = %v, want [9]", identity.ClientIDs)
	}
	if identity.Attributes["login_mode"] != "client_vkey" || identity.Attributes["client_id"] != "9" {
		t.Fatalf("Authenticate() attributes = %v, want client_vkey client_id=9", identity.Attributes)
	}
	if !containsString(identity.Roles, RoleClient) {
		t.Fatalf("Authenticate() roles = %v, want client role", identity.Roles)
	}
	if !permissionSetAllows(identity.Permissions, PermissionClientsRead) {
		t.Fatalf("Authenticate() permissions = %v, want clients:read", identity.Permissions)
	}
	if permissionSetAllows(identity.Permissions, PermissionClientsUpdate) {
		t.Fatalf("Authenticate() permissions = %v, client login should not allow clients:update", identity.Permissions)
	}
}

func TestDefaultAuthServiceAuthenticateUsesVerifyKeyRepositoryFastPath(t *testing.T) {
	service := DefaultAuthService{
		ConfigProvider: func() *servercfg.Snapshot {
			return &servercfg.Snapshot{
				Feature: servercfg.FeatureConfig{
					AllowUserVkeyLogin: true,
				},
			}
		},
		Backend: Backend{
			Repository: stubRepository{
				rangeClients: func(func(*file.Client) bool) {
					t.Fatal("RangeClients() should not be used when GetClientByVerifyKey is available")
				},
				getClientByVerifyKey: func(vkey string) (*file.Client, error) {
					if vkey != "vk-fast" {
						t.Fatalf("GetClientByVerifyKey(%q), want vk-fast", vkey)
					}
					return &file.Client{
						Id:        15,
						Status:    true,
						VerifyKey: "vk-fast",
						Cnf:       &file.Config{},
						Flow:      &file.Flow{},
					}, nil
				},
			},
		},
	}

	identity, err := service.Authenticate(AuthenticateInput{
		Password: "vk-fast",
	})
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if identity == nil {
		t.Fatal("Authenticate() returned nil identity")
	}
	if identity.Kind != "client" || identity.SubjectID != "client:vkey:15" || identity.Username != "vkey-client-15" {
		t.Fatalf("Authenticate() identity = %+v, want indexed client principal", identity)
	}
	if !reflect.DeepEqual(identity.ClientIDs, []int{15}) {
		t.Fatalf("Authenticate() client ids = %v, want [15]", identity.ClientIDs)
	}
}

func TestDefaultAuthServiceAuthenticateUsesBoundOwnerFromVerifyKeyFastPath(t *testing.T) {
	owner := &file.User{
		Id:        7,
		Username:  "tenant",
		Kind:      "local",
		Status:    1,
		TotalFlow: &file.Flow{},
	}
	client := &file.Client{
		Id:          15,
		OwnerUserID: 7,
		Status:      true,
		VerifyKey:   "vk-fast-owner",
		Cnf:         &file.Config{},
		Flow:        &file.Flow{},
	}
	client.BindOwnerUser(owner)
	service := DefaultAuthService{
		ConfigProvider: func() *servercfg.Snapshot {
			return &servercfg.Snapshot{
				Feature: servercfg.FeatureConfig{
					AllowUserVkeyLogin: true,
				},
			}
		},
		Backend: Backend{
			Repository: stubRepository{
				rangeClients: func(func(*file.Client) bool) {
					t.Fatal("RangeClients() should not be used when GetClientByVerifyKey is available")
				},
				getUser: func(int) (*file.User, error) {
					t.Fatal("GetUser() should not be used when client already carries a bound owner snapshot")
					return nil, nil
				},
				getClientByVerifyKey: func(vkey string) (*file.Client, error) {
					if vkey != "vk-fast-owner" {
						t.Fatalf("GetClientByVerifyKey(%q), want vk-fast-owner", vkey)
					}
					return client, nil
				},
			},
		},
	}

	identity, err := service.Authenticate(AuthenticateInput{
		Password: "vk-fast-owner",
	})
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if identity == nil {
		t.Fatal("Authenticate() returned nil identity")
	}
	if identity.Kind != "client" || identity.SubjectID != "client:vkey:15" || identity.Username != "vkey-client-15" {
		t.Fatalf("Authenticate() identity = %+v, want indexed client principal", identity)
	}
	if !reflect.DeepEqual(identity.ClientIDs, []int{15}) {
		t.Fatalf("Authenticate() client ids = %v, want [15]", identity.ClientIDs)
	}
}

func TestDefaultAuthServiceAuthenticateFallsBackToRangeForDuplicateVerifyKeyWhenFastPathHitsBlockedClient(t *testing.T) {
	blocked := &file.Client{
		Id:        15,
		Status:    true,
		VerifyKey: "vk-shared",
		NoDisplay: true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}
	allowed := &file.Client{
		Id:        16,
		Status:    true,
		VerifyKey: "vk-shared",
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}
	rangeCalls := 0
	service := DefaultAuthService{
		ConfigProvider: func() *servercfg.Snapshot {
			return &servercfg.Snapshot{
				Feature: servercfg.FeatureConfig{
					AllowUserVkeyLogin: true,
				},
			}
		},
		Backend: Backend{
			Repository: stubRepository{
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

	identity, err := service.Authenticate(AuthenticateInput{
		Password: "vk-shared",
	})
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if rangeCalls != 1 {
		t.Fatalf("RangeClients() called %d times, want 1 fallback pass", rangeCalls)
	}
	if identity == nil {
		t.Fatal("Authenticate() returned nil identity")
	}
	if identity.Kind != "client" || identity.SubjectID != "client:vkey:16" || identity.Username != "vkey-client-16" {
		t.Fatalf("Authenticate() identity = %+v, want fallback client principal", identity)
	}
	if !reflect.DeepEqual(identity.ClientIDs, []int{16}) {
		t.Fatalf("Authenticate() client ids = %v, want [16]", identity.ClientIDs)
	}
}

func TestDefaultAuthServiceAuthenticateRejectsExpiredVerifyKeyFallbackClient(t *testing.T) {
	service := DefaultAuthService{
		ConfigProvider: func() *servercfg.Snapshot {
			return &servercfg.Snapshot{
				Feature: servercfg.FeatureConfig{
					AllowUserVkeyLogin: true,
				},
			}
		},
		Backend: Backend{
			Repository: stubRepository{
				rangeClients: func(fn func(*file.Client) bool) {
					fn(&file.Client{
						Id:        10,
						Status:    true,
						VerifyKey: "vk-expired",
						ExpireAt:  1,
						Cnf:       &file.Config{},
						Flow:      &file.Flow{},
					})
				},
			},
		},
	}

	identity, err := service.Authenticate(AuthenticateInput{
		Password: "vk-expired",
	})
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Authenticate() error = %v, want %v", err, ErrInvalidCredentials)
	}
	if identity != nil {
		t.Fatalf("Authenticate() identity = %+v, want nil", identity)
	}
}

func TestDefaultAuthServiceAuthenticateRejectsVerifyKeyFallbackWhenUsernameProvided(t *testing.T) {
	service := DefaultAuthService{
		ConfigProvider: func() *servercfg.Snapshot {
			return &servercfg.Snapshot{
				Feature: servercfg.FeatureConfig{
					AllowUserVkeyLogin: true,
				},
			}
		},
		Backend: Backend{
			Repository: stubRepository{
				rangeClients: func(fn func(*file.Client) bool) {
					fn(&file.Client{
						Id:        9,
						Status:    true,
						VerifyKey: "vk-unowned",
						Cnf:       &file.Config{},
						Flow:      &file.Flow{},
					})
				},
			},
		},
	}

	identity, err := service.Authenticate(AuthenticateInput{
		Username: "ignored",
		Password: "vk-unowned",
	})
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Authenticate() error = %v, want %v", err, ErrInvalidCredentials)
	}
	if identity != nil {
		t.Fatalf("Authenticate() identity = %+v, want nil", identity)
	}
}

func TestRefreshSessionIdentityRejectsDisabledLocalUser(t *testing.T) {
	identity := (&SessionIdentity{
		Authenticated: true,
		Kind:          "user",
		Provider:      "local",
		SubjectID:     "user:tenant",
		Username:      "tenant",
		Roles:         []string{RoleUser},
		Attributes: map[string]string{
			"user_id":    "7",
			"login_mode": "password",
		},
	}).Normalize()

	refreshed, err := RefreshSessionIdentity(identity, DefaultPermissionResolver(), stubRepository{
		getUser: func(id int) (*file.User, error) {
			return &file.User{
				Id:        id,
				Username:  "tenant",
				Kind:      "local",
				Status:    0,
				TotalFlow: &file.Flow{},
			}, nil
		},
	}, time.Unix(100, 0))
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("RefreshSessionIdentity() error = %v, want %v", err, ErrUnauthenticated)
	}
	if refreshed != nil {
		t.Fatalf("RefreshSessionIdentity() refreshed = %+v, want nil", refreshed)
	}
}

func TestRefreshSessionIdentityTreatsMissingUserAsUnauthenticated(t *testing.T) {
	identity := (&SessionIdentity{
		Authenticated: true,
		Kind:          "user",
		Provider:      "local",
		SubjectID:     "user:tenant",
		Username:      "tenant",
		Roles:         []string{RoleUser},
		Attributes: map[string]string{
			"user_id":    "7",
			"login_mode": "password",
		},
	}).Normalize()

	refreshed, err := RefreshSessionIdentity(identity, DefaultPermissionResolver(), stubRepository{
		getUser: func(int) (*file.User, error) {
			return nil, file.ErrUserNotFound
		},
	}, time.Unix(100, 0))
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("RefreshSessionIdentity() error = %v, want %v", err, ErrUnauthenticated)
	}
	if refreshed != nil {
		t.Fatalf("RefreshSessionIdentity() refreshed = %+v, want nil", refreshed)
	}
}

func TestRefreshSessionIdentityPropagatesUnexpectedUserLookupError(t *testing.T) {
	lookupErr := errors.New("user lookup failed")
	identity := (&SessionIdentity{
		Authenticated: true,
		Kind:          "user",
		Provider:      "local",
		SubjectID:     "user:tenant",
		Username:      "tenant",
		Roles:         []string{RoleUser},
		Attributes: map[string]string{
			"user_id":    "7",
			"login_mode": "password",
		},
	}).Normalize()

	refreshed, err := RefreshSessionIdentity(identity, DefaultPermissionResolver(), stubRepository{
		getUser: func(int) (*file.User, error) {
			return nil, lookupErr
		},
	}, time.Unix(100, 0))
	if !errors.Is(err, lookupErr) {
		t.Fatalf("RefreshSessionIdentity() error = %v, want %v", err, lookupErr)
	}
	if refreshed != nil {
		t.Fatalf("RefreshSessionIdentity() refreshed = %+v, want nil", refreshed)
	}
}

func TestRefreshSessionIdentityRecomputesManagedClientIDs(t *testing.T) {
	identity := (&SessionIdentity{
		Authenticated: true,
		Kind:          "user",
		Provider:      "local",
		SubjectID:     "user:tenant-old",
		Username:      "tenant-old",
		ClientIDs:     []int{99},
		Roles:         []string{RoleUser},
		Attributes: map[string]string{
			"user_id":    "7",
			"login_mode": "password",
		},
	}).Normalize()

	refreshed, err := RefreshSessionIdentity(identity, DefaultPermissionResolver(), stubRepository{
		getUser: func(id int) (*file.User, error) {
			return &file.User{
				Id:        id,
				Username:  "tenant-new",
				Kind:      "local",
				Status:    1,
				TotalFlow: &file.Flow{},
			}, nil
		},
		rangeClients: func(fn func(*file.Client) bool) {
			fn(&file.Client{Id: 11, UserId: 7, OwnerUserID: 7, Status: true, Cnf: &file.Config{}, Flow: &file.Flow{}})
			fn(&file.Client{Id: 12, UserId: 9, OwnerUserID: 9, ManagerUserIDs: []int{7}, Status: true, Cnf: &file.Config{}, Flow: &file.Flow{}})
			fn(&file.Client{Id: 13, UserId: 8, OwnerUserID: 8, Status: true, Cnf: &file.Config{}, Flow: &file.Flow{}})
			fn(&file.Client{Id: 14, UserId: 7, OwnerUserID: 7, Status: false, Cnf: &file.Config{}, Flow: &file.Flow{}})
		},
	}, time.Unix(100, 0))
	if err != nil {
		t.Fatalf("RefreshSessionIdentity() error = %v", err)
	}
	if refreshed == nil {
		t.Fatal("RefreshSessionIdentity() returned nil identity")
	}
	if refreshed.Username != "tenant-new" || refreshed.SubjectID != "user:tenant-new" {
		t.Fatalf("RefreshSessionIdentity() identity = %+v, want updated username/subject", refreshed)
	}
	if !reflect.DeepEqual(refreshed.ClientIDs, []int{11, 12}) {
		t.Fatalf("RefreshSessionIdentity() client ids = %v, want [11 12]", refreshed.ClientIDs)
	}
}

func TestRefreshSessionIdentityUsesManagedClientIDFastPath(t *testing.T) {
	identity := (&SessionIdentity{
		Authenticated: true,
		Kind:          "user",
		Provider:      "local",
		SubjectID:     "user:tenant-old",
		Username:      "tenant-old",
		ClientIDs:     []int{99},
		Roles:         []string{RoleUser},
		Attributes: map[string]string{
			"user_id":    "7",
			"login_mode": "password",
		},
	}).Normalize()

	refreshed, err := RefreshSessionIdentity(identity, DefaultPermissionResolver(), stubRepository{
		getUser: func(id int) (*file.User, error) {
			return &file.User{
				Id:        id,
				Username:  "tenant-new",
				Kind:      "local",
				Status:    1,
				TotalFlow: &file.Flow{},
			}, nil
		},
		rangeClients: func(func(*file.Client) bool) {
			t.Fatal("RangeClients() should not be used when GetManagedClientIDsByUserID is available")
		},
		getManagedClientIDs: func(userID int) ([]int, error) {
			if userID != 7 {
				t.Fatalf("GetManagedClientIDsByUserID(%d), want 7", userID)
			}
			return []int{12, 11, 12}, nil
		},
		authoritativeManagedClientIDs: true,
	}, time.Unix(100, 0))
	if err != nil {
		t.Fatalf("RefreshSessionIdentity() error = %v", err)
	}
	if refreshed == nil {
		t.Fatal("RefreshSessionIdentity() returned nil identity")
	}
	if !reflect.DeepEqual(refreshed.ClientIDs, []int{11, 12}) {
		t.Fatalf("RefreshSessionIdentity() client ids = %v, want [11 12]", refreshed.ClientIDs)
	}
}

func TestRefreshSessionIdentityPropagatesUnexpectedManagedClientLookupError(t *testing.T) {
	identity := (&SessionIdentity{
		Authenticated: true,
		Kind:          "user",
		Provider:      "local",
		SubjectID:     "user:tenant-old",
		Username:      "tenant-old",
		ClientIDs:     []int{99},
		Roles:         []string{RoleUser},
		Attributes: map[string]string{
			"user_id":    "7",
			"login_mode": "password",
		},
	}).Normalize()

	lookupErr := errors.New("managed client lookup failed")
	refreshed, err := RefreshSessionIdentity(identity, DefaultPermissionResolver(), stubRepository{
		getUser: func(id int) (*file.User, error) {
			return &file.User{
				Id:        id,
				Username:  "tenant-new",
				Kind:      "local",
				Status:    1,
				TotalFlow: &file.Flow{},
			}, nil
		},
		rangeClients: func(func(*file.Client) bool) {
			t.Fatal("RangeClients() should not be used after GetManagedClientIDsByUserID backend error")
		},
		getManagedClientIDs: func(userID int) ([]int, error) {
			if userID != 7 {
				t.Fatalf("GetManagedClientIDsByUserID(%d), want 7", userID)
			}
			return nil, lookupErr
		},
		authoritativeManagedClientIDs: true,
	}, time.Unix(100, 0))
	if !errors.Is(err, lookupErr) {
		t.Fatalf("RefreshSessionIdentity() error = %v, want %v", err, lookupErr)
	}
	if refreshed != nil {
		t.Fatalf("RefreshSessionIdentity() refreshed = %+v, want nil", refreshed)
	}
}

func TestRefreshSessionIdentityRejectsDisabledClientVKeySession(t *testing.T) {
	identity := (&SessionIdentity{
		Authenticated: true,
		Kind:          "client",
		Provider:      "local",
		SubjectID:     "client:vkey:9",
		Username:      "vkey-client-9",
		ClientIDs:     []int{9},
		Roles:         []string{RoleClient},
		Attributes: map[string]string{
			"client_id":  "9",
			"login_mode": "client_vkey",
		},
	}).Normalize()

	refreshed, err := RefreshSessionIdentity(identity, DefaultPermissionResolver(), stubRepository{
		getClient: func(id int) (*file.Client, error) {
			return &file.Client{
				Id:        id,
				Status:    false,
				VerifyKey: "vk-disabled",
				Cnf:       &file.Config{},
				Flow:      &file.Flow{},
			}, nil
		},
	}, time.Unix(100, 0))
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("RefreshSessionIdentity() error = %v, want %v", err, ErrUnauthenticated)
	}
	if refreshed != nil {
		t.Fatalf("RefreshSessionIdentity() refreshed = %+v, want nil", refreshed)
	}
}

func TestRefreshSessionIdentityPropagatesUnexpectedClientLookupError(t *testing.T) {
	lookupErr := errors.New("client lookup failed")
	identity := (&SessionIdentity{
		Authenticated: true,
		Kind:          "client",
		Provider:      "local",
		SubjectID:     "client:vkey:9",
		Username:      "vkey-client-9",
		ClientIDs:     []int{9},
		Roles:         []string{RoleClient},
		Attributes: map[string]string{
			"client_id":  "9",
			"login_mode": "client_vkey",
		},
	}).Normalize()

	refreshed, err := RefreshSessionIdentity(identity, DefaultPermissionResolver(), stubRepository{
		getClient: func(int) (*file.Client, error) {
			return nil, lookupErr
		},
	}, time.Unix(100, 0))
	if !errors.Is(err, lookupErr) {
		t.Fatalf("RefreshSessionIdentity() error = %v, want %v", err, lookupErr)
	}
	if refreshed != nil {
		t.Fatalf("RefreshSessionIdentity() refreshed = %+v, want nil", refreshed)
	}
}

func TestRefreshSessionIdentityTreatsMissingClientAsUnauthenticated(t *testing.T) {
	identity := (&SessionIdentity{
		Authenticated: true,
		Kind:          "client",
		Provider:      "local",
		SubjectID:     "client:vkey:9",
		Username:      "vkey-client-9",
		ClientIDs:     []int{9},
		Roles:         []string{RoleClient},
		Attributes: map[string]string{
			"client_id":  "9",
			"login_mode": "client_vkey",
		},
	}).Normalize()

	refreshed, err := RefreshSessionIdentity(identity, DefaultPermissionResolver(), stubRepository{
		getClient: func(int) (*file.Client, error) {
			return nil, file.ErrClientNotFound
		},
	}, time.Unix(100, 0))
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("RefreshSessionIdentity() error = %v, want %v", err, ErrUnauthenticated)
	}
	if refreshed != nil {
		t.Fatalf("RefreshSessionIdentity() refreshed = %+v, want nil", refreshed)
	}
}

func TestRefreshSessionIdentityKeepsClientVKeyScopeFresh(t *testing.T) {
	identity := (&SessionIdentity{
		Authenticated: true,
		Kind:          "client",
		Provider:      "local",
		SubjectID:     "client:vkey:9",
		Username:      "vkey-client-stale",
		ClientIDs:     []int{99},
		Roles:         []string{RoleClient},
		Attributes: map[string]string{
			"client_id":  "9",
			"login_mode": "vkey",
		},
	}).Normalize()

	refreshed, err := RefreshSessionIdentity(identity, DefaultPermissionResolver(), stubRepository{
		getClient: func(id int) (*file.Client, error) {
			return &file.Client{
				Id:        id,
				Status:    true,
				VerifyKey: "vk-live",
				Cnf:       &file.Config{},
				Flow:      &file.Flow{},
			}, nil
		},
	}, time.Unix(100, 0))
	if err != nil {
		t.Fatalf("RefreshSessionIdentity() error = %v", err)
	}
	if refreshed == nil {
		t.Fatal("RefreshSessionIdentity() returned nil identity")
	}
	if refreshed.Kind != "client" || refreshed.SubjectID != "client:vkey:9" || refreshed.Username != "vkey-client-9" {
		t.Fatalf("RefreshSessionIdentity() identity = %+v, want refreshed client principal", refreshed)
	}
	if !reflect.DeepEqual(refreshed.ClientIDs, []int{9}) {
		t.Fatalf("RefreshSessionIdentity() client ids = %v, want [9]", refreshed.ClientIDs)
	}
	if refreshed.Attributes["login_mode"] != "client_vkey" || refreshed.Attributes["client_id"] != "9" {
		t.Fatalf("RefreshSessionIdentity() attributes = %v, want refreshed client_vkey metadata", refreshed.Attributes)
	}
}

func TestRefreshSessionIdentityUsesBoundClientOwnerWithoutExtraLookup(t *testing.T) {
	identity := (&SessionIdentity{
		Authenticated: true,
		Kind:          "client",
		Provider:      "local",
		SubjectID:     "client:vkey:9",
		Username:      "vkey-client-9",
		ClientIDs:     []int{9},
		Roles:         []string{RoleClient},
		Attributes: map[string]string{
			"client_id":  "9",
			"login_mode": "client_vkey",
		},
	}).Normalize()

	owner := &file.User{
		Id:        3,
		Username:  "tenant",
		Kind:      "local",
		Status:    1,
		TotalFlow: &file.Flow{},
	}
	client := &file.Client{
		Id:          9,
		OwnerUserID: 3,
		Status:      true,
		VerifyKey:   "vk-live",
		Cnf:         &file.Config{},
		Flow:        &file.Flow{},
	}
	client.BindOwnerUser(owner)

	refreshed, err := RefreshSessionIdentity(identity, DefaultPermissionResolver(), stubRepository{
		getClient: func(id int) (*file.Client, error) {
			if id != 9 {
				t.Fatalf("GetClient(%d), want 9", id)
			}
			return client, nil
		},
		getUser: func(int) (*file.User, error) {
			t.Fatal("GetUser() should not be used when refreshed client already carries a bound owner snapshot")
			return nil, nil
		},
	}, time.Unix(100, 0))
	if err != nil {
		t.Fatalf("RefreshSessionIdentity() error = %v", err)
	}
	if refreshed == nil {
		t.Fatal("RefreshSessionIdentity() returned nil identity")
	}
	if refreshed.Kind != "client" || refreshed.SubjectID != "client:vkey:9" || refreshed.Username != "vkey-client-9" {
		t.Fatalf("RefreshSessionIdentity() identity = %+v, want refreshed client principal", refreshed)
	}
	if !reflect.DeepEqual(refreshed.ClientIDs, []int{9}) {
		t.Fatalf("RefreshSessionIdentity() client ids = %v, want [9]", refreshed.ClientIDs)
	}
}

func TestRefreshSessionIdentityRejectsBoundClientWhenOwnerDisabled(t *testing.T) {
	identity := (&SessionIdentity{
		Authenticated: true,
		Kind:          "client",
		Provider:      "local",
		SubjectID:     "client:vkey:9",
		Username:      "vkey-client-9",
		ClientIDs:     []int{9},
		Roles:         []string{RoleClient},
		Attributes: map[string]string{
			"client_id":  "9",
			"login_mode": "client_vkey",
		},
	}).Normalize()

	refreshed, err := RefreshSessionIdentity(identity, DefaultPermissionResolver(), stubRepository{
		getClient: func(id int) (*file.Client, error) {
			return &file.Client{
				Id:        id,
				UserId:    3,
				Status:    true,
				VerifyKey: "vk-live",
				Cnf:       &file.Config{},
				Flow:      &file.Flow{},
			}, nil
		},
		getUser: func(id int) (*file.User, error) {
			return &file.User{
				Id:        id,
				Username:  "tenant",
				Kind:      "local",
				Status:    0,
				TotalFlow: &file.Flow{},
			}, nil
		},
	}, time.Unix(100, 0))
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("RefreshSessionIdentity() error = %v, want %v", err, ErrUnauthenticated)
	}
	if refreshed != nil {
		t.Fatalf("RefreshSessionIdentity() refreshed = %+v, want nil", refreshed)
	}
}

func TestRefreshSessionIdentityTreatsMissingClientOwnerAsUnauthenticated(t *testing.T) {
	identity := (&SessionIdentity{
		Authenticated: true,
		Kind:          "client",
		Provider:      "local",
		SubjectID:     "client:vkey:9",
		Username:      "vkey-client-9",
		ClientIDs:     []int{9},
		Roles:         []string{RoleClient},
		Attributes: map[string]string{
			"client_id":  "9",
			"login_mode": "client_vkey",
		},
	}).Normalize()

	refreshed, err := RefreshSessionIdentity(identity, DefaultPermissionResolver(), stubRepository{
		getClient: func(id int) (*file.Client, error) {
			return &file.Client{
				Id:          id,
				OwnerUserID: 3,
				Status:      true,
				VerifyKey:   "vk-live",
				Cnf:         &file.Config{},
				Flow:        &file.Flow{},
			}, nil
		},
		getUser: func(int) (*file.User, error) {
			return nil, file.ErrUserNotFound
		},
	}, time.Unix(100, 0))
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("RefreshSessionIdentity() error = %v, want %v", err, ErrUnauthenticated)
	}
	if refreshed != nil {
		t.Fatalf("RefreshSessionIdentity() refreshed = %+v, want nil", refreshed)
	}
}

func TestRefreshSessionIdentityPropagatesUnexpectedClientOwnerLookupError(t *testing.T) {
	lookupErr := errors.New("owner lookup failed")
	identity := (&SessionIdentity{
		Authenticated: true,
		Kind:          "client",
		Provider:      "local",
		SubjectID:     "client:vkey:9",
		Username:      "vkey-client-9",
		ClientIDs:     []int{9},
		Roles:         []string{RoleClient},
		Attributes: map[string]string{
			"client_id":  "9",
			"login_mode": "client_vkey",
		},
	}).Normalize()

	refreshed, err := RefreshSessionIdentity(identity, DefaultPermissionResolver(), stubRepository{
		getClient: func(id int) (*file.Client, error) {
			return &file.Client{
				Id:          id,
				OwnerUserID: 3,
				Status:      true,
				VerifyKey:   "vk-live",
				Cnf:         &file.Config{},
				Flow:        &file.Flow{},
			}, nil
		},
		getUser: func(int) (*file.User, error) {
			return nil, lookupErr
		},
	}, time.Unix(100, 0))
	if !errors.Is(err, lookupErr) {
		t.Fatalf("RefreshSessionIdentity() error = %v, want %v", err, lookupErr)
	}
	if refreshed != nil {
		t.Fatalf("RefreshSessionIdentity() refreshed = %+v, want nil", refreshed)
	}
}
