package service

import (
	"fmt"
	"sync"
	"testing"

	"github.com/djylb/nps/lib/file"
)

func TestDefaultRepositorySnapshotsHandleConcurrentRuntimeMutation(t *testing.T) {
	t.Run("users", func(t *testing.T) {
		resetBackendTestDB(t)
		if err := file.GetDb().NewUser(&file.User{
			Id:        7,
			Username:  "tenant",
			Password:  "secret",
			Status:    1,
			TotalFlow: &file.Flow{},
		}); err != nil {
			t.Fatalf("NewUser() error = %v", err)
		}
		stored, err := file.GetDb().GetUser(7)
		if err != nil {
			t.Fatalf("GetUser() error = %v", err)
		}
		runSnapshotMutationRace(t, func() {
			defaultRepository{}.RangeUsers(func(user *file.User) bool {
				if user == nil || user.Id != 7 {
					t.Fatalf("RangeUsers() returned %+v, want cloned user", user)
				}
				return true
			})
		}, func(i int) {
			stored.Lock()
			stored.Username = fmt.Sprintf("tenant-%d", i)
			stored.Status = (i % 2) + 1
			stored.Unlock()
			file.InitializeUserRuntime(stored)
		})
	})

	t.Run("clients", func(t *testing.T) {
		resetBackendTestDB(t)
		if err := file.GetDb().NewClient(&file.Client{
			Id:        11,
			VerifyKey: "demo-vkey",
			Remark:    "client",
			Status:    true,
			Cnf:       &file.Config{U: "demo"},
			Flow:      &file.Flow{},
		}); err != nil {
			t.Fatalf("NewClient() error = %v", err)
		}
		stored, err := file.GetDb().GetClient(11)
		if err != nil {
			t.Fatalf("GetClient() error = %v", err)
		}
		runSnapshotMutationRace(t, func() {
			defaultRepository{}.RangeClients(func(client *file.Client) bool {
				if client == nil || client.Id != 11 {
					t.Fatalf("RangeClients() returned %+v, want cloned client", client)
				}
				return true
			})
		}, func(i int) {
			stored.Lock()
			stored.Remark = fmt.Sprintf("client-%d", i)
			stored.EntryAclRules = fmt.Sprintf("127.0.0.%d", (i%254)+1)
			stored.Unlock()
			file.InitializeClientRuntime(stored)
		})
	})

	t.Run("tunnels", func(t *testing.T) {
		resetBackendTestDB(t)
		client := &file.Client{Id: 11, VerifyKey: "demo-vkey", Status: true, Cnf: &file.Config{}, Flow: &file.Flow{}}
		if err := file.GetDb().NewClient(client); err != nil {
			t.Fatalf("NewClient() error = %v", err)
		}
		if err := file.GetDb().NewTask(&file.Tunnel{
			Id:     21,
			Port:   8080,
			Mode:   "tcp",
			Remark: "tunnel",
			Client: &file.Client{Id: 11},
			Target: &file.Target{TargetStr: "127.0.0.1:8080"},
			Flow:   &file.Flow{},
		}); err != nil {
			t.Fatalf("NewTask() error = %v", err)
		}
		stored, err := file.GetDb().GetTask(21)
		if err != nil {
			t.Fatalf("GetTask() error = %v", err)
		}
		runSnapshotMutationRace(t, func() {
			defaultRepository{}.RangeTunnels(func(tunnel *file.Tunnel) bool {
				if tunnel == nil || tunnel.Id != 21 {
					t.Fatalf("RangeTunnels() returned %+v, want cloned tunnel", tunnel)
				}
				return true
			})
		}, func(i int) {
			stored.Lock()
			stored.Remark = fmt.Sprintf("tunnel-%d", i)
			stored.EntryAclRules = fmt.Sprintf("10.0.%d.1", i%255)
			stored.Unlock()
			file.InitializeTunnelRuntime(stored)
		})
	})

	t.Run("hosts", func(t *testing.T) {
		resetBackendTestDB(t)
		client := &file.Client{Id: 11, VerifyKey: "demo-vkey", Status: true, Cnf: &file.Config{}, Flow: &file.Flow{}}
		if err := file.GetDb().NewClient(client); err != nil {
			t.Fatalf("NewClient() error = %v", err)
		}
		if err := file.GetDb().NewHost(&file.Host{
			Id:       31,
			Host:     "demo.example.com",
			Location: "/",
			Scheme:   "http",
			Remark:   "host",
			Client:   &file.Client{Id: 11},
			Target:   &file.Target{TargetStr: "127.0.0.1:9090"},
			Flow:     &file.Flow{},
		}); err != nil {
			t.Fatalf("NewHost() error = %v", err)
		}
		stored, err := file.GetDb().GetHostById(31)
		if err != nil {
			t.Fatalf("GetHostById() error = %v", err)
		}
		runSnapshotMutationRace(t, func() {
			defaultRepository{}.RangeHosts(func(host *file.Host) bool {
				if host == nil || host.Id != 31 {
					t.Fatalf("RangeHosts() returned %+v, want cloned host", host)
				}
				return true
			})
		}, func(i int) {
			stored.Lock()
			stored.Remark = fmt.Sprintf("host-%d", i)
			stored.EntryAclRules = fmt.Sprintf("172.16.%d.1", i%255)
			stored.Unlock()
			file.InitializeHostRuntime(stored)
		})
	})
}

func runSnapshotMutationRace(t *testing.T, snapshot func(), mutate func(int)) {
	t.Helper()
	const iterations = 256
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < iterations; i++ {
			snapshot()
		}
	}()

	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < iterations; i++ {
			mutate(i)
		}
	}()

	close(start)
	wg.Wait()
}
