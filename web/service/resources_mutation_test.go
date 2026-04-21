package service

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/file"
)

func generateServiceTestCertificatePair(t *testing.T, dnsNames []string, notAfter time.Time) (string, string) {
	t.Helper()

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	commonName := "service-test.example.com"
	if len(dnsNames) > 0 {
		commonName = dnsNames[0]
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: commonName,
		},
		NotBefore:             notAfter.Add(-time.Hour),
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              append([]string(nil), dnsNames...),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("CreateCertificate() error = %v", err)
	}
	cert := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	key := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)}))
	return cert, key
}

func TestDefaultIndexServiceGetTunnelReturnsWorkingCopy(t *testing.T) {
	original := &file.Tunnel{
		Id:       7,
		Mode:     "tcp",
		Port:     10080,
		Remark:   "before",
		Target:   &file.Target{TargetStr: "127.0.0.1:80"},
		UserAuth: &file.MultiAccount{Content: "demo:secret", AccountMap: map[string]string{"demo": "secret"}},
		Flow:     &file.Flow{InletFlow: 11, ExportFlow: 12, FlowLimit: 13},
		Client:   &file.Client{Id: 3, UserId: 4, VerifyKey: "vk-3", Cnf: &file.Config{}, Flow: &file.Flow{}},
	}
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				getTunnel: func(id int) (*file.Tunnel, error) {
					return original, nil
				},
			},
		},
	}

	got, err := service.GetTunnel(7)
	if err != nil {
		t.Fatalf("GetTunnel() error = %v", err)
	}
	if got == original {
		t.Fatal("GetTunnel() returned original live tunnel pointer")
	}
	got.Remark = "after"
	got.Target.TargetStr = "127.0.0.1:81"
	got.Flow.InletFlow = 99
	got.Client.VerifyKey = "mutated-vkey"
	if original.Remark != "before" || original.Target.TargetStr != "127.0.0.1:80" || original.Flow.InletFlow != 11 {
		t.Fatalf("original tunnel mutated through GetTunnel() result = %+v", original)
	}
	if original.Client == nil || original.Client.VerifyKey != "vk-3" {
		t.Fatalf("original tunnel client mutated through GetTunnel() result = %+v", original.Client)
	}
}

func TestDefaultIndexServiceGetTunnelUsesInjectedRuntimeStatus(t *testing.T) {
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				getTunnel: func(id int) (*file.Tunnel, error) {
					return &file.Tunnel{
						Id:     id,
						Mode:   "tcp",
						Port:   10080,
						Target: &file.Target{TargetStr: "127.0.0.1:80"},
						Flow:   &file.Flow{},
						Client: &file.Client{Id: 3, Cnf: &file.Config{}, Flow: &file.Flow{}},
					}, nil
				},
			},
			Runtime: stubRuntime{
				tunnelRunning: func(id int) bool {
					return id == 7
				},
			},
		},
	}

	got, err := service.GetTunnel(7)
	if err != nil {
		t.Fatalf("GetTunnel() error = %v", err)
	}
	if got == nil || !got.RunStatus {
		t.Fatalf("GetTunnel() RunStatus = %v, want true from injected runtime", got)
	}
}

func TestDefaultIndexServiceGetTunnelReusesDetachedRepositorySnapshot(t *testing.T) {
	detached := &file.Tunnel{
		Id:     17,
		Mode:   "tcp",
		Port:   10080,
		Target: &file.Target{TargetStr: "127.0.0.1:80"},
		Flow:   &file.Flow{},
		Client: &file.Client{Id: 3, Cnf: &file.Config{}, Flow: &file.Flow{}},
	}
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				detachedSnapshots: true,
				getTunnel: func(id int) (*file.Tunnel, error) {
					if id != 17 {
						t.Fatalf("GetTunnel(%d), want 17", id)
					}
					return detached, nil
				},
			},
			Runtime: stubRuntime{
				tunnelRunning: func(id int) bool {
					return id == 17
				},
			},
		},
	}

	got, err := service.GetTunnel(17)
	if err != nil {
		t.Fatalf("GetTunnel() error = %v", err)
	}
	if got != detached {
		t.Fatalf("GetTunnel() got = %p, want detached repo snapshot %p", got, detached)
	}
	if !detached.RunStatus {
		t.Fatalf("detached.RunStatus = %v, want true from injected runtime", detached.RunStatus)
	}
}

func TestDefaultIndexServiceGetTunnelPropagatesUnexpectedRepositoryError(t *testing.T) {
	errWant := errors.New("repository unavailable")
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				getTunnel: func(id int) (*file.Tunnel, error) {
					return nil, errWant
				},
			},
		},
	}

	_, err := service.GetTunnel(7)
	if !errors.Is(err, errWant) {
		t.Fatalf("GetTunnel() error = %v, want %v", err, errWant)
	}
}

func TestDefaultIndexServiceOperationsMapNilRepositoryResources(t *testing.T) {
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{},
			Runtime:    stubRuntime{},
		},
	}
	cases := []struct {
		name string
		run  func() error
		want error
	}{
		{
			name: "GetTunnel",
			run: func() error {
				_, err := service.GetTunnel(7)
				return err
			},
			want: ErrTunnelNotFound,
		},
		{
			name: "GetHost",
			run: func() error {
				_, err := service.GetHost(8)
				return err
			},
			want: ErrHostNotFound,
		},
		{
			name: "AddTunnel",
			run: func() error {
				_, err := service.AddTunnel(AddTunnelInput{
					ClientID:   3,
					Port:       10080,
					Mode:       "tcp",
					TargetType: common.CONN_TCP,
					Target:     "127.0.0.1:80",
				})
				return err
			},
			want: ErrClientNotFound,
		},
		{
			name: "AddHost",
			run: func() error {
				_, err := service.AddHost(AddHostInput{
					ClientID: 3,
					Host:     "demo.example.com",
					Target:   "127.0.0.1:80",
				})
				return err
			},
			want: ErrClientNotFound,
		},
		{
			name: "EditTunnel",
			run: func() error {
				_, err := service.EditTunnel(EditTunnelInput{
					ID:       7,
					ClientID: 3,
					Port:     10080,
					Mode:     "tcp",
					Target:   "127.0.0.1:80",
				})
				return err
			},
			want: ErrTunnelNotFound,
		},
		{
			name: "EditHost",
			run: func() error {
				_, err := service.EditHost(EditHostInput{
					ID:       8,
					ClientID: 3,
					Host:     "demo.example.com",
					Target:   "127.0.0.1:80",
				})
				return err
			},
			want: ErrHostNotFound,
		},
		{
			name: "StartHost",
			run: func() error {
				_, err := service.StartHost(8, "")
				return err
			},
			want: ErrHostNotFound,
		},
		{
			name: "StopHost",
			run: func() error {
				_, err := service.StopHost(8, "")
				return err
			},
			want: ErrHostNotFound,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, tc.want) {
				t.Fatalf("%s() error = %v, want %v", tc.name, err, tc.want)
			}
		})
	}
}

func TestDefaultIndexServiceGetHostReturnsWorkingCopy(t *testing.T) {
	original := &file.Host{
		Id:           8,
		Host:         "demo.example.com",
		Remark:       "before",
		Location:     "/api",
		Target:       &file.Target{TargetStr: "127.0.0.1:8080"},
		UserAuth:     &file.MultiAccount{Content: "demo:secret", AccountMap: map[string]string{"demo": "secret"}},
		Flow:         &file.Flow{InletFlow: 21, ExportFlow: 22, FlowLimit: 23},
		Client:       &file.Client{Id: 4, UserId: 5, VerifyKey: "vk-4", Cnf: &file.Config{}, Flow: &file.Flow{}},
		HeaderChange: "X-Test=1",
	}
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				getHost: func(id int) (*file.Host, error) {
					return original, nil
				},
			},
		},
	}

	got, err := service.GetHost(8)
	if err != nil {
		t.Fatalf("GetHost() error = %v", err)
	}
	if got == original {
		t.Fatal("GetHost() returned original live host pointer")
	}
	got.Remark = "after"
	got.Target.TargetStr = "127.0.0.1:8081"
	got.Flow.InletFlow = 99
	got.Client.VerifyKey = "mutated-vkey"
	if original.Remark != "before" || original.Target.TargetStr != "127.0.0.1:8080" || original.Flow.InletFlow != 21 {
		t.Fatalf("original host mutated through GetHost() result = %+v", original)
	}
	if original.Client == nil || original.Client.VerifyKey != "vk-4" {
		t.Fatalf("original host client mutated through GetHost() result = %+v", original.Client)
	}
}

func TestDefaultIndexServiceGetHostPropagatesUnexpectedRepositoryError(t *testing.T) {
	errWant := errors.New("repository unavailable")
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				getHost: func(id int) (*file.Host, error) {
					return nil, errWant
				},
			},
		},
	}

	_, err := service.GetHost(8)
	if !errors.Is(err, errWant) {
		t.Fatalf("GetHost() error = %v, want %v", err, errWant)
	}
}

func TestResourceStatusHelpersMapNilRepositoryResources(t *testing.T) {
	repo := stubRepository{}
	if _, err := changeTunnelStatusWithRepo(repo, nil, 7, "http", "start"); !errors.Is(err, ErrTunnelNotFound) {
		t.Fatalf("changeTunnelStatusWithRepo() error = %v, want %v", err, ErrTunnelNotFound)
	}
	if _, err := changeHostStatusWithRepo(repo, 8, "auto_ssl", "start"); !errors.Is(err, ErrHostNotFound) {
		t.Fatalf("changeHostStatusWithRepo() error = %v, want %v", err, ErrHostNotFound)
	}
	if _, err := clearClientStatusByIDWithRepo(repo, 9, "flow"); !errors.Is(err, ErrClientNotFound) {
		t.Fatalf("clearClientStatusByIDWithRepo() error = %v, want %v", err, ErrClientNotFound)
	}
}

func TestDefaultIndexServiceListTunnelsReturnsSnapshotClientCopies(t *testing.T) {
	original := &file.Tunnel{
		Id:     9,
		Mode:   "tcp",
		Port:   10090,
		Client: &file.Client{Id: 4, UserId: 5, VerifyKey: "vk-4", Cnf: &file.Config{}, Flow: &file.Flow{}},
		Target: &file.Target{TargetStr: "127.0.0.1:90"},
		Flow:   &file.Flow{},
	}
	service := DefaultIndexService{
		Backend: Backend{
			Runtime: stubRuntime{
				listTunnels: func(offset, limit int, tunnelType string, clientID int, search, sort, order string) ([]*file.Tunnel, int) {
					return []*file.Tunnel{original}, 1
				},
			},
		},
	}

	rows, count := service.ListTunnels(TunnelListInput{})
	if count != 1 || len(rows) != 1 {
		t.Fatalf("ListTunnels() count=%d rows=%d, want 1/1", count, len(rows))
	}
	if rows[0] == original {
		t.Fatal("ListTunnels() returned original live tunnel pointer")
	}
	rows[0].Client.VerifyKey = "mutated-vkey"
	if original.Client == nil || original.Client.VerifyKey != "vk-4" {
		t.Fatalf("original tunnel client mutated through ListTunnels() result = %+v", original.Client)
	}
}

func TestDefaultIndexServiceListHostsReturnsSnapshotClientCopies(t *testing.T) {
	original := &file.Host{
		Id:     10,
		Host:   "demo.example.com",
		Client: &file.Client{Id: 5, UserId: 6, VerifyKey: "vk-5", Cnf: &file.Config{}, Flow: &file.Flow{}},
		Target: &file.Target{TargetStr: "127.0.0.1:100"},
		Flow:   &file.Flow{},
	}
	service := DefaultIndexService{
		Backend: Backend{
			Runtime: stubRuntime{
				listHosts: func(offset, limit, clientID int, search, sort, order string) ([]*file.Host, int) {
					return []*file.Host{original}, 1
				},
			},
		},
	}

	rows, count := service.ListHosts(HostListInput{})
	if count != 1 || len(rows) != 1 {
		t.Fatalf("ListHosts() count=%d rows=%d, want 1/1", count, len(rows))
	}
	if rows[0] == original {
		t.Fatal("ListHosts() returned original live host pointer")
	}
	rows[0].Client.VerifyKey = "mutated-vkey"
	if original.Client == nil || original.Client.VerifyKey != "vk-5" {
		t.Fatalf("original host client mutated through ListHosts() result = %+v", original.Client)
	}
}

func TestDefaultIndexServiceModeMutationsUseInjectedRepository(t *testing.T) {
	tunnel := &file.Tunnel{
		Id:        17,
		HttpProxy: true,
		Flow:      &file.Flow{},
		Client:    &file.Client{Id: 3, Cnf: &file.Config{}, Flow: &file.Flow{}},
	}
	host := &file.Host{
		Id:       18,
		AutoSSL:  false,
		Flow:     &file.Flow{},
		Client:   &file.Client{Id: 4, Cnf: &file.Config{}, Flow: &file.Flow{}},
		CertFile: "cert.pem",
		KeyFile:  "key.pem",
	}
	var savedTunnel *file.Tunnel
	var savedHost *file.Host
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				getTunnel: func(id int) (*file.Tunnel, error) {
					if id != 17 {
						t.Fatalf("GetTunnel(%d), want 17", id)
					}
					return tunnel, nil
				},
				saveTunnel: func(current *file.Tunnel) error {
					savedTunnel = current
					return nil
				},
				getHost: func(id int) (*file.Host, error) {
					if id != 18 {
						t.Fatalf("GetHost(%d), want 18", id)
					}
					return host, nil
				},
				saveHost: func(current *file.Host, oldHost string) error {
					savedHost = current
					return nil
				},
			},
			Runtime: stubRuntime{},
		},
	}

	if _, err := service.StopTunnel(17, "http"); err != nil {
		t.Fatalf("StopTunnel(mode) error = %v", err)
	}
	if savedTunnel == nil {
		t.Fatal("StopTunnel(mode) did not save through injected repository")
	}
	if savedTunnel == tunnel {
		t.Fatal("StopTunnel(mode) saved original live tunnel pointer, want working copy")
	}
	if savedTunnel.HttpProxy {
		t.Fatalf("savedTunnel.HttpProxy = %v, want false", savedTunnel.HttpProxy)
	}
	if !tunnel.HttpProxy {
		t.Fatalf("original tunnel.HttpProxy = %v, want true", tunnel.HttpProxy)
	}
	if savedTunnel.Revision != 1 || savedTunnel.UpdatedAt <= 0 {
		t.Fatalf("savedTunnel meta = revision:%d updated_at:%d, want touched metadata", savedTunnel.Revision, savedTunnel.UpdatedAt)
	}

	if _, err := service.StartHost(18, "auto_ssl"); err != nil {
		t.Fatalf("StartHost(mode) error = %v", err)
	}
	if savedHost == nil {
		t.Fatal("StartHost(mode) did not save through injected repository")
	}
	if savedHost == host {
		t.Fatal("StartHost(mode) saved original live host pointer, want working copy")
	}
	if !savedHost.AutoSSL {
		t.Fatalf("savedHost.AutoSSL = %v, want true", savedHost.AutoSSL)
	}
	if host.AutoSSL {
		t.Fatalf("original host.AutoSSL = %v, want false", host.AutoSSL)
	}
	if savedHost.Revision != 1 || savedHost.UpdatedAt <= 0 {
		t.Fatalf("savedHost meta = revision:%d updated_at:%d, want touched metadata", savedHost.Revision, savedHost.UpdatedAt)
	}
}

func TestDefaultIndexServiceModeMutationsReuseDetachedRepositorySnapshots(t *testing.T) {
	detachedTunnel := &file.Tunnel{
		Id:        27,
		HttpProxy: true,
		Flow:      &file.Flow{},
		Client:    &file.Client{Id: 3, Cnf: &file.Config{}, Flow: &file.Flow{}},
	}
	detachedHost := &file.Host{
		Id:       28,
		AutoSSL:  false,
		Flow:     &file.Flow{},
		Client:   &file.Client{Id: 4, Cnf: &file.Config{}, Flow: &file.Flow{}},
		CertFile: "cert.pem",
		KeyFile:  "key.pem",
	}
	var savedTunnel *file.Tunnel
	var savedHost *file.Host
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				detachedSnapshots: true,
				getTunnel: func(id int) (*file.Tunnel, error) {
					if id != 27 {
						t.Fatalf("GetTunnel(%d), want 27", id)
					}
					return detachedTunnel, nil
				},
				saveTunnel: func(current *file.Tunnel) error {
					savedTunnel = current
					return nil
				},
				getHost: func(id int) (*file.Host, error) {
					if id != 28 {
						t.Fatalf("GetHost(%d), want 28", id)
					}
					return detachedHost, nil
				},
				saveHost: func(current *file.Host, oldHost string) error {
					savedHost = current
					return nil
				},
			},
			Runtime: stubRuntime{},
		},
	}

	if _, err := service.StopTunnel(27, "http"); err != nil {
		t.Fatalf("StopTunnel(mode) error = %v", err)
	}
	if savedTunnel != detachedTunnel {
		t.Fatalf("StopTunnel(mode) saved = %p, want detached repo snapshot %p", savedTunnel, detachedTunnel)
	}
	if detachedTunnel.HttpProxy {
		t.Fatalf("detachedTunnel.HttpProxy = %v, want false after in-place detached mutation", detachedTunnel.HttpProxy)
	}
	if detachedTunnel.Revision != 1 || detachedTunnel.UpdatedAt <= 0 {
		t.Fatalf("detachedTunnel meta = revision:%d updated_at:%d, want touched metadata", detachedTunnel.Revision, detachedTunnel.UpdatedAt)
	}

	if _, err := service.StartHost(28, "auto_ssl"); err != nil {
		t.Fatalf("StartHost(mode) error = %v", err)
	}
	if savedHost != detachedHost {
		t.Fatalf("StartHost(mode) saved = %p, want detached repo snapshot %p", savedHost, detachedHost)
	}
	if !detachedHost.AutoSSL {
		t.Fatalf("detachedHost.AutoSSL = %v, want true after in-place detached mutation", detachedHost.AutoSSL)
	}
	if detachedHost.Revision != 1 || detachedHost.UpdatedAt <= 0 {
		t.Fatalf("detachedHost meta = revision:%d updated_at:%d, want touched metadata", detachedHost.Revision, detachedHost.UpdatedAt)
	}
}

func TestDefaultIndexServiceAddTunnelReturnsRollbackFailureWhenRuntimeStartCleanupFails(t *testing.T) {
	runtimeErr := errors.New("the port open error")
	rollbackErr := errors.New("delete tunnel failed")
	client := &file.Client{Id: 3, Cnf: &file.Config{}, Flow: &file.Flow{}}
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				nextTunnelID: func() int { return 17 },
				getClient: func(id int) (*file.Client, error) {
					if id != client.Id {
						t.Fatalf("GetClient(%d), want %d", id, client.Id)
					}
					return client, nil
				},
				createTunnel: func(tunnel *file.Tunnel) error {
					if tunnel == nil || tunnel.Id != 17 {
						t.Fatalf("CreateTunnel() tunnel = %+v, want id 17", tunnel)
					}
					return nil
				},
				deleteTunnelRecord: func(id int) error {
					if id != 17 {
						t.Fatalf("DeleteTunnelRecord(%d), want 17", id)
					}
					return rollbackErr
				},
			},
			Runtime: stubRuntime{
				addTunnel: func(tunnel *file.Tunnel) error {
					if tunnel == nil || tunnel.Id != 17 {
						t.Fatalf("AddTunnel() tunnel = %+v, want id 17", tunnel)
					}
					return runtimeErr
				},
			},
		},
	}

	_, err := service.AddTunnel(AddTunnelInput{
		ClientID:   client.Id,
		Port:       10080,
		Mode:       "tcp",
		TargetType: common.CONN_TCP,
		Target:     "127.0.0.1:80",
	})
	if !errors.Is(err, ErrPortUnavailable) {
		t.Fatalf("AddTunnel() error = %v, want %v", err, ErrPortUnavailable)
	}
	if !errors.Is(err, rollbackErr) {
		t.Fatalf("AddTunnel() error = %v, want rollback failure", err)
	}
}

func TestDefaultIndexServiceAddTunnelPropagatesUnexpectedClientLookupError(t *testing.T) {
	errWant := errors.New("repository unavailable")
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				nextTunnelID: func() int { return 17 },
				getClient: func(id int) (*file.Client, error) {
					return nil, errWant
				},
			},
			Runtime: stubRuntime{},
		},
	}

	_, err := service.AddTunnel(AddTunnelInput{
		ClientID:   3,
		Port:       10080,
		Mode:       "tcp",
		TargetType: common.CONN_TCP,
		Target:     "127.0.0.1:80",
	})
	if !errors.Is(err, errWant) {
		t.Fatalf("AddTunnel() error = %v, want %v", err, errWant)
	}
}

func TestDefaultIndexServiceAddTunnelClearsDestinationACLRulesWhenDisabled(t *testing.T) {
	client := &file.Client{Id: 3, Cnf: &file.Config{}, Flow: &file.Flow{}}
	var created *file.Tunnel
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				nextTunnelID: func() int { return 17 },
				getClient: func(id int) (*file.Client, error) {
					if id != client.Id {
						t.Fatalf("GetClient(%d), want %d", id, client.Id)
					}
					return client, nil
				},
				createTunnel: func(tunnel *file.Tunnel) error {
					created = tunnel
					return nil
				},
			},
			Runtime: stubRuntime{
				addTunnel: func(*file.Tunnel) error { return nil },
			},
		},
	}

	_, err := service.AddTunnel(AddTunnelInput{
		ClientID:     client.Id,
		Port:         10080,
		Mode:         "tcp",
		TargetType:   common.CONN_TCP,
		Target:       "127.0.0.1:80",
		DestACLMode:  file.AclOff,
		DestACLRules: "full:db.internal.example",
	})
	if err != nil {
		t.Fatalf("AddTunnel() error = %v", err)
	}
	if created == nil {
		t.Fatal("AddTunnel() did not persist tunnel")
	}
	if created.DestAclMode != file.AclOff || created.DestAclRules != "" {
		t.Fatalf("created destination acl = (%d, %q), want (%d, %q)", created.DestAclMode, created.DestAclRules, file.AclOff, "")
	}
}

func TestDefaultIndexServiceStartStopHostUseWorkingCopies(t *testing.T) {
	original := &file.Host{
		Id:      19,
		IsClose: true,
		Flow:    &file.Flow{},
		Client:  &file.Client{Id: 4, Cnf: &file.Config{}, Flow: &file.Flow{}},
		Target:  &file.Target{TargetStr: "127.0.0.1:8080"},
	}
	var saved *file.Host
	var removed []int
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				getHost: func(id int) (*file.Host, error) {
					if id != 19 {
						t.Fatalf("GetHost(%d), want 19", id)
					}
					return original, nil
				},
				saveHost: func(current *file.Host, oldHost string) error {
					saved = current
					return nil
				},
			},
			Runtime: stubRuntime{
				removeHostCache: func(id int) {
					removed = append(removed, id)
				},
			},
		},
	}

	if _, err := service.StartHost(19, ""); err != nil {
		t.Fatalf("StartHost() error = %v", err)
	}
	if saved == nil || saved == original {
		t.Fatalf("StartHost() saved = %#v, want working copy", saved)
	}
	if saved.IsClose {
		t.Fatalf("saved.IsClose = %v, want false", saved.IsClose)
	}
	if !original.IsClose {
		t.Fatalf("original.IsClose = %v, want true", original.IsClose)
	}
	if saved.Revision != 1 || saved.UpdatedAt <= 0 {
		t.Fatalf("saved start meta = revision:%d updated_at:%d, want touched metadata", saved.Revision, saved.UpdatedAt)
	}
	if len(removed) != 1 || removed[0] != 19 {
		t.Fatalf("StartHost() removed cache ids = %v, want [19]", removed)
	}

	saved = nil
	if _, err := service.StopHost(19, ""); err != nil {
		t.Fatalf("StopHost() error = %v", err)
	}
	if saved == nil || saved == original {
		t.Fatalf("StopHost() saved = %#v, want working copy", saved)
	}
	if !saved.IsClose {
		t.Fatalf("saved.IsClose = %v, want true", saved.IsClose)
	}
	if !original.IsClose {
		t.Fatalf("original.IsClose = %v, want unchanged true", original.IsClose)
	}
	if saved.Revision != 1 || saved.UpdatedAt <= 0 {
		t.Fatalf("saved stop meta = revision:%d updated_at:%d, want touched metadata", saved.Revision, saved.UpdatedAt)
	}
	if len(removed) != 2 || removed[1] != 19 {
		t.Fatalf("StopHost() removed cache ids = %v, want [19 19]", removed)
	}
}

func TestDefaultIndexServiceEditHostDoesNotMutateLiveHostOnSaveError(t *testing.T) {
	originalClient := &file.Client{Id: 8, UserId: 9, Cnf: &file.Config{}, Flow: &file.Flow{}}
	original := &file.Host{
		Id:               28,
		Host:             "demo.example.com",
		HeaderChange:     "X-Test=1",
		RespHeaderChange: "X-Resp=1",
		Remark:           "before",
		Location:         "/api",
		PathRewrite:      "/v1",
		RedirectURL:      "https://demo.example.com",
		Scheme:           "https",
		CertFile:         "cert-before",
		KeyFile:          "key-before",
		RateLimit:        5,
		MaxConn:          6,
		TargetIsHttps:    false,
		Target:           &file.Target{TargetStr: "127.0.0.1:8080"},
		UserAuth:         &file.MultiAccount{Content: "demo:secret", AccountMap: map[string]string{"demo": "secret"}},
		Flow:             &file.Flow{InletFlow: 21, ExportFlow: 22, FlowLimit: 23},
		Client:           originalClient,
	}
	removedID := 0
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				getHost: func(id int) (*file.Host, error) {
					if id != 28 {
						t.Fatalf("GetHost(%d), want 28", id)
					}
					return original, nil
				},
				getClient: func(id int) (*file.Client, error) {
					if id != originalClient.Id {
						t.Fatalf("GetClient(%d), want %d", id, originalClient.Id)
					}
					return originalClient, nil
				},
				saveHost: func(*file.Host, string) error {
					return errors.New("save failed")
				},
			},
			Runtime: stubRuntime{
				removeHostCache: func(id int) {
					removedID = id
				},
			},
		},
	}

	_, err := service.EditHost(EditHostInput{
		ID:             28,
		ClientID:       originalClient.Id,
		Host:           "api.example.com",
		Target:         "127.0.0.1:9090",
		Header:         "X-Test=2",
		RespHeader:     "X-Resp=2",
		Auth:           "demo:new-secret",
		Remark:         "after",
		Location:       "/v2",
		PathRewrite:    "/v3",
		RedirectURL:    "https://api.example.com",
		Scheme:         "http",
		CertFile:       "cert-after",
		KeyFile:        "key-after",
		RateLimit:      10,
		MaxConnections: 11,
		ResetFlow:      true,
		TargetIsHTTPS:  true,
	})
	if err == nil || err.Error() != "save failed" {
		t.Fatalf("EditHost() error = %v, want save failed", err)
	}
	if original.Host != "demo.example.com" || original.Remark != "before" || original.Location != "/api" {
		t.Fatalf("original host mutated = %+v", original)
	}
	if original.Target == nil || original.Target.TargetStr != "127.0.0.1:8080" {
		t.Fatalf("original target mutated = %+v", original.Target)
	}
	if original.UserAuth == nil || original.UserAuth.Content != "demo:secret" {
		t.Fatalf("original user auth mutated = %+v", original.UserAuth)
	}
	if original.Flow == nil || original.Flow.InletFlow != 21 || original.Flow.ExportFlow != 22 || original.Flow.FlowLimit != 23 {
		t.Fatalf("original flow mutated = %+v", original.Flow)
	}
	if original.RateLimit != 5 || original.MaxConn != 6 || original.TargetIsHttps {
		t.Fatalf("original limits mutated = rate=%d max_conn=%d target_https=%v", original.RateLimit, original.MaxConn, original.TargetIsHttps)
	}
	if removedID != 0 {
		t.Fatalf("RemoveHostCache() id = %d, want 0 on save failure", removedID)
	}
}

func TestDefaultIndexServiceEditHostPropagatesUnexpectedClientLookupError(t *testing.T) {
	errWant := errors.New("repository unavailable")
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				getHost: func(id int) (*file.Host, error) {
					return &file.Host{
						Id:     id,
						Host:   "demo.example.com",
						Scheme: "https",
						Client: &file.Client{Id: 2, Flow: &file.Flow{}},
						Flow:   &file.Flow{},
						Target: &file.Target{TargetStr: "127.0.0.1:8080"},
					}, nil
				},
				getClient: func(id int) (*file.Client, error) {
					return nil, errWant
				},
			},
			Runtime: stubRuntime{},
		},
	}

	_, err := service.EditHost(EditHostInput{
		ID:       88,
		ClientID: 2,
		Host:     "api.example.com",
		Target:   "127.0.0.1:8081",
		Scheme:   "https",
	})
	if !errors.Is(err, errWant) {
		t.Fatalf("EditHost() error = %v, want %v", err, errWant)
	}
}

func TestDefaultIndexServiceEditTunnelPropagatesUnexpectedClientLookupError(t *testing.T) {
	errWant := errors.New("repository unavailable")
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				getTunnel: func(id int) (*file.Tunnel, error) {
					return &file.Tunnel{
						Id:     id,
						Port:   18080,
						Mode:   "tcp",
						Client: &file.Client{Id: 1, Flow: &file.Flow{}},
						Flow:   &file.Flow{},
						Target: &file.Target{TargetStr: "127.0.0.1:8080"},
					}, nil
				},
				getClient: func(id int) (*file.Client, error) {
					return nil, errWant
				},
			},
			Runtime: stubRuntime{},
		},
	}

	_, err := service.EditTunnel(EditTunnelInput{
		ID:       88,
		ClientID: 1,
		Port:     18080,
		Mode:     "tcp",
		Target:   "127.0.0.1:8081",
	})
	if !errors.Is(err, errWant) {
		t.Fatalf("EditTunnel() error = %v, want %v", err, errWant)
	}
}

func TestDefaultIndexServiceAddHostPropagatesUnexpectedClientLookupError(t *testing.T) {
	errWant := errors.New("repository unavailable")
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				nextHostID: func() int { return 11 },
				getClient: func(id int) (*file.Client, error) {
					return nil, errWant
				},
			},
			Runtime: stubRuntime{},
		},
	}

	_, err := service.AddHost(AddHostInput{
		ClientID: 3,
		Host:     "demo.example.com",
		Target:   "127.0.0.1:8080",
	})
	if !errors.Is(err, errWant) {
		t.Fatalf("AddHost() error = %v, want %v", err, errWant)
	}
}

func TestDefaultIndexServiceDeleteTunnelDeletesRepositoryAfterRuntimeCleanup(t *testing.T) {
	calls := make([]string, 0, 2)
	original := &file.Tunnel{
		Id:        77,
		Mode:      "tcp",
		RunStatus: true,
		NowConn:   4,
		Flow:      &file.Flow{},
		Client:    &file.Client{Id: 8, Cnf: &file.Config{}, Flow: &file.Flow{}},
		Target:    &file.Target{TargetStr: "127.0.0.1:8080"},
	}
	service := DefaultIndexService{
		Backend: Backend{
			Runtime: stubRuntime{
				deleteTunnel: func(id int) error {
					if id != 77 {
						t.Fatalf("DeleteTunnel(%d), want 77", id)
					}
					calls = append(calls, "runtime")
					return nil
				},
			},
			Repository: stubRepository{
				getTunnel: func(id int) (*file.Tunnel, error) {
					if id != 77 {
						t.Fatalf("GetTunnel(%d), want 77", id)
					}
					return original, nil
				},
				deleteTunnelRecord: func(id int) error {
					if id != 77 {
						t.Fatalf("DeleteTunnelRecord(%d), want 77", id)
					}
					calls = append(calls, "repo")
					return nil
				},
			},
		},
	}

	result, err := service.DeleteTunnel(77)
	if err != nil {
		t.Fatalf("DeleteTunnel() error = %v", err)
	}
	if len(calls) != 2 || calls[0] != "runtime" || calls[1] != "repo" {
		t.Fatalf("DeleteTunnel() call order = %v, want [runtime repo]", calls)
	}
	if result.ID != 77 || result.Tunnel == nil {
		t.Fatalf("DeleteTunnel() result = %+v, want tunnel snapshot id 77", result)
	}
	if result.Tunnel.RunStatus || result.Tunnel.NowConn != 0 {
		t.Fatalf("DeleteTunnel() returned runtime state = run:%v now_conn:%d, want false/0", result.Tunnel.RunStatus, result.Tunnel.NowConn)
	}
	if !original.RunStatus || original.NowConn != 4 {
		t.Fatalf("DeleteTunnel() mutated original tunnel = %+v", original)
	}
}

func TestDefaultIndexServiceDeleteTunnelDoesNotDeleteRepositoryWhenRuntimeCleanupFails(t *testing.T) {
	calls := make([]string, 0, 2)
	runtimeErr := errors.New("close tunnel runtime failed")
	service := DefaultIndexService{
		Backend: Backend{
			Runtime: stubRuntime{
				deleteTunnel: func(id int) error {
					if id != 77 {
						t.Fatalf("DeleteTunnel(%d), want 77", id)
					}
					calls = append(calls, "runtime")
					return runtimeErr
				},
			},
			Repository: stubRepository{
				deleteTunnelRecord: func(id int) error {
					calls = append(calls, "repo")
					return nil
				},
			},
		},
	}

	if _, err := service.DeleteTunnel(77); !errors.Is(err, runtimeErr) {
		t.Fatalf("DeleteTunnel() error = %v, want %v", err, runtimeErr)
	}
	if len(calls) != 1 || calls[0] != "runtime" {
		t.Fatalf("DeleteTunnel() call order = %v, want [runtime]", calls)
	}
}

func TestDefaultIndexServiceDeleteTunnelMapsMissingRepositoryRecord(t *testing.T) {
	service := DefaultIndexService{
		Backend: Backend{
			Runtime: stubRuntime{
				deleteTunnel: func(id int) error {
					if id != 77 {
						t.Fatalf("DeleteTunnel(%d), want 77", id)
					}
					return nil
				},
			},
			Repository: stubRepository{
				deleteTunnelRecord: func(id int) error {
					if id != 77 {
						t.Fatalf("DeleteTunnelRecord(%d), want 77", id)
					}
					return file.ErrTaskNotFound
				},
			},
		},
	}

	if _, err := service.DeleteTunnel(77); !errors.Is(err, ErrTunnelNotFound) {
		t.Fatalf("DeleteTunnel() error = %v, want %v", err, ErrTunnelNotFound)
	}
}

func TestDefaultIndexServiceDeleteTunnelDeletesStoppedTunnelRecord(t *testing.T) {
	deleted := 0
	service := DefaultIndexService{
		Backend: Backend{
			Runtime: stubRuntime{
				deleteTunnel: func(id int) error {
					return errors.New("task is not running")
				},
			},
			Repository: stubRepository{
				deleteTunnelRecord: func(id int) error {
					deleted = id
					return nil
				},
			},
		},
	}

	if _, err := service.DeleteTunnel(77); err != nil {
		t.Fatalf("DeleteTunnel() error = %v", err)
	}
	if deleted != 77 {
		t.Fatalf("DeleteTunnelRecord() id = %d, want 77", deleted)
	}
}

func TestDefaultIndexServiceStartTunnelMapsMissingRuntimeTask(t *testing.T) {
	service := DefaultIndexService{
		Backend: Backend{
			Runtime: stubRuntime{
				startTunnel: func(id int) error {
					if id != 77 {
						t.Fatalf("StartTunnel(%d), want 77", id)
					}
					return file.ErrTaskNotFound
				},
			},
		},
	}

	if _, err := service.StartTunnel(77, ""); !errors.Is(err, ErrTunnelNotFound) {
		t.Fatalf("StartTunnel() error = %v, want %v", err, ErrTunnelNotFound)
	}
}

func TestDefaultIndexServiceStopTunnelMapsMissingRuntimeTask(t *testing.T) {
	service := DefaultIndexService{
		Backend: Backend{
			Runtime: stubRuntime{
				stopTunnel: func(id int) error {
					if id != 77 {
						t.Fatalf("StopTunnel(%d), want 77", id)
					}
					return file.ErrTaskNotFound
				},
			},
		},
	}

	if _, err := service.StopTunnel(77, ""); !errors.Is(err, ErrTunnelNotFound) {
		t.Fatalf("StopTunnel() error = %v, want %v", err, ErrTunnelNotFound)
	}
}

func TestDefaultIndexServiceStartTunnelModeMapsMissingRepositoryTunnel(t *testing.T) {
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				getTunnel: func(id int) (*file.Tunnel, error) {
					if id != 77 {
						t.Fatalf("GetTunnel(%d), want 77", id)
					}
					return nil, file.ErrTaskNotFound
				},
			},
		},
	}

	if _, err := service.StartTunnel(77, "http"); !errors.Is(err, ErrTunnelNotFound) {
		t.Fatalf("StartTunnel(mode) error = %v, want %v", err, ErrTunnelNotFound)
	}
}

func TestDefaultIndexServiceClearTunnelModeMapsMissingRepositoryTunnelOnSave(t *testing.T) {
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				getTunnel: func(id int) (*file.Tunnel, error) {
					return &file.Tunnel{
						Id:        id,
						HttpProxy: true,
						Flow:      &file.Flow{},
						Client:    &file.Client{Id: 3, Cnf: &file.Config{}, Flow: &file.Flow{}},
					}, nil
				},
				saveTunnel: func(current *file.Tunnel) error {
					return file.ErrTaskNotFound
				},
			},
		},
	}

	if _, err := service.ClearTunnel(77, "http"); !errors.Is(err, ErrTunnelNotFound) {
		t.Fatalf("ClearTunnel(mode) error = %v, want %v", err, ErrTunnelNotFound)
	}
}

func TestDefaultIndexServiceDeleteHostMapsMissingRepositoryHost(t *testing.T) {
	removedID := 0
	service := DefaultIndexService{
		Backend: Backend{
			Runtime: stubRuntime{
				removeHostCache: func(id int) {
					removedID = id
				},
			},
			Repository: stubRepository{
				deleteHostRecord: func(id int) error {
					if id != 88 {
						t.Fatalf("DeleteHostRecord(%d), want 88", id)
					}
					return file.ErrHostNotFound
				},
			},
		},
	}

	if _, err := service.DeleteHost(88); !errors.Is(err, ErrHostNotFound) {
		t.Fatalf("DeleteHost() error = %v, want %v", err, ErrHostNotFound)
	}
	if removedID != 0 {
		t.Fatalf("RemoveHostCache() id = %d, want 0 on delete failure", removedID)
	}
}

func TestDefaultIndexServiceDeleteHostReturnsDeletedSnapshot(t *testing.T) {
	original := &file.Host{
		Id:      88,
		Host:    "demo.example.com",
		NowConn: 2,
		Flow:    &file.Flow{},
		Client:  &file.Client{Id: 9, Cnf: &file.Config{}, Flow: &file.Flow{}},
		Target:  &file.Target{TargetStr: "127.0.0.1:8080"},
	}
	removedID := 0
	service := DefaultIndexService{
		Backend: Backend{
			Runtime: stubRuntime{
				removeHostCache: func(id int) {
					removedID = id
				},
			},
			Repository: stubRepository{
				getHost: func(id int) (*file.Host, error) {
					if id != 88 {
						t.Fatalf("GetHost(%d), want 88", id)
					}
					return original, nil
				},
				deleteHostRecord: func(id int) error {
					if id != 88 {
						t.Fatalf("DeleteHostRecord(%d), want 88", id)
					}
					return nil
				},
			},
		},
	}

	result, err := service.DeleteHost(88)
	if err != nil {
		t.Fatalf("DeleteHost() error = %v", err)
	}
	if removedID != 88 {
		t.Fatalf("RemoveHostCache() id = %d, want 88", removedID)
	}
	if result.ID != 88 || result.Host == nil {
		t.Fatalf("DeleteHost() result = %+v, want host snapshot id 88", result)
	}
	if result.Host.NowConn != 0 {
		t.Fatalf("DeleteHost() returned now_conn = %d, want 0", result.Host.NowConn)
	}
	if original.NowConn != 2 {
		t.Fatalf("DeleteHost() mutated original host = %+v", original)
	}
}

func TestDefaultIndexServiceEditTunnelPreservesCurrentClientWhenInputOmitsClientID(t *testing.T) {
	currentClient := &file.Client{Id: 7, UserId: 8, VerifyKey: "vk-7", Cnf: &file.Config{}, Flow: &file.Flow{}}
	current := &file.Tunnel{
		Id:     20,
		Port:   1234,
		Mode:   "tcp",
		Flow:   &file.Flow{},
		Client: currentClient,
		Target: &file.Target{TargetStr: "127.0.0.1:8080"},
	}
	var saved *file.Tunnel
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				getTunnel: func(id int) (*file.Tunnel, error) {
					if id != current.Id {
						t.Fatalf("GetTunnel(%d), want %d", id, current.Id)
					}
					return current, nil
				},
				getClient: func(id int) (*file.Client, error) {
					if id != currentClient.Id {
						t.Fatalf("GetClient(%d), want %d", id, currentClient.Id)
					}
					return currentClient, nil
				},
				saveTunnel: func(tunnel *file.Tunnel) error {
					saved = cloneTunnelForMutation(tunnel)
					return nil
				},
			},
			Runtime: stubRuntime{
				stopTunnel: func(int) error { return errors.New("task is not running") },
				startTunnel: func(id int) error {
					if id != current.Id {
						t.Fatalf("StartTunnel(%d), want %d", id, current.Id)
					}
					return nil
				},
			},
		},
	}

	result, err := service.EditTunnel(EditTunnelInput{
		ID:       current.Id,
		ClientID: 0,
		Port:     current.Port,
		Mode:     current.Mode,
		Target:   current.Target.TargetStr,
	})
	if err != nil {
		t.Fatalf("EditTunnel() error = %v", err)
	}
	if result.ID != current.Id || result.ClientID != currentClient.Id {
		t.Fatalf("EditTunnel() result = %+v, want current tunnel/client ids", result)
	}
	if saved == nil || saved.Client == nil || saved.Client.Id != currentClient.Id {
		t.Fatalf("saved tunnel = %+v, want current client preserved", saved)
	}
}

func TestDefaultIndexServiceStartHostModeMapsMissingRepositoryHost(t *testing.T) {
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				getHost: func(id int) (*file.Host, error) {
					if id != 88 {
						t.Fatalf("GetHost(%d), want 88", id)
					}
					return nil, file.ErrHostNotFound
				},
			},
		},
	}

	if _, err := service.StartHost(88, "auto_ssl"); !errors.Is(err, ErrHostNotFound) {
		t.Fatalf("StartHost(mode) error = %v, want %v", err, ErrHostNotFound)
	}
}

func TestDefaultIndexServiceStartHostMapsMissingRepositoryHostOnSave(t *testing.T) {
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				getHost: func(id int) (*file.Host, error) {
					return &file.Host{
						Id:      id,
						IsClose: true,
						Flow:    &file.Flow{},
						Client:  &file.Client{Id: 4, Cnf: &file.Config{}, Flow: &file.Flow{}},
						Target:  &file.Target{TargetStr: "127.0.0.1:8080"},
					}, nil
				},
				saveHost: func(current *file.Host, oldHost string) error {
					return file.ErrHostNotFound
				},
			},
		},
	}

	if _, err := service.StartHost(88, ""); !errors.Is(err, ErrHostNotFound) {
		t.Fatalf("StartHost() error = %v, want %v", err, ErrHostNotFound)
	}
}

func TestDefaultIndexServiceStartHostPropagatesUnexpectedRepositoryError(t *testing.T) {
	errWant := errors.New("repository unavailable")
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				getHost: func(id int) (*file.Host, error) {
					return nil, errWant
				},
			},
		},
	}

	if _, err := service.StartHost(88, ""); !errors.Is(err, errWant) {
		t.Fatalf("StartHost() error = %v, want %v", err, errWant)
	}
}

func TestDefaultIndexServiceStopHostMapsMissingRepositoryHostOnSave(t *testing.T) {
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				getHost: func(id int) (*file.Host, error) {
					return &file.Host{
						Id:      id,
						IsClose: false,
						Flow:    &file.Flow{},
						Client:  &file.Client{Id: 4, Cnf: &file.Config{}, Flow: &file.Flow{}},
						Target:  &file.Target{TargetStr: "127.0.0.1:8080"},
					}, nil
				},
				saveHost: func(current *file.Host, oldHost string) error {
					return file.ErrHostNotFound
				},
			},
		},
	}

	if _, err := service.StopHost(88, ""); !errors.Is(err, ErrHostNotFound) {
		t.Fatalf("StopHost() error = %v, want %v", err, ErrHostNotFound)
	}
}

func TestDefaultIndexServiceClearHostModeMapsMissingRepositoryHostOnSave(t *testing.T) {
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				getHost: func(id int) (*file.Host, error) {
					return &file.Host{
						Id:      id,
						AutoSSL: true,
						Flow:    &file.Flow{},
						Client:  &file.Client{Id: 4, Cnf: &file.Config{}, Flow: &file.Flow{}},
						Target:  &file.Target{TargetStr: "127.0.0.1:8080"},
					}, nil
				},
				saveHost: func(current *file.Host, oldHost string) error {
					return file.ErrHostNotFound
				},
			},
		},
	}

	if _, err := service.ClearHost(88, "auto_ssl"); !errors.Is(err, ErrHostNotFound) {
		t.Fatalf("ClearHost(mode) error = %v, want %v", err, ErrHostNotFound)
	}
}

func TestDefaultIndexServiceEditTunnelMapsMissingRepositoryTunnelOnSave(t *testing.T) {
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				getTunnel: func(id int) (*file.Tunnel, error) {
					return &file.Tunnel{
						Id:     id,
						Port:   18080,
						Mode:   "tcp",
						Client: &file.Client{Id: 1, Flow: &file.Flow{}},
						Flow:   &file.Flow{},
						Target: &file.Target{TargetStr: "127.0.0.1:8080"},
					}, nil
				},
				getClient: func(id int) (*file.Client, error) {
					return &file.Client{Id: id, Flow: &file.Flow{}}, nil
				},
				saveTunnel: func(current *file.Tunnel) error {
					return file.ErrTaskNotFound
				},
			},
			Runtime: stubRuntime{},
		},
	}

	_, err := service.EditTunnel(EditTunnelInput{
		ID:       88,
		ClientID: 1,
		Port:     18080,
		Mode:     "tcp",
		Target:   "127.0.0.1:8081",
	})
	if !errors.Is(err, ErrTunnelNotFound) {
		t.Fatalf("EditTunnel() error = %v, want %v", err, ErrTunnelNotFound)
	}
}

func TestDefaultIndexServiceEditTunnelMapsMissingRuntimeTaskOnStop(t *testing.T) {
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				getTunnel: func(id int) (*file.Tunnel, error) {
					return &file.Tunnel{
						Id:     id,
						Port:   18080,
						Mode:   "tcp",
						Client: &file.Client{Id: 1, Flow: &file.Flow{}},
						Flow:   &file.Flow{},
						Target: &file.Target{TargetStr: "127.0.0.1:8080"},
					}, nil
				},
				getClient: func(id int) (*file.Client, error) {
					return &file.Client{Id: id, Flow: &file.Flow{}}, nil
				},
				saveTunnel: func(current *file.Tunnel) error {
					return nil
				},
			},
			Runtime: stubRuntime{
				stopTunnel: func(id int) error {
					return file.ErrTaskNotFound
				},
			},
		},
	}

	_, err := service.EditTunnel(EditTunnelInput{
		ID:       88,
		ClientID: 1,
		Port:     18080,
		Mode:     "tcp",
		Target:   "127.0.0.1:8081",
	})
	if !errors.Is(err, ErrTunnelNotFound) {
		t.Fatalf("EditTunnel() error = %v, want %v", err, ErrTunnelNotFound)
	}
}

func TestDefaultIndexServiceEditTunnelMapsMissingRuntimeTaskOnStart(t *testing.T) {
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				getTunnel: func(id int) (*file.Tunnel, error) {
					return &file.Tunnel{
						Id:     id,
						Port:   18080,
						Mode:   "tcp",
						Client: &file.Client{Id: 1, Flow: &file.Flow{}},
						Flow:   &file.Flow{},
						Target: &file.Target{TargetStr: "127.0.0.1:8080"},
					}, nil
				},
				getClient: func(id int) (*file.Client, error) {
					return &file.Client{Id: id, Flow: &file.Flow{}}, nil
				},
				saveTunnel: func(current *file.Tunnel) error {
					return nil
				},
			},
			Runtime: stubRuntime{
				stopTunnel: func(id int) error {
					return errors.New("task is not running")
				},
				startTunnel: func(id int) error {
					return file.ErrTaskNotFound
				},
			},
		},
	}

	_, err := service.EditTunnel(EditTunnelInput{
		ID:       88,
		ClientID: 1,
		Port:     18080,
		Mode:     "tcp",
		Target:   "127.0.0.1:8081",
	})
	if !errors.Is(err, ErrTunnelNotFound) {
		t.Fatalf("EditTunnel() error = %v, want %v", err, ErrTunnelNotFound)
	}
}

func TestDefaultIndexServiceEditHostMapsMissingRepositoryHostOnSave(t *testing.T) {
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				getHost: func(id int) (*file.Host, error) {
					return &file.Host{
						Id:     id,
						Host:   "demo.example.com",
						Scheme: "https",
						Client: &file.Client{Id: 2, Flow: &file.Flow{}},
						Flow:   &file.Flow{},
						Target: &file.Target{TargetStr: "127.0.0.1:8080"},
					}, nil
				},
				getClient: func(id int) (*file.Client, error) {
					return &file.Client{Id: id, Flow: &file.Flow{}}, nil
				},
				saveHost: func(current *file.Host, oldHost string) error {
					return file.ErrHostNotFound
				},
			},
			Runtime: stubRuntime{},
		},
	}

	_, err := service.EditHost(EditHostInput{
		ID:       88,
		ClientID: 2,
		Host:     "api.example.com",
		Target:   "127.0.0.1:8081",
		Scheme:   "https",
	})
	if !errors.Is(err, ErrHostNotFound) {
		t.Fatalf("EditHost() error = %v, want %v", err, ErrHostNotFound)
	}
}

func TestDefaultIndexServiceEditHostPropagatesUnexpectedRepositoryError(t *testing.T) {
	errWant := errors.New("repository unavailable")
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				getHost: func(id int) (*file.Host, error) {
					return nil, errWant
				},
			},
			Runtime: stubRuntime{},
		},
	}

	_, err := service.EditHost(EditHostInput{
		ID:       88,
		ClientID: 2,
		Host:     "api.example.com",
		Target:   "127.0.0.1:8081",
		Scheme:   "https",
	})
	if !errors.Is(err, errWant) {
		t.Fatalf("EditHost() error = %v, want %v", err, errWant)
	}
}

func TestDefaultIndexServiceEditHostSyncsMatchingTextCertificates(t *testing.T) {
	currentCert, currentKey := generateServiceTestCertificatePair(t, []string{"*.example.com"}, time.Now().Add(2*time.Hour))

	currentClient := &file.Client{Id: 7, UserId: 8, VerifyKey: "vk-7", Cnf: &file.Config{}, Flow: &file.Flow{}}
	current := &file.Host{
		Id:       20,
		Host:     "alpha.example.com",
		Scheme:   "https",
		CertType: "text",
		CertFile: currentCert,
		KeyFile:  currentKey,
		Flow:     &file.Flow{},
		Client:   currentClient,
		Target:   &file.Target{TargetStr: "127.0.0.1:8080"},
	}
	matchedExact := &file.Host{
		Id:       21,
		Host:     "beta.example.com",
		Scheme:   "https",
		Location: "/api",
		CertType: "empty",
		Flow:     &file.Flow{},
		Client:   currentClient,
		Target:   &file.Target{TargetStr: "127.0.0.1:8081"},
	}
	matchedWildcard := &file.Host{
		Id:     22,
		Host:   "*.example.com",
		Scheme: "all",
		Flow:   &file.Flow{},
		Client: currentClient,
		Target: &file.Target{TargetStr: "127.0.0.1:8082"},
	}
	unmatchedDeep := &file.Host{
		Id:     23,
		Host:   "deep.api.example.com",
		Scheme: "https",
		Flow:   &file.Flow{},
		Client: currentClient,
		Target: &file.Target{TargetStr: "127.0.0.1:8083"},
	}
	matchedAutoSSL := &file.Host{
		Id:      24,
		Host:    "api.example.com",
		Scheme:  "https",
		AutoSSL: true,
		Flow:    &file.Flow{},
		Client:  currentClient,
		Target:  &file.Target{TargetStr: "127.0.0.1:8084"},
	}
	outOfScope := &file.Host{
		Id:     25,
		Host:   "ops.example.com",
		Scheme: "https",
		Flow:   &file.Flow{},
		Client: currentClient,
		Target: &file.Target{TargetStr: "127.0.0.1:8085"},
	}
	hostsByID := map[int]*file.Host{
		current.Id:         current,
		matchedExact.Id:    matchedExact,
		matchedWildcard.Id: matchedWildcard,
		unmatchedDeep.Id:   unmatchedDeep,
		matchedAutoSSL.Id:  matchedAutoSSL,
		outOfScope.Id:      outOfScope,
	}

	saved := make(map[int]*file.Host)
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				getHost: func(id int) (*file.Host, error) {
					host, ok := hostsByID[id]
					if !ok {
						t.Fatalf("GetHost(%d) unexpected lookup", id)
					}
					return host, nil
				},
				getClient: func(id int) (*file.Client, error) {
					if id != currentClient.Id {
						t.Fatalf("GetClient(%d), want %d", id, currentClient.Id)
					}
					return currentClient, nil
				},
				rangeHosts: func(func(*file.Host) bool) {
					t.Fatal("RangeHosts() should not be used when SyncHostIDs can be resolved directly")
				},
				saveHost: func(host *file.Host, oldHost string) error {
					saved[host.Id] = cloneHostForMutation(host)
					return nil
				},
			},
			Runtime: stubRuntime{},
		},
	}

	result, err := service.EditHost(EditHostInput{
		ID:                      20,
		ClientID:                0,
		Host:                    current.Host,
		Target:                  current.Target.TargetStr,
		Scheme:                  current.Scheme,
		CertFile:                currentCert,
		KeyFile:                 currentKey,
		SyncCertToMatchingHosts: true,
		SyncHostIDs:             []int{20, 21, 22, 23, 24},
	})
	if err != nil {
		t.Fatalf("EditHost() error = %v", err)
	}
	if result.ID != 20 || result.ClientID != currentClient.Id {
		t.Fatalf("EditHost() result = %+v, want current host/client ids", result)
	}

	if saved[20] == nil || saved[20].CertFile != currentCert || saved[20].KeyFile != currentKey {
		t.Fatalf("saved current host = %+v, want updated manual cert", saved[20])
	}
	if saved[21] == nil || saved[21].CertFile != currentCert || saved[21].KeyFile != currentKey {
		t.Fatalf("saved exact host = %+v, want switched manual cert", saved[21])
	}
	if saved[22] == nil || saved[22].CertFile != currentCert || saved[22].KeyFile != currentKey {
		t.Fatalf("saved wildcard host = %+v, want switched manual cert", saved[22])
	}
	if _, ok := saved[23]; ok {
		t.Fatalf("deep subdomain host should not be synced by single-label wildcard: %+v", saved[23])
	}
	if saved[24] == nil || saved[24].CertFile != currentCert || !saved[24].AutoSSL {
		t.Fatalf("saved auto ssl host = %+v, want manual cert saved while AutoSSL stays enabled", saved[24])
	}
	if _, ok := saved[25]; ok {
		t.Fatalf("out-of-scope host should not be synced: %+v", saved[25])
	}
}

func TestDefaultIndexServiceEditHostSyncsMatchingFileCertificates(t *testing.T) {
	currentCert, currentKey := generateServiceTestCertificatePair(t, []string{"*.example.org"}, time.Now().Add(2*time.Hour))
	tmpDir := t.TempDir()
	certPath := tmpDir + "/manual-cert.pem"
	keyPath := tmpDir + "/manual-key.pem"
	if err := os.WriteFile(certPath, []byte(currentCert), 0o600); err != nil {
		t.Fatalf("WriteFile(cert) error = %v", err)
	}
	if err := os.WriteFile(keyPath, []byte(currentKey), 0o600); err != nil {
		t.Fatalf("WriteFile(key) error = %v", err)
	}

	currentClient := &file.Client{Id: 9, UserId: 10, VerifyKey: "vk-9", Cnf: &file.Config{}, Flow: &file.Flow{}}
	current := &file.Host{
		Id:       30,
		Host:     "alpha.example.org",
		Scheme:   "https",
		CertType: "file",
		CertFile: certPath,
		KeyFile:  keyPath,
		Flow:     &file.Flow{},
		Client:   currentClient,
		Target:   &file.Target{TargetStr: "127.0.0.1:9080"},
	}
	matched := &file.Host{
		Id:      31,
		Host:    "api.example.org",
		Scheme:  "https",
		AutoSSL: true,
		Flow:    &file.Flow{},
		Client:  currentClient,
		Target:  &file.Target{TargetStr: "127.0.0.1:9081"},
	}
	unmatched := &file.Host{
		Id:     32,
		Host:   "deep.api.example.org",
		Scheme: "https",
		Flow:   &file.Flow{},
		Client: currentClient,
		Target: &file.Target{TargetStr: "127.0.0.1:9082"},
	}
	hostsByID := map[int]*file.Host{
		current.Id:   current,
		matched.Id:   matched,
		unmatched.Id: unmatched,
	}

	saved := make(map[int]*file.Host)
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				getHost: func(id int) (*file.Host, error) {
					host, ok := hostsByID[id]
					if !ok {
						t.Fatalf("GetHost(%d) unexpected lookup", id)
					}
					return host, nil
				},
				getClient: func(id int) (*file.Client, error) {
					if id != currentClient.Id {
						t.Fatalf("GetClient(%d), want %d", id, currentClient.Id)
					}
					return currentClient, nil
				},
				rangeHosts: func(func(*file.Host) bool) {
					t.Fatal("RangeHosts() should not be used when SyncHostIDs can be resolved directly")
				},
				saveHost: func(host *file.Host, oldHost string) error {
					saved[host.Id] = cloneHostForMutation(host)
					return nil
				},
			},
			Runtime: stubRuntime{},
		},
	}

	_, err := service.EditHost(EditHostInput{
		ID:                      30,
		ClientID:                currentClient.Id,
		Host:                    current.Host,
		Target:                  current.Target.TargetStr,
		Scheme:                  current.Scheme,
		CertFile:                certPath,
		KeyFile:                 keyPath,
		SyncCertToMatchingHosts: true,
		SyncHostIDs:             []int{30, 31, 32},
	})
	if err != nil {
		t.Fatalf("EditHost() error = %v", err)
	}

	if saved[31] == nil || saved[31].CertType != "file" || saved[31].CertFile != certPath || saved[31].KeyFile != keyPath || !saved[31].AutoSSL {
		t.Fatalf("saved matched host = %+v, want file cert stored while AutoSSL stays enabled", saved[31])
	}
	if _, ok := saved[32]; ok {
		t.Fatalf("deep subdomain host should not be synced by single-label wildcard file cert: %+v", saved[32])
	}
}

func TestDefaultIndexServiceEditHostSyncFailureDoesNotClearMatchedHostCacheEarly(t *testing.T) {
	currentCert, currentKey := generateServiceTestCertificatePair(t, []string{"*.example.net"}, time.Now().Add(2*time.Hour))
	currentClient := &file.Client{Id: 12, UserId: 13, VerifyKey: "vk-12", Cnf: &file.Config{}, Flow: &file.Flow{}}
	current := &file.Host{
		Id:       40,
		Host:     "alpha.example.net",
		Scheme:   "https",
		CertType: "text",
		CertFile: currentCert,
		KeyFile:  currentKey,
		Flow:     &file.Flow{},
		Client:   currentClient,
		Target:   &file.Target{TargetStr: "127.0.0.1:10080"},
	}
	matched := &file.Host{
		Id:       41,
		Host:     "beta.example.net",
		Scheme:   "https",
		CertType: "empty",
		Flow:     &file.Flow{},
		Client:   currentClient,
		Target:   &file.Target{TargetStr: "127.0.0.1:10081"},
	}
	hostsByID := map[int]*file.Host{
		current.Id: current,
		matched.Id: matched,
	}
	var removed []int
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				getHost: func(id int) (*file.Host, error) {
					host, ok := hostsByID[id]
					if !ok {
						t.Fatalf("GetHost(%d) unexpected lookup", id)
					}
					return host, nil
				},
				getClient: func(id int) (*file.Client, error) {
					return currentClient, nil
				},
				rangeHosts: func(func(*file.Host) bool) {
					t.Fatal("RangeHosts() should not be used when SyncHostIDs can be resolved directly")
				},
				saveHost: func(host *file.Host, oldHost string) error {
					if host.Id == matched.Id {
						return errors.New("sync save failed")
					}
					return nil
				},
			},
			Runtime: stubRuntime{
				removeHostCache: func(id int) {
					removed = append(removed, id)
				},
			},
		},
	}

	_, err := service.EditHost(EditHostInput{
		ID:                      current.Id,
		ClientID:                currentClient.Id,
		Host:                    current.Host,
		Target:                  current.Target.TargetStr,
		Scheme:                  current.Scheme,
		CertFile:                currentCert,
		KeyFile:                 currentKey,
		SyncCertToMatchingHosts: true,
		SyncHostIDs:             []int{current.Id, matched.Id},
	})
	if err == nil || err.Error() != "sync save failed" {
		t.Fatalf("EditHost() error = %v, want sync save failed", err)
	}
	if len(removed) != 1 || removed[0] != current.Id {
		t.Fatalf("RemoveHostCache() ids = %v, want only current host [%d]", removed, current.Id)
	}
}

func TestDefaultIndexServiceEditHostSyncSkipsMissingMatchedHosts(t *testing.T) {
	currentCert, currentKey := generateServiceTestCertificatePair(t, []string{"*.example.net"}, time.Now().Add(2*time.Hour))
	currentClient := &file.Client{Id: 12, UserId: 13, VerifyKey: "vk-12", Cnf: &file.Config{}, Flow: &file.Flow{}}
	current := &file.Host{
		Id:       40,
		Host:     "alpha.example.net",
		Scheme:   "https",
		CertType: "text",
		CertFile: currentCert,
		KeyFile:  currentKey,
		Flow:     &file.Flow{},
		Client:   currentClient,
		Target:   &file.Target{TargetStr: "127.0.0.1:10080"},
	}
	matched := &file.Host{
		Id:     41,
		Host:   "beta.example.net",
		Scheme: "https",
		Flow:   &file.Flow{},
		Client: currentClient,
		Target: &file.Target{TargetStr: "127.0.0.1:10081"},
	}
	hostsByID := map[int]*file.Host{
		current.Id: current,
		matched.Id: matched,
	}
	saved := 0
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				getHost: func(id int) (*file.Host, error) {
					host, ok := hostsByID[id]
					if !ok {
						t.Fatalf("GetHost(%d) unexpected lookup", id)
					}
					return host, nil
				},
				getClient: func(id int) (*file.Client, error) {
					return currentClient, nil
				},
				rangeHosts: func(func(*file.Host) bool) {
					t.Fatal("RangeHosts() should not be used when SyncHostIDs can be resolved directly")
				},
				saveHost: func(host *file.Host, oldHost string) error {
					saved++
					if host.Id == matched.Id {
						return file.ErrHostNotFound
					}
					return nil
				},
			},
			Runtime: stubRuntime{},
		},
	}

	_, err := service.EditHost(EditHostInput{
		ID:                      current.Id,
		ClientID:                currentClient.Id,
		Host:                    current.Host,
		Target:                  current.Target.TargetStr,
		Scheme:                  current.Scheme,
		CertFile:                currentCert,
		KeyFile:                 currentKey,
		SyncCertToMatchingHosts: true,
		SyncHostIDs:             []int{current.Id, matched.Id},
	})
	if err != nil {
		t.Fatalf("EditHost() error = %v", err)
	}
	if saved != 2 {
		t.Fatalf("SaveHost() call count = %d, want 2", saved)
	}
}

func TestBuildAddTunnelInputUsesPrincipalForAdminPolicy(t *testing.T) {
	input := BuildAddTunnelInput(IndexMutationContext{
		Principal: Principal{
			Authenticated: true,
			Kind:          "admin",
			Roles:         []string{RoleAdmin},
			Permissions:   []string{PermissionAll},
		},
		AllowLocalProxy: true,
		AllowUserLocal:  false,
	}, AddTunnelRequest{
		TunnelWriteRequest: TunnelWriteRequest{
			ClientID:       7,
			Port:           10080,
			ServerIP:       "0.0.0.0",
			Mode:           "tcp",
			TargetType:     "tcp",
			Target:         "bridge://127.0.0.1:80",
			ProxyProtocol:  2,
			LocalProxy:     true,
			Auth:           "demo:secret",
			Remark:         "created via admin",
			Password:       "pw",
			LocalPath:      "/tmp/demo",
			StripPre:       "/old",
			EnableHTTP:     true,
			EnableSocks5:   true,
			DestACLMode:    2,
			DestACLRules:   "10.0.0.0/8",
			FlowLimit:      2048,
			TimeLimit:      "2026-03-26 12:00:00",
			RateLimit:      8192,
			MaxConnections: 6,
		},
	})

	if !input.IsAdmin || !input.AllowUserLocal {
		t.Fatalf("input policy = %+v, want admin/local enabled", input)
	}
	if input.Target != "bridge://127.0.0.1:80" || input.ClientID != 7 || input.Port != 10080 || input.RateLimit != 8192 || input.MaxConnections != 6 {
		t.Fatalf("input fields = %+v, want request values copied", input)
	}
}

func TestBuildEditHostInputBlocksUserLocalProxyWithoutUserFeature(t *testing.T) {
	input := BuildEditHostInput(IndexMutationContext{
		Principal: Principal{
			Authenticated: true,
			Kind:          "user",
			Permissions:   []string{PermissionHostsUpdate},
		},
		AllowLocalProxy: true,
		AllowUserLocal:  false,
	}, EditHostRequest{
		ID: 9,
		HostWriteRequest: HostWriteRequest{
			ClientID:       3,
			Host:           "example.com",
			Target:         "127.0.0.1:8080",
			ProxyProtocol:  1,
			LocalProxy:     true,
			Auth:           "demo:secret",
			Header:         "X-Test=1",
			RespHeader:     "X-Out=1",
			HostChange:     "backend.example.com",
			Remark:         "updated by user",
			Location:       "/api",
			PathRewrite:    "/v1",
			RedirectURL:    "https://example.com/ok",
			FlowLimit:      1024,
			TimeLimit:      "2026-03-27 09:00:00",
			RateLimit:      4096,
			MaxConnections: 5,
			Scheme:         "https",
			HTTPSJustProxy: true,
			TLSOffload:     true,
			AutoSSL:        true,
			KeyFile:        "key.pem",
			CertFile:       "cert.pem",
			AutoHTTPS:      true,
			AutoCORS:       true,
			CompatMode:     true,
			TargetIsHTTPS:  true,
		},
		ResetFlow:               true,
		SyncCertToMatchingHosts: true,
		SyncHostIDs:             []int{9, 10},
	})

	if input.IsAdmin || input.AllowUserLocal {
		t.Fatalf("input policy = %+v, want user/local disabled", input)
	}
	if input.ID != 9 || input.Host != "example.com" || !input.ResetFlow || !input.TargetIsHTTPS || input.RateLimit != 4096 || input.MaxConnections != 5 {
		t.Fatalf("input fields = %+v, want request values copied", input)
	}
	if !input.SyncCertToMatchingHosts || len(input.SyncHostIDs) != 2 || input.SyncHostIDs[1] != 10 {
		t.Fatalf("sync fields = %+v, want copied sync flags/ids", input)
	}
}

func TestBuildAddTunnelInputUsesAuthorizationFallbackForAdminPolicy(t *testing.T) {
	input := BuildAddTunnelInput(IndexMutationContext{
		Principal: Principal{Username: "mapped-admin"},
		Authz: DefaultAuthorizationService{
			Resolver: stubPermissionResolver{
				normalizePrincipal: func(principal Principal) Principal {
					if principal.Username == "mapped-admin" {
						principal.Authenticated = true
						principal.Kind = "admin"
						principal.IsAdmin = true
						principal.Roles = []string{RoleAdmin}
						principal.Permissions = []string{PermissionAll}
					}
					return principal
				},
			},
		},
		AllowLocalProxy: true,
		AllowUserLocal:  false,
	}, AddTunnelRequest{
		TunnelWriteRequest: TunnelWriteRequest{
			ClientID:   7,
			Mode:       "tcp",
			TargetType: "tcp",
			Target:     "127.0.0.1:80",
		},
	})

	if !input.IsAdmin || !input.AllowUserLocal {
		t.Fatalf("input policy = %+v, want admin/local enabled from authorization fallback", input)
	}
}

func TestNormalizeRulesNormalizesLineEndingsAndWhitespace(t *testing.T) {
	raw := "  regexp:^API[0-9]+\\.EDGE\\.TEST$\r\next-domain:GeoSite.DAT:CN\r\n# Comment\r\n  "
	got := normalizeRules(raw)
	want := "regexp:^API[0-9]+\\.EDGE\\.TEST$\next-domain:GeoSite.DAT:CN\n# Comment"
	if got != want {
		t.Fatalf("normalizeRules() = %q, want %q", got, want)
	}
}

func TestDefaultResourceStatusHelpersPersistMutationsViaDefaultBackend(t *testing.T) {
	resetBackendTestDB(t)

	client := &file.Client{
		Id:        1,
		VerifyKey: "service-client",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	tunnel := &file.Tunnel{
		Id:         11,
		Mode:       "tcp",
		Status:     true,
		Client:     client,
		TargetType: common.CONN_TCP,
		Target:     &file.Target{TargetStr: "127.0.0.1:80"},
		Flow:       &file.Flow{},
	}
	if err := file.GetDb().NewTask(tunnel); err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}
	host := &file.Host{
		Id:     21,
		Host:   "service.example.com",
		Client: client,
		Target: &file.Target{TargetStr: "127.0.0.1:8080"},
		Flow:   &file.Flow{},
	}
	if err := file.GetDb().NewHost(host); err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}

	if !ClientOwnsTunnel(1, 11) || ClientOwnsTunnel(2, 11) {
		t.Fatal("ClientOwnsTunnel() returned unexpected ownership result")
	}
	if !ClientOwnsHost(1, 21) || ClientOwnsHost(2, 21) {
		t.Fatal("ClientOwnsHost() returned unexpected ownership result")
	}

	if err := ChangeTunnelStatus(11, "http", "start"); err != nil {
		t.Fatalf("ChangeTunnelStatus() error = %v", err)
	}
	savedTunnel, err := file.GetDb().GetTask(11)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if !savedTunnel.HttpProxy {
		t.Fatalf("ChangeTunnelStatus() should enable HttpProxy, got %+v", savedTunnel)
	}

	if err := ChangeHostStatus(21, "auto_https", "start"); err != nil {
		t.Fatalf("ChangeHostStatus() error = %v", err)
	}
	savedHost, err := file.GetDb().GetHostById(21)
	if err != nil {
		t.Fatalf("GetHostById() error = %v", err)
	}
	if !savedHost.AutoHttps {
		t.Fatalf("ChangeHostStatus() should enable AutoHttps, got %+v", savedHost)
	}

	client.RateLimit = 256
	if err := file.GetDb().UpdateClient(client); err != nil {
		t.Fatalf("UpdateClient() error = %v", err)
	}
	if err := ClearClientStatusByID(1, "rate_limit"); err != nil {
		t.Fatalf("ClearClientStatusByID() error = %v", err)
	}
	savedClient, err := file.GetDb().GetClient(1)
	if err != nil {
		t.Fatalf("GetClient() error = %v", err)
	}
	if savedClient.RateLimit != 0 {
		t.Fatalf("ClearClientStatusByID() should clear RateLimit, got %+v", savedClient)
	}
}
