package service

import (
	"errors"
	"reflect"
	"testing"

	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
)

type rangeFallbackOwnedClientLookupStoreRepo struct {
	rangeUsers           func(func(*file.User) bool)
	rangeClients         func(func(*file.Client) bool)
	getClientIDsByUserID func(int) ([]int, error)
}

func (r rangeFallbackOwnedClientLookupStoreRepo) RangeUsers(fn func(*file.User) bool) {
	if r.rangeUsers != nil {
		r.rangeUsers(fn)
	}
}

func (r rangeFallbackOwnedClientLookupStoreRepo) RangeClients(fn func(*file.Client) bool) {
	if r.rangeClients != nil {
		r.rangeClients(fn)
	}
}

func (r rangeFallbackOwnedClientLookupStoreRepo) GetClientIDsByUserID(userID int) ([]int, error) {
	if r.getClientIDsByUserID != nil {
		return r.getClientIDsByUserID(userID)
	}
	return nil, nil
}

func (rangeFallbackOwnedClientLookupStoreRepo) SupportsGetClientIDsByUserID() bool {
	return true
}

func TestDefaultManagementPlatformStoreServiceUsernameUsesRepositoryUsers(t *testing.T) {
	store := DefaultManagementPlatformStore{
		Backend: Backend{
			Repository: stubRepository{
				rangeUsers: func(fn func(*file.User) bool) {
					fn(&file.User{Id: 1, Username: "local", Kind: "local"})
					fn(&file.User{Id: 2, Username: "svc-master-a", Kind: "platform_service", ExternalPlatformID: "master-a", Hidden: true})
				},
			},
		},
	}

	if got := store.ServiceUsername("master-a", "configured-master-a"); got != "svc-master-a" {
		t.Fatalf("ServiceUsername(master-a) = %q, want svc-master-a", got)
	}
	if got := store.ServiceUsername("master-b", "configured-master-b"); got != "configured-master-b" {
		t.Fatalf("ServiceUsername(master-b) = %q, want configured-master-b", got)
	}
	if got := store.ServiceUsername("master-c", ""); got != servercfg.DefaultPlatformServiceUsername("master-c") {
		t.Fatalf("ServiceUsername(master-c) = %q, want %q", got, servercfg.DefaultPlatformServiceUsername("master-c"))
	}
}

func TestDefaultManagementPlatformStoreServiceUsernameUsesFastLookupWhenAvailable(t *testing.T) {
	store := DefaultManagementPlatformStore{
		Backend: Backend{
			Repository: stubRepository{
				rangeUsers: func(func(*file.User) bool) {
					t.Fatal("RangeUsers() should not be used when GetUserByExternalPlatformID is available")
				},
				getUserByPlatformID: func(platformID string) (*file.User, error) {
					if platformID != "master-a" {
						t.Fatalf("GetUserByExternalPlatformID(%q), want master-a", platformID)
					}
					return &file.User{Id: 2, Username: "svc-master-a", Kind: "platform_service", ExternalPlatformID: "master-a", Hidden: true}, nil
				},
			},
		},
	}

	if got := store.ServiceUsername("master-a", "configured-master-a"); got != "svc-master-a" {
		t.Fatalf("ServiceUsername(master-a) = %q, want svc-master-a", got)
	}
}

func TestDefaultManagementPlatformStoreLookupServiceUserPropagatesFastLookupErrors(t *testing.T) {
	backendErr := errors.New("platform user lookup failed")
	store := DefaultManagementPlatformStore{
		Backend: Backend{
			Repository: stubRepository{
				rangeUsers: func(func(*file.User) bool) {
					t.Fatal("RangeUsers() should not run after fast lookup backend error")
				},
				getUserByPlatformID: func(platformID string) (*file.User, error) {
					if platformID != "master-a" {
						t.Fatalf("GetUserByExternalPlatformID(%q), want master-a", platformID)
					}
					return nil, backendErr
				},
			},
		},
	}

	user, err := store.lookupServiceUser("master-a")
	if !errors.Is(err, backendErr) {
		t.Fatalf("lookupServiceUser(master-a) error = %v, want %v", err, backendErr)
	}
	if user != nil {
		t.Fatalf("lookupServiceUser(master-a) user = %+v, want nil on backend error", user)
	}
}

func TestDefaultManagementPlatformStoreEnsureServiceUserPropagatesLookupErrors(t *testing.T) {
	backendErr := errors.New("platform user lookup failed")
	store := DefaultManagementPlatformStore{
		Backend: Backend{
			Repository: stubRepository{
				rangeUsers: func(func(*file.User) bool) {
					t.Fatal("RangeUsers() should not run after fast lookup backend error")
				},
				getUserByPlatformID: func(platformID string) (*file.User, error) {
					if platformID != "master-a" {
						t.Fatalf("GetUserByExternalPlatformID(%q), want master-a", platformID)
					}
					return nil, backendErr
				},
			},
		},
	}

	user, err := store.EnsureServiceUser(servercfg.ManagementPlatformConfig{
		PlatformID:      "master-a",
		ServiceUsername: "svc-master-a",
		Enabled:         true,
	})
	if !errors.Is(err, backendErr) {
		t.Fatalf("EnsureServiceUser(master-a) error = %v, want %v", err, backendErr)
	}
	if user != nil {
		t.Fatalf("EnsureServiceUser(master-a) user = %+v, want nil on backend error", user)
	}
}

func TestDefaultManagementPlatformStoreOwnedClientIDsUsesRepositoryClients(t *testing.T) {
	store := DefaultManagementPlatformStore{
		Backend: Backend{
			Repository: stubRepository{
				rangeClients: func(fn func(*file.Client) bool) {
					fn(&file.Client{Id: 9, OwnerUserID: 7})
					fn(&file.Client{Id: 3, OwnerUserID: 7})
					fn(&file.Client{Id: 4, OwnerUserID: 8})
					fn(&file.Client{Id: 0, OwnerUserID: 7})
					fn(&file.Client{Id: 5, UserId: 7})
				},
			},
		},
	}

	got, err := store.OwnedClientIDs(7)
	if err != nil {
		t.Fatalf("OwnedClientIDs(7) error = %v", err)
	}
	if !reflect.DeepEqual(got, []int{3, 5, 9}) {
		t.Fatalf("OwnedClientIDs(7) = %v, want [3 5 9]", got)
	}
}

func TestDefaultManagementPlatformStoreOwnedClientIDsUsesFastLookupWhenAvailable(t *testing.T) {
	store := DefaultManagementPlatformStore{
		Backend: Backend{
			Repository: stubRepository{
				rangeClients: func(func(*file.Client) bool) {
					t.Fatal("RangeClients() should not be used when GetClientsByUserID is available")
				},
				getClientsByUserID: func(userID int) ([]*file.Client, error) {
					if userID != 7 {
						t.Fatalf("GetClientsByUserID(%d), want 7", userID)
					}
					return []*file.Client{
						{Id: 9, OwnerUserID: 7},
						{Id: 3, OwnerUserID: 7},
						{Id: 3, OwnerUserID: 7},
						{Id: 4, OwnerUserID: 8},
						nil,
						{Id: 0, OwnerUserID: 7},
						{Id: 5, UserId: 7},
					}, nil
				},
			},
		},
	}

	got, err := store.OwnedClientIDs(7)
	if err != nil {
		t.Fatalf("OwnedClientIDs(7) error = %v", err)
	}
	if !reflect.DeepEqual(got, []int{3, 5, 9}) {
		t.Fatalf("OwnedClientIDs(7) = %v, want [3 5 9]", got)
	}
}

func TestDefaultManagementPlatformStoreOwnedClientIDsUsesIDFastPathWhenAvailable(t *testing.T) {
	store := DefaultManagementPlatformStore{
		Backend: Backend{
			Repository: stubRepository{
				rangeClients: func(func(*file.Client) bool) {
					t.Fatal("RangeClients() should not be used when GetClientIDsByUserID is available")
				},
				getClientsByUserID: func(int) ([]*file.Client, error) {
					t.Fatal("GetClientsByUserID() should not be used when GetClientIDsByUserID is available")
					return nil, nil
				},
				getClientIDsByUserID: func(userID int) ([]int, error) {
					if userID != 7 {
						t.Fatalf("GetClientIDsByUserID(%d), want 7", userID)
					}
					return []int{9, 3, 3, 0, 5}, nil
				},
				authoritativeClientIDsByUserID: true,
			},
		},
	}

	got, err := store.OwnedClientIDs(7)
	if err != nil {
		t.Fatalf("OwnedClientIDs(7) error = %v", err)
	}
	if !reflect.DeepEqual(got, []int{3, 5, 9}) {
		t.Fatalf("OwnedClientIDs(7) = %v, want [3 5 9]", got)
	}
}

func TestDefaultManagementPlatformStoreOwnedClientIDsUsesVerifiedLookupWhenIDFastPathIsNotAuthoritative(t *testing.T) {
	store := DefaultManagementPlatformStore{
		Backend: Backend{
			Repository: stubRepository{
				rangeClients: func(func(*file.Client) bool) {
					t.Fatal("RangeClients() should not be used when verified lookup is available")
				},
				getClientIDsByUserID: func(userID int) ([]int, error) {
					if userID != 7 {
						t.Fatalf("GetClientIDsByUserID(%d), want 7", userID)
					}
					return []int{9, 3, 5}, nil
				},
				getClientsByUserID: func(userID int) ([]*file.Client, error) {
					if userID != 7 {
						t.Fatalf("GetClientsByUserID(%d), want 7", userID)
					}
					return []*file.Client{
						{Id: 3, OwnerUserID: 7},
						{Id: 5, UserId: 7},
						{Id: 9, OwnerUserID: 8},
						{Id: 3, OwnerUserID: 7},
					}, nil
				},
			},
		},
	}

	got, err := store.OwnedClientIDs(7)
	if err != nil {
		t.Fatalf("OwnedClientIDs(7) error = %v", err)
	}
	if !reflect.DeepEqual(got, []int{3, 5}) {
		t.Fatalf("OwnedClientIDs(7) = %v, want [3 5] after verified filtering", got)
	}
}

func TestDefaultManagementPlatformStoreOwnedClientIDsFallsBackToRangeWhenIDFastPathIsNotAuthoritativeAndCannotBeVerified(t *testing.T) {
	rangeCalls := 0
	store := DefaultManagementPlatformStore{
		Repo: rangeFallbackOwnedClientLookupStoreRepo{
			getClientIDsByUserID: func(userID int) ([]int, error) {
				if userID != 7 {
					t.Fatalf("GetClientIDsByUserID(%d), want 7", userID)
				}
				return []int{9, 3, 5}, nil
			},
			rangeClients: func(fn func(*file.Client) bool) {
				rangeCalls++
				fn(&file.Client{Id: 3, OwnerUserID: 7})
				fn(&file.Client{Id: 5, UserId: 7})
				fn(&file.Client{Id: 9, OwnerUserID: 8})
			},
		},
	}

	got, err := store.OwnedClientIDs(7)
	if err != nil {
		t.Fatalf("OwnedClientIDs(7) error = %v", err)
	}
	if rangeCalls != 1 {
		t.Fatalf("RangeClients() calls = %d, want 1 fallback verification pass", rangeCalls)
	}
	if !reflect.DeepEqual(got, []int{3, 5}) {
		t.Fatalf("OwnedClientIDs(7) = %v, want [3 5] after range verification", got)
	}
}

func TestDefaultManagementPlatformStoreOwnedClientIDsFailsClosedOnFastLookupError(t *testing.T) {
	backendErr := errors.New("client ownership lookup failed")
	store := DefaultManagementPlatformStore{
		Backend: Backend{
			Repository: stubRepository{
				rangeClients: func(func(*file.Client) bool) {
					t.Fatal("RangeClients() should not run after fast lookup backend error")
				},
				getClientsByUserID: func(userID int) ([]*file.Client, error) {
					if userID != 7 {
						t.Fatalf("GetClientsByUserID(%d), want 7", userID)
					}
					return nil, backendErr
				},
			},
		},
	}

	got, err := store.OwnedClientIDs(7)
	if !errors.Is(err, backendErr) {
		t.Fatalf("OwnedClientIDs(7) error = %v, want %v", err, backendErr)
	}
	if len(got) != 0 {
		t.Fatalf("OwnedClientIDs(7) = %v, want nil/empty on backend error", got)
	}
}

func TestInMemoryManagementPlatformRuntimeStatusStoreTracksCallbackAndReverseFields(t *testing.T) {
	store := NewInMemoryManagementPlatformRuntimeStatusStore()
	store.NoteConfigured("master-a", "reverse", "wss://master-a/ws", true)
	store.NoteCallbackConfigured("master-a", "https://master-a/callback", true, 5, 3, 2, 4, 1)
	store.NoteReverseConnected("master-a")
	store.NoteReverseHello("master-a")
	store.NoteCallbackQueued("master-a", 2, true)
	store.NoteCallbackFailed("master-a", errors.New("callback failed"), 502)
	store.NoteCallbackDelivered("master-a", 204)

	status := store.Status("master-a")
	if status.ConnectMode != "reverse" || status.ReverseWSURL != "wss://master-a/ws" || !status.ReverseEnabled {
		t.Fatalf("reverse config = %+v", status)
	}
	if status.CallbackURL != "https://master-a/callback" || !status.CallbackEnabled || status.CallbackTimeoutSeconds != 5 || status.CallbackRetryMax != 3 || status.CallbackRetryBackoffSec != 2 || status.CallbackQueueMax != 4 {
		t.Fatalf("callback config = %+v", status)
	}
	if !status.ReverseConnected || status.LastConnectedAt == 0 || status.LastHelloAt == 0 {
		t.Fatalf("reverse runtime = %+v", status)
	}
	if status.CallbackQueueSize != 2 || status.CallbackDropped != 1 {
		t.Fatalf("callback queue = %+v", status)
	}
	if status.CallbackFailures != 1 || status.LastCallbackStatusCode != 204 || status.CallbackDeliveries != 1 || status.CallbackConsecutiveFailures != 0 {
		t.Fatalf("callback delivery/failure = %+v", status)
	}
	if status.LastCallbackAt == 0 || status.LastCallbackSuccessAt == 0 {
		t.Fatalf("callback timestamps = %+v", status)
	}
	if status.LastCallbackError != "" || status.LastCallbackErrorAt != 0 {
		t.Fatalf("callback error should be cleared after success: %+v", status)
	}
}

func TestInMemoryManagementPlatformRuntimeStatusStoreResetAndBlankPlatformSafety(t *testing.T) {
	store := NewInMemoryManagementPlatformRuntimeStatusStore()
	store.NoteCallbackConfigured(" ", "https://ignored.example/callback", true, 5, 3, 2, 4, 1)
	if status := store.Status(" "); status != (ManagementPlatformReverseRuntimeStatus{}) {
		t.Fatalf("blank Status() = %+v, want zero value", status)
	}

	store.NoteConfigured("master-a", "direct", "", false)
	store.Reset()
	if status := store.Status("master-a"); status != (ManagementPlatformReverseRuntimeStatus{}) {
		t.Fatalf("Status(master-a) after Reset() = %+v, want zero value", status)
	}
}
