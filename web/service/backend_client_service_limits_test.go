package service

import (
	"errors"
	"testing"

	"github.com/djylb/nps/lib/file"
)

func TestDefaultClientServiceAddHonorsUserMaxClients(t *testing.T) {
	var saveUserCalled bool
	var createClientCalled bool
	service := DefaultClientService{
		Backend: Backend{
			Repository: stubRepository{
				nextClientID: func() int { return 9 },
				getUser: func(id int) (*file.User, error) {
					return &file.User{
						Id:         id,
						Username:   "demo",
						Status:     1,
						MaxClients: 1,
						TotalFlow:  &file.Flow{},
					}, nil
				},
				rangeClients: func(fn func(*file.Client) bool) {
					fn(&file.Client{Id: 7, UserId: 3})
				},
				saveUser: func(*file.User) error {
					saveUserCalled = true
					return nil
				},
				createClient: func(*file.Client) error {
					createClientCalled = true
					return nil
				},
			},
		},
	}

	_, err := service.Add(AddClientInput{
		ReservedAdminUsername: "admin",
		UserID:                3,
		OwnerSpecified:        true,
		ManageUserBinding:     true,
		VKey:                  "demo-vkey",
	})
	if !errors.Is(err, ErrClientLimitExceeded) {
		t.Fatalf("Add() error = %v, want %v", err, ErrClientLimitExceeded)
	}
	if saveUserCalled || createClientCalled {
		t.Fatalf("Add() should not persist user/client when max clients exceeded")
	}
}

func TestDefaultClientServiceEditHonorsUserMaxClients(t *testing.T) {
	var saveUserCalled bool
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
					}, nil
				},
				getUser: func(id int) (*file.User, error) {
					return &file.User{
						Id:         id,
						Username:   "demo",
						Status:     1,
						MaxClients: 1,
						TotalFlow:  &file.Flow{},
					}, nil
				},
				rangeClients: func(fn func(*file.Client) bool) {
					fn(&file.Client{Id: 99, UserId: 5})
				},
				saveUser: func(*file.User) error {
					saveUserCalled = true
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
	})
	if !errors.Is(err, ErrClientLimitExceeded) {
		t.Fatalf("Edit() error = %v, want %v", err, ErrClientLimitExceeded)
	}
	if saveUserCalled {
		t.Fatalf("Edit() should not persist user when max clients exceeded")
	}
}

func TestDefaultClientServiceAddRejectsOwnerRateLimitOverflow(t *testing.T) {
	var createClientCalled bool
	service := DefaultClientService{
		Backend: Backend{
			Repository: stubRepository{
				nextClientID: func() int { return 9 },
				getUser: func(id int) (*file.User, error) {
					return &file.User{
						Id:        id,
						Username:  "demo",
						Status:    1,
						RateLimit: 5,
						TotalFlow: &file.Flow{},
					}, nil
				},
				createClient: func(*file.Client) error {
					createClientCalled = true
					return nil
				},
			},
		},
	}

	_, err := service.Add(AddClientInput{
		ReservedAdminUsername: "admin",
		UserID:                3,
		OwnerSpecified:        true,
		ManageUserBinding:     true,
		VKey:                  "demo-vkey",
		RateLimit:             6,
	})
	if !errors.Is(err, ErrClientRateLimitExceeded) {
		t.Fatalf("Add() error = %v, want %v", err, ErrClientRateLimitExceeded)
	}
	if createClientCalled {
		t.Fatalf("Add() should not persist client when owner rate limit is exceeded")
	}
}

func TestDefaultClientServiceAddRejectsOwnerConnectionOverflow(t *testing.T) {
	var createClientCalled bool
	service := DefaultClientService{
		Backend: Backend{
			Repository: stubRepository{
				nextClientID: func() int { return 9 },
				getUser: func(id int) (*file.User, error) {
					return &file.User{
						Id:             id,
						Username:       "demo",
						Status:         1,
						MaxConnections: 4,
						TotalFlow:      &file.Flow{},
					}, nil
				},
				createClient: func(*file.Client) error {
					createClientCalled = true
					return nil
				},
			},
		},
	}

	_, err := service.Add(AddClientInput{
		ReservedAdminUsername: "admin",
		UserID:                3,
		OwnerSpecified:        true,
		ManageUserBinding:     true,
		VKey:                  "demo-vkey",
		MaxConn:               5,
	})
	if !errors.Is(err, ErrClientConnLimitExceeded) {
		t.Fatalf("Add() error = %v, want %v", err, ErrClientConnLimitExceeded)
	}
	if createClientCalled {
		t.Fatalf("Add() should not persist client when owner connection limit is exceeded")
	}
}

func TestDefaultClientServiceEditRejectsOwnerRateLimitOverflow(t *testing.T) {
	var saveClientCalled bool
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
				getUser: func(id int) (*file.User, error) {
					return &file.User{
						Id:        id,
						Username:  "demo",
						Status:    1,
						RateLimit: 5,
						TotalFlow: &file.Flow{},
					}, nil
				},
				saveClient: func(*file.Client) error {
					saveClientCalled = true
					return nil
				},
			},
		},
	}

	_, err := service.Edit(EditClientInput{
		ID:                    7,
		IsAdmin:               true,
		ReservedAdminUsername: "admin",
		VKey:                  "client-vkey",
		RateLimit:             6,
	})
	if !errors.Is(err, ErrClientRateLimitExceeded) {
		t.Fatalf("Edit() error = %v, want %v", err, ErrClientRateLimitExceeded)
	}
	if saveClientCalled {
		t.Fatalf("Edit() should not persist client when owner rate limit is exceeded")
	}
}

func TestDefaultClientServiceEditRejectsOwnerConnectionOverflow(t *testing.T) {
	var saveClientCalled bool
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
				getUser: func(id int) (*file.User, error) {
					return &file.User{
						Id:             id,
						Username:       "demo",
						Status:         1,
						MaxConnections: 4,
						TotalFlow:      &file.Flow{},
					}, nil
				},
				saveClient: func(*file.Client) error {
					saveClientCalled = true
					return nil
				},
			},
		},
	}

	_, err := service.Edit(EditClientInput{
		ID:                    7,
		IsAdmin:               true,
		ReservedAdminUsername: "admin",
		VKey:                  "client-vkey",
		MaxConn:               5,
	})
	if !errors.Is(err, ErrClientConnLimitExceeded) {
		t.Fatalf("Edit() error = %v, want %v", err, ErrClientConnLimitExceeded)
	}
	if saveClientCalled {
		t.Fatalf("Edit() should not persist client when owner connection limit is exceeded")
	}
}
