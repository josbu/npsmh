package service

import (
	"testing"

	"github.com/djylb/nps/lib/file"
)

func TestDefaultUserServiceDeleteCascadesAttachedClients(t *testing.T) {
	var deletedClients []int
	var disconnected []int
	var cleaned []int
	var deletedUser int
	service := DefaultUserService{
		Backend: Backend{
			Repository: stubRepository{
				getUser: func(id int) (*file.User, error) {
					return &file.User{Id: id, Username: "tenant", TotalFlow: &file.Flow{}}, nil
				},
				rangeClients: func(fn func(*file.Client) bool) {
					fn(&file.Client{Id: 8, UserId: 3})
					fn(&file.Client{Id: 9, UserId: 3})
					fn(&file.Client{Id: 10, UserId: 4})
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

	result, err := service.Delete(3)
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if result.ID != 3 || result.User == nil {
		t.Fatalf("Delete() result = %+v, want deleted user snapshot id 3", result)
	}
	if deletedUser != 3 {
		t.Fatalf("Delete() deleted user = %d, want 3", deletedUser)
	}
	if len(deletedClients) != 2 || deletedClients[0] != 8 || deletedClients[1] != 9 {
		t.Fatalf("Delete() deleted clients = %v, want [8 9]", deletedClients)
	}
	if len(disconnected) != 2 || disconnected[0] != 8 || disconnected[1] != 9 {
		t.Fatalf("Delete() disconnected clients = %v, want [8 9]", disconnected)
	}
	if len(cleaned) != 2 || cleaned[0] != 8 || cleaned[1] != 9 {
		t.Fatalf("Delete() cleaned client resources = %v, want [8 9]", cleaned)
	}
}

func TestDefaultUserServiceAddInitializesCommercialFields(t *testing.T) {
	var created *file.User
	service := DefaultUserService{
		Backend: Backend{
			Repository: stubRepository{
				nextUserID: func() int { return 22 },
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
		Password:              "secret",
		Status:                true,
		ExpireAt:              "2026-03-21 10:00:00",
		FlowLimit:             2048,
		MaxClients:            3,
		MaxTunnels:            4,
		MaxHosts:              5,
		MaxConnections:        6,
		RateLimit:             7,
	})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if result.ID != 22 || created == nil {
		t.Fatalf("Add() id=%d created=%+v, want id 22 and created user", result.ID, created)
	}
	if created.Status != 1 || created.FlowLimit != 2048 || created.MaxClients != 3 || created.MaxTunnels != 4 || created.MaxHosts != 5 || created.MaxConnections != 6 || created.RateLimit != 7 {
		t.Fatalf("Add() created user = %+v", created)
	}
	if created.TotalFlow == nil {
		t.Fatal("Add() should initialize TotalFlow")
	}
	if created.ExpireAt == 0 {
		t.Fatal("Add() should parse ExpireAt")
	}
}

func TestDefaultUserServiceChangeStatusDisconnectsBoundClients(t *testing.T) {
	var disconnected []int
	var saved *file.User
	service := DefaultUserService{
		Backend: Backend{
			Repository: stubRepository{
				getUser: func(id int) (*file.User, error) {
					return &file.User{Id: id, Username: "tenant", Status: 1, TotalFlow: &file.Flow{}}, nil
				},
				saveUser: func(user *file.User) error {
					saved = user
					return nil
				},
				rangeClients: func(fn func(*file.Client) bool) {
					fn(&file.Client{Id: 8, UserId: 3})
					fn(&file.Client{Id: 9, UserId: 3})
					fn(&file.Client{Id: 10, UserId: 4})
				},
			},
			Runtime: stubRuntime{
				disconnectClient: func(id int) {
					disconnected = append(disconnected, id)
				},
			},
		},
	}

	_, err := service.ChangeStatus(3, false)
	if err != nil {
		t.Fatalf("ChangeStatus() error = %v", err)
	}
	if saved == nil || saved.Status != 0 {
		t.Fatalf("ChangeStatus() saved user = %+v, want disabled user", saved)
	}
	if len(disconnected) != 2 || disconnected[0] != 8 || disconnected[1] != 9 {
		t.Fatalf("ChangeStatus() disconnected clients = %v, want [8 9]", disconnected)
	}
}

func TestDefaultUserServiceEditDisconnectsBoundClientsWhenLifecycleExceeded(t *testing.T) {
	var disconnected []int
	service := DefaultUserService{
		Backend: Backend{
			Repository: stubRepository{
				getUser: func(id int) (*file.User, error) {
					return &file.User{
						Id:        id,
						Username:  "tenant",
						Status:    1,
						TotalFlow: &file.Flow{InletFlow: 600, ExportFlow: 500},
					}, nil
				},
				saveUser: func(*file.User) error {
					return nil
				},
				rangeClients: func(fn func(*file.Client) bool) {
					fn(&file.Client{Id: 8, UserId: 3})
					fn(&file.Client{Id: 9, UserId: 3})
					fn(&file.Client{Id: 10, UserId: 4})
				},
			},
			Runtime: stubRuntime{
				disconnectClient: func(id int) {
					disconnected = append(disconnected, id)
				},
			},
		},
	}

	_, err := service.Edit(EditUserInput{
		ID:                    3,
		ReservedAdminUsername: "admin",
		Username:              "tenant",
		Password:              "secret",
		Status:                true,
		FlowLimit:             1000,
	})
	if err != nil {
		t.Fatalf("Edit() error = %v", err)
	}
	if len(disconnected) != 2 || disconnected[0] != 8 || disconnected[1] != 9 {
		t.Fatalf("Edit() disconnected clients = %v, want [8 9]", disconnected)
	}
}
