package service

import (
	"errors"
	"strings"
	"testing"

	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
)

type rangeFallbackUserOwnedClientRepo struct {
	getUser              func(int) (*file.User, error)
	saveUser             func(*file.User) error
	rangeClients         func(func(*file.Client) bool)
	getClientIDsByUserID func(int) ([]int, error)
}

func (r rangeFallbackUserOwnedClientRepo) RangeClients(fn func(*file.Client) bool) {
	if r.rangeClients != nil {
		r.rangeClients(fn)
	}
}

func (rangeFallbackUserOwnedClientRepo) RangeUsers(func(*file.User) bool)     {}
func (rangeFallbackUserOwnedClientRepo) RangeTunnels(func(*file.Tunnel) bool) {}
func (rangeFallbackUserOwnedClientRepo) RangeHosts(func(*file.Host) bool)     {}
func (rangeFallbackUserOwnedClientRepo) NextUserID() int                      { return 0 }
func (r rangeFallbackUserOwnedClientRepo) GetUser(id int) (*file.User, error) {
	if r.getUser != nil {
		return r.getUser(id)
	}
	return nil, nil
}
func (rangeFallbackUserOwnedClientRepo) CreateUser(*file.User) error { return nil }
func (r rangeFallbackUserOwnedClientRepo) SaveUser(user *file.User) error {
	if r.saveUser != nil {
		return r.saveUser(user)
	}
	return nil
}
func (rangeFallbackUserOwnedClientRepo) SaveClient(*file.Client) error { return nil }
func (rangeFallbackUserOwnedClientRepo) DeleteClient(int) error        { return nil }
func (rangeFallbackUserOwnedClientRepo) DeleteUser(int) error          { return nil }
func (r rangeFallbackUserOwnedClientRepo) GetClientIDsByUserID(userID int) ([]int, error) {
	if r.getClientIDsByUserID != nil {
		return r.getClientIDsByUserID(userID)
	}
	return nil, nil
}
func (rangeFallbackUserOwnedClientRepo) SupportsGetClientIDsByUserID() bool { return true }

func TestDefaultUserServiceGetRejectsHiddenServiceUser(t *testing.T) {
	service := DefaultUserService{
		Backend: Backend{
			Repository: stubRepository{
				getUser: func(id int) (*file.User, error) {
					return &file.User{
						Id:                 id,
						Username:           "__platform_master-a",
						Kind:               "platform_service",
						ExternalPlatformID: "master-a",
						Hidden:             true,
						TotalFlow:          &file.Flow{},
					}, nil
				},
			},
		},
	}

	user, err := service.Get(7)
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("Get() error = %v, want %v", err, ErrForbidden)
	}
	if user != nil {
		t.Fatalf("Get() user = %+v, want nil for hidden service user", user)
	}
}

func TestDefaultUserServiceGetMapsMissingRepositoryUser(t *testing.T) {
	service := DefaultUserService{
		Backend: Backend{
			Repository: stubRepository{
				getUser: func(id int) (*file.User, error) {
					return nil, file.ErrUserNotFound
				},
			},
		},
	}

	if _, err := service.Get(7); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("Get() error = %v, want %v", err, ErrUserNotFound)
	}
}

func TestDefaultUserServiceGetReturnsWorkingCopy(t *testing.T) {
	original := &file.User{
		Id:             7,
		Username:       "tenant",
		Password:       "secret",
		Kind:           "local",
		Status:         1,
		TotalFlow:      &file.Flow{InletFlow: 11, ExportFlow: 12},
		MaxClients:     1,
		MaxTunnels:     2,
		MaxHosts:       3,
		MaxConnections: 4,
		RateLimit:      5,
		DestAclMode:    file.AclWhitelist,
		DestAclRules:   "full:db.internal.example",
	}
	service := DefaultUserService{
		Backend: Backend{
			Repository: stubRepository{
				getUser: func(id int) (*file.User, error) {
					return original, nil
				},
			},
		},
	}

	got, err := service.Get(7)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got == original {
		t.Fatal("Get() returned original live user pointer")
	}
	if got.DestAclMode != original.DestAclMode || got.DestAclRules != original.DestAclRules {
		t.Fatalf("Get() destination ACL = (%d, %q), want (%d, %q)", got.DestAclMode, got.DestAclRules, original.DestAclMode, original.DestAclRules)
	}
	got.Username = "changed"
	got.TotalFlow.InletFlow = 99
	if original.Username != "tenant" || original.TotalFlow.InletFlow != 11 {
		t.Fatalf("original user mutated through Get() result = %+v", original)
	}
}

func TestDefaultUserServiceOperationsMapNilRepositoryUser(t *testing.T) {
	service := DefaultUserService{
		Backend: Backend{
			Repository: stubRepository{},
			Runtime:    stubRuntime{},
		},
	}
	cases := []struct {
		name string
		run  func() error
	}{
		{
			name: "Get",
			run: func() error {
				_, err := service.Get(7)
				return err
			},
		},
		{
			name: "Edit",
			run: func() error {
				_, err := service.Edit(EditUserInput{ID: 7})
				return err
			},
		},
		{
			name: "ChangeStatus",
			run: func() error {
				_, err := service.ChangeStatus(7, false)
				return err
			},
		},
		{
			name: "Delete",
			run: func() error {
				_, err := service.Delete(7)
				return err
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, ErrUserNotFound) {
				t.Fatalf("%s() error = %v, want %v", tc.name, err, ErrUserNotFound)
			}
		})
	}
}

func TestDefaultUserServiceAddRejectsInvalidTOTPSecret(t *testing.T) {
	service := DefaultUserService{}
	_, err := service.Add(AddUserInput{
		ReservedAdminUsername: "admin",
		Username:              "tenant",
		Password:              "secret",
		TOTPSecret:            "invalid-secret",
		Status:                true,
	})
	if !errors.Is(err, ErrInvalidTOTPSecret) {
		t.Fatalf("Add() error = %v, want %v", err, ErrInvalidTOTPSecret)
	}
}

func TestDefaultUserServiceAddAllowsTOTPOnlyUser(t *testing.T) {
	secret, err := crypt.GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("GenerateTOTPSecret() error = %v", err)
	}
	var created *file.User
	service := DefaultUserService{
		Backend: Backend{
			Repository: stubRepository{
				nextUserID: func() int { return 21 },
				createUser: func(user *file.User) error {
					created = user
					return nil
				},
			},
		},
	}

	result, err := service.Add(AddUserInput{
		ReservedAdminUsername: "admin",
		Username:              "tenant",
		Password:              "",
		TOTPSecret:            secret,
		Status:                true,
	})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if result.ID != 21 || created == nil {
		t.Fatalf("Add() id=%d created=%+v, want id 21 and created user", result.ID, created)
	}
	if created.Password != "" || created.TOTPSecret != secret {
		t.Fatalf("Add() created user credentials = password:%q totp:%q, want empty password and %q", created.Password, created.TOTPSecret, secret)
	}
}

func TestDefaultUserServiceEditStoresNormalizedTOTPSecret(t *testing.T) {
	secret, err := crypt.GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("GenerateTOTPSecret() error = %v", err)
	}
	var saved *file.User
	service := DefaultUserService{
		Backend: Backend{
			Repository: stubRepository{
				getUser: func(id int) (*file.User, error) {
					return &file.User{
						Id:        id,
						Username:  "tenant",
						Password:  "secret",
						Kind:      "local",
						Status:    1,
						TotalFlow: &file.Flow{},
					}, nil
				},
				saveUser: func(user *file.User) error {
					saved = user
					return nil
				},
			},
		},
	}

	_, err = service.Edit(EditUserInput{
		ID:                    7,
		ReservedAdminUsername: "admin",
		Username:              "tenant",
		Password:              "secret",
		TOTPSecret:            " " + strings.ToLower(secret) + " ",
		Status:                true,
	})
	if err != nil {
		t.Fatalf("Edit() error = %v", err)
	}
	if saved == nil || saved.TOTPSecret != secret {
		t.Fatalf("Edit() saved user = %+v, want normalized TOTP secret %q", saved, secret)
	}
}

func TestDefaultUserServiceEditAllowsClearingPasswordWhenTOTPRemainsEnabled(t *testing.T) {
	secret, err := crypt.GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("GenerateTOTPSecret() error = %v", err)
	}
	var saved *file.User
	service := DefaultUserService{
		Backend: Backend{
			Repository: stubRepository{
				getUser: func(id int) (*file.User, error) {
					return &file.User{
						Id:         id,
						Username:   "tenant",
						Password:   "secret",
						TOTPSecret: secret,
						Kind:       "local",
						Status:     1,
						TotalFlow:  &file.Flow{},
					}, nil
				},
				saveUser: func(user *file.User) error {
					saved = user
					return nil
				},
			},
		},
	}

	_, err = service.Edit(EditUserInput{
		ID:                    7,
		ReservedAdminUsername: "admin",
		Username:              "tenant",
		Password:              "",
		PasswordProvided:      true,
		Status:                true,
	})
	if err != nil {
		t.Fatalf("Edit() error = %v", err)
	}
	if saved == nil || saved.Password != "" || saved.TOTPSecret != secret {
		t.Fatalf("Edit() saved user = %+v, want cleared password and preserved TOTP", saved)
	}
}

func TestDefaultUserServiceEditPreservesCurrentStatusWhenInputOmitsStatus(t *testing.T) {
	var saved *file.User
	service := DefaultUserService{
		Backend: Backend{
			Repository: stubRepository{
				getUser: func(id int) (*file.User, error) {
					return &file.User{
						Id:        id,
						Username:  "tenant",
						Password:  "secret",
						Kind:      "local",
						Status:    1,
						TotalFlow: &file.Flow{},
					}, nil
				},
				saveUser: func(user *file.User) error {
					saved = user
					return nil
				},
			},
		},
	}

	_, err := service.Edit(EditUserInput{
		ID:                    7,
		ReservedAdminUsername: "admin",
		Username:              "tenant",
		Password:              "secret",
	})
	if err != nil {
		t.Fatalf("Edit() error = %v", err)
	}
	if saved == nil || saved.Status != 1 {
		t.Fatalf("Edit() saved user = %+v, want current enabled status preserved", saved)
	}
}

func TestDefaultUserServiceEditAppliesExplicitDisabledStatus(t *testing.T) {
	var saved *file.User
	service := DefaultUserService{
		Backend: Backend{
			Repository: stubRepository{
				getUser: func(id int) (*file.User, error) {
					return &file.User{
						Id:        id,
						Username:  "tenant",
						Password:  "secret",
						Kind:      "local",
						Status:    1,
						TotalFlow: &file.Flow{},
					}, nil
				},
				saveUser: func(user *file.User) error {
					saved = user
					return nil
				},
			},
		},
	}

	_, err := service.Edit(EditUserInput{
		ID:                    7,
		ReservedAdminUsername: "admin",
		Username:              "tenant",
		Password:              "secret",
		Status:                false,
		StatusProvided:        true,
	})
	if err != nil {
		t.Fatalf("Edit() error = %v", err)
	}
	if saved == nil || saved.Status != 0 {
		t.Fatalf("Edit() saved user = %+v, want explicit disabled status", saved)
	}
}

func TestDefaultUserServiceEditClearsDestinationACLRulesWhenDisabled(t *testing.T) {
	var saved *file.User
	service := DefaultUserService{
		Backend: Backend{
			Repository: stubRepository{
				getUser: func(id int) (*file.User, error) {
					return &file.User{
						Id:           id,
						Username:     "tenant",
						Password:     "secret",
						Kind:         "local",
						Status:       1,
						TotalFlow:    &file.Flow{},
						DestAclMode:  file.AclWhitelist,
						DestAclRules: "full:db.internal.example",
					}, nil
				},
				saveUser: func(user *file.User) error {
					saved = user
					return nil
				},
			},
		},
	}

	_, err := service.Edit(EditUserInput{
		ID:                    7,
		ReservedAdminUsername: "admin",
		Username:              "tenant",
		Password:              "secret",
		Status:                true,
		DestACLMode:           file.AclOff,
		DestACLRules:          "full:db.internal.example",
	})
	if err != nil {
		t.Fatalf("Edit() error = %v", err)
	}
	if saved == nil {
		t.Fatal("Edit() did not persist user")
	}
	if saved.DestAclMode != file.AclOff || saved.DestAclRules != "" {
		t.Fatalf("saved destination acl = (%d, %q), want (%d, %q)", saved.DestAclMode, saved.DestAclRules, file.AclOff, "")
	}
}

func TestDefaultUserServiceEditRejectsClearingLastCredential(t *testing.T) {
	service := DefaultUserService{
		Backend: Backend{
			Repository: stubRepository{
				getUser: func(id int) (*file.User, error) {
					return &file.User{
						Id:        id,
						Username:  "tenant",
						Password:  "secret",
						Kind:      "local",
						Status:    1,
						TotalFlow: &file.Flow{},
					}, nil
				},
			},
		},
	}

	_, err := service.Edit(EditUserInput{
		ID:                    7,
		ReservedAdminUsername: "admin",
		Username:              "tenant",
		Password:              "",
		PasswordProvided:      true,
		Status:                true,
	})
	if !errors.Is(err, ErrUserPasswordRequired) {
		t.Fatalf("Edit() error = %v, want %v", err, ErrUserPasswordRequired)
	}
}

func TestDefaultUserServiceEditRejectsHiddenServiceUser(t *testing.T) {
	var saveCalled bool
	service := DefaultUserService{
		Backend: Backend{
			Repository: stubRepository{
				getUser: func(id int) (*file.User, error) {
					return &file.User{
						Id:                 id,
						Username:           "__platform_master-a",
						Password:           "secret",
						Kind:               "platform_service",
						ExternalPlatformID: "master-a",
						Hidden:             true,
						TotalFlow:          &file.Flow{},
					}, nil
				},
				saveUser: func(*file.User) error {
					saveCalled = true
					return nil
				},
			},
		},
	}

	_, err := service.Edit(EditUserInput{
		ID:                    7,
		ReservedAdminUsername: "admin",
		Username:              "tenant",
		Status:                true,
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("Edit() error = %v, want %v", err, ErrForbidden)
	}
	if saveCalled {
		t.Fatal("Edit() should not persist hidden service user")
	}
}

func TestDefaultUserServiceChangeStatusRejectsHiddenServiceUser(t *testing.T) {
	var saveCalled bool
	service := DefaultUserService{
		Backend: Backend{
			Repository: stubRepository{
				getUser: func(id int) (*file.User, error) {
					return &file.User{
						Id:                 id,
						Username:           "__platform_master-a",
						Kind:               "platform_service",
						ExternalPlatformID: "master-a",
						Hidden:             true,
						Status:             1,
						TotalFlow:          &file.Flow{},
					}, nil
				},
				saveUser: func(*file.User) error {
					saveCalled = true
					return nil
				},
			},
			Runtime: stubRuntime{},
		},
	}

	_, err := service.ChangeStatus(7, false)
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("ChangeStatus() error = %v, want %v", err, ErrForbidden)
	}
	if saveCalled {
		t.Fatal("ChangeStatus() should not persist hidden service user")
	}
}

func TestDefaultUserServiceListCountsOwnerUserIDFallback(t *testing.T) {
	service := DefaultUserService{
		Backend: Backend{
			Repository: stubRepository{
				rangeClients: func(fn func(*file.Client) bool) {
					fn(&file.Client{Id: 7, OwnerUserID: 3})
				},
				rangeTunnels: func(fn func(*file.Tunnel) bool) {
					fn(&file.Tunnel{Id: 11, Client: &file.Client{Id: 7, OwnerUserID: 3}})
				},
				rangeHosts: func(fn func(*file.Host) bool) {
					fn(&file.Host{Id: 13, Client: &file.Client{Id: 7, OwnerUserID: 3}})
				},
				rangeUsers: func(fn func(*file.User) bool) {
					fn(&file.User{Id: 3, Username: "tenant", Status: 1, TotalFlow: &file.Flow{}})
				},
			},
		},
	}

	result := service.List(ListUsersInput{})
	if result.Total != 1 || len(result.Rows) != 1 {
		t.Fatalf("List() total=%d rows=%d, want 1/1", result.Total, len(result.Rows))
	}
	row := result.Rows[0]
	if row.ClientCount != 1 || row.TunnelCount != 1 || row.HostCount != 1 {
		t.Fatalf("List() row counts = clients:%d tunnels:%d hosts:%d, want 1/1/1", row.ClientCount, row.TunnelCount, row.HostCount)
	}
}

func TestDefaultUserServiceListUsesOwnedResourceCountFastPath(t *testing.T) {
	service := DefaultUserService{
		Backend: Backend{
			Repository: stubRepository{
				rangeClients: func(func(*file.Client) bool) {
					t.Fatal("RangeClients() should not be used when CollectOwnedResourceCounts is available")
				},
				rangeTunnels: func(func(*file.Tunnel) bool) {
					t.Fatal("RangeTunnels() should not be used when CollectOwnedResourceCounts is available")
				},
				rangeHosts: func(func(*file.Host) bool) {
					t.Fatal("RangeHosts() should not be used when CollectOwnedResourceCounts is available")
				},
				collectOwnedCounts: func() (map[int]int, map[int]int, map[int]int, error) {
					return map[int]int{3: 1}, map[int]int{3: 2}, map[int]int{3: 3}, nil
				},
				rangeUsers: func(fn func(*file.User) bool) {
					fn(&file.User{Id: 3, Username: "tenant", Status: 1, TotalFlow: &file.Flow{}})
				},
			},
		},
	}

	result := service.List(ListUsersInput{})
	if result.Total != 1 || len(result.Rows) != 1 {
		t.Fatalf("List() total=%d rows=%d, want 1/1", result.Total, len(result.Rows))
	}
	row := result.Rows[0]
	if row.ClientCount != 1 || row.TunnelCount != 2 || row.HostCount != 3 {
		t.Fatalf("List() row counts = clients:%d tunnels:%d hosts:%d, want 1/2/3", row.ClientCount, row.TunnelCount, row.HostCount)
	}
}

func TestDefaultUserServiceListUsesPerUserOwnedResourceCountFastPath(t *testing.T) {
	service := DefaultUserService{
		Backend: Backend{
			Repository: stubRepository{
				rangeClients: func(func(*file.Client) bool) {
					t.Fatal("RangeClients() should not be used when CountOwnedResourcesByUserID is available")
				},
				rangeTunnels: func(func(*file.Tunnel) bool) {
					t.Fatal("RangeTunnels() should not be used when CountOwnedResourcesByUserID is available")
				},
				rangeHosts: func(func(*file.Host) bool) {
					t.Fatal("RangeHosts() should not be used when CountOwnedResourcesByUserID is available")
				},
				countOwnedByUser: func(userID int) (int, int, int, error) {
					switch userID {
					case 3:
						return 1, 2, 3, nil
					case 4:
						return 0, 0, 0, nil
					default:
						t.Fatalf("CountOwnedResourcesByUserID(%d) unexpected lookup", userID)
						return 0, 0, 0, nil
					}
				},
				rangeUsers: func(fn func(*file.User) bool) {
					fn(&file.User{Id: 3, Username: "tenant-a", Status: 1, TotalFlow: &file.Flow{}})
					fn(&file.User{Id: 4, Username: "tenant-b", Status: 1, TotalFlow: &file.Flow{}})
				},
			},
		},
	}

	result := service.List(ListUsersInput{})
	if result.Total != 2 || len(result.Rows) != 2 {
		t.Fatalf("List() total=%d rows=%d, want 2/2", result.Total, len(result.Rows))
	}
	if result.Rows[0].Id != 3 || result.Rows[0].ClientCount != 1 || result.Rows[0].TunnelCount != 2 || result.Rows[0].HostCount != 3 {
		t.Fatalf("row[0] = %+v, want user 3 counts 1/2/3", result.Rows[0])
	}
	if result.Rows[1].Id != 4 || result.Rows[1].ClientCount != 0 || result.Rows[1].TunnelCount != 0 || result.Rows[1].HostCount != 0 {
		t.Fatalf("row[1] = %+v, want user 4 zero counts", result.Rows[1])
	}
}

func TestDefaultUserServiceListFallsBackAfterPerUserOwnedResourceCountError(t *testing.T) {
	service := DefaultUserService{
		Backend: Backend{
			Repository: stubRepository{
				countOwnedByUser: func(int) (int, int, int, error) {
					return 0, 0, 0, errors.New("count failed")
				},
				rangeClients: func(fn func(*file.Client) bool) {
					fn(&file.Client{Id: 7, OwnerUserID: 3})
				},
				rangeTunnels: func(fn func(*file.Tunnel) bool) {
					fn(&file.Tunnel{Id: 11, Client: &file.Client{Id: 7, OwnerUserID: 3}})
				},
				rangeHosts: func(fn func(*file.Host) bool) {
					fn(&file.Host{Id: 13, Client: &file.Client{Id: 7, OwnerUserID: 3}})
				},
				rangeUsers: func(fn func(*file.User) bool) {
					fn(&file.User{Id: 3, Username: "tenant", Status: 1, TotalFlow: &file.Flow{}})
				},
			},
		},
	}

	result := service.List(ListUsersInput{})
	if result.Total != 1 || len(result.Rows) != 1 {
		t.Fatalf("List() total=%d rows=%d, want 1/1", result.Total, len(result.Rows))
	}
	row := result.Rows[0]
	if row.ClientCount != 1 || row.TunnelCount != 1 || row.HostCount != 1 {
		t.Fatalf("List() row counts = clients:%d tunnels:%d hosts:%d, want 1/1/1", row.ClientCount, row.TunnelCount, row.HostCount)
	}
}

func TestDefaultUserServiceListFallsBackAfterOwnedResourceCountFastPathError(t *testing.T) {
	service := DefaultUserService{
		Backend: Backend{
			Repository: stubRepository{
				collectOwnedCounts: func() (map[int]int, map[int]int, map[int]int, error) {
					return nil, nil, nil, errors.New("count failed")
				},
				rangeClients: func(fn func(*file.Client) bool) {
					fn(&file.Client{Id: 7, OwnerUserID: 3})
				},
				rangeTunnels: func(fn func(*file.Tunnel) bool) {
					fn(&file.Tunnel{Id: 11, Client: &file.Client{Id: 7, OwnerUserID: 3}})
				},
				rangeHosts: func(fn func(*file.Host) bool) {
					fn(&file.Host{Id: 13, Client: &file.Client{Id: 7, OwnerUserID: 3}})
				},
				rangeUsers: func(fn func(*file.User) bool) {
					fn(&file.User{Id: 3, Username: "tenant", Status: 1, TotalFlow: &file.Flow{}})
				},
			},
		},
	}

	result := service.List(ListUsersInput{})
	if result.Total != 1 || len(result.Rows) != 1 {
		t.Fatalf("List() total=%d rows=%d, want 1/1", result.Total, len(result.Rows))
	}
	row := result.Rows[0]
	if row.ClientCount != 1 || row.TunnelCount != 1 || row.HostCount != 1 {
		t.Fatalf("List() row counts = clients:%d tunnels:%d hosts:%d, want 1/1/1", row.ClientCount, row.TunnelCount, row.HostCount)
	}
}

func TestDefaultUserServiceListRespectsOffsetWhenLimitIsZero(t *testing.T) {
	service := DefaultUserService{
		Backend: Backend{
			Repository: stubRepository{
				rangeUsers: func(fn func(*file.User) bool) {
					fn(&file.User{Id: 1, Username: "alpha", Status: 1, TotalFlow: &file.Flow{}})
					fn(&file.User{Id: 2, Username: "bravo", Status: 1, TotalFlow: &file.Flow{}})
					fn(&file.User{Id: 3, Username: "charlie", Status: 1, TotalFlow: &file.Flow{}})
				},
			},
		},
	}

	result := service.List(ListUsersInput{Offset: 1, Limit: 0, Sort: "Id", Order: "asc"})
	if result.Total != 3 || len(result.Rows) != 2 {
		t.Fatalf("List() total=%d rows=%d, want 3/2", result.Total, len(result.Rows))
	}
	if result.Rows[0].Id != 2 || result.Rows[1].Id != 3 {
		t.Fatalf("List() ids = [%d %d], want [2 3]", result.Rows[0].Id, result.Rows[1].Id)
	}
}

func TestDefaultUserServiceListReturnsDetachedUserSnapshots(t *testing.T) {
	original := &file.User{
		Id:         7,
		Username:   "tenant",
		Password:   "secret",
		TOTPSecret: "SECRET123",
		Status:     1,
		TotalFlow:  &file.Flow{InletFlow: 11, ExportFlow: 12},
	}
	service := DefaultUserService{
		Backend: Backend{
			Repository: stubRepository{
				rangeUsers: func(fn func(*file.User) bool) {
					fn(original)
				},
			},
		},
	}

	result := service.List(ListUsersInput{})
	if result.Total != 1 || len(result.Rows) != 1 {
		t.Fatalf("List() total=%d rows=%d, want 1/1", result.Total, len(result.Rows))
	}
	if result.Rows[0] == nil || result.Rows[0].User == nil {
		t.Fatalf("List() row = %#v, want detached user snapshot", result.Rows[0])
	}
	if result.Rows[0].User == original {
		t.Fatal("List() returned original live user pointer")
	}
	result.Rows[0].Username = "changed"
	result.Rows[0].TotalFlow.InletFlow = 99
	if original.Username != "tenant" || original.TotalFlow.InletFlow != 11 {
		t.Fatalf("original user mutated through List() result = %+v", original)
	}
	if original.TotalTraffic != nil || original.TotalMeter != nil || original.Rate != nil {
		t.Fatalf("original runtime fields mutated by List() = traffic:%#v meter:%#v rate:%#v", original.TotalTraffic, original.TotalMeter, original.Rate)
	}
}

func TestDefaultUserServiceChangeStatusDisconnectsOwnerUserIDFallback(t *testing.T) {
	var disconnected []int
	service := DefaultUserService{
		Backend: Backend{
			Repository: stubRepository{
				getUser: func(id int) (*file.User, error) {
					return &file.User{Id: id, Username: "tenant", Status: 1, TotalFlow: &file.Flow{}}, nil
				},
				saveUser: func(*file.User) error {
					return nil
				},
				rangeClients: func(fn func(*file.Client) bool) {
					fn(&file.Client{Id: 9, OwnerUserID: 7})
				},
			},
			Runtime: stubRuntime{
				disconnectClient: func(id int) {
					disconnected = append(disconnected, id)
				},
			},
		},
	}

	if _, err := service.ChangeStatus(7, false); err != nil {
		t.Fatalf("ChangeStatus() error = %v", err)
	}
	if len(disconnected) != 1 || disconnected[0] != 9 {
		t.Fatalf("ChangeStatus() disconnected clients = %v, want [9]", disconnected)
	}
}

func TestDefaultUserServiceChangeStatusUsesUserClientLookupFastPath(t *testing.T) {
	var disconnected []int
	service := DefaultUserService{
		Backend: Backend{
			Repository: stubRepository{
				getUser: func(id int) (*file.User, error) {
					return &file.User{Id: id, Username: "tenant", Status: 1, TotalFlow: &file.Flow{}}, nil
				},
				saveUser: func(*file.User) error {
					return nil
				},
				rangeClients: func(func(*file.Client) bool) {
					t.Fatal("RangeClients() should not be used when GetClientsByUserID is available")
				},
				getClientsByUserID: func(userID int) ([]*file.Client, error) {
					if userID != 7 {
						t.Fatalf("GetClientsByUserID(%d), want 7", userID)
					}
					return []*file.Client{{Id: 9, OwnerUserID: 7}}, nil
				},
			},
			Runtime: stubRuntime{
				disconnectClient: func(id int) {
					disconnected = append(disconnected, id)
				},
			},
		},
	}

	if _, err := service.ChangeStatus(7, false); err != nil {
		t.Fatalf("ChangeStatus() error = %v", err)
	}
	if len(disconnected) != 1 || disconnected[0] != 9 {
		t.Fatalf("ChangeStatus() disconnected clients = %v, want [9]", disconnected)
	}
}

func TestDefaultUserServiceChangeStatusUsesUserClientIDLookupFastPath(t *testing.T) {
	var disconnected []int
	service := DefaultUserService{
		Backend: Backend{
			Repository: stubRepository{
				getUser: func(id int) (*file.User, error) {
					return &file.User{Id: id, Username: "tenant", Status: 1, TotalFlow: &file.Flow{}}, nil
				},
				saveUser: func(*file.User) error {
					return nil
				},
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
					return []int{9}, nil
				},
				authoritativeClientIDsByUserID: true,
			},
			Runtime: stubRuntime{
				disconnectClient: func(id int) {
					disconnected = append(disconnected, id)
				},
			},
		},
	}

	if _, err := service.ChangeStatus(7, false); err != nil {
		t.Fatalf("ChangeStatus() error = %v", err)
	}
	if len(disconnected) != 1 || disconnected[0] != 9 {
		t.Fatalf("ChangeStatus() disconnected clients = %v, want [9]", disconnected)
	}
}

func TestDefaultUserServiceChangeStatusDeduplicatesUserClientIDLookupFastPath(t *testing.T) {
	var disconnected []int
	service := DefaultUserService{
		Backend: Backend{
			Repository: stubRepository{
				getUser: func(id int) (*file.User, error) {
					return &file.User{Id: id, Username: "tenant", Status: 1, TotalFlow: &file.Flow{}}, nil
				},
				saveUser: func(*file.User) error {
					return nil
				},
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
					return []int{9, 9, 0, 9}, nil
				},
				authoritativeClientIDsByUserID: true,
			},
			Runtime: stubRuntime{
				disconnectClient: func(id int) {
					disconnected = append(disconnected, id)
				},
			},
		},
	}

	if _, err := service.ChangeStatus(7, false); err != nil {
		t.Fatalf("ChangeStatus() error = %v", err)
	}
	if len(disconnected) != 1 || disconnected[0] != 9 {
		t.Fatalf("ChangeStatus() disconnected clients = %v, want deduplicated [9]", disconnected)
	}
}

func TestDefaultUserServiceChangeStatusUsesVerifiedUserClientLookupWhenIDFastPathIsNotAuthoritative(t *testing.T) {
	var disconnected []int
	service := DefaultUserService{
		Backend: Backend{
			Repository: stubRepository{
				getUser: func(id int) (*file.User, error) {
					return &file.User{Id: id, Username: "tenant", Status: 1, TotalFlow: &file.Flow{}}, nil
				},
				saveUser: func(*file.User) error {
					return nil
				},
				rangeClients: func(func(*file.Client) bool) {
					t.Fatal("RangeClients() should not be used when verified user lookup is available")
				},
				getClientIDsByUserID: func(userID int) ([]int, error) {
					if userID != 7 {
						t.Fatalf("GetClientIDsByUserID(%d), want 7", userID)
					}
					return []int{9}, nil
				},
				getClientsByUserID: func(userID int) ([]*file.Client, error) {
					if userID != 7 {
						t.Fatalf("GetClientsByUserID(%d), want 7", userID)
					}
					return nil, nil
				},
			},
			Runtime: stubRuntime{
				disconnectClient: func(id int) {
					disconnected = append(disconnected, id)
				},
			},
		},
	}

	if _, err := service.ChangeStatus(7, false); err != nil {
		t.Fatalf("ChangeStatus() error = %v", err)
	}
	if len(disconnected) != 0 {
		t.Fatalf("ChangeStatus() disconnected clients = %v, want none because untrusted id fast path should be verified", disconnected)
	}
}

func TestDefaultUserServiceChangeStatusVerifiesUserClientIDsWhenOnlyIDLookupExists(t *testing.T) {
	var disconnected []int
	service := DefaultUserService{
		Backend: Backend{
			Repository: stubRepository{
				getUser: func(id int) (*file.User, error) {
					return &file.User{Id: id, Username: "tenant", Status: 1, TotalFlow: &file.Flow{}}, nil
				},
				saveUser: func(*file.User) error {
					return nil
				},
				rangeClients: func(func(*file.Client) bool) {
					t.Fatal("RangeClients() should not be used when GetClient can verify user client ids")
				},
				getClientIDsByUserID: func(userID int) ([]int, error) {
					if userID != 7 {
						t.Fatalf("GetClientIDsByUserID(%d), want 7", userID)
					}
					return []int{9, 10, 0, 10, 404}, nil
				},
				getClient: func(id int) (*file.Client, error) {
					switch id {
					case 9:
						return &file.Client{Id: 9, OwnerUserID: 8}, nil
					case 10:
						return &file.Client{Id: 10, OwnerUserID: 7}, nil
					case 404:
						return nil, file.ErrClientNotFound
					default:
						t.Fatalf("GetClient(%d) unexpected lookup", id)
						return nil, nil
					}
				},
			},
			Runtime: stubRuntime{
				disconnectClient: func(id int) {
					disconnected = append(disconnected, id)
				},
			},
		},
	}

	if _, err := service.ChangeStatus(7, false); err != nil {
		t.Fatalf("ChangeStatus() error = %v", err)
	}
	if len(disconnected) != 1 || disconnected[0] != 10 {
		t.Fatalf("ChangeStatus() disconnected clients = %v, want verified deduplicated [10]", disconnected)
	}
}

func TestDefaultUserServiceChangeStatusFallsBackToRangeWhenUserClientIDFastPathIsNotAuthoritativeAndCannotBeVerified(t *testing.T) {
	var disconnected []int
	rangeCalls := 0
	service := DefaultUserService{
		Repo: rangeFallbackUserOwnedClientRepo{
			getUser: func(id int) (*file.User, error) {
				return &file.User{Id: id, Username: "tenant", Status: 1, TotalFlow: &file.Flow{}}, nil
			},
			saveUser: func(*file.User) error {
				return nil
			},
			getClientIDsByUserID: func(userID int) ([]int, error) {
				if userID != 7 {
					t.Fatalf("GetClientIDsByUserID(%d), want 7", userID)
				}
				return []int{9}, nil
			},
			rangeClients: func(fn func(*file.Client) bool) {
				rangeCalls++
				fn(&file.Client{Id: 10, OwnerUserID: 7})
				fn(&file.Client{Id: 9, OwnerUserID: 8})
			},
		},
		Runtime: stubRuntime{
			disconnectClient: func(id int) {
				disconnected = append(disconnected, id)
			},
		},
	}

	if _, err := service.ChangeStatus(7, false); err != nil {
		t.Fatalf("ChangeStatus() error = %v", err)
	}
	if rangeCalls != 1 {
		t.Fatalf("RangeClients() calls = %d, want 1 fallback verification pass", rangeCalls)
	}
	if len(disconnected) != 1 || disconnected[0] != 10 {
		t.Fatalf("ChangeStatus() disconnected clients = %v, want range-verified [10]", disconnected)
	}
}

func TestDefaultUserServiceChangeStatusPropagatesUnexpectedUserClientLookupError(t *testing.T) {
	errWant := errors.New("client lookup unavailable")
	service := DefaultUserService{
		Backend: Backend{
			Repository: stubRepository{
				getUser: func(id int) (*file.User, error) {
					return &file.User{Id: id, Username: "tenant", Status: 1, TotalFlow: &file.Flow{}}, nil
				},
				saveUser: func(*file.User) error {
					return nil
				},
				rangeClients: func(func(*file.Client) bool) {
					t.Fatal("RangeClients() should not be used after GetClientsByUserID backend error")
				},
				getClientsByUserID: func(userID int) ([]*file.Client, error) {
					if userID != 7 {
						t.Fatalf("GetClientsByUserID(%d), want 7", userID)
					}
					return nil, errWant
				},
			},
			Runtime: stubRuntime{
				disconnectClient: func(id int) {
					t.Fatalf("DisconnectClient(%d) should not run after lookup error", id)
				},
			},
		},
	}

	_, err := service.ChangeStatus(7, false)
	if !errors.Is(err, errWant) {
		t.Fatalf("ChangeStatus() error = %v, want %v", err, errWant)
	}
}

func TestDefaultUserServiceDeleteUsesUserClientIDLookupWhenDeleteUserCascadesClientRefs(t *testing.T) {
	var (
		deletedClients []int
		disconnected   []int
		cleaned        []int
		deletedUser    int
	)
	service := DefaultUserService{
		Backend: Backend{
			Repository: stubRepository{
				getUser: func(id int) (*file.User, error) {
					return &file.User{Id: id, Username: "tenant", TotalFlow: &file.Flow{}}, nil
				},
				rangeClients: func(func(*file.Client) bool) {
					t.Fatal("RangeClients() should not be used when delete-user cascade and user client ids are available")
				},
				saveClient: func(*file.Client) error {
					t.Fatal("SaveClient() should not be used when DeleteUser cascades client reference cleanup")
					return nil
				},
				getClientIDsByUserID: func(userID int) ([]int, error) {
					if userID != 5 {
						t.Fatalf("GetClientIDsByUserID(%d), want 5", userID)
					}
					return []int{11, 12, 0, 12}, nil
				},
				authoritativeClientIDsByUserID: true,
				deleteUserCascadeClientRefs:    true,
				deleteClient: func(id int) error {
					deletedClients = append(deletedClients, id)
					return nil
				},
				deleteUser: func(id int) error {
					deletedUser = id
					return nil
				},
			},
			Runtime: stubRuntime{
				disconnectClient: func(id int) {
					disconnected = append(disconnected, id)
				},
				deleteClientResources: func(id int) {
					cleaned = append(cleaned, id)
				},
			},
		},
	}

	if _, err := service.Delete(5); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if len(deletedClients) != 2 || deletedClients[0] != 11 || deletedClients[1] != 12 {
		t.Fatalf("Delete() deleted clients = %v, want deduplicated [11 12]", deletedClients)
	}
	if len(disconnected) != 2 || disconnected[0] != 11 || disconnected[1] != 12 {
		t.Fatalf("Delete() disconnected clients = %v, want [11 12]", disconnected)
	}
	if len(cleaned) != 2 || cleaned[0] != 11 || cleaned[1] != 12 {
		t.Fatalf("Delete() cleaned client resources = %v, want [11 12]", cleaned)
	}
	if deletedUser != 5 {
		t.Fatalf("Delete() deleted user = %d, want 5", deletedUser)
	}
}

func TestDefaultUserServiceDeleteVerifiesUserClientIDsBeforeDeletingWhenCascadeRepoIDsAreNotAuthoritative(t *testing.T) {
	var (
		deletedClients []int
		deletedUser    int
	)
	service := DefaultUserService{
		Backend: Backend{
			Repository: stubRepository{
				getUser: func(id int) (*file.User, error) {
					return &file.User{Id: id, Username: "tenant", TotalFlow: &file.Flow{}}, nil
				},
				rangeClients: func(func(*file.Client) bool) {
					t.Fatal("RangeClients() should not be used when GetClient can verify user-owned client ids")
				},
				saveClient: func(*file.Client) error {
					t.Fatal("SaveClient() should not be used when DeleteUser cascades client reference cleanup")
					return nil
				},
				getClientIDsByUserID: func(userID int) ([]int, error) {
					if userID != 5 {
						t.Fatalf("GetClientIDsByUserID(%d), want 5", userID)
					}
					return []int{11, 12, 12, 404}, nil
				},
				getClient: func(id int) (*file.Client, error) {
					switch id {
					case 11:
						return &file.Client{Id: 11, OwnerUserID: 8}, nil
					case 12:
						return &file.Client{Id: 12, OwnerUserID: 5}, nil
					case 404:
						return nil, file.ErrClientNotFound
					default:
						t.Fatalf("GetClient(%d) unexpected lookup", id)
						return nil, nil
					}
				},
				deleteUserCascadeClientRefs: true,
				deleteClient: func(id int) error {
					deletedClients = append(deletedClients, id)
					return nil
				},
				deleteUser: func(id int) error {
					deletedUser = id
					return nil
				},
			},
			Runtime: stubRuntime{},
		},
	}

	if _, err := service.Delete(5); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if len(deletedClients) != 1 || deletedClients[0] != 12 {
		t.Fatalf("Delete() deleted clients = %v, want verified deduplicated [12]", deletedClients)
	}
	if deletedUser != 5 {
		t.Fatalf("Delete() deleted user = %d, want 5", deletedUser)
	}
}

func TestDefaultUserServiceDeleteUsesManagedClientScopeFastPathForManualCleanup(t *testing.T) {
	var (
		savedClients   []*file.Client
		deletedClients []int
		disconnected   []int
		cleaned        []int
		deletedUser    int
	)
	service := DefaultUserService{
		Backend: Backend{
			Repository: stubRepository{
				getUser: func(id int) (*file.User, error) {
					return &file.User{Id: id, Username: "tenant", TotalFlow: &file.Flow{}}, nil
				},
				rangeClients: func(func(*file.Client) bool) {
					t.Fatal("RangeClients() should not be used when GetAllManagedClientIDsByUserID and GetClient are available")
				},
				getAllManagedClientIDs: func(userID int) ([]int, error) {
					if userID != 3 {
						t.Fatalf("GetAllManagedClientIDsByUserID(%d), want 3", userID)
					}
					return []int{8, 9, 10, 9}, nil
				},
				authoritativeAllManagedClientIDs: true,
				getClient: func(id int) (*file.Client, error) {
					switch id {
					case 8:
						return &file.Client{Id: 8, UserId: 4, OwnerUserID: 4, ManagerUserIDs: []int{3, 5}}, nil
					case 9:
						return &file.Client{Id: 9, UserId: 3, OwnerUserID: 3}, nil
					case 10:
						return &file.Client{Id: 10, UserId: 4, OwnerUserID: 4, ManagerUserIDs: []int{6}}, nil
					default:
						t.Fatalf("GetClient(%d) unexpected lookup", id)
						return nil, nil
					}
				},
				saveClient: func(client *file.Client) error {
					snapshot := &file.Client{
						Id:             client.Id,
						UserId:         client.UserId,
						OwnerUserID:    client.OwnerUserID,
						ManagerUserIDs: append([]int(nil), client.ManagerUserIDs...),
					}
					savedClients = append(savedClients, snapshot)
					return nil
				},
				deleteClient: func(id int) error {
					deletedClients = append(deletedClients, id)
					return nil
				},
				deleteUser: func(id int) error {
					deletedUser = id
					return nil
				},
			},
			Runtime: stubRuntime{
				disconnectClient: func(id int) {
					disconnected = append(disconnected, id)
				},
				deleteClientResources: func(id int) {
					cleaned = append(cleaned, id)
				},
			},
		},
	}

	if _, err := service.Delete(3); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if deletedUser != 3 {
		t.Fatalf("Delete() deleted user = %d, want 3", deletedUser)
	}
	if len(savedClients) != 1 || savedClients[0].Id != 8 {
		t.Fatalf("Delete() saved clients = %+v, want client 8 manager cleanup", savedClients)
	}
	if len(savedClients[0].ManagerUserIDs) != 1 || savedClients[0].ManagerUserIDs[0] != 5 {
		t.Fatalf("Delete() saved client manager ids = %v, want [5]", savedClients[0].ManagerUserIDs)
	}
	if len(deletedClients) != 1 || deletedClients[0] != 9 {
		t.Fatalf("Delete() deleted clients = %v, want [9]", deletedClients)
	}
	if len(disconnected) != 1 || disconnected[0] != 9 {
		t.Fatalf("Delete() disconnected clients = %v, want [9]", disconnected)
	}
	if len(cleaned) != 1 || cleaned[0] != 9 {
		t.Fatalf("Delete() cleaned client resources = %v, want [9]", cleaned)
	}
}

func TestDefaultUserServiceDeleteFallsBackToRangeWhenManagedClientIndexIsNotAuthoritative(t *testing.T) {
	var (
		savedClients   []*file.Client
		deletedClients []int
		deletedUser    int
		rangeCalls     int
	)
	service := DefaultUserService{
		Backend: Backend{
			Repository: stubRepository{
				getUser: func(id int) (*file.User, error) {
					return &file.User{Id: id, Username: "tenant", TotalFlow: &file.Flow{}}, nil
				},
				getAllManagedClientIDs: func(userID int) ([]int, error) {
					if userID != 3 {
						t.Fatalf("GetAllManagedClientIDsByUserID(%d), want 3", userID)
					}
					return []int{8}, nil
				},
				getClient: func(id int) (*file.Client, error) {
					t.Fatalf("GetClient(%d) should not be trusted for non-authoritative managed delete scope", id)
					return nil, nil
				},
				rangeClients: func(fn func(*file.Client) bool) {
					rangeCalls++
					fn(&file.Client{Id: 8, UserId: 4, OwnerUserID: 4, ManagerUserIDs: []int{3, 5}})
					fn(&file.Client{Id: 9, UserId: 3, OwnerUserID: 3})
				},
				saveClient: func(client *file.Client) error {
					snapshot := &file.Client{
						Id:             client.Id,
						UserId:         client.UserId,
						OwnerUserID:    client.OwnerUserID,
						ManagerUserIDs: append([]int(nil), client.ManagerUserIDs...),
					}
					savedClients = append(savedClients, snapshot)
					return nil
				},
				deleteClient: func(id int) error {
					deletedClients = append(deletedClients, id)
					return nil
				},
				deleteUser: func(id int) error {
					deletedUser = id
					return nil
				},
			},
			Runtime: stubRuntime{},
		},
	}

	if _, err := service.Delete(3); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if rangeCalls != 1 {
		t.Fatalf("RangeClients() calls = %d, want 1 full fallback pass", rangeCalls)
	}
	if deletedUser != 3 {
		t.Fatalf("Delete() deleted user = %d, want 3", deletedUser)
	}
	if len(savedClients) != 1 || savedClients[0].Id != 8 {
		t.Fatalf("Delete() saved clients = %+v, want client 8 manager cleanup from range fallback", savedClients)
	}
	if len(savedClients[0].ManagerUserIDs) != 1 || savedClients[0].ManagerUserIDs[0] != 5 {
		t.Fatalf("Delete() saved client manager ids = %v, want [5]", savedClients[0].ManagerUserIDs)
	}
	if len(deletedClients) != 1 || deletedClients[0] != 9 {
		t.Fatalf("Delete() deleted clients = %v, want [9] recovered from range fallback", deletedClients)
	}
}

func TestDefaultUserServiceDeleteRemovesManagerUserReferences(t *testing.T) {
	var (
		savedClients   []*file.Client
		deletedClients []int
		disconnected   []int
		cleaned        []int
		deletedUser    int
	)
	service := DefaultUserService{
		Backend: Backend{
			Repository: stubRepository{
				getUser: func(id int) (*file.User, error) {
					return &file.User{Id: id, Username: "tenant", TotalFlow: &file.Flow{}}, nil
				},
				rangeClients: func(fn func(*file.Client) bool) {
					fn(&file.Client{Id: 8, UserId: 4, OwnerUserID: 4, ManagerUserIDs: []int{3, 5}})
					fn(&file.Client{Id: 9, UserId: 3, OwnerUserID: 3})
					fn(&file.Client{Id: 10, UserId: 4, OwnerUserID: 4, ManagerUserIDs: []int{6}})
				},
				saveClient: func(client *file.Client) error {
					snapshot := &file.Client{
						Id:             client.Id,
						UserId:         client.UserId,
						OwnerUserID:    client.OwnerUserID,
						ManagerUserIDs: append([]int(nil), client.ManagerUserIDs...),
					}
					savedClients = append(savedClients, snapshot)
					return nil
				},
				deleteClient: func(id int) error {
					deletedClients = append(deletedClients, id)
					return nil
				},
				deleteUser: func(id int) error {
					deletedUser = id
					return nil
				},
			},
			Runtime: stubRuntime{
				disconnectClient: func(id int) {
					disconnected = append(disconnected, id)
				},
				deleteClientResources: func(id int) {
					cleaned = append(cleaned, id)
				},
			},
		},
	}

	if _, err := service.Delete(3); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if deletedUser != 3 {
		t.Fatalf("Delete() deleted user = %d, want 3", deletedUser)
	}
	if len(savedClients) != 1 || savedClients[0].Id != 8 {
		t.Fatalf("Delete() saved clients = %+v, want client 8 manager cleanup", savedClients)
	}
	if len(savedClients[0].ManagerUserIDs) != 1 || savedClients[0].ManagerUserIDs[0] != 5 {
		t.Fatalf("Delete() saved client manager ids = %v, want [5]", savedClients[0].ManagerUserIDs)
	}
	if len(deletedClients) != 1 || deletedClients[0] != 9 {
		t.Fatalf("Delete() deleted clients = %v, want [9]", deletedClients)
	}
	if len(disconnected) != 1 || disconnected[0] != 9 {
		t.Fatalf("Delete() disconnected clients = %v, want [9]", disconnected)
	}
	if len(cleaned) != 1 || cleaned[0] != 9 {
		t.Fatalf("Delete() cleaned client resources = %v, want [9]", cleaned)
	}
}

func TestDefaultUserServiceDeleteFailsClosedOnManagedClientScopeLookupError(t *testing.T) {
	errWant := errors.New("managed client lookup failed")
	var deletedUser int
	service := DefaultUserService{
		Backend: Backend{
			Repository: stubRepository{
				getUser: func(id int) (*file.User, error) {
					return &file.User{Id: id, Username: "tenant", TotalFlow: &file.Flow{}}, nil
				},
				rangeClients: func(func(*file.Client) bool) {
					t.Fatal("RangeClients() should not run after GetAllManagedClientIDsByUserID backend error")
				},
				getAllManagedClientIDs: func(userID int) ([]int, error) {
					if userID != 5 {
						t.Fatalf("GetAllManagedClientIDsByUserID(%d), want 5", userID)
					}
					return nil, errWant
				},
				authoritativeAllManagedClientIDs: true,
				deleteClient: func(int) error {
					t.Fatal("DeleteClient() should not run after managed client lookup error")
					return nil
				},
				saveClient: func(*file.Client) error {
					t.Fatal("SaveClient() should not run after managed client lookup error")
					return nil
				},
				deleteUser: func(id int) error {
					deletedUser = id
					return nil
				},
			},
			Runtime: stubRuntime{
				disconnectClient: func(id int) {
					t.Fatalf("DisconnectClient(%d) should not run after managed client lookup error", id)
				},
				deleteClientResources: func(id int) {
					t.Fatalf("DeleteClientResources(%d) should not run after managed client lookup error", id)
				},
			},
		},
	}

	if _, err := service.Delete(5); !errors.Is(err, errWant) {
		t.Fatalf("Delete() error = %v, want %v", err, errWant)
	}
	if deletedUser != 0 {
		t.Fatalf("Delete() deleted user = %d, want 0 on lookup failure", deletedUser)
	}
}

func TestDefaultUserServiceDeleteDoesNotCleanupRuntimeWhenClientDeleteFails(t *testing.T) {
	errWant := errors.New("delete failed")
	var disconnected []int
	var cleaned []int
	service := DefaultUserService{
		Backend: Backend{
			Repository: stubRepository{
				getUser: func(id int) (*file.User, error) {
					return &file.User{Id: id, Username: "tenant", TotalFlow: &file.Flow{}}, nil
				},
				rangeClients: func(fn func(*file.Client) bool) {
					fn(&file.Client{Id: 11, UserId: 5, OwnerUserID: 5})
				},
				deleteClient: func(id int) error {
					if id != 11 {
						t.Fatalf("DeleteClient(%d), want 11", id)
					}
					return errWant
				},
			},
			Runtime: stubRuntime{
				disconnectClient: func(id int) {
					disconnected = append(disconnected, id)
				},
				deleteClientResources: func(id int) {
					cleaned = append(cleaned, id)
				},
			},
		},
	}

	if _, err := service.Delete(5); !errors.Is(err, errWant) {
		t.Fatalf("Delete() error = %v, want %v", err, errWant)
	}
	if len(disconnected) != 0 {
		t.Fatalf("Delete() disconnected clients = %v, want none on delete failure", disconnected)
	}
	if len(cleaned) != 0 {
		t.Fatalf("Delete() cleaned client resources = %v, want none on delete failure", cleaned)
	}
}

func TestDefaultUserServiceDeleteMapsClientNotFoundDuringCascade(t *testing.T) {
	var disconnected []int
	var cleaned []int
	service := DefaultUserService{
		Backend: Backend{
			Repository: stubRepository{
				getUser: func(id int) (*file.User, error) {
					return &file.User{Id: id, Username: "tenant", TotalFlow: &file.Flow{}}, nil
				},
				rangeClients: func(fn func(*file.Client) bool) {
					fn(&file.Client{Id: 11, UserId: 5, OwnerUserID: 5})
				},
				deleteClient: func(id int) error {
					return file.ErrClientNotFound
				},
			},
			Runtime: stubRuntime{
				disconnectClient: func(id int) {
					disconnected = append(disconnected, id)
				},
				deleteClientResources: func(id int) {
					cleaned = append(cleaned, id)
				},
			},
		},
	}

	if _, err := service.Delete(5); !errors.Is(err, ErrClientNotFound) {
		t.Fatalf("Delete() error = %v, want %v", err, ErrClientNotFound)
	}
	if len(disconnected) != 0 {
		t.Fatalf("Delete() disconnected clients = %v, want none on delete failure", disconnected)
	}
	if len(cleaned) != 0 {
		t.Fatalf("Delete() cleaned client resources = %v, want none on delete failure", cleaned)
	}
}

func TestDefaultUserServiceEditDoesNotMutateLiveUserOnSaveError(t *testing.T) {
	original := &file.User{
		Id:             7,
		Username:       "tenant",
		Password:       "secret",
		Kind:           "local",
		Status:         1,
		ExpireAt:       123,
		FlowLimit:      456,
		TotalFlow:      &file.Flow{InletFlow: 11, ExportFlow: 12},
		MaxClients:     1,
		MaxTunnels:     2,
		MaxHosts:       3,
		MaxConnections: 4,
		RateLimit:      5,
		DestAclMode:    file.AclWhitelist,
		DestAclRules:   "full:db.internal.example",
	}
	service := DefaultUserService{
		Backend: Backend{
			Repository: stubRepository{
				getUser: func(id int) (*file.User, error) {
					return original, nil
				},
				saveUser: func(*file.User) error {
					return errors.New("save failed")
				},
			},
		},
	}

	_, err := service.Edit(EditUserInput{
		ID:                    7,
		ReservedAdminUsername: "admin",
		Username:              "updated",
		Password:              "new-secret",
		Status:                false,
		StatusProvided:        true,
		ExpireAt:              "2026-03-27 10:11:12",
		FlowLimit:             999,
		MaxClients:            6,
		MaxTunnels:            7,
		MaxHosts:              8,
		MaxConnections:        9,
		RateLimit:             10,
		ResetFlow:             true,
		DestACLMode:           file.AclBlacklist,
		DestACLRules:          "full:blocked.internal.example",
	})
	if err == nil || err.Error() != "save failed" {
		t.Fatalf("Edit() error = %v, want save failed", err)
	}
	if original.Username != "tenant" || original.Password != "secret" || original.Status != 1 {
		t.Fatalf("original user mutated = %+v", original)
	}
	if original.FlowLimit != 456 || original.MaxClients != 1 || original.MaxTunnels != 2 || original.MaxHosts != 3 || original.MaxConnections != 4 || original.RateLimit != 5 {
		t.Fatalf("original user limits mutated = %+v", original)
	}
	if original.DestAclMode != file.AclWhitelist || original.DestAclRules != "full:db.internal.example" {
		t.Fatalf("original user destination acl mutated = (%d, %q)", original.DestAclMode, original.DestAclRules)
	}
	if original.TotalFlow == nil || original.TotalFlow.InletFlow != 11 || original.TotalFlow.ExportFlow != 12 {
		t.Fatalf("original user flow mutated = %+v", original.TotalFlow)
	}
}

func TestDefaultUserServiceChangeStatusDoesNotMutateLiveUserOnSaveError(t *testing.T) {
	original := &file.User{
		Id:        8,
		Username:  "tenant",
		Kind:      "local",
		Status:    1,
		TotalFlow: &file.Flow{},
	}
	service := DefaultUserService{
		Backend: Backend{
			Repository: stubRepository{
				getUser: func(id int) (*file.User, error) {
					return original, nil
				},
				saveUser: func(*file.User) error {
					return errors.New("save failed")
				},
			},
		},
	}

	_, err := service.ChangeStatus(8, false)
	if err == nil || err.Error() != "save failed" {
		t.Fatalf("ChangeStatus() error = %v, want save failed", err)
	}
	if original.Status != 1 {
		t.Fatalf("original user status mutated = %d, want 1", original.Status)
	}
}

func TestDefaultUserServiceChangeStatusReusesDetachedRepositorySnapshot(t *testing.T) {
	detached := &file.User{
		Id:        18,
		Username:  "tenant",
		Kind:      "local",
		Status:    1,
		TotalFlow: &file.Flow{},
	}
	var saved *file.User
	service := DefaultUserService{
		Backend: Backend{
			Repository: stubRepository{
				detachedSnapshots: true,
				getUser: func(id int) (*file.User, error) {
					if id != 18 {
						t.Fatalf("GetUser(%d), want 18", id)
					}
					return detached, nil
				},
				saveUser: func(user *file.User) error {
					saved = user
					return nil
				},
			},
		},
	}

	if _, err := service.ChangeStatus(18, false); err != nil {
		t.Fatalf("ChangeStatus() error = %v", err)
	}
	if saved != detached {
		t.Fatalf("ChangeStatus() saved = %p, want detached repo snapshot %p", saved, detached)
	}
	if detached.Status != 0 {
		t.Fatalf("detached.Status = %d, want 0 after in-place detached mutation", detached.Status)
	}
	if detached.Revision != 1 || detached.UpdatedAt <= 0 {
		t.Fatalf("detached meta = revision:%d updated_at:%d, want touched metadata", detached.Revision, detached.UpdatedAt)
	}
}

func TestDefaultUserServiceChangeStatusMapsUserNotFoundOnSave(t *testing.T) {
	service := DefaultUserService{
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
				saveUser: func(*file.User) error {
					return file.ErrUserNotFound
				},
			},
		},
	}

	if _, err := service.ChangeStatus(8, false); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("ChangeStatus() error = %v, want %v", err, ErrUserNotFound)
	}
}

func TestSortUserRowsDefaultsToAscendingWhenOrderEmpty(t *testing.T) {
	rows := []*UserListRow{
		{User: &file.User{Id: 2, Username: "bravo", TotalFlow: &file.Flow{}}},
		{User: &file.User{Id: 1, Username: "alpha", TotalFlow: &file.Flow{}}},
	}

	sortUserRows(rows, "Username", "")
	if rows[0].Id != 1 || rows[1].Id != 2 {
		t.Fatalf("sortUserRows(username, empty order) ids = [%d %d], want [1 2]", rows[0].Id, rows[1].Id)
	}
}

func TestSortUserRowsUsesIDAsTieBreakerForEqualValues(t *testing.T) {
	rows := []*UserListRow{
		{User: &file.User{Id: 3, Username: "charlie", TotalFlow: &file.Flow{}}, ClientCount: 5},
		{User: &file.User{Id: 1, Username: "alpha", TotalFlow: &file.Flow{}}, ClientCount: 5},
		{User: &file.User{Id: 2, Username: "bravo", TotalFlow: &file.Flow{}}, ClientCount: 6},
	}

	sortUserRows(rows, "ClientCount", "desc")

	if rows[0].Id != 2 || rows[1].Id != 1 || rows[2].Id != 3 {
		t.Fatalf("sortUserRows(client_count, desc) ids = [%d %d %d], want [2 1 3]", rows[0].Id, rows[1].Id, rows[2].Id)
	}
}

func TestDefaultUserServiceDeleteUsesDeferredPersistenceForCascadeBatch(t *testing.T) {
	deferredCalls := 0
	var deletedUser int
	service := DefaultUserService{
		Backend: Backend{
			Repository: stubRepository{
				withDeferredPersistence: func(run func() error) error {
					deferredCalls++
					return run()
				},
				getUser: func(id int) (*file.User, error) {
					return &file.User{Id: id, Username: "tenant", Status: 1, TotalFlow: &file.Flow{}}, nil
				},
				rangeClients: func(fn func(*file.Client) bool) {
					fn(&file.Client{
						Id:          9,
						VerifyKey:   "owned",
						OwnerUserID: 5,
						Status:      true,
						Cnf:         &file.Config{},
						Flow:        &file.Flow{},
					})
				},
				deleteClient: func(int) error { return nil },
				deleteUser: func(id int) error {
					deletedUser = id
					return nil
				},
			},
			Runtime: stubRuntime{},
		},
	}

	if _, err := service.Delete(5); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if deferredCalls != 1 {
		t.Fatalf("WithDeferredPersistence() calls = %d, want 1", deferredCalls)
	}
	if deletedUser != 5 {
		t.Fatalf("Delete() deleted user = %d, want 5", deletedUser)
	}
}
