package service

import (
	"errors"
	"testing"
	"time"

	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/rate"
)

func TestDefaultClientServiceEditDoesNotMutateLiveClientOnValidationError(t *testing.T) {
	original := &file.Client{
		Id:             7,
		VerifyKey:      "original-vkey",
		Status:         true,
		Cnf:            &file.Config{U: "old-user", P: "old-pass", Compress: true},
		Flow:           &file.Flow{ExportFlow: 11, InletFlow: 12, FlowLimit: 13},
		Remark:         "before",
		RateLimit:      3,
		MaxConn:        4,
		MaxTunnelNum:   5,
		ManagerUserIDs: []int{2, 3},
		EntryAclMode:   file.AclBlacklist,
		EntryAclRules:  "1.1.1.1",
	}
	var saveCalled bool
	service := DefaultClientService{
		Backend: Backend{
			Repository: stubRepository{
				getClient: func(id int) (*file.Client, error) {
					if id != 7 {
						t.Fatalf("GetClient(%d), want 7", id)
					}
					return original, nil
				},
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
					fn(&file.Client{Id: 99, UserId: 5})
				},
				saveClient: func(*file.Client) error {
					saveCalled = true
					return nil
				},
			},
		},
	}

	_, err := service.Edit(EditClientInput{
		ID:                      7,
		IsAdmin:                 true,
		ReservedAdminUsername:   "admin",
		UserID:                  5,
		OwnerSpecified:          true,
		ManageUserBinding:       true,
		AllowManagerUserIDs:     true,
		ManagerUserIDsSpecified: true,
		ManagerUserIDs:          []int{8, 9},
		VKey:                    "updated-vkey",
		Remark:                  "after",
		User:                    "new-user",
		Password:                "new-pass",
		PasswordProvided:        true,
		Compress:                false,
		Crypt:                   true,
		ConfigConnAllow:         true,
		RateLimit:               8,
		MaxConn:                 9,
		MaxTunnelNum:            10,
		FlowLimit:               2048,
		TimeLimit:               "2026-03-27 10:00:00",
		ResetFlow:               true,
	})
	if !errors.Is(err, ErrClientLimitExceeded) {
		t.Fatalf("Edit() error = %v, want %v", err, ErrClientLimitExceeded)
	}
	if saveCalled {
		t.Fatal("Edit() should not save when validation fails")
	}
	if original.VerifyKey != "original-vkey" || original.Remark != "before" {
		t.Fatalf("original client mutated = %+v", original)
	}
	if original.Cnf.U != "old-user" || original.Cnf.P != "old-pass" || !original.Cnf.Compress || original.Cnf.Crypt {
		t.Fatalf("original config mutated = %+v", original.Cnf)
	}
	if original.RateLimit != 3 || original.MaxConn != 4 || original.MaxTunnelNum != 5 {
		t.Fatalf("original limits mutated = rate=%d max_conn=%d max_tunnel=%d", original.RateLimit, original.MaxConn, original.MaxTunnelNum)
	}
	if len(original.ManagerUserIDs) != 2 || original.ManagerUserIDs[0] != 2 || original.ManagerUserIDs[1] != 3 {
		t.Fatalf("original manager ids mutated = %v", original.ManagerUserIDs)
	}
	if original.EntryAclMode != file.AclBlacklist || original.EntryAclRules != "1.1.1.1" {
		t.Fatalf("original entry acl mutated = (%d, %q)", original.EntryAclMode, original.EntryAclRules)
	}
	if original.Flow == nil || original.Flow.ExportFlow != 11 || original.Flow.InletFlow != 12 || original.Flow.FlowLimit != 13 {
		t.Fatalf("original flow mutated = %+v", original.Flow)
	}
}

func TestDefaultClientServiceEditPreservesPasswordWhenOmitted(t *testing.T) {
	original := &file.Client{
		Id:        7,
		VerifyKey: "vk-7",
		Status:    true,
		Remark:    "before",
		Cnf:       &file.Config{U: "demo", P: "keep-me", Compress: true},
		Flow:      &file.Flow{},
	}
	var saved *file.Client
	service := DefaultClientService{
		Backend: Backend{
			Repository: stubRepository{
				getClient: func(id int) (*file.Client, error) {
					return original, nil
				},
				saveClient: func(client *file.Client) error {
					saved = client
					return nil
				},
			},
		},
	}

	if _, err := service.Edit(EditClientInput{
		ID:               7,
		IsAdmin:          true,
		VKey:             "vk-7",
		Remark:           "after",
		User:             "changed-user",
		PasswordProvided: false,
		Compress:         false,
		Crypt:            true,
	}); err != nil {
		t.Fatalf("Edit() error = %v", err)
	}
	if saved == nil {
		t.Fatal("Edit() should persist the updated client")
	}
	if saved.Cnf.P != "keep-me" {
		t.Fatalf("saved.Cnf.P = %q, want preserved password", saved.Cnf.P)
	}
	if saved.Cnf.U != "changed-user" {
		t.Fatalf("saved.Cnf.U = %q, want changed-user", saved.Cnf.U)
	}
}

func TestDefaultClientServiceEditRejectsMissingOwnerLookup(t *testing.T) {
	original := &file.Client{
		Id:          7,
		VerifyKey:   "vk-7",
		Status:      true,
		Cnf:         &file.Config{U: "demo", P: "keep-me"},
		Flow:        &file.Flow{},
		OwnerUserID: 7,
	}
	saveCalled := false
	service := DefaultClientService{
		Backend: Backend{
			Repository: stubRepository{
				getClient: func(id int) (*file.Client, error) {
					return original, nil
				},
				getUser: func(id int) (*file.User, error) {
					if id != 7 {
						t.Fatalf("GetUser(%d), want 7", id)
					}
					return nil, file.ErrUserNotFound
				},
				saveClient: func(client *file.Client) error {
					saveCalled = true
					return nil
				},
			},
		},
	}

	_, err := service.Edit(EditClientInput{
		ID:               7,
		IsAdmin:          false,
		Remark:           "after",
		User:             "changed-user",
		PasswordProvided: false,
	})
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("Edit() error = %v, want %v", err, ErrUserNotFound)
	}
	if saveCalled {
		t.Fatal("Edit() should not save when owner lookup fails")
	}
	if original.OwnerID() != 7 || original.OwnerUser() != nil {
		t.Fatalf("original ownership mutated = id:%d owner:%#v", original.OwnerID(), original.OwnerUser())
	}
}

func TestDefaultClientServiceEditAllowsExplicitEmptyPassword(t *testing.T) {
	original := &file.Client{
		Id:        7,
		VerifyKey: "vk-7",
		Status:    true,
		Cnf:       &file.Config{U: "demo", P: "clear-me"},
		Flow:      &file.Flow{},
	}
	var saved *file.Client
	service := DefaultClientService{
		Backend: Backend{
			Repository: stubRepository{
				getClient: func(id int) (*file.Client, error) {
					return original, nil
				},
				saveClient: func(client *file.Client) error {
					saved = client
					return nil
				},
			},
		},
	}

	if _, err := service.Edit(EditClientInput{
		ID:               7,
		IsAdmin:          true,
		VKey:             "vk-7",
		Password:         "",
		PasswordProvided: true,
	}); err != nil {
		t.Fatalf("Edit() error = %v", err)
	}
	if saved == nil {
		t.Fatal("Edit() should persist the updated client")
	}
	if saved.Cnf.P != "" {
		t.Fatalf("saved.Cnf.P = %q, want cleared password", saved.Cnf.P)
	}
}

func TestDefaultClientServiceChangeStatusUsesWorkingCopy(t *testing.T) {
	original := &file.Client{
		Id:        7,
		Status:    true,
		VerifyKey: "vk-7",
		Cnf:       &file.Config{},
		Flow:      &file.Flow{ExportFlow: 11, InletFlow: 12},
	}
	var saved *file.Client
	var disconnected int
	service := DefaultClientService{
		Backend: Backend{
			Repository: stubRepository{
				getClient: func(id int) (*file.Client, error) {
					if id != 7 {
						t.Fatalf("GetClient(%d), want 7", id)
					}
					return original, nil
				},
				saveClient: func(client *file.Client) error {
					saved = client
					return nil
				},
			},
			Runtime: stubRuntime{
				disconnectClient: func(id int) {
					disconnected = id
				},
			},
		},
	}

	if _, err := service.ChangeStatus(7, false); err != nil {
		t.Fatalf("ChangeStatus() error = %v", err)
	}
	if saved == nil {
		t.Fatal("ChangeStatus() did not save working copy")
	}
	if saved == original {
		t.Fatal("ChangeStatus() saved original live client pointer, want working copy")
	}
	if saved.Status {
		t.Fatalf("saved.Status = %v, want false", saved.Status)
	}
	if original.Status != true {
		t.Fatalf("original.Status = %v, want true", original.Status)
	}
	if disconnected != 7 {
		t.Fatalf("disconnectClient(%d), want 7", disconnected)
	}
}

func TestDefaultClientServiceChangeStatusReusesDetachedRepositorySnapshot(t *testing.T) {
	detached := &file.Client{
		Id:        27,
		Status:    true,
		VerifyKey: "vk-27",
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}
	var saved *file.Client
	var disconnected int
	service := DefaultClientService{
		Backend: Backend{
			Repository: stubRepository{
				detachedSnapshots: true,
				getClient: func(id int) (*file.Client, error) {
					if id != 27 {
						t.Fatalf("GetClient(%d), want 27", id)
					}
					return detached, nil
				},
				saveClient: func(client *file.Client) error {
					saved = client
					return nil
				},
			},
			Runtime: stubRuntime{
				disconnectClient: func(id int) {
					disconnected = id
				},
			},
		},
	}

	if _, err := service.ChangeStatus(27, false); err != nil {
		t.Fatalf("ChangeStatus() error = %v", err)
	}
	if saved != detached {
		t.Fatalf("ChangeStatus() saved = %p, want detached repo snapshot %p", saved, detached)
	}
	if detached.Status {
		t.Fatalf("detached.Status = %v, want false after in-place detached mutation", detached.Status)
	}
	if detached.Revision != 1 || detached.UpdatedAt <= 0 {
		t.Fatalf("detached meta = revision:%d updated_at:%d, want touched metadata", detached.Revision, detached.UpdatedAt)
	}
	if disconnected != 27 {
		t.Fatalf("disconnectClient(%d), want 27", disconnected)
	}
}

func TestCloneClientSnapshotPreservesBoundOwnerLifecycle(t *testing.T) {
	owner := &file.User{
		Id:        8,
		ExpireAt:  1_900_000_000,
		FlowLimit: 16 * 1024 * 1024,
		TotalFlow: &file.Flow{},
	}
	client := &file.Client{
		Id:          7,
		OwnerUserID: owner.Id,
		Cnf:         &file.Config{},
		Flow:        &file.Flow{},
	}
	client.BindOwnerUser(owner)

	cloned := cloneClientSnapshotForMutation(client)
	if cloned == nil {
		t.Fatal("cloneClientSnapshotForMutation() = nil")
	}
	if got := cloned.EffectiveExpireAt(); got != owner.ExpireAt {
		t.Fatalf("cloned.EffectiveExpireAt() = %d, want %d", got, owner.ExpireAt)
	}
	if got := cloned.EffectiveFlowLimitBytes(); got != owner.FlowLimit {
		t.Fatalf("cloned.EffectiveFlowLimitBytes() = %d, want %d", got, owner.FlowLimit)
	}
	if cloned.OwnerUser() == nil {
		t.Fatal("cloned.OwnerUser() = nil, want detached owner snapshot")
	}
	if cloned.OwnerUser() == owner {
		t.Fatal("cloned.OwnerUser() should not alias the live owner object")
	}
	if cloned.OwnerUser().ExpireAt != owner.ExpireAt || cloned.OwnerUser().FlowLimit != owner.FlowLimit {
		t.Fatalf("cloned owner lifecycle = %+v, want expire_at=%d flow_limit=%d", cloned.OwnerUser(), owner.ExpireAt, owner.FlowLimit)
	}
}

func TestDefaultClientServiceChangeStatusDoesNotDisconnectOnSaveError(t *testing.T) {
	original := &file.Client{
		Id:        17,
		Status:    true,
		VerifyKey: "vk-17",
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}
	errWant := errors.New("save failed")
	disconnected := false
	service := DefaultClientService{
		Backend: Backend{
			Repository: stubRepository{
				getClient: func(id int) (*file.Client, error) {
					return original, nil
				},
				saveClient: func(client *file.Client) error {
					if client == original {
						t.Fatal("ChangeStatus() SaveClient received original live client pointer")
					}
					return errWant
				},
			},
			Runtime: stubRuntime{
				disconnectClient: func(id int) {
					disconnected = true
				},
			},
		},
	}

	if _, err := service.ChangeStatus(17, false); !errors.Is(err, errWant) {
		t.Fatalf("ChangeStatus() error = %v, want %v", err, errWant)
	}
	if disconnected {
		t.Fatal("ChangeStatus() should not disconnect runtime client when SaveClient fails")
	}
	if !original.Status {
		t.Fatalf("original.Status = %v, want true", original.Status)
	}
}

func TestDefaultClientServiceChangeStatusMapsClientNotFoundOnSave(t *testing.T) {
	service := DefaultClientService{
		Backend: Backend{
			Repository: stubRepository{
				getClient: func(id int) (*file.Client, error) {
					return &file.Client{
						Id:        id,
						Status:    true,
						VerifyKey: "vk-17",
						Cnf:       &file.Config{},
						Flow:      &file.Flow{},
					}, nil
				},
				saveClient: func(*file.Client) error {
					return file.ErrClientNotFound
				},
			},
		},
	}

	if _, err := service.ChangeStatus(17, false); !errors.Is(err, ErrClientNotFound) {
		t.Fatalf("ChangeStatus() error = %v, want %v", err, ErrClientNotFound)
	}
}

func TestDefaultClientServiceGetReturnsWorkingCopy(t *testing.T) {
	original := &file.Client{
		Id:        7,
		VerifyKey: "vk-7",
		Remark:    "before",
		Cnf:       &file.Config{U: "demo", P: "secret", Compress: true},
		Flow:      &file.Flow{InletFlow: 11, ExportFlow: 12, FlowLimit: 13},
	}
	service := DefaultClientService{
		Backend: Backend{
			Repository: stubRepository{
				getClient: func(id int) (*file.Client, error) {
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
		t.Fatal("Get() returned original live client pointer")
	}
	got.Remark = "after"
	got.Cnf.U = "changed"
	got.Flow.InletFlow = 99
	if original.Remark != "before" || original.Cnf.U != "demo" || original.Flow.InletFlow != 11 {
		t.Fatalf("original client mutated through Get() result = %+v", original)
	}
}

func TestDefaultClientServiceOperationsMapNilRepositoryClient(t *testing.T) {
	service := DefaultClientService{
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
			name: "Ping",
			run: func() error {
				_, err := service.Ping(7, "")
				return err
			},
		},
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
				_, err := service.Edit(EditClientInput{ID: 7})
				return err
			},
		},
		{
			name: "Clear",
			run: func() error {
				_, err := service.Clear(7, "flow", true)
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
			if err := tc.run(); !errors.Is(err, ErrClientNotFound) {
				t.Fatalf("%s() error = %v, want %v", tc.name, err, ErrClientNotFound)
			}
		})
	}
}

func TestDefaultClientServiceClearUsesInjectedRepository(t *testing.T) {
	original := &file.Client{
		Id:        8,
		VerifyKey: "vk-8",
		RateLimit: 12,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}
	var saved *file.Client
	service := DefaultClientService{
		Backend: Backend{
			Repository: stubRepository{
				getClient: func(id int) (*file.Client, error) {
					if id != 8 {
						t.Fatalf("GetClient(%d), want 8", id)
					}
					return original, nil
				},
				saveClient: func(client *file.Client) error {
					saved = client
					return nil
				},
			},
		},
	}

	if _, err := service.Clear(8, "rate_limit", true); err != nil {
		t.Fatalf("Clear() error = %v", err)
	}
	if saved == nil {
		t.Fatal("Clear() did not save through injected repository")
	}
	if saved == original {
		t.Fatal("Clear() saved original live client pointer, want working copy")
	}
	if saved.RateLimit != 0 {
		t.Fatalf("saved.RateLimit = %d, want 0", saved.RateLimit)
	}
	if original.RateLimit != 12 {
		t.Fatalf("original.RateLimit = %d, want 12", original.RateLimit)
	}
	if saved.Revision != 1 || saved.UpdatedAt <= 0 {
		t.Fatalf("saved meta = revision:%d updated_at:%d, want touched metadata", saved.Revision, saved.UpdatedAt)
	}
}

func TestDefaultClientServiceRejectsReservedRuntimeClientOperations(t *testing.T) {
	reserved := &file.Client{
		Id:        77,
		VerifyKey: "reserved-vkey",
		NoStore:   true,
		NoDisplay: true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}
	deleteCalled := false
	saveCalled := false
	service := DefaultClientService{
		Backend: Backend{
			Repository: stubRepository{
				getClient: func(id int) (*file.Client, error) {
					if id != reserved.Id {
						t.Fatalf("GetClient(%d), want %d", id, reserved.Id)
					}
					return reserved, nil
				},
				saveClient: func(*file.Client) error {
					saveCalled = true
					return nil
				},
				deleteClient: func(id int) error {
					deleteCalled = true
					return nil
				},
			},
		},
	}

	if _, err := service.Get(reserved.Id); !errors.Is(err, ErrForbidden) {
		t.Fatalf("Get() error = %v, want %v", err, ErrForbidden)
	}
	if _, err := service.Ping(reserved.Id, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("Ping() error = %v, want %v", err, ErrForbidden)
	}
	if _, err := service.Edit(EditClientInput{ID: reserved.Id}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("Edit() error = %v, want %v", err, ErrForbidden)
	}
	if _, err := service.ChangeStatus(reserved.Id, false); !errors.Is(err, ErrForbidden) {
		t.Fatalf("ChangeStatus() error = %v, want %v", err, ErrForbidden)
	}
	if _, err := service.Clear(reserved.Id, "flow", true); !errors.Is(err, ErrForbidden) {
		t.Fatalf("Clear() error = %v, want %v", err, ErrForbidden)
	}
	if _, err := service.Delete(reserved.Id); !errors.Is(err, ErrForbidden) {
		t.Fatalf("Delete() error = %v, want %v", err, ErrForbidden)
	}
	if saveCalled {
		t.Fatal("reserved runtime client operations should not save changes")
	}
	if deleteCalled {
		t.Fatal("reserved runtime client operations should not delete hidden client")
	}
}

func TestDefaultClientServiceDeleteDeletesRepositoryBeforeRuntimeCleanup(t *testing.T) {
	var calls []string
	original := &file.Client{
		Id:        9,
		VerifyKey: "vk-9",
		Status:    true,
		IsConnect: true,
		NowConn:   3,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}
	service := DefaultClientService{
		Backend: Backend{
			Repository: stubRepository{
				getClient: func(id int) (*file.Client, error) {
					return original, nil
				},
				deleteClient: func(id int) error {
					calls = append(calls, "delete")
					if len(calls) != 1 || calls[0] != "delete" {
						t.Fatalf("DeleteClient() called in order %v, want [delete]", calls)
					}
					return nil
				},
			},
			Runtime: stubRuntime{
				disconnectClient: func(id int) {
					calls = append(calls, "disconnect")
				},
				deleteClientResources: func(id int) {
					calls = append(calls, "cleanup")
				},
			},
		},
	}

	result, err := service.Delete(9)
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if len(calls) != 3 || calls[0] != "delete" || calls[1] != "disconnect" || calls[2] != "cleanup" {
		t.Fatalf("Delete() call order = %v, want [delete disconnect cleanup]", calls)
	}
	if result.ID != 9 || result.Client == nil {
		t.Fatalf("Delete() result = %+v, want client snapshot id 9", result)
	}
	if result.Client.IsConnect || result.Client.NowConn != 0 {
		t.Fatalf("Delete() returned runtime state = connected:%v now_conn:%d, want false/0", result.Client.IsConnect, result.Client.NowConn)
	}
	if !original.IsConnect || original.NowConn != 3 {
		t.Fatalf("Delete() mutated original client = %+v", original)
	}
}

func TestDefaultClientServiceGetMapsMissingRepositoryClient(t *testing.T) {
	service := DefaultClientService{
		Backend: Backend{
			Repository: stubRepository{
				getClient: func(id int) (*file.Client, error) {
					return nil, file.ErrClientNotFound
				},
			},
		},
	}

	if _, err := service.Get(42); !errors.Is(err, ErrClientNotFound) {
		t.Fatalf("Get() error = %v, want %v", err, ErrClientNotFound)
	}
}

func TestDefaultClientServiceDeleteDoesNotCleanupRuntimeWhenDeleteFails(t *testing.T) {
	var calls []string
	service := DefaultClientService{
		Backend: Backend{
			Repository: stubRepository{
				getClient: func(id int) (*file.Client, error) {
					return &file.Client{
						Id:        id,
						VerifyKey: "vk-delete-missing",
						Status:    true,
						Cnf:       &file.Config{},
						Flow:      &file.Flow{},
					}, nil
				},
				deleteClient: func(id int) error {
					calls = append(calls, "delete")
					return file.ErrClientNotFound
				},
			},
			Runtime: stubRuntime{
				disconnectClient: func(id int) {
					calls = append(calls, "disconnect")
				},
				deleteClientResources: func(id int) {
					calls = append(calls, "cleanup")
				},
			},
		},
	}

	if _, err := service.Delete(9); !errors.Is(err, ErrClientNotFound) {
		t.Fatalf("Delete() error = %v, want %v", err, ErrClientNotFound)
	}
	if len(calls) != 1 || calls[0] != "delete" {
		t.Fatalf("Delete() call order = %v, want [delete]", calls)
	}
}

func TestClearClientStatusCopyBasicFields(t *testing.T) {
	timeLimit := time.Now().Add(time.Hour)

	tests := []struct {
		name   string
		mode   string
		assert func(*testing.T, *file.Client)
	}{
		{
			name: "clear flow limit",
			mode: "flow_limit",
			assert: func(t *testing.T, client *file.Client) {
				t.Helper()
				if client.FlowLimit != 0 {
					t.Fatalf("FlowLimit = %d, want 0", client.FlowLimit)
				}
				if client.Flow.FlowLimit != 0 {
					t.Fatalf("Flow.FlowLimit = %d, want 0", client.Flow.FlowLimit)
				}
			},
		},
		{
			name: "clear time limit",
			mode: "time_limit",
			assert: func(t *testing.T, client *file.Client) {
				t.Helper()
				if client.ExpireAt != 0 {
					t.Fatalf("ExpireAt = %d, want 0", client.ExpireAt)
				}
				if !client.Flow.TimeLimit.IsZero() {
					t.Fatalf("Flow.TimeLimit = %v, want zero", client.Flow.TimeLimit)
				}
			},
		},
		{
			name: "clear connection limit",
			mode: "conn_limit",
			assert: func(t *testing.T, client *file.Client) {
				t.Helper()
				if client.MaxConn != 0 {
					t.Fatalf("MaxConn = %d, want 0", client.MaxConn)
				}
			},
		},
		{
			name: "clear tunnel limit",
			mode: "tunnel_limit",
			assert: func(t *testing.T, client *file.Client) {
				t.Helper()
				if client.MaxTunnelNum != 0 {
					t.Fatalf("MaxTunnelNum = %d, want 0", client.MaxTunnelNum)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &file.Client{
				ExpireAt:     timeLimit.Unix(),
				FlowLimit:    1024,
				Flow:         &file.Flow{FlowLimit: 1024, TimeLimit: timeLimit},
				Rate:         rate.NewRate(1024),
				MaxConn:      10,
				MaxTunnelNum: 20,
			}
			originalExpireAt := client.ExpireAt
			originalFlowLimit := client.FlowLimit
			originalMaxConn := client.MaxConn
			originalMaxTunnelNum := client.MaxTunnelNum
			originalFlowLimitValue := client.Flow.FlowLimit
			originalFlowTimeLimit := client.Flow.TimeLimit

			cleared := ClearClientStatusCopy(client, tt.mode)
			tt.assert(t, cleared)

			if client.ExpireAt != originalExpireAt || client.FlowLimit != originalFlowLimit || client.MaxConn != originalMaxConn || client.MaxTunnelNum != originalMaxTunnelNum {
				t.Fatalf("ClearClientStatusCopy() mutated original client = %+v", client)
			}
			if client.Flow == nil || client.Flow.FlowLimit != originalFlowLimitValue || !client.Flow.TimeLimit.Equal(originalFlowTimeLimit) {
				t.Fatalf("ClearClientStatusCopy() mutated original flow = %+v", client.Flow)
			}
		})
	}
}

func TestClearClientStatusCopyRateLimit(t *testing.T) {
	t.Run("create rate when nil", func(t *testing.T) {
		client := &file.Client{Flow: &file.Flow{}, RateLimit: 123}

		cleared := ClearClientStatusCopy(client, "rate_limit")

		if client.RateLimit != 123 {
			t.Fatalf("original RateLimit = %d, want 123", client.RateLimit)
		}
		if cleared.RateLimit != 0 {
			t.Fatalf("cleared RateLimit = %d, want 0", cleared.RateLimit)
		}
		if cleared.Rate == nil {
			t.Fatal("ClearClientStatusCopy() should initialize rate limiter")
		}
		if cleared.Rate.Limit() != 0 {
			t.Fatalf("cleared Rate.Limit() = %d, want 0", cleared.Rate.Limit())
		}
	})

	t.Run("reset existing rate limit", func(t *testing.T) {
		client := &file.Client{Flow: &file.Flow{}, RateLimit: 256, Rate: rate.NewRate(256 * 1024)}

		cleared := ClearClientStatusCopy(client, "rate_limit")

		if client.RateLimit != 256 {
			t.Fatalf("original RateLimit = %d, want 256", client.RateLimit)
		}
		if cleared.RateLimit != 0 {
			t.Fatalf("cleared RateLimit = %d, want 0", cleared.RateLimit)
		}
		if cleared.Rate == nil || cleared.Rate.Limit() != 0 {
			t.Fatalf("cleared Rate = %#v, want limit 0", cleared.Rate)
		}
	})
}

func TestClearClientStatusCopyIgnoresUnknownModeWithoutAllocatingRate(t *testing.T) {
	client := &file.Client{
		Id:        9,
		VerifyKey: "vk-9",
		Flow:      &file.Flow{},
		Cnf:       &file.Config{},
	}

	cleared := ClearClientStatusCopy(client, "unknown")

	if cleared == nil {
		t.Fatal("ClearClientStatusCopy() = nil")
	}
	if cleared.Rate != nil {
		t.Fatalf("ClearClientStatusCopy() allocated rate for unknown mode: %#v", cleared.Rate)
	}
}

func TestDefaultClientServiceClearRejectsUnknownModeBeforeRepositoryLookup(t *testing.T) {
	service := DefaultClientService{
		Backend: Backend{
			Repository: stubRepository{
				getClient: func(id int) (*file.Client, error) {
					t.Fatalf("GetClient(%d) should not be called for unknown clear mode", id)
					return nil, nil
				},
			},
		},
	}

	if _, err := service.Clear(8, "unknown", true); !errors.Is(err, ErrClientModifyFailed) {
		t.Fatalf("Clear() error = %v, want %v", err, ErrClientModifyFailed)
	}
}

func TestClearClientStatusByIDWithRepoRejectsUnknownModeBeforeRange(t *testing.T) {
	rangeCalled := false

	_, err := clearClientStatusByIDWithRepo(stubRepository{
		rangeClients: func(func(*file.Client) bool) {
			rangeCalled = true
		},
	}, 0, "unknown")
	if !errors.Is(err, ErrClientModifyFailed) {
		t.Fatalf("clearClientStatusByIDWithRepo() error = %v, want %v", err, ErrClientModifyFailed)
	}
	if rangeCalled {
		t.Fatal("clearClientStatusByIDWithRepo() should reject unknown mode before ranging clients")
	}
}

func TestBuildAddClientInputForPlatformUserForcesServiceOwner(t *testing.T) {
	input := BuildAddClientInput(ClientMutationContext{
		Principal: Principal{
			Authenticated: true,
			Kind:          "platform_user",
			Permissions:   []string{PermissionClientsCreate},
			Attributes: map[string]string{
				"platform_id":              "master-a",
				"platform_actor_id":        "acct-user",
				"platform_service_user_id": "7",
			},
		},
		ReservedAdminUsername: "admin",
	}, AddClientRequest{
		ClientWriteRequest: ClientWriteRequest{
			UserID:          99,
			OwnerSpecified:  true,
			ManagerUserIDs:  []int{2, 3},
			VKey:            "tenant-client",
			Remark:          "created by platform user",
			ConfigConnAllow: true,
			MaxTunnelNum:    4,
			RateLimit:       5,
			MaxConn:         6,
			FlowLimit:       7,
			TimeLimit:       "2026-03-26 10:00:00",
			Crypt:           true,
			Compress:        true,
			User:            "u",
			Password:        "p",
		},
	})

	if input.CurrentUserID != 7 {
		t.Fatalf("CurrentUserID = %d, want 7", input.CurrentUserID)
	}
	if input.ManageUserBinding || input.AllowManagerUserIDs {
		t.Fatalf("platform user should not manage binding or manager ids: %+v", input)
	}
	if input.UserID != 99 || !input.OwnerSpecified {
		t.Fatalf("requested owner should be preserved for service resolution, got %+v", input)
	}
	if input.SourceType != "platform_user" || input.SourcePlatformID != "master-a" || input.SourceActorID != "acct-user" {
		t.Fatalf("source meta = %q/%q/%q, want platform_user/master-a/acct-user", input.SourceType, input.SourcePlatformID, input.SourceActorID)
	}
}

func TestBuildAddClientInputForFullPlatformAdminDefaultsOwnerToServiceUser(t *testing.T) {
	input := BuildAddClientInput(ClientMutationContext{
		Principal: Principal{
			Authenticated: true,
			Kind:          "platform_admin",
			IsAdmin:       true,
			Roles:         []string{RoleAdmin},
			Permissions:   []string{PermissionAll},
			Attributes: map[string]string{
				"platform_id":              "master-a",
				"platform_actor_id":        "platform-admin",
				"platform_service_user_id": "9",
			},
		},
		ReservedAdminUsername: "admin",
	}, AddClientRequest{
		ClientWriteRequest: ClientWriteRequest{
			VKey: "full-admin-client",
		},
	})

	if input.CurrentUserID != 9 {
		t.Fatalf("CurrentUserID = %d, want 9", input.CurrentUserID)
	}
	if !input.ManageUserBinding || !input.AllowManagerUserIDs {
		t.Fatalf("full platform admin should retain admin mutation privileges: %+v", input)
	}
	if input.SourceType != "platform_admin" {
		t.Fatalf("SourceType = %q, want platform_admin", input.SourceType)
	}
}

func TestBuildAddClientInputForNodeUserForcesCurrentUser(t *testing.T) {
	input := BuildAddClientInput(ClientMutationContext{
		Principal: Principal{
			Authenticated: true,
			Kind:          "user",
			Permissions:   []string{PermissionClientsCreate},
			Attributes: map[string]string{
				"user_id": "11",
			},
		},
	}, AddClientRequest{
		ClientWriteRequest: ClientWriteRequest{
			UserID:         77,
			OwnerSpecified: true,
			ManagerUserIDs: []int{2},
			VKey:           "node-user-client",
		},
	})

	if input.CurrentUserID != 11 {
		t.Fatalf("CurrentUserID = %d, want 11", input.CurrentUserID)
	}
	if input.ManageUserBinding || input.AllowManagerUserIDs {
		t.Fatalf("node user should not manage binding or manager ids: %+v", input)
	}
	if input.SourceType != "node_user" {
		t.Fatalf("SourceType = %q, want node_user", input.SourceType)
	}
}

func TestBuildEditClientInputUsesManagementPermission(t *testing.T) {
	adminInput := BuildEditClientInput(ClientMutationContext{
		Principal: Principal{
			Authenticated: true,
			Kind:          "admin",
			Roles:         []string{RoleAdmin},
			Permissions:   []string{PermissionAll},
		},
		ReservedAdminUsername:   "admin",
		AllowUserChangeUsername: true,
	}, EditClientRequest{
		ID: 7,
		ClientWriteRequest: ClientWriteRequest{
			UserID:                  5,
			OwnerSpecified:          true,
			ManagerUserIDsSpecified: true,
			ManagerUserIDs:          []int{2, 3},
			VKey:                    "client-vkey",
			Password:                "",
			PasswordProvided:        true,
		},
		ResetFlow: true,
	})
	if !adminInput.IsAdmin || !adminInput.ManageUserBinding || !adminInput.AllowManagerUserIDs {
		t.Fatalf("admin edit input = %+v, want admin mutation flags", adminInput)
	}
	if !adminInput.AllowUserChangeUsername || !adminInput.ResetFlow {
		t.Fatalf("admin edit config flags = %+v, want preserved options", adminInput)
	}
	if !adminInput.PasswordProvided || adminInput.Password != "" {
		t.Fatalf("admin edit password flags = %+v, want explicit empty password preserved", adminInput)
	}

	userInput := BuildEditClientInput(ClientMutationContext{
		Principal: Principal{
			Authenticated: true,
			Kind:          "user",
			Permissions:   []string{PermissionClientsUpdate},
		},
		ReservedAdminUsername: "admin",
	}, EditClientRequest{
		ID: 8,
		ClientWriteRequest: ClientWriteRequest{
			ManagerUserIDsSpecified: true,
			ManagerUserIDs:          []int{4},
			VKey:                    "user-client-vkey",
		},
	})
	if userInput.IsAdmin || userInput.ManageUserBinding || userInput.AllowManagerUserIDs {
		t.Fatalf("regular user edit input = %+v, want non-admin mutation flags", userInput)
	}
	if len(userInput.ManagerUserIDs) != 1 || userInput.ManagerUserIDs[0] != 4 {
		t.Fatalf("ManagerUserIDs = %v, want copied request values", userInput.ManagerUserIDs)
	}
	if userInput.PasswordProvided {
		t.Fatalf("regular user edit input should keep password omitted when request omitted it: %+v", userInput)
	}
}

func TestBuildAddClientInputUsesAuthorizationFallbackForPlatformAdmin(t *testing.T) {
	input := BuildAddClientInput(ClientMutationContext{
		Principal: Principal{Username: "mapped-platform"},
		Authz: DefaultAuthorizationService{
			Resolver: stubPermissionResolver{
				normalizePrincipal: func(principal Principal) Principal {
					if principal.Username == "mapped-platform" {
						principal.Authenticated = true
						principal.Kind = "platform_admin"
						principal.Permissions = []string{PermissionAll}
						principal.Attributes = map[string]string{
							"platform_id":              "platform-a",
							"platform_actor_id":        "platform-admin",
							"platform_service_user_id": "12",
						}
					}
					return principal
				},
			},
		},
	}, AddClientRequest{
		ClientWriteRequest: ClientWriteRequest{
			VKey: "resolver-client",
		},
	})

	if input.CurrentUserID != 12 {
		t.Fatalf("CurrentUserID = %d, want 12 from resolver-shaped platform admin", input.CurrentUserID)
	}
	if !input.ManageUserBinding || !input.AllowManagerUserIDs {
		t.Fatalf("input = %+v, want admin mutation privileges from authorization fallback", input)
	}
	if input.SourceType != "platform_admin" || input.SourcePlatformID != "platform-a" || input.SourceActorID != "platform-admin" {
		t.Fatalf("source meta = %q/%q/%q, want platform_admin/platform-a/platform-admin", input.SourceType, input.SourcePlatformID, input.SourceActorID)
	}
}

func TestClearClientStatusByIDWithRepoUsesDeferredPersistenceForBulkClear(t *testing.T) {
	deferredCalls := 0
	saveCalls := 0

	_, err := clearClientStatusByIDWithRepo(stubRepository{
		withDeferredPersistence: func(run func() error) error {
			deferredCalls++
			return run()
		},
		rangeClients: func(fn func(*file.Client) bool) {
			if !fn(&file.Client{Id: 7, VerifyKey: "demo-7", Flow: &file.Flow{}}) {
				return
			}
			fn(&file.Client{Id: 8, VerifyKey: "demo-8", Flow: &file.Flow{}})
		},
		saveClient: func(client *file.Client) error {
			saveCalls++
			return nil
		},
	}, 0, "rate_limit")
	if err != nil {
		t.Fatalf("clearClientStatusByIDWithRepo() error = %v", err)
	}
	if deferredCalls != 1 {
		t.Fatalf("WithDeferredPersistence() calls = %d, want 1", deferredCalls)
	}
	if saveCalls != 2 {
		t.Fatalf("SaveClient() calls = %d, want 2", saveCalls)
	}
}
