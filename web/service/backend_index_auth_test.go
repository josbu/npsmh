package service

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/djylb/nps/lib/file"
)

func resetBackendTestDB(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	confDir := filepath.Join(root, "conf")
	if err := os.MkdirAll(confDir, 0o755); err != nil {
		t.Fatalf("create temp conf dir: %v", err)
	}
	oldDb := file.GetDb()
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

type managedClientVisibilityLookupRepo struct {
	rangeClients           func(func(*file.Client) bool)
	getManagedClientIDs    func(int) ([]int, error)
	getAllManagedClientIDs func(int) ([]int, error)
	authoritativeManaged   bool
	authoritativeAll       bool
	getClient              func(int) (*file.Client, error)
}

func (r managedClientVisibilityLookupRepo) RangeClients(fn func(*file.Client) bool) {
	if r.rangeClients != nil {
		r.rangeClients(fn)
	}
}

func (r managedClientVisibilityLookupRepo) GetManagedClientIDsByUserID(userID int) ([]int, error) {
	if r.getManagedClientIDs != nil {
		return r.getManagedClientIDs(userID)
	}
	return nil, nil
}

func (managedClientVisibilityLookupRepo) SupportsGetManagedClientIDsByUserID() bool { return true }

func (r managedClientVisibilityLookupRepo) SupportsAuthoritativeManagedClientIDsByUserID() bool {
	return r.authoritativeManaged
}

func (r managedClientVisibilityLookupRepo) GetAllManagedClientIDsByUserID(userID int) ([]int, error) {
	if r.getAllManagedClientIDs != nil {
		return r.getAllManagedClientIDs(userID)
	}
	return nil, nil
}

func (managedClientVisibilityLookupRepo) SupportsGetAllManagedClientIDsByUserID() bool { return true }

func (r managedClientVisibilityLookupRepo) SupportsAuthoritativeAllManagedClientIDsByUserID() bool {
	return r.authoritativeAll
}

func (r managedClientVisibilityLookupRepo) GetClient(id int) (*file.Client, error) {
	if r.getClient != nil {
		return r.getClient(id)
	}
	return nil, nil
}

func (r managedClientVisibilityLookupRepo) SupportsGetClientByID() bool { return r.getClient != nil }

func TestDefaultIndexServiceUsesInjectedRuntimeForDashboard(t *testing.T) {
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{},
			Runtime: stubRuntime{
				dashboardData: func(force bool) map[string]interface{} {
					return map[string]interface{}{"force": force, "source": "backend"}
				},
			},
		},
		QuotaStore: quotaStore{
			users: map[int]*file.User{
				8: {Id: 8, Username: "tenant-8", Status: 1, MaxTunnels: 1, TotalFlow: &file.Flow{}},
			},
			tunnelCounts: map[int]int{8: 1},
		},
	}

	data := service.DashboardData(true)
	if data["source"] != "backend" || data["force"] != true {
		t.Fatalf("DashboardData() = %+v, want injected runtime data", data)
	}
}

func TestDefaultIndexServiceListTunnelsFiltersVisibleClientsForMultiClientUser(t *testing.T) {
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{},
			Runtime: stubRuntime{
				listVisibleTunnels: func(offset, limit int, tunnelType string, clientIDs []int, search, sort, order string) ([]*file.Tunnel, int) {
					if offset != 1 || limit != 1 {
						t.Fatalf("ListVisibleTunnels() called with offset=%d limit=%d, want 1/1 for visible-scope pagination", offset, limit)
					}
					if len(clientIDs) != 2 || clientIDs[0] != 7 || clientIDs[1] != 8 {
						t.Fatalf("ListVisibleTunnels() called with clientIDs=%v, want [7 8]", clientIDs)
					}
					return []*file.Tunnel{
						{Id: 12, Client: &file.Client{Id: 8}},
					}, 2
				},
			},
		},
	}

	rows, count := service.ListTunnels(TunnelListInput{
		Offset: 1,
		Limit:  1,
		Visibility: ClientVisibility{
			ClientIDs: []int{7, 8},
		},
	})
	if count != 2 {
		t.Fatalf("ListTunnels() count = %d, want 2", count)
	}
	if len(rows) != 1 || rows[0].Id != 12 {
		t.Fatalf("ListTunnels() rows = %+v, want tunnel 12 after visible-scope pagination", rows)
	}
}

func TestDefaultIndexServiceListHostsFiltersVisibleClientsForMultiClientUser(t *testing.T) {
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{},
			Runtime: stubRuntime{
				listVisibleHosts: func(offset, limit int, clientIDs []int, search, sort, order string) ([]*file.Host, int) {
					if offset != 0 || limit != 0 {
						t.Fatalf("ListVisibleHosts() called with offset=%d limit=%d, want 0/0 for full visible-scope listing", offset, limit)
					}
					if len(clientIDs) != 2 || clientIDs[0] != 7 || clientIDs[1] != 8 {
						t.Fatalf("ListVisibleHosts() called with clientIDs=%v, want [7 8]", clientIDs)
					}
					return []*file.Host{
						{Id: 21, Client: &file.Client{Id: 7}},
						{Id: 22, Client: &file.Client{Id: 8}},
					}, 2
				},
			},
		},
	}

	rows, count := service.ListHosts(HostListInput{
		Offset: 0,
		Limit:  0,
		Visibility: ClientVisibility{
			ClientIDs: []int{7, 8},
		},
	})
	if count != 2 {
		t.Fatalf("ListHosts() count = %d, want 2", count)
	}
	if len(rows) != 2 || rows[0].Id != 21 || rows[1].Id != 22 {
		t.Fatalf("ListHosts() rows = %+v, want hosts 21 and 22", rows)
	}
}

func TestDefaultIndexServiceAddTunnelUsesInjectedRuntimePortPolicy(t *testing.T) {
	var created struct {
		ID        int
		Port      int
		ClientID  int
		Mode      string
		RateLimit int
		MaxConn   int
	}
	var createdSet bool
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				nextTunnelID: func() int { return 9 },
				getClient: func(id int) (*file.Client, error) {
					return &file.Client{Id: id, Status: true, Flow: &file.Flow{}}, nil
				},
				createTunnel: func(tunnel *file.Tunnel) error {
					created = struct {
						ID        int
						Port      int
						ClientID  int
						Mode      string
						RateLimit int
						MaxConn   int
					}{
						ID:        tunnel.Id,
						Port:      tunnel.Port,
						ClientID:  tunnel.Client.Id,
						Mode:      tunnel.Mode,
						RateLimit: tunnel.RateLimit,
						MaxConn:   tunnel.MaxConn,
					}
					createdSet = true
					return nil
				},
			},
			Runtime: stubRuntime{
				generatePort: func(tunnel *file.Tunnel) int {
					if tunnel == nil || tunnel.Mode != "tcp" {
						t.Fatalf("GenerateTunnelPort() tunnel = %#v, want tcp tunnel", tunnel)
					}
					return 18080
				},
				portAvailable: func(tunnel *file.Tunnel) bool {
					return tunnel != nil && tunnel.Port == 18080 && tunnel.Mode == "tcp"
				},
			},
		},
	}

	result, err := service.AddTunnel(AddTunnelInput{
		ClientID:       1,
		Mode:           "tcp",
		RateLimit:      12,
		MaxConnections: 7,
	})
	if err != nil {
		t.Fatalf("AddTunnel() error = %v", err)
	}
	if !createdSet || created.Port != 18080 || created.ID != 9 || created.ClientID != 1 || created.Mode != "tcp" || created.RateLimit != 12 || created.MaxConn != 7 {
		t.Fatalf("AddTunnel() created tunnel = %+v, want runtime-selected port 18080 and id 9", created)
	}
	if result.ID != 9 || result.ClientID != 1 || result.Mode != "tcp" {
		t.Fatalf("AddTunnel() = %+v, want runtime-backed mutation", result)
	}
}

func TestDefaultIndexServiceAddHostPersistsMaxConnections(t *testing.T) {
	var created struct {
		ID        int
		ClientID  int
		Host      string
		RateLimit int
		MaxConn   int
	}
	var createdSet bool
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				nextHostID: func() int { return 11 },
				getClient: func(id int) (*file.Client, error) {
					return &file.Client{Id: id, Status: true, Flow: &file.Flow{}}, nil
				},
				createHost: func(host *file.Host) error {
					created = struct {
						ID        int
						ClientID  int
						Host      string
						RateLimit int
						MaxConn   int
					}{
						ID:        host.Id,
						ClientID:  host.Client.Id,
						Host:      host.Host,
						RateLimit: host.RateLimit,
						MaxConn:   host.MaxConn,
					}
					createdSet = true
					return nil
				},
			},
			Runtime: stubRuntime{},
		},
		QuotaStore: quotaStore{
			users: map[int]*file.User{
				9: {Id: 9, Username: "tenant-9", Status: 1, TotalFlow: &file.Flow{}},
			},
		},
	}

	result, err := service.AddHost(AddHostInput{
		ClientID:       2,
		Host:           "demo.example.com",
		Target:         "127.0.0.1:8080",
		RateLimit:      9,
		MaxConnections: 4,
	})
	if err != nil {
		t.Fatalf("AddHost() error = %v", err)
	}
	if !createdSet || created.ID != 11 || created.ClientID != 2 || created.Host != "demo.example.com" || created.RateLimit != 9 || created.MaxConn != 4 {
		t.Fatalf("AddHost() created host = %+v, want persisted host mutation with max conn", created)
	}
	if result.ID != 11 || result.ClientID != 2 {
		t.Fatalf("AddHost() = %+v, want runtime-backed mutation", result)
	}
}

func TestDefaultIndexServiceEditTunnelEnforcesTargetUserQuotaOnCrossUserMove(t *testing.T) {
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				getTunnel: func(id int) (*file.Tunnel, error) {
					return &file.Tunnel{
						Id:     id,
						Port:   80,
						Mode:   "tcp",
						Client: &file.Client{Id: 1, UserId: 3},
						Flow:   &file.Flow{},
						Target: &file.Target{TargetStr: "127.0.0.1:80"},
					}, nil
				},
				getClient: func(id int) (*file.Client, error) {
					return &file.Client{Id: id, UserId: 8, Flow: &file.Flow{}}, nil
				},
			},
			Runtime: stubRuntime{},
		},
		QuotaStore: quotaStore{
			users: map[int]*file.User{
				8: {Id: 8, Username: "tenant-8", Status: 1, MaxTunnels: 1, TotalFlow: &file.Flow{}},
			},
			tunnelCounts: map[int]int{8: 1},
		},
	}

	_, err := service.EditTunnel(EditTunnelInput{
		ID:       11,
		ClientID: 2,
	})
	if !errors.Is(err, ErrTunnelLimitExceeded) {
		t.Fatalf("EditTunnel() error = %v, want %v", err, ErrTunnelLimitExceeded)
	}
}

func TestDefaultIndexServiceAddTunnelEnforcesUserQuotaFromOwnerUserID(t *testing.T) {
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				nextTunnelID: func() int { return 15 },
				getClient: func(id int) (*file.Client, error) {
					return &file.Client{Id: id, OwnerUserID: 8, Flow: &file.Flow{}}, nil
				},
			},
			Runtime: stubRuntime{
				portAvailable: func(*file.Tunnel) bool { return true },
			},
		},
		QuotaStore: quotaStore{
			users: map[int]*file.User{
				8: {Id: 8, Username: "tenant-8", Status: 1, MaxTunnels: 1, TotalFlow: &file.Flow{}},
			},
			tunnelCounts: map[int]int{8: 1},
		},
	}

	_, err := service.AddTunnel(AddTunnelInput{
		ClientID: 2,
		Port:     18080,
		Mode:     "tcp",
		Target:   "127.0.0.1:80",
	})
	if !errors.Is(err, ErrTunnelLimitExceeded) {
		t.Fatalf("AddTunnel() error = %v, want %v", err, ErrTunnelLimitExceeded)
	}
}

func TestDefaultIndexServiceAddTunnelUsesBoundOwnerQuotaFastPath(t *testing.T) {
	owner := &file.User{Id: 8, Username: "tenant-8", Status: 1, MaxTunnels: 1, TotalFlow: &file.Flow{}}
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				nextTunnelID: func() int { return 15 },
				getClient: func(id int) (*file.Client, error) {
					client := &file.Client{Id: id, OwnerUserID: owner.Id, Flow: &file.Flow{}}
					client.BindOwnerUser(owner)
					return client, nil
				},
			},
			Runtime: stubRuntime{
				portAvailable: func(*file.Tunnel) bool { return true },
			},
		},
		QuotaStore: quotaStore{
			getUser: func(id int) (*file.User, error) {
				t.Fatalf("GetUser(%d) should not be used when client already carries a bound owner snapshot", id)
				return nil, nil
			},
			tunnelCounts: map[int]int{8: 1},
		},
	}

	_, err := service.AddTunnel(AddTunnelInput{
		ClientID: 2,
		Port:     18080,
		Mode:     "tcp",
		Target:   "127.0.0.1:80",
	})
	if !errors.Is(err, ErrTunnelLimitExceeded) {
		t.Fatalf("AddTunnel() error = %v, want %v", err, ErrTunnelLimitExceeded)
	}
}

func TestDefaultIndexServiceAddTunnelPropagatesQuotaUserLookupError(t *testing.T) {
	lookupErr := errors.New("quota user lookup failed")
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				nextTunnelID: func() int { return 15 },
				getClient: func(id int) (*file.Client, error) {
					return &file.Client{Id: id, OwnerUserID: 8, Flow: &file.Flow{}}, nil
				},
			},
			Runtime: stubRuntime{
				portAvailable: func(*file.Tunnel) bool { return true },
			},
		},
		QuotaStore: quotaStore{
			getUser: func(int) (*file.User, error) {
				return nil, lookupErr
			},
		},
	}

	_, err := service.AddTunnel(AddTunnelInput{
		ClientID: 2,
		Port:     18080,
		Mode:     "tcp",
		Target:   "127.0.0.1:80",
	})
	if !errors.Is(err, lookupErr) {
		t.Fatalf("AddTunnel() error = %v, want %v", err, lookupErr)
	}
}

func TestDefaultIndexServiceEditTunnelDoesNotMutateLiveTunnelOnValidationError(t *testing.T) {
	original := &file.Tunnel{
		Id:     11,
		Port:   80,
		Mode:   "tcp",
		Client: &file.Client{Id: 1, UserId: 3},
		Flow:   &file.Flow{ExportFlow: 7, InletFlow: 8},
		Target: &file.Target{TargetStr: "127.0.0.1:80"},
	}
	var saveCalled bool
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				getTunnel: func(id int) (*file.Tunnel, error) {
					return original, nil
				},
				getClient: func(id int) (*file.Client, error) {
					return &file.Client{Id: id, UserId: 9, MaxTunnelNum: 1, Flow: &file.Flow{}}, nil
				},
				rangeTunnels: func(fn func(*file.Tunnel) bool) {
					fn(&file.Tunnel{Id: 33, Client: &file.Client{Id: 2}})
				},
				saveTunnel: func(*file.Tunnel) error {
					saveCalled = true
					return nil
				},
			},
			Runtime: stubRuntime{},
		},
		QuotaStore: quotaStore{
			users: map[int]*file.User{
				9: {Id: 9, Username: "tenant-9", Status: 1, TotalFlow: &file.Flow{}},
			},
		},
	}

	_, err := service.EditTunnel(EditTunnelInput{
		ID:       11,
		ClientID: 2,
		Port:     81,
		Mode:     "tcp",
		Target:   "127.0.0.1:81",
	})
	if !errors.Is(err, ErrClientResourceLimitExceeded) {
		t.Fatalf("EditTunnel() error = %v, want %v", err, ErrClientResourceLimitExceeded)
	}
	if saveCalled {
		t.Fatal("EditTunnel() should not save on validation error")
	}
	if original.Client == nil || original.Client.Id != 1 || original.Port != 80 {
		t.Fatalf("original tunnel mutated = %+v", original)
	}
	if original.Target == nil || original.Target.TargetStr != "127.0.0.1:80" {
		t.Fatalf("original tunnel target mutated = %+v", original.Target)
	}
}

func TestDefaultIndexServiceEditTunnelUsesClientResourceCountFastPath(t *testing.T) {
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				getTunnel: func(id int) (*file.Tunnel, error) {
					return &file.Tunnel{
						Id:     id,
						Port:   80,
						Mode:   "tcp",
						Client: &file.Client{Id: 1, UserId: 3},
						Flow:   &file.Flow{},
						Target: &file.Target{TargetStr: "127.0.0.1:80"},
					}, nil
				},
				getClient: func(id int) (*file.Client, error) {
					return &file.Client{Id: id, UserId: 9, MaxTunnelNum: 1, Flow: &file.Flow{}}, nil
				},
				rangeTunnels: func(func(*file.Tunnel) bool) {
					t.Fatal("RangeTunnels() should not be used when CountResourcesByClientID is available")
				},
				rangeHosts: func(func(*file.Host) bool) {
					t.Fatal("RangeHosts() should not be used when CountResourcesByClientID is available")
				},
				countResourcesByID: func(clientID int) (int, error) {
					if clientID != 2 {
						t.Fatalf("CountResourcesByClientID(%d), want 2", clientID)
					}
					return 1, nil
				},
			},
			Runtime: stubRuntime{},
		},
		QuotaStore: quotaStore{
			users: map[int]*file.User{
				9: {Id: 9, Username: "tenant-9", Status: 1, TotalFlow: &file.Flow{}},
			},
		},
	}

	_, err := service.EditTunnel(EditTunnelInput{
		ID:       11,
		ClientID: 2,
		Port:     81,
		Mode:     "tcp",
		Target:   "127.0.0.1:81",
	})
	if !errors.Is(err, ErrClientResourceLimitExceeded) {
		t.Fatalf("EditTunnel() error = %v, want %v", err, ErrClientResourceLimitExceeded)
	}
}

func TestDefaultIndexServiceEditTunnelFallsBackAfterClientResourceCountFastPathError(t *testing.T) {
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				getTunnel: func(id int) (*file.Tunnel, error) {
					return &file.Tunnel{
						Id:     id,
						Port:   80,
						Mode:   "tcp",
						Client: &file.Client{Id: 1, UserId: 3},
						Flow:   &file.Flow{},
						Target: &file.Target{TargetStr: "127.0.0.1:80"},
					}, nil
				},
				getClient: func(id int) (*file.Client, error) {
					return &file.Client{Id: id, UserId: 9, MaxTunnelNum: 1, Flow: &file.Flow{}}, nil
				},
				countResourcesByID: func(int) (int, error) {
					return 0, errors.New("count failed")
				},
				rangeTunnels: func(fn func(*file.Tunnel) bool) {
					fn(&file.Tunnel{Id: 33, Client: &file.Client{Id: 2}})
				},
			},
			Runtime: stubRuntime{},
		},
		QuotaStore: quotaStore{
			users: map[int]*file.User{
				9: {Id: 9, Username: "tenant-9", Status: 1, TotalFlow: &file.Flow{}},
			},
		},
	}

	_, err := service.EditTunnel(EditTunnelInput{
		ID:       11,
		ClientID: 2,
		Port:     81,
		Mode:     "tcp",
		Target:   "127.0.0.1:81",
	})
	if !errors.Is(err, ErrClientResourceLimitExceeded) {
		t.Fatalf("EditTunnel() error = %v, want %v", err, ErrClientResourceLimitExceeded)
	}
}

func TestDefaultIndexServiceEditTunnelUsesScopedClientResourceRangeFallback(t *testing.T) {
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				getTunnel: func(id int) (*file.Tunnel, error) {
					return &file.Tunnel{
						Id:     id,
						Port:   80,
						Mode:   "tcp",
						Client: &file.Client{Id: 1, UserId: 3},
						Flow:   &file.Flow{},
						Target: &file.Target{TargetStr: "127.0.0.1:80"},
					}, nil
				},
				getClient: func(id int) (*file.Client, error) {
					return &file.Client{Id: id, UserId: 9, MaxTunnelNum: 1, Flow: &file.Flow{}}, nil
				},
				rangeTunnels: func(func(*file.Tunnel) bool) {
					t.Fatal("RangeTunnels() should not be used when scoped client tunnel iteration is available")
				},
				rangeHosts: func(func(*file.Host) bool) {
					t.Fatal("RangeHosts() should not be used when scoped client host iteration is available")
				},
				rangeTunnelsByID: func(clientID int, fn func(*file.Tunnel) bool) {
					if clientID != 2 {
						t.Fatalf("RangeTunnelsByClientID(%d), want 2", clientID)
					}
					fn(&file.Tunnel{Id: 33, Client: &file.Client{Id: 2}})
				},
				rangeHostsByID: func(clientID int, fn func(*file.Host) bool) {
					if clientID != 2 {
						t.Fatalf("RangeHostsByClientID(%d), want 2", clientID)
					}
				},
			},
			Runtime: stubRuntime{},
		},
		QuotaStore: quotaStore{
			users: map[int]*file.User{
				9: {Id: 9, Username: "tenant-9", Status: 1, TotalFlow: &file.Flow{}},
			},
		},
	}

	_, err := service.EditTunnel(EditTunnelInput{
		ID:       11,
		ClientID: 2,
		Port:     81,
		Mode:     "tcp",
		Target:   "127.0.0.1:81",
	})
	if !errors.Is(err, ErrClientResourceLimitExceeded) {
		t.Fatalf("EditTunnel() error = %v, want %v", err, ErrClientResourceLimitExceeded)
	}
}

func TestDefaultIndexServiceEditTunnelStopsScopedClientResourceCountingAtLimit(t *testing.T) {
	tunnelCalls := 0
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				getTunnel: func(id int) (*file.Tunnel, error) {
					return &file.Tunnel{
						Id:     id,
						Port:   80,
						Mode:   "tcp",
						Client: &file.Client{Id: 1, UserId: 3},
						Flow:   &file.Flow{},
						Target: &file.Target{TargetStr: "127.0.0.1:80"},
					}, nil
				},
				getClient: func(id int) (*file.Client, error) {
					return &file.Client{Id: id, UserId: 9, MaxTunnelNum: 1, Flow: &file.Flow{}}, nil
				},
				rangeHosts: func(func(*file.Host) bool) {
					t.Fatal("RangeHosts() should not be used once scoped tunnel counting already reached the client limit")
				},
				rangeTunnelsByID: func(clientID int, fn func(*file.Tunnel) bool) {
					if clientID != 2 {
						t.Fatalf("RangeTunnelsByClientID(%d), want 2", clientID)
					}
					tunnelCalls++
					if fn(&file.Tunnel{Id: 33, Client: &file.Client{Id: 2}}) {
						t.Fatal("RangeTunnelsByClientID() should stop after the client limit is reached")
					}
				},
				rangeHostsByID: func(int, func(*file.Host) bool) {
					t.Fatal("RangeHostsByClientID() should not be used once scoped tunnel counting already reached the client limit")
				},
			},
			Runtime: stubRuntime{},
		},
		QuotaStore: quotaStore{
			users: map[int]*file.User{
				9: {Id: 9, Username: "tenant-9", Status: 1, TotalFlow: &file.Flow{}},
			},
		},
	}

	_, err := service.EditTunnel(EditTunnelInput{
		ID:       11,
		ClientID: 2,
		Port:     81,
		Mode:     "tcp",
		Target:   "127.0.0.1:81",
	})
	if !errors.Is(err, ErrClientResourceLimitExceeded) {
		t.Fatalf("EditTunnel() error = %v, want %v", err, ErrClientResourceLimitExceeded)
	}
	if tunnelCalls != 1 {
		t.Fatalf("RangeTunnelsByClientID() calls = %d, want 1", tunnelCalls)
	}
}

func TestDefaultIndexServiceEditTunnelStopsGlobalClientResourceCountingAtLimit(t *testing.T) {
	tunnelCalls := 0
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				getTunnel: func(id int) (*file.Tunnel, error) {
					return &file.Tunnel{
						Id:     id,
						Port:   80,
						Mode:   "tcp",
						Client: &file.Client{Id: 1, UserId: 3},
						Flow:   &file.Flow{},
						Target: &file.Target{TargetStr: "127.0.0.1:80"},
					}, nil
				},
				getClient: func(id int) (*file.Client, error) {
					return &file.Client{Id: id, UserId: 9, MaxTunnelNum: 1, Flow: &file.Flow{}}, nil
				},
				rangeTunnels: func(fn func(*file.Tunnel) bool) {
					tunnelCalls++
					if fn(&file.Tunnel{Id: 33, Client: &file.Client{Id: 2}}) {
						t.Fatal("RangeTunnels() should stop after the client limit is reached")
					}
				},
				rangeHosts: func(func(*file.Host) bool) {
					t.Fatal("RangeHosts() should not be used once global tunnel counting already reached the client limit")
				},
			},
			Runtime: stubRuntime{},
		},
		QuotaStore: quotaStore{
			users: map[int]*file.User{
				9: {Id: 9, Username: "tenant-9", Status: 1, TotalFlow: &file.Flow{}},
			},
		},
	}

	_, err := service.EditTunnel(EditTunnelInput{
		ID:       11,
		ClientID: 2,
		Port:     81,
		Mode:     "tcp",
		Target:   "127.0.0.1:81",
	})
	if !errors.Is(err, ErrClientResourceLimitExceeded) {
		t.Fatalf("EditTunnel() error = %v, want %v", err, ErrClientResourceLimitExceeded)
	}
	if tunnelCalls != 1 {
		t.Fatalf("RangeTunnels() calls = %d, want 1", tunnelCalls)
	}
}

func TestOwnedResourceCountsByUserUsesScopedOwnedClientResources(t *testing.T) {
	clientCount, tunnelCount, hostCount := OwnedResourceCountsByUser(stubRepository{
		rangeClients: func(func(*file.Client) bool) {
			t.Fatal("RangeClients() should not be used when GetClientsByUserID is available")
		},
		rangeTunnels: func(func(*file.Tunnel) bool) {
			t.Fatal("RangeTunnels() should not be used when scoped client tunnel iteration is available")
		},
		rangeHosts: func(func(*file.Host) bool) {
			t.Fatal("RangeHosts() should not be used when scoped client host iteration is available")
		},
		getClientsByUserID: func(userID int) ([]*file.Client, error) {
			if userID != 7 {
				t.Fatalf("GetClientsByUserID(%d), want 7", userID)
			}
			return []*file.Client{
				{Id: 11, OwnerUserID: 7},
				{Id: 12, OwnerUserID: 7},
			}, nil
		},
		rangeTunnelsByID: func(clientID int, fn func(*file.Tunnel) bool) {
			switch clientID {
			case 11:
				fn(&file.Tunnel{Id: 21, Client: &file.Client{Id: 11}})
			case 12:
				fn(&file.Tunnel{Id: 22, Client: &file.Client{Id: 12}})
				fn(&file.Tunnel{Id: 23, Client: &file.Client{Id: 12}})
			default:
				t.Fatalf("RangeTunnelsByClientID(%d), want 11 or 12", clientID)
			}
		},
		rangeHostsByID: func(clientID int, fn func(*file.Host) bool) {
			switch clientID {
			case 11:
				fn(&file.Host{Id: 31, Client: &file.Client{Id: 11}})
			case 12:
			default:
				t.Fatalf("RangeHostsByClientID(%d), want 11 or 12", clientID)
			}
		},
	}, 7)
	if clientCount != 2 || tunnelCount != 3 || hostCount != 1 {
		t.Fatalf("OwnedResourceCountsByUser() = (%d,%d,%d), want (2,3,1)", clientCount, tunnelCount, hostCount)
	}
}

func TestOwnedResourceCountsByUserUsesScopedTunnelRangeWithGlobalHostFallback(t *testing.T) {
	hostRangeCalls := 0
	clientCount, tunnelCount, hostCount := OwnedResourceCountsByUser(stubRepository{
		rangeClients: func(func(*file.Client) bool) {
			t.Fatal("RangeClients() should not be used when GetClientsByUserID is available")
		},
		rangeTunnels: func(func(*file.Tunnel) bool) {
			t.Fatal("RangeTunnels() should not be used when scoped client tunnel iteration is available")
		},
		rangeHosts: func(fn func(*file.Host) bool) {
			hostRangeCalls++
			fn(&file.Host{Id: 31, Client: &file.Client{Id: 11}})
			fn(&file.Host{Id: 32, Client: &file.Client{Id: 99}})
		},
		getClientsByUserID: func(userID int) ([]*file.Client, error) {
			if userID != 7 {
				t.Fatalf("GetClientsByUserID(%d), want 7", userID)
			}
			return []*file.Client{
				{Id: 11, OwnerUserID: 7},
				{Id: 12, OwnerUserID: 7},
			}, nil
		},
		rangeTunnelsByID: func(clientID int, fn func(*file.Tunnel) bool) {
			switch clientID {
			case 11:
				fn(&file.Tunnel{Id: 21, Client: &file.Client{Id: 11}})
			case 12:
				fn(&file.Tunnel{Id: 22, Client: &file.Client{Id: 12}})
			default:
				t.Fatalf("RangeTunnelsByClientID(%d), want 11 or 12", clientID)
			}
		},
	}, 7)
	if hostRangeCalls != 1 {
		t.Fatalf("RangeHosts() calls = %d, want 1 fallback pass", hostRangeCalls)
	}
	if clientCount != 2 || tunnelCount != 2 || hostCount != 1 {
		t.Fatalf("OwnedResourceCountsByUser() = (%d,%d,%d), want (2,2,1)", clientCount, tunnelCount, hostCount)
	}
}

func TestOwnedResourceCountsByUserUsesGlobalTunnelFallbackWithScopedHostRange(t *testing.T) {
	tunnelRangeCalls := 0
	clientCount, tunnelCount, hostCount := OwnedResourceCountsByUser(stubRepository{
		rangeClients: func(func(*file.Client) bool) {
			t.Fatal("RangeClients() should not be used when GetClientsByUserID is available")
		},
		rangeTunnels: func(fn func(*file.Tunnel) bool) {
			tunnelRangeCalls++
			fn(&file.Tunnel{Id: 21, Client: &file.Client{Id: 11}})
			fn(&file.Tunnel{Id: 22, Client: &file.Client{Id: 99}})
		},
		rangeHosts: func(func(*file.Host) bool) {
			t.Fatal("RangeHosts() should not be used when scoped client host iteration is available")
		},
		getClientsByUserID: func(userID int) ([]*file.Client, error) {
			if userID != 7 {
				t.Fatalf("GetClientsByUserID(%d), want 7", userID)
			}
			return []*file.Client{
				{Id: 11, OwnerUserID: 7},
				{Id: 12, OwnerUserID: 7},
			}, nil
		},
		rangeHostsByID: func(clientID int, fn func(*file.Host) bool) {
			switch clientID {
			case 11:
				fn(&file.Host{Id: 31, Client: &file.Client{Id: 11}})
			case 12:
				fn(&file.Host{Id: 32, Client: &file.Client{Id: 12}})
			default:
				t.Fatalf("RangeHostsByClientID(%d), want 11 or 12", clientID)
			}
		},
	}, 7)
	if tunnelRangeCalls != 1 {
		t.Fatalf("RangeTunnels() calls = %d, want 1 fallback pass", tunnelRangeCalls)
	}
	if clientCount != 2 || tunnelCount != 1 || hostCount != 2 {
		t.Fatalf("OwnedResourceCountsByUser() = (%d,%d,%d), want (2,1,2)", clientCount, tunnelCount, hostCount)
	}
}

func TestManagedClientIDsByUserFallsBackToRangeWhenIndexIsNotAuthoritativeEvenWithClientLookup(t *testing.T) {
	rangeCalls := 0
	clientIDs, err := ManagedClientIDsByUser(managedClientVisibilityLookupRepo{
		getManagedClientIDs: func(userID int) ([]int, error) {
			if userID != 7 {
				t.Fatalf("GetManagedClientIDsByUserID(%d), want 7", userID)
			}
			return []int{13, 11, 12, 11}, nil
		},
		getClient: func(id int) (*file.Client, error) {
			t.Fatalf("GetClient(%d) should not be trusted for non-authoritative managed visibility", id)
			return nil, nil
		},
		rangeClients: func(fn func(*file.Client) bool) {
			rangeCalls++
			fn(&file.Client{Id: 11, Status: true, OwnerUserID: 8, ManagerUserIDs: []int{7}})
			fn(&file.Client{Id: 12, Status: false, OwnerUserID: 8, ManagerUserIDs: []int{7}})
			fn(&file.Client{Id: 13, Status: true, NoDisplay: true, OwnerUserID: 8, ManagerUserIDs: []int{7}})
		},
	}, 7)
	if err != nil {
		t.Fatalf("ManagedClientIDsByUser() error = %v", err)
	}
	if rangeCalls != 1 {
		t.Fatalf("RangeClients() calls = %d, want 1 completeness-preserving fallback pass", rangeCalls)
	}
	if len(clientIDs) != 1 || clientIDs[0] != 11 {
		t.Fatalf("ManagedClientIDsByUser() = %v, want [11] after range verification", clientIDs)
	}
}

func TestAllManagedClientIDsByUserFallsBackToRangeWhenIndexIsNotAuthoritativeAndCannotBeVerified(t *testing.T) {
	rangeCalls := 0
	clientIDs, err := AllManagedClientIDsByUser(managedClientVisibilityLookupRepo{
		getAllManagedClientIDs: func(userID int) ([]int, error) {
			if userID != 7 {
				t.Fatalf("GetAllManagedClientIDsByUserID(%d), want 7", userID)
			}
			return []int{13, 11}, nil
		},
		rangeClients: func(fn func(*file.Client) bool) {
			rangeCalls++
			fn(&file.Client{Id: 11, Status: true, OwnerUserID: 8, ManagerUserIDs: []int{7}})
			fn(&file.Client{Id: 12, Status: true, OwnerUserID: 7})
			fn(&file.Client{Id: 13, Status: true, OwnerUserID: 8, ManagerUserIDs: []int{9}})
		},
	}, 7)
	if err != nil {
		t.Fatalf("AllManagedClientIDsByUser() error = %v", err)
	}
	if rangeCalls != 1 {
		t.Fatalf("RangeClients() calls = %d, want 1 fallback verification pass", rangeCalls)
	}
	if len(clientIDs) != 2 || clientIDs[0] != 11 || clientIDs[1] != 12 {
		t.Fatalf("AllManagedClientIDsByUser() = %v, want [11 12] after range verification", clientIDs)
	}
}

func TestDefaultIndexServiceEditTunnelAllowsMoveWithinSameUserQuota(t *testing.T) {
	var saved *file.Tunnel
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				getTunnel: func(id int) (*file.Tunnel, error) {
					return &file.Tunnel{
						Id:     id,
						Port:   80,
						Mode:   "tcp",
						Client: &file.Client{Id: 1, UserId: 8},
						Flow:   &file.Flow{},
						Target: &file.Target{TargetStr: "127.0.0.1:80"},
					}, nil
				},
				getClient: func(id int) (*file.Client, error) {
					return &file.Client{Id: id, UserId: 8, Flow: &file.Flow{}}, nil
				},
				saveTunnel: func(tunnel *file.Tunnel) error {
					saved = tunnel
					return nil
				},
			},
			Runtime: stubRuntime{},
		},
		QuotaStore: quotaStore{
			users: map[int]*file.User{
				8: {Id: 8, Username: "tenant-8", Status: 1, MaxTunnels: 1, TotalFlow: &file.Flow{}},
			},
			tunnelCounts: map[int]int{8: 1},
		},
	}

	_, err := service.EditTunnel(EditTunnelInput{
		ID:       12,
		ClientID: 2,
	})
	if err != nil {
		t.Fatalf("EditTunnel() error = %v", err)
	}
	if saved == nil || saved.Client == nil || saved.Client.Id != 2 {
		t.Fatalf("EditTunnel() saved = %+v, want target client id 2", saved)
	}
}

func TestDefaultIndexServiceEditHostEnforcesTargetClientResourceQuota(t *testing.T) {
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				getHost: func(id int) (*file.Host, error) {
					return &file.Host{
						Id:     id,
						Host:   "old.example.com",
						Scheme: "all",
						Client: &file.Client{Id: 1, UserId: 3},
						Flow:   &file.Flow{},
						Target: &file.Target{TargetStr: "127.0.0.1:80"},
					}, nil
				},
				getClient: func(id int) (*file.Client, error) {
					return &file.Client{Id: id, UserId: 9, MaxTunnelNum: 1, Flow: &file.Flow{}}, nil
				},
				rangeTunnels: func(fn func(*file.Tunnel) bool) {
					fn(&file.Tunnel{Id: 33, Client: &file.Client{Id: 2}})
				},
			},
			Runtime: stubRuntime{},
		},
		QuotaStore: quotaStore{
			users: map[int]*file.User{
				9: {Id: 9, Username: "tenant-9", Status: 1, MaxHosts: 2, TotalFlow: &file.Flow{}},
			},
			hostCounts: map[int]int{9: 1},
		},
	}

	_, err := service.EditHost(EditHostInput{
		ID:       21,
		ClientID: 2,
		Host:     "new.example.com",
	})
	if !errors.Is(err, ErrClientResourceLimitExceeded) {
		t.Fatalf("EditHost() error = %v, want %v", err, ErrClientResourceLimitExceeded)
	}
}

func TestDefaultIndexServiceAddHostEnforcesUserQuotaFromOwnerUserID(t *testing.T) {
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				nextHostID: func() int { return 22 },
				getClient: func(id int) (*file.Client, error) {
					return &file.Client{Id: id, OwnerUserID: 9, Flow: &file.Flow{}}, nil
				},
			},
			Runtime: stubRuntime{},
		},
		QuotaStore: quotaStore{
			users: map[int]*file.User{
				9: {Id: 9, Username: "tenant-9", Status: 1, MaxHosts: 1, TotalFlow: &file.Flow{}},
			},
			hostCounts: map[int]int{9: 1},
		},
	}

	_, err := service.AddHost(AddHostInput{
		ClientID: 2,
		Host:     "demo.example.com",
		Target:   "127.0.0.1:80",
	})
	if !errors.Is(err, ErrHostLimitExceeded) {
		t.Fatalf("AddHost() error = %v, want %v", err, ErrHostLimitExceeded)
	}
}

func TestDefaultIndexServiceAddHostUsesBoundOwnerQuotaFastPath(t *testing.T) {
	owner := &file.User{Id: 9, Username: "tenant-9", Status: 1, MaxHosts: 1, TotalFlow: &file.Flow{}}
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				nextHostID: func() int { return 22 },
				getClient: func(id int) (*file.Client, error) {
					client := &file.Client{Id: id, OwnerUserID: owner.Id, Flow: &file.Flow{}}
					client.BindOwnerUser(owner)
					return client, nil
				},
			},
			Runtime: stubRuntime{},
		},
		QuotaStore: quotaStore{
			getUser: func(id int) (*file.User, error) {
				t.Fatalf("GetUser(%d) should not be used when client already carries a bound owner snapshot", id)
				return nil, nil
			},
			hostCounts: map[int]int{9: 1},
		},
	}

	_, err := service.AddHost(AddHostInput{
		ClientID: 2,
		Host:     "demo.example.com",
		Target:   "127.0.0.1:80",
	})
	if !errors.Is(err, ErrHostLimitExceeded) {
		t.Fatalf("AddHost() error = %v, want %v", err, ErrHostLimitExceeded)
	}
}

func TestDefaultIndexServiceAddHostPropagatesQuotaUserLookupError(t *testing.T) {
	lookupErr := errors.New("quota user lookup failed")
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				nextHostID: func() int { return 22 },
				getClient: func(id int) (*file.Client, error) {
					return &file.Client{Id: id, OwnerUserID: 9, Flow: &file.Flow{}}, nil
				},
			},
			Runtime: stubRuntime{},
		},
		QuotaStore: quotaStore{
			getUser: func(int) (*file.User, error) {
				return nil, lookupErr
			},
		},
	}

	_, err := service.AddHost(AddHostInput{
		ClientID: 2,
		Host:     "demo.example.com",
		Target:   "127.0.0.1:80",
	})
	if !errors.Is(err, lookupErr) {
		t.Fatalf("AddHost() error = %v, want %v", err, lookupErr)
	}
}

func TestDefaultIndexServiceEditHostDoesNotMutateLiveHostOnValidationError(t *testing.T) {
	original := &file.Host{
		Id:     21,
		Host:   "old.example.com",
		Scheme: "all",
		Client: &file.Client{Id: 1, UserId: 3},
		Flow:   &file.Flow{ExportFlow: 5, InletFlow: 6},
		Target: &file.Target{TargetStr: "127.0.0.1:80"},
	}
	var saveCalled bool
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				getHost: func(id int) (*file.Host, error) {
					return original, nil
				},
				getClient: func(id int) (*file.Client, error) {
					return &file.Client{Id: id, UserId: 9, MaxTunnelNum: 1, Flow: &file.Flow{}}, nil
				},
				rangeTunnels: func(fn func(*file.Tunnel) bool) {
					fn(&file.Tunnel{Id: 33, Client: &file.Client{Id: 2}})
				},
				saveHost: func(*file.Host, string) error {
					saveCalled = true
					return nil
				},
			},
			Runtime: stubRuntime{},
		},
		QuotaStore: quotaStore{
			users: map[int]*file.User{
				9: {Id: 9, Username: "tenant-9", Status: 1, TotalFlow: &file.Flow{}},
			},
		},
	}

	_, err := service.EditHost(EditHostInput{
		ID:       21,
		ClientID: 2,
		Host:     "new.example.com",
		Target:   "127.0.0.1:81",
		Scheme:   "https",
	})
	if !errors.Is(err, ErrClientResourceLimitExceeded) {
		t.Fatalf("EditHost() error = %v, want %v", err, ErrClientResourceLimitExceeded)
	}
	if saveCalled {
		t.Fatal("EditHost() should not save on validation error")
	}
	if original.Client == nil || original.Client.Id != 1 || original.Host != "old.example.com" || original.Scheme != "all" {
		t.Fatalf("original host mutated = %+v", original)
	}
	if original.Target == nil || original.Target.TargetStr != "127.0.0.1:80" {
		t.Fatalf("original host target mutated = %+v", original.Target)
	}
}

func TestChangeTunnelStatusWithRepoDoesNotMutateLiveTunnelOnSaveError(t *testing.T) {
	original := &file.Tunnel{
		Id:        7,
		HttpProxy: false,
		Flow:      &file.Flow{InletFlow: 11, ExportFlow: 12},
	}
	_, err := changeTunnelStatusWithRepo(stubRepository{
		getTunnel: func(id int) (*file.Tunnel, error) {
			return original, nil
		},
		saveTunnel: func(*file.Tunnel) error {
			return errors.New("save failed")
		},
	}, nil, 7, "http", "start")
	if err == nil || err.Error() != "save failed" {
		t.Fatalf("changeTunnelStatusWithRepo() error = %v, want save failed", err)
	}
	if original.HttpProxy {
		t.Fatalf("original tunnel mutated = %+v", original)
	}
	if original.Flow == nil || original.Flow.InletFlow != 11 || original.Flow.ExportFlow != 12 {
		t.Fatalf("original tunnel flow mutated = %+v", original.Flow)
	}
}

func TestClearClientStatusByIDWithRepoClearsRelatedFlowsViaCopies(t *testing.T) {
	originalClient := &file.Client{
		Id:         7,
		VerifyKey:  "demo",
		Flow:       &file.Flow{InletFlow: 11, ExportFlow: 12},
		InletFlow:  13,
		ExportFlow: 14,
	}
	originalTunnel := &file.Tunnel{
		Id:     8,
		Client: &file.Client{Id: 7},
		Flow:   &file.Flow{InletFlow: 21, ExportFlow: 22},
	}
	originalHost := &file.Host{
		Id:     9,
		Client: &file.Client{Id: 7},
		Flow:   &file.Flow{InletFlow: 31, ExportFlow: 32},
	}
	var savedClient *file.Client
	var savedTunnel *file.Tunnel
	var savedHost *file.Host

	_, err := clearClientStatusByIDWithRepo(stubRepository{
		getClient: func(id int) (*file.Client, error) {
			return originalClient, nil
		},
		rangeTunnels: func(fn func(*file.Tunnel) bool) {
			fn(originalTunnel)
		},
		rangeHosts: func(fn func(*file.Host) bool) {
			fn(originalHost)
		},
		saveClient: func(client *file.Client) error {
			savedClient = client
			return nil
		},
		saveTunnel: func(tunnel *file.Tunnel) error {
			savedTunnel = tunnel
			return nil
		},
		saveHost: func(host *file.Host, oldHost string) error {
			savedHost = host
			return nil
		},
	}, 7, "flow")
	if err != nil {
		t.Fatalf("clearClientStatusByIDWithRepo() error = %v", err)
	}
	if savedClient == nil || savedTunnel == nil || savedHost == nil {
		t.Fatalf("saved copies missing: client=%+v tunnel=%+v host=%+v", savedClient, savedTunnel, savedHost)
	}
	if savedClient.Flow.InletFlow != 0 || savedClient.Flow.ExportFlow != 0 || savedClient.InletFlow != 0 || savedClient.ExportFlow != 0 {
		t.Fatalf("saved client flow not cleared = %+v", savedClient)
	}
	if savedTunnel.Flow.InletFlow != 0 || savedTunnel.Flow.ExportFlow != 0 {
		t.Fatalf("saved tunnel flow not cleared = %+v", savedTunnel.Flow)
	}
	if savedHost.Flow.InletFlow != 0 || savedHost.Flow.ExportFlow != 0 {
		t.Fatalf("saved host flow not cleared = %+v", savedHost.Flow)
	}
	if originalClient.Flow.InletFlow != 11 || originalClient.Flow.ExportFlow != 12 || originalClient.InletFlow != 13 || originalClient.ExportFlow != 14 {
		t.Fatalf("original client mutated = %+v", originalClient)
	}
	if originalTunnel.Flow.InletFlow != 21 || originalTunnel.Flow.ExportFlow != 22 {
		t.Fatalf("original tunnel mutated = %+v", originalTunnel.Flow)
	}
	if originalHost.Flow.InletFlow != 31 || originalHost.Flow.ExportFlow != 32 {
		t.Fatalf("original host mutated = %+v", originalHost.Flow)
	}
}

func TestClearClientStatusByIDWithRepoUsesScopedResourceRangeFastPath(t *testing.T) {
	client := &file.Client{
		Id:        7,
		VerifyKey: "demo",
		Flow:      &file.Flow{InletFlow: 11, ExportFlow: 12},
	}
	var savedClient *file.Client
	var savedTunnel *file.Tunnel
	var savedHost *file.Host

	_, err := clearClientStatusByIDWithRepo(stubRepository{
		getClient: func(id int) (*file.Client, error) {
			return client, nil
		},
		rangeHosts: func(func(*file.Host) bool) {
			t.Fatal("RangeHosts() should not be used when RangeHostsByClientID is available")
		},
		rangeTunnels: func(func(*file.Tunnel) bool) {
			t.Fatal("RangeTunnels() should not be used when RangeTunnelsByClientID is available")
		},
		rangeHostsByID: func(clientID int, fn func(*file.Host) bool) {
			if clientID != 7 {
				t.Fatalf("RangeHostsByClientID(%d), want 7", clientID)
			}
			fn(&file.Host{
				Id:     9,
				Client: &file.Client{Id: 7},
				Flow:   &file.Flow{InletFlow: 31, ExportFlow: 32},
			})
		},
		rangeTunnelsByID: func(clientID int, fn func(*file.Tunnel) bool) {
			if clientID != 7 {
				t.Fatalf("RangeTunnelsByClientID(%d), want 7", clientID)
			}
			fn(&file.Tunnel{
				Id:     8,
				Client: &file.Client{Id: 7},
				Flow:   &file.Flow{InletFlow: 21, ExportFlow: 22},
			})
		},
		saveHost: func(host *file.Host, oldHost string) error {
			savedHost = host
			return nil
		},
		saveTunnel: func(tunnel *file.Tunnel) error {
			savedTunnel = tunnel
			return nil
		},
		saveClient: func(current *file.Client) error {
			savedClient = current
			return nil
		},
	}, 7, "flow")
	if err != nil {
		t.Fatalf("clearClientStatusByIDWithRepo() error = %v", err)
	}
	if savedClient == nil || savedTunnel == nil || savedHost == nil {
		t.Fatalf("saved copies missing: client=%+v tunnel=%+v host=%+v", savedClient, savedTunnel, savedHost)
	}
}

func TestClearClientStatusByIDWithRepoMapsMissingClient(t *testing.T) {
	_, err := clearClientStatusByIDWithRepo(stubRepository{
		getClient: func(id int) (*file.Client, error) {
			if id != 7 {
				t.Fatalf("GetClient(%d), want 7", id)
			}
			return nil, file.ErrClientNotFound
		},
	}, 7, "flow")
	if !errors.Is(err, ErrClientNotFound) {
		t.Fatalf("clearClientStatusByIDWithRepo() error = %v, want %v", err, ErrClientNotFound)
	}
}

func TestClearClientStatusByIDWithRepoSkipsMissingRelatedResources(t *testing.T) {
	client := &file.Client{
		Id:        7,
		VerifyKey: "demo",
		Flow:      &file.Flow{InletFlow: 11, ExportFlow: 12},
	}
	var savedClient *file.Client
	hostSaves := 0
	tunnelSaves := 0

	_, err := clearClientStatusByIDWithRepo(stubRepository{
		getClient: func(id int) (*file.Client, error) {
			return client, nil
		},
		rangeHosts: func(fn func(*file.Host) bool) {
			fn(&file.Host{
				Id:     9,
				Client: &file.Client{Id: 7},
				Flow:   &file.Flow{InletFlow: 31, ExportFlow: 32},
				Target: &file.Target{TargetStr: "127.0.0.1:8080"},
			})
		},
		rangeTunnels: func(fn func(*file.Tunnel) bool) {
			fn(&file.Tunnel{
				Id:     8,
				Client: &file.Client{Id: 7},
				Flow:   &file.Flow{InletFlow: 21, ExportFlow: 22},
				Target: &file.Target{TargetStr: "127.0.0.1:9090"},
			})
		},
		saveHost: func(host *file.Host, oldHost string) error {
			hostSaves++
			return file.ErrHostNotFound
		},
		saveTunnel: func(tunnel *file.Tunnel) error {
			tunnelSaves++
			return file.ErrTaskNotFound
		},
		saveClient: func(current *file.Client) error {
			savedClient = current
			return nil
		},
	}, 7, "flow")
	if err != nil {
		t.Fatalf("clearClientStatusByIDWithRepo() error = %v", err)
	}
	if hostSaves != 1 || tunnelSaves != 1 {
		t.Fatalf("related save attempts = host:%d tunnel:%d, want 1/1", hostSaves, tunnelSaves)
	}
	if savedClient == nil || savedClient.Flow == nil || savedClient.Flow.InletFlow != 0 || savedClient.Flow.ExportFlow != 0 {
		t.Fatalf("saved client = %+v, want cleared flow", savedClient)
	}
}

func TestClearClientStatusByIDWithRepoClearsAllClientFlowsInSingleResourcePass(t *testing.T) {
	clients := []*file.Client{
		{Id: 7, VerifyKey: "demo-7", Flow: &file.Flow{InletFlow: 11, ExportFlow: 12}},
		{Id: 8, VerifyKey: "demo-8", Flow: &file.Flow{InletFlow: 13, ExportFlow: 14}},
	}
	hosts := []*file.Host{
		{Id: 9, Client: &file.Client{Id: 7}, Flow: &file.Flow{InletFlow: 21, ExportFlow: 22}},
		{Id: 10, Client: &file.Client{Id: 8}, Flow: &file.Flow{InletFlow: 23, ExportFlow: 24}},
		{Id: 11, Client: &file.Client{Id: 99}, Flow: &file.Flow{InletFlow: 25, ExportFlow: 26}},
	}
	tunnels := []*file.Tunnel{
		{Id: 12, Client: &file.Client{Id: 7}, Flow: &file.Flow{InletFlow: 31, ExportFlow: 32}},
		{Id: 13, Client: &file.Client{Id: 8}, Flow: &file.Flow{InletFlow: 33, ExportFlow: 34}},
		{Id: 14, Client: &file.Client{Id: 99}, Flow: &file.Flow{InletFlow: 35, ExportFlow: 36}},
	}
	rangeHostsCalls := 0
	rangeTunnelsCalls := 0
	savedClientIDs := make([]int, 0, len(clients))
	savedHostIDs := make([]int, 0, len(hosts))
	savedTunnelIDs := make([]int, 0, len(tunnels))

	_, err := clearClientStatusByIDWithRepo(stubRepository{
		rangeClients: func(fn func(*file.Client) bool) {
			for _, client := range clients {
				if !fn(client) {
					break
				}
			}
		},
		rangeHosts: func(fn func(*file.Host) bool) {
			rangeHostsCalls++
			for _, host := range hosts {
				if !fn(host) {
					break
				}
			}
		},
		rangeTunnels: func(fn func(*file.Tunnel) bool) {
			rangeTunnelsCalls++
			for _, tunnel := range tunnels {
				if !fn(tunnel) {
					break
				}
			}
		},
		saveClient: func(client *file.Client) error {
			savedClientIDs = append(savedClientIDs, client.Id)
			if client.Flow == nil || client.Flow.InletFlow != 0 || client.Flow.ExportFlow != 0 {
				t.Fatalf("saved client flow = %+v, want cleared", client.Flow)
			}
			return nil
		},
		saveHost: func(host *file.Host, oldHost string) error {
			savedHostIDs = append(savedHostIDs, host.Id)
			if host.Flow == nil || host.Flow.InletFlow != 0 || host.Flow.ExportFlow != 0 {
				t.Fatalf("saved host flow = %+v, want cleared", host.Flow)
			}
			return nil
		},
		saveTunnel: func(tunnel *file.Tunnel) error {
			savedTunnelIDs = append(savedTunnelIDs, tunnel.Id)
			if tunnel.Flow == nil || tunnel.Flow.InletFlow != 0 || tunnel.Flow.ExportFlow != 0 {
				t.Fatalf("saved tunnel flow = %+v, want cleared", tunnel.Flow)
			}
			return nil
		},
	}, 0, "flow")
	if err != nil {
		t.Fatalf("clearClientStatusByIDWithRepo() error = %v", err)
	}
	if rangeHostsCalls != 1 || rangeTunnelsCalls != 1 {
		t.Fatalf("resource range calls = hosts:%d tunnels:%d, want single global pass", rangeHostsCalls, rangeTunnelsCalls)
	}
	if len(savedClientIDs) != 2 || savedClientIDs[0] != 7 || savedClientIDs[1] != 8 {
		t.Fatalf("saved client ids = %v, want [7 8]", savedClientIDs)
	}
	if len(savedHostIDs) != 2 || savedHostIDs[0] != 9 || savedHostIDs[1] != 10 {
		t.Fatalf("saved host ids = %v, want [9 10]", savedHostIDs)
	}
	if len(savedTunnelIDs) != 2 || savedTunnelIDs[0] != 12 || savedTunnelIDs[1] != 13 {
		t.Fatalf("saved tunnel ids = %v, want [12 13]", savedTunnelIDs)
	}
}
