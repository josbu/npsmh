package service

import (
	"errors"
	"testing"

	"github.com/djylb/nps/lib/file"
)

func TestDefaultClientServiceAddBindsUserByID(t *testing.T) {
	var created *file.Client
	service := DefaultClientService{
		Backend: Backend{
			Repository: stubRepository{
				nextClientID: func() int { return 9 },
				getUser: func(id int) (*file.User, error) {
					return &file.User{
						Id:         id,
						Username:   "tenant",
						Status:     1,
						MaxClients: 2,
						TotalFlow:  &file.Flow{},
					}, nil
				},
				rangeClients: func(fn func(*file.Client) bool) {
					fn(&file.Client{Id: 7, UserId: 5})
				},
				createClient: func(client *file.Client) error {
					created = client
					return nil
				},
			},
		},
	}

	result, err := service.Add(AddClientInput{
		ReservedAdminUsername: "admin",
		UserID:                5,
		OwnerSpecified:        true,
		ManageUserBinding:     true,
		VKey:                  "demo-vkey",
	})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if result.ID != 9 || created == nil {
		t.Fatalf("Add() id=%d created=%+v, want id 9 and created client", result.ID, created)
	}
	if created.UserId != 5 {
		t.Fatalf("Add() created.UserId = %d, want 5", created.UserId)
	}
	if created.FlowLimit != 0 || created.ExpireAt != 0 {
		t.Fatalf("Add() bound client should clear direct lifecycle fields, got flow_limit=%d expire_at=%d", created.FlowLimit, created.ExpireAt)
	}
	legacyUsername, legacyPassword, legacyTOTP := created.LegacyWebLoginImport()
	if legacyUsername != "" || legacyPassword != "" || legacyTOTP != "" {
		t.Fatalf("Add() bound client should clear direct web credentials, got username=%q password=%q totp=%q", legacyUsername, legacyPassword, legacyTOTP)
	}
}

func TestDefaultClientServiceAddStoresLifecycleFieldsForPlatformManagedClient(t *testing.T) {
	var created *file.Client
	service := DefaultClientService{
		Backend: Backend{
			Repository: stubRepository{
				nextClientID: func() int { return 19 },
				createClient: func(client *file.Client) error {
					created = client
					return nil
				},
			},
		},
	}

	result, err := service.Add(AddClientInput{
		ReservedAdminUsername: "admin",
		ManageUserBinding:     true,
		VKey:                  "demo-vkey",
		FlowLimit:             2048,
		TimeLimit:             "2026-03-21 10:11:12",
	})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if result.ID != 19 || created == nil {
		t.Fatalf("Add() id=%d created=%+v, want id 19 and created client", result.ID, created)
	}
	if created.FlowLimit != 2048 {
		t.Fatalf("Add() created.FlowLimit = %d, want 2048", created.FlowLimit)
	}
	if created.ExpireAt != parseClientExpireAt("2026-03-21 10:11:12") {
		t.Fatalf("Add() created.ExpireAt = %d, want %d", created.ExpireAt, parseClientExpireAt("2026-03-21 10:11:12"))
	}
}

func TestDefaultClientServiceAddCountsOwnerUserIDTowardsMaxClients(t *testing.T) {
	service := DefaultClientService{
		Backend: Backend{
			Repository: stubRepository{
				nextClientID: func() int { return 10 },
				getUser: func(id int) (*file.User, error) {
					return &file.User{
						Id:         id,
						Username:   "tenant",
						Status:     1,
						MaxClients: 1,
						TotalFlow:  &file.Flow{},
					}, nil
				},
				rangeClients: func(fn func(*file.Client) bool) {
					fn(&file.Client{Id: 7, OwnerUserID: 5})
				},
			},
		},
	}

	_, err := service.Add(AddClientInput{
		ReservedAdminUsername: "admin",
		UserID:                5,
		OwnerSpecified:        true,
		ManageUserBinding:     true,
		VKey:                  "demo-vkey",
	})
	if err != ErrClientLimitExceeded {
		t.Fatalf("Add() error = %v, want %v", err, ErrClientLimitExceeded)
	}
}

func TestDefaultClientServiceAddUsesUserClientLookupFastPathForMaxClients(t *testing.T) {
	service := DefaultClientService{
		Backend: Backend{
			Repository: stubRepository{
				nextClientID: func() int { return 10 },
				getUser: func(id int) (*file.User, error) {
					return &file.User{
						Id:         id,
						Username:   "tenant",
						Status:     1,
						MaxClients: 1,
						TotalFlow:  &file.Flow{},
					}, nil
				},
				rangeClients: func(func(*file.Client) bool) {
					t.Fatal("RangeClients() should not be used when GetClientsByUserID is available")
				},
				getClientsByUserID: func(userID int) ([]*file.Client, error) {
					if userID != 5 {
						t.Fatalf("GetClientsByUserID(%d), want 5", userID)
					}
					return []*file.Client{{Id: 7, OwnerUserID: 5}}, nil
				},
			},
		},
	}

	_, err := service.Add(AddClientInput{
		ReservedAdminUsername: "admin",
		UserID:                5,
		OwnerSpecified:        true,
		ManageUserBinding:     true,
		VKey:                  "demo-vkey",
	})
	if err != ErrClientLimitExceeded {
		t.Fatalf("Add() error = %v, want %v", err, ErrClientLimitExceeded)
	}
}

func TestDefaultClientServiceAddDeduplicatesUserClientLookupFastPathForMaxClients(t *testing.T) {
	var created *file.Client
	service := DefaultClientService{
		Backend: Backend{
			Repository: stubRepository{
				nextClientID: func() int { return 10 },
				getUser: func(id int) (*file.User, error) {
					return &file.User{
						Id:         id,
						Username:   "tenant",
						Status:     1,
						MaxClients: 2,
						TotalFlow:  &file.Flow{},
					}, nil
				},
				rangeClients: func(func(*file.Client) bool) {
					t.Fatal("RangeClients() should not be used when GetClientsByUserID is available")
				},
				getClientsByUserID: func(userID int) ([]*file.Client, error) {
					if userID != 5 {
						t.Fatalf("GetClientsByUserID(%d), want 5", userID)
					}
					return []*file.Client{
						{Id: 7, OwnerUserID: 5},
						{Id: 7, OwnerUserID: 5},
						{Id: 0, OwnerUserID: 5},
						nil,
					}, nil
				},
				createClient: func(client *file.Client) error {
					created = client
					return nil
				},
			},
		},
	}

	result, err := service.Add(AddClientInput{
		ReservedAdminUsername: "admin",
		UserID:                5,
		OwnerSpecified:        true,
		ManageUserBinding:     true,
		VKey:                  "demo-vkey",
	})
	if err != nil {
		t.Fatalf("Add() error = %v, want success after duplicate fast-path entries are deduplicated", err)
	}
	if result.ID != 10 || created == nil {
		t.Fatalf("Add() id=%d created=%+v, want id 10 and persisted client", result.ID, created)
	}
}

func TestDefaultClientServiceAddUsesUserClientIDLookupFastPathForMaxClients(t *testing.T) {
	service := DefaultClientService{
		Backend: Backend{
			Repository: stubRepository{
				nextClientID: func() int { return 10 },
				getUser: func(id int) (*file.User, error) {
					return &file.User{
						Id:         id,
						Username:   "tenant",
						Status:     1,
						MaxClients: 1,
						TotalFlow:  &file.Flow{},
					}, nil
				},
				rangeClients: func(func(*file.Client) bool) {
					t.Fatal("RangeClients() should not be used when GetClientIDsByUserID is available")
				},
				getClientsByUserID: func(int) ([]*file.Client, error) {
					t.Fatal("GetClientsByUserID() should not be used when GetClientIDsByUserID is available")
					return nil, nil
				},
				getClientIDsByUserID: func(userID int) ([]int, error) {
					if userID != 5 {
						t.Fatalf("GetClientIDsByUserID(%d), want 5", userID)
					}
					return []int{7}, nil
				},
				authoritativeClientIDsByUserID: true,
			},
		},
	}

	_, err := service.Add(AddClientInput{
		ReservedAdminUsername: "admin",
		UserID:                5,
		OwnerSpecified:        true,
		ManageUserBinding:     true,
		VKey:                  "demo-vkey",
	})
	if err != ErrClientLimitExceeded {
		t.Fatalf("Add() error = %v, want %v", err, ErrClientLimitExceeded)
	}
}

func TestDefaultClientServiceAddUsesVerifiedUserClientLookupWhenIDFastPathIsNotAuthoritative(t *testing.T) {
	var created *file.Client
	service := DefaultClientService{
		Backend: Backend{
			Repository: stubRepository{
				nextClientID: func() int { return 10 },
				getUser: func(id int) (*file.User, error) {
					return &file.User{
						Id:         id,
						Username:   "tenant",
						Status:     1,
						MaxClients: 1,
						TotalFlow:  &file.Flow{},
					}, nil
				},
				rangeClients: func(func(*file.Client) bool) {
					t.Fatal("RangeClients() should not be used when verified user lookup is available")
				},
				getClientIDsByUserID: func(userID int) ([]int, error) {
					if userID != 5 {
						t.Fatalf("GetClientIDsByUserID(%d), want 5", userID)
					}
					return []int{99}, nil
				},
				getClientsByUserID: func(userID int) ([]*file.Client, error) {
					if userID != 5 {
						t.Fatalf("GetClientsByUserID(%d), want 5", userID)
					}
					return nil, nil
				},
				createClient: func(client *file.Client) error {
					created = client
					return nil
				},
			},
		},
	}

	result, err := service.Add(AddClientInput{
		ReservedAdminUsername: "admin",
		UserID:                5,
		OwnerSpecified:        true,
		ManageUserBinding:     true,
		VKey:                  "demo-vkey",
	})
	if err != nil {
		t.Fatalf("Add() error = %v, want verified empty ownership set to allow create", err)
	}
	if result.ID != 10 || created == nil {
		t.Fatalf("Add() id=%d created=%+v, want id 10 and created client", result.ID, created)
	}
}

func TestDefaultClientServiceAddPropagatesUnexpectedOwnerLookupError(t *testing.T) {
	errWant := errors.New("repository unavailable")
	service := DefaultClientService{
		Backend: Backend{
			Repository: stubRepository{
				nextClientID: func() int { return 10 },
				getUser: func(id int) (*file.User, error) {
					return nil, errWant
				},
			},
		},
	}

	_, err := service.Add(AddClientInput{
		ReservedAdminUsername: "admin",
		UserID:                5,
		OwnerSpecified:        true,
		ManageUserBinding:     true,
		VKey:                  "demo-vkey",
	})
	if !errors.Is(err, errWant) {
		t.Fatalf("Add() error = %v, want %v", err, errWant)
	}
}

func TestDefaultClientServiceAddPropagatesUnexpectedUserClientLookupError(t *testing.T) {
	errWant := errors.New("client lookup unavailable")
	service := DefaultClientService{
		Backend: Backend{
			Repository: stubRepository{
				nextClientID: func() int { return 10 },
				getUser: func(id int) (*file.User, error) {
					return &file.User{
						Id:         id,
						Username:   "tenant",
						Status:     1,
						MaxClients: 1,
						TotalFlow:  &file.Flow{},
					}, nil
				},
				rangeClients: func(func(*file.Client) bool) {
					t.Fatal("RangeClients() should not be used after GetClientsByUserID backend error")
				},
				getClientsByUserID: func(userID int) ([]*file.Client, error) {
					if userID != 5 {
						t.Fatalf("GetClientsByUserID(%d), want 5", userID)
					}
					return nil, errWant
				},
			},
		},
	}

	_, err := service.Add(AddClientInput{
		ReservedAdminUsername: "admin",
		UserID:                5,
		OwnerSpecified:        true,
		ManageUserBinding:     true,
		VKey:                  "demo-vkey",
	})
	if !errors.Is(err, errWant) {
		t.Fatalf("Add() error = %v, want %v", err, errWant)
	}
}

func TestDefaultClientServiceEditAdminCanUnbindUser(t *testing.T) {
	var saved *file.Client
	service := DefaultClientService{
		Backend: Backend{
			Repository: stubRepository{
				getClient: func(id int) (*file.Client, error) {
					return &file.Client{
						Id:        id,
						UserId:    5,
						VerifyKey: "client-vkey",
						Status:    true,
						Cnf:       &file.Config{},
						Flow:      &file.Flow{},
					}, nil
				},
				saveClient: func(client *file.Client) error {
					saved = client
					return nil
				},
			},
		},
	}

	_, err := service.Edit(EditClientInput{
		ID:                    7,
		IsAdmin:               true,
		ReservedAdminUsername: "admin",
		ManageUserBinding:     true,
		UserID:                0,
		OwnerSpecified:        true,
		VKey:                  "client-vkey",
	})
	if err != nil {
		t.Fatalf("Edit() error = %v", err)
	}
	if saved == nil {
		t.Fatal("Edit() should persist the updated client")
	}
	if saved.UserId != 0 {
		t.Fatalf("Edit() saved.UserId = %d, want 0 after explicit unbind", saved.UserId)
	}
	legacyUsername, legacyPassword, legacyTOTP := saved.LegacyWebLoginImport()
	if legacyUsername != "" || legacyPassword != "" || legacyTOTP != "" {
		t.Fatalf("Edit() should keep legacy web credentials cleared after unbind, got username=%q password=%q totp=%q", legacyUsername, legacyPassword, legacyTOTP)
	}
}

func TestDefaultClientServiceEditAdminBindingUserClearsLifecycleFields(t *testing.T) {
	var saved *file.Client
	service := DefaultClientService{
		Backend: Backend{
			Repository: stubRepository{
				getClient: func(id int) (*file.Client, error) {
					return &file.Client{
						Id:        id,
						VerifyKey: "client-vkey",
						Status:    true,
						Cnf:       &file.Config{},
						Flow:      &file.Flow{},
						FlowLimit: 4096,
						ExpireAt:  parseClientExpireAt("2026-03-22 11:12:13"),
					}, nil
				},
				getUser: func(id int) (*file.User, error) {
					return &file.User{
						Id:        id,
						Username:  "tenant",
						Status:    1,
						TotalFlow: &file.Flow{},
					}, nil
				},
				saveClient: func(client *file.Client) error {
					saved = client
					return nil
				},
			},
		},
	}

	_, err := service.Edit(EditClientInput{
		ID:                    7,
		IsAdmin:               true,
		ReservedAdminUsername: "admin",
		UserID:                5,
		OwnerSpecified:        true,
		ManageUserBinding:     true,
		VKey:                  "client-vkey",
		FlowLimit:             8192,
		TimeLimit:             "2026-03-23 12:13:14",
	})
	if err != nil {
		t.Fatalf("Edit() error = %v", err)
	}
	if saved == nil {
		t.Fatal("Edit() should persist the updated client")
	}
	if saved.UserId != 5 {
		t.Fatalf("Edit() saved.UserId = %d, want 5", saved.UserId)
	}
	if saved.FlowLimit != 0 || saved.ExpireAt != 0 {
		t.Fatalf("Edit() bound client should clear direct lifecycle fields, got flow_limit=%d expire_at=%d", saved.FlowLimit, saved.ExpireAt)
	}
}

func TestDefaultClientServiceEditUsesBoundUserFastPathForRegularUser(t *testing.T) {
	var saved *file.Client
	owner := &file.User{
		Id:        5,
		Username:  "tenant",
		Status:    1,
		FlowLimit: 4096,
		ExpireAt:  parseClientExpireAt("2026-03-24 12:13:14"),
		TotalFlow: &file.Flow{},
	}
	service := DefaultClientService{
		Backend: Backend{
			Repository: stubRepository{
				getClient: func(id int) (*file.Client, error) {
					client := &file.Client{
						Id:        id,
						UserId:    5,
						VerifyKey: "client-vkey",
						Status:    true,
						Cnf:       &file.Config{},
						Flow:      &file.Flow{},
					}
					client.BindOwnerUser(owner)
					return client, nil
				},
				getUser: func(id int) (*file.User, error) {
					t.Fatalf("GetUser(%d) should not be used when client already carries a bound owner snapshot", id)
					return nil, nil
				},
				saveClient: func(client *file.Client) error {
					saved = client
					return nil
				},
			},
		},
	}

	_, err := service.Edit(EditClientInput{
		ID:                    7,
		IsAdmin:               false,
		ReservedAdminUsername: "admin",
		ManageUserBinding:     false,
		VKey:                  "client-vkey",
	})
	if err != nil {
		t.Fatalf("Edit() error = %v", err)
	}
	if saved == nil {
		t.Fatal("Edit() should persist the updated client")
	}
	if saved.UserId != 5 {
		t.Fatalf("Edit() saved.UserId = %d, want 5 for regular-user edit", saved.UserId)
	}
	if saved.FlowLimit != 0 || saved.ExpireAt != 0 {
		t.Fatalf("Edit() bound client should keep direct lifecycle fields cleared, got flow_limit=%d expire_at=%d", saved.FlowLimit, saved.ExpireAt)
	}
	if saved.OwnerUser() == nil || saved.OwnerUser().Id != owner.Id {
		t.Fatalf("Edit() saved owner = %#v, want bound owner %d", saved.OwnerUser(), owner.Id)
	}
	legacyUsername, legacyPassword, legacyTOTP := saved.LegacyWebLoginImport()
	if legacyUsername != "" || legacyPassword != "" || legacyTOTP != "" {
		t.Fatalf("Edit() should keep inherited user login clean for bound client, got username=%q password=%q totp=%q", legacyUsername, legacyPassword, legacyTOTP)
	}
}

func TestDefaultClientServiceEditClearsManagerUserIDsWhenExplicitlyEmpty(t *testing.T) {
	var saved *file.Client
	service := DefaultClientService{
		Backend: Backend{
			Repository: stubRepository{
				getClient: func(id int) (*file.Client, error) {
					return &file.Client{
						Id:             id,
						VerifyKey:      "client-vkey",
						Status:         true,
						Cnf:            &file.Config{},
						Flow:           &file.Flow{},
						ManagerUserIDs: []int{2, 3},
					}, nil
				},
				saveClient: func(client *file.Client) error {
					saved = client
					return nil
				},
			},
		},
	}

	_, err := service.Edit(EditClientInput{
		ID:                      7,
		IsAdmin:                 true,
		ReservedAdminUsername:   "admin",
		AllowManagerUserIDs:     true,
		ManagerUserIDsSpecified: true,
		ManagerUserIDs:          []int{},
		VKey:                    "client-vkey",
	})
	if err != nil {
		t.Fatalf("Edit() error = %v", err)
	}
	if saved == nil {
		t.Fatal("Edit() should persist the updated client")
	}
	if len(saved.ManagerUserIDs) != 0 {
		t.Fatalf("Edit() saved.ManagerUserIDs = %v, want empty slice after explicit clear", saved.ManagerUserIDs)
	}
}

func TestDefaultClientServiceEditSanitizesManagerUserIDs(t *testing.T) {
	var saved *file.Client
	service := DefaultClientService{
		Backend: Backend{
			Repository: stubRepository{
				getClient: func(id int) (*file.Client, error) {
					return &file.Client{
						Id:          id,
						OwnerUserID: 5,
						VerifyKey:   "client-vkey",
						Status:      true,
						Cnf:         &file.Config{},
						Flow:        &file.Flow{},
					}, nil
				},
				getUser: func(id int) (*file.User, error) {
					if id != 5 {
						t.Fatalf("GetUser(%d), want 5", id)
					}
					return &file.User{Id: 5, Username: "tenant", Status: 1, TotalFlow: &file.Flow{}}, nil
				},
				saveClient: func(client *file.Client) error {
					saved = client
					return nil
				},
			},
		},
	}

	_, err := service.Edit(EditClientInput{
		ID:                      7,
		IsAdmin:                 true,
		ReservedAdminUsername:   "admin",
		AllowManagerUserIDs:     true,
		ManagerUserIDsSpecified: true,
		ManagerUserIDs:          []int{0, 5, 8, 8, 9, 8},
		VKey:                    "client-vkey",
	})
	if err != nil {
		t.Fatalf("Edit() error = %v", err)
	}
	if saved == nil {
		t.Fatal("Edit() should persist the updated client")
	}
	if len(saved.ManagerUserIDs) != 2 || saved.ManagerUserIDs[0] != 8 || saved.ManagerUserIDs[1] != 9 {
		t.Fatalf("Edit() saved.ManagerUserIDs = %v, want [8 9]", saved.ManagerUserIDs)
	}
}
