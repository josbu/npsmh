package service

import (
	"testing"

	"github.com/djylb/nps/bridge"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/server"
)

func TestDefaultRepositoryRangeTunnelsReturnsWorkingCopies(t *testing.T) {
	resetBackendTestDB(t)

	if err := file.GetDb().NewClient(&file.Client{
		Id:        11,
		VerifyKey: "demo-vkey",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if err := file.GetDb().NewTask(&file.Tunnel{
		Id:     21,
		Port:   8080,
		Mode:   "tcp",
		Remark: "original-tunnel",
		Client: &file.Client{Id: 11},
		Target: &file.Target{TargetStr: "127.0.0.1:8080"},
		Flow:   &file.Flow{},
	}); err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}

	var got *file.Tunnel
	defaultRepository{}.RangeTunnels(func(tunnel *file.Tunnel) bool {
		got = tunnel
		return false
	})
	if got == nil {
		t.Fatal("RangeTunnels() should return a tunnel")
	}

	stored, err := file.GetDb().GetTask(21)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if got == stored {
		t.Fatal("RangeTunnels() should return a cloned tunnel, not the live pointer")
	}

	got.Port = 9090
	got.Remark = "mutated-tunnel"
	got.Flow.Add(1, 2)
	got.Target.TargetStr = "127.0.0.1:9090"
	got.Client.Remark = "mutated-client"
	got.Client.VerifyKey = "mutated-vkey"

	if stored.Port != 8080 || stored.Remark != "original-tunnel" {
		t.Fatalf("stored tunnel mutated = %+v", stored)
	}
	if stored.Flow == nil || stored.Flow.InletFlow != 0 || stored.Flow.ExportFlow != 0 {
		t.Fatalf("stored tunnel flow mutated = %+v", stored.Flow)
	}
	if stored.Target == nil || stored.Target.TargetStr != "127.0.0.1:8080" {
		t.Fatalf("stored tunnel target mutated = %+v", stored.Target)
	}
	if stored.Client == nil || stored.Client.Remark != "" || stored.Client.VerifyKey != "demo-vkey" {
		t.Fatalf("stored tunnel client mutated = %+v", stored.Client)
	}
}

func TestDefaultRepositoryRangeTunnelsDropsInvalidEntry(t *testing.T) {
	resetBackendTestDB(t)

	if err := file.GetDb().NewClient(&file.Client{
		Id:        11,
		VerifyKey: "demo-vkey",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if err := file.GetDb().NewTask(&file.Tunnel{
		Id:     21,
		Port:   8080,
		Mode:   "tcp",
		Client: &file.Client{Id: 11},
		Target: &file.Target{TargetStr: "127.0.0.1:8080"},
		Flow:   &file.Flow{},
	}); err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}
	file.GetDb().JsonDb.Tasks.Store(999, "bad-tunnel-entry")

	count := 0
	defaultRepository{}.RangeTunnels(func(tunnel *file.Tunnel) bool {
		count++
		return true
	})

	if count != 1 {
		t.Fatalf("RangeTunnels() count = %d, want 1 valid tunnel", count)
	}
	if _, ok := file.GetDb().JsonDb.Tasks.Load(999); ok {
		t.Fatal("RangeTunnels() should remove invalid tunnel entry")
	}
}

func TestDefaultRepositoryRangeHostsReturnsWorkingCopies(t *testing.T) {
	resetBackendTestDB(t)

	if err := file.GetDb().NewClient(&file.Client{
		Id:        11,
		VerifyKey: "demo-vkey",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if err := file.GetDb().NewHost(&file.Host{
		Id:     31,
		Host:   "demo.example.com",
		Scheme: "http",
		Remark: "original-host",
		Client: &file.Client{Id: 11},
		Target: &file.Target{TargetStr: "127.0.0.1:9090"},
		Flow:   &file.Flow{},
	}); err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}

	var got *file.Host
	defaultRepository{}.RangeHosts(func(host *file.Host) bool {
		got = host
		return false
	})
	if got == nil {
		t.Fatal("RangeHosts() should return a host")
	}

	stored, err := file.GetDb().GetHostById(31)
	if err != nil {
		t.Fatalf("GetHostById() error = %v", err)
	}
	if got == stored {
		t.Fatal("RangeHosts() should return a cloned host, not the live pointer")
	}

	got.Host = "mutated.example.com"
	got.Remark = "mutated-host"
	got.Flow.Add(3, 4)
	got.Target.TargetStr = "127.0.0.1:9191"
	got.Client.Remark = "mutated-client"
	got.Client.VerifyKey = "mutated-vkey"

	if stored.Host != "demo.example.com" || stored.Remark != "original-host" {
		t.Fatalf("stored host mutated = %+v", stored)
	}
	if stored.Flow == nil || stored.Flow.InletFlow != 0 || stored.Flow.ExportFlow != 0 {
		t.Fatalf("stored host flow mutated = %+v", stored.Flow)
	}
	if stored.Target == nil || stored.Target.TargetStr != "127.0.0.1:9090" {
		t.Fatalf("stored host target mutated = %+v", stored.Target)
	}
	if stored.Client == nil || stored.Client.Remark != "" || stored.Client.VerifyKey != "demo-vkey" {
		t.Fatalf("stored host client mutated = %+v", stored.Client)
	}
}

func TestDefaultRepositoryRangeHostsDropsInvalidEntry(t *testing.T) {
	resetBackendTestDB(t)

	if err := file.GetDb().NewClient(&file.Client{
		Id:        11,
		VerifyKey: "demo-vkey",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if err := file.GetDb().NewHost(&file.Host{
		Id:     31,
		Host:   "demo.example.com",
		Scheme: "http",
		Client: &file.Client{Id: 11},
		Target: &file.Target{TargetStr: "127.0.0.1:9090"},
		Flow:   &file.Flow{},
	}); err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	file.GetDb().JsonDb.Hosts.Store(999, "bad-host-entry")

	count := 0
	defaultRepository{}.RangeHosts(func(host *file.Host) bool {
		count++
		return true
	})

	if count != 1 {
		t.Fatalf("RangeHosts() count = %d, want 1 valid host", count)
	}
	if _, ok := file.GetDb().JsonDb.Hosts.Load(999); ok {
		t.Fatal("RangeHosts() should remove invalid host entry")
	}
}

func TestDefaultRepositoryCountResourcesByClientIDCountsIndexedEntries(t *testing.T) {
	resetBackendTestDB(t)

	client := &file.Client{Id: 7, VerifyKey: "client-7", Status: true, Cnf: &file.Config{}, Flow: &file.Flow{}}
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if err := file.GetDb().NewTask(&file.Tunnel{
		Id:     21,
		Mode:   "tcp",
		Status: true,
		Client: client,
		Target: &file.Target{TargetStr: "127.0.0.1:80"},
		Flow:   &file.Flow{},
	}); err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}
	if err := file.GetDb().NewHost(&file.Host{
		Id:      31,
		Host:    "demo.example.com",
		Scheme:  "all",
		Client:  client,
		Target:  &file.Target{TargetStr: "127.0.0.1:81"},
		Flow:    &file.Flow{},
		IsClose: false,
	}); err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	file.GetDb().JsonDb.Tasks.Store(999, "bad-tunnel-entry")
	file.GetDb().JsonDb.Hosts.Store(998, "bad-host-entry")

	count, err := defaultRepository{}.CountResourcesByClientID(7)
	if err != nil {
		t.Fatalf("CountResourcesByClientID() error = %v", err)
	}
	if count != 2 {
		t.Fatalf("CountResourcesByClientID() = %d, want 2", count)
	}
}

func TestDefaultRepositoryCollectOwnedResourceCountsUsesIndexedOwnerCounts(t *testing.T) {
	resetBackendTestDB(t)

	clientA := &file.Client{Id: 7, VerifyKey: "client-7", OwnerUserID: 3, Status: true, Cnf: &file.Config{}, Flow: &file.Flow{}}
	clientB := &file.Client{Id: 8, VerifyKey: "client-8", OwnerUserID: 4, Status: true, Cnf: &file.Config{}, Flow: &file.Flow{}}
	if err := file.GetDb().NewClient(clientA); err != nil {
		t.Fatalf("NewClient(clientA) error = %v", err)
	}
	if err := file.GetDb().NewClient(clientB); err != nil {
		t.Fatalf("NewClient(clientB) error = %v", err)
	}
	if err := file.GetDb().NewTask(&file.Tunnel{
		Id:     21,
		Mode:   "tcp",
		Status: true,
		Client: clientA,
		Target: &file.Target{TargetStr: "127.0.0.1:80"},
		Flow:   &file.Flow{},
	}); err != nil {
		t.Fatalf("NewTask(clientA) error = %v", err)
	}
	if err := file.GetDb().NewHost(&file.Host{
		Id:      31,
		Host:    "demo.example.com",
		Scheme:  "all",
		Client:  clientA,
		Target:  &file.Target{TargetStr: "127.0.0.1:81"},
		Flow:    &file.Flow{},
		IsClose: false,
	}); err != nil {
		t.Fatalf("NewHost(clientA) error = %v", err)
	}
	file.GetDb().JsonDb.Clients.Store(999, "bad-client-entry")
	file.GetDb().JsonDb.Tasks.Store(998, "bad-tunnel-entry")
	file.GetDb().JsonDb.Hosts.Store(997, "bad-host-entry")

	clientCounts, tunnelCounts, hostCounts, err := defaultRepository{}.CollectOwnedResourceCounts()
	if err != nil {
		t.Fatalf("CollectOwnedResourceCounts() error = %v", err)
	}
	if clientCounts[3] != 1 || clientCounts[4] != 1 {
		t.Fatalf("CollectOwnedResourceCounts() client counts = %v, want owner 3/4 => 1/1", clientCounts)
	}
	if tunnelCounts[3] != 1 || hostCounts[3] != 1 {
		t.Fatalf("CollectOwnedResourceCounts() tunnel/host counts = %v/%v, want owner 3 => 1/1", tunnelCounts, hostCounts)
	}
	if _, ok := file.GetDb().JsonDb.Clients.Load(999); ok {
		t.Fatal("CollectOwnedResourceCounts() should remove invalid client entry")
	}
}

func TestDefaultRepositoryCountOwnedResourcesByUserIDUsesClientScopedIndexes(t *testing.T) {
	resetBackendTestDB(t)

	clientA := &file.Client{Id: 7, VerifyKey: "client-7", OwnerUserID: 3, Status: true, Cnf: &file.Config{}, Flow: &file.Flow{}}
	clientB := &file.Client{Id: 8, VerifyKey: "client-8", OwnerUserID: 4, Status: true, Cnf: &file.Config{}, Flow: &file.Flow{}}
	if err := file.GetDb().NewClient(clientA); err != nil {
		t.Fatalf("NewClient(clientA) error = %v", err)
	}
	if err := file.GetDb().NewClient(clientB); err != nil {
		t.Fatalf("NewClient(clientB) error = %v", err)
	}
	if err := file.GetDb().NewTask(&file.Tunnel{
		Id:     21,
		Mode:   "tcp",
		Status: true,
		Client: clientA,
		Target: &file.Target{TargetStr: "127.0.0.1:80"},
		Flow:   &file.Flow{},
	}); err != nil {
		t.Fatalf("NewTask(clientA) error = %v", err)
	}
	if err := file.GetDb().NewHost(&file.Host{
		Id:      31,
		Host:    "demo.example.com",
		Scheme:  "all",
		Client:  clientA,
		Target:  &file.Target{TargetStr: "127.0.0.1:81"},
		Flow:    &file.Flow{},
		IsClose: false,
	}); err != nil {
		t.Fatalf("NewHost(clientA) error = %v", err)
	}
	file.GetDb().JsonDb.Clients.Store(999, "bad-client-entry")
	file.GetDb().JsonDb.Tasks.Store(998, "bad-tunnel-entry")
	file.GetDb().JsonDb.Hosts.Store(997, "bad-host-entry")

	clientCount, tunnelCount, hostCount, err := defaultRepository{}.CountOwnedResourcesByUserID(3)
	if err != nil {
		t.Fatalf("CountOwnedResourcesByUserID() error = %v", err)
	}
	if clientCount != 1 || tunnelCount != 1 || hostCount != 1 {
		t.Fatalf("CountOwnedResourcesByUserID() = (%d,%d,%d), want (1,1,1)", clientCount, tunnelCount, hostCount)
	}
}

func TestDefaultRepositoryRangeResourcesByClientIDReturnsWorkingCopies(t *testing.T) {
	resetBackendTestDB(t)

	clientA := &file.Client{Id: 7, VerifyKey: "client-7", Status: true, Cnf: &file.Config{}, Flow: &file.Flow{}}
	clientB := &file.Client{Id: 8, VerifyKey: "client-8", Status: true, Cnf: &file.Config{}, Flow: &file.Flow{}}
	if err := file.GetDb().NewClient(clientA); err != nil {
		t.Fatalf("NewClient(clientA) error = %v", err)
	}
	if err := file.GetDb().NewClient(clientB); err != nil {
		t.Fatalf("NewClient(clientB) error = %v", err)
	}
	if err := file.GetDb().NewTask(&file.Tunnel{
		Id:     21,
		Mode:   "tcp",
		Status: true,
		Client: clientA,
		Target: &file.Target{TargetStr: "127.0.0.1:80"},
		Flow:   &file.Flow{},
	}); err != nil {
		t.Fatalf("NewTask(clientA) error = %v", err)
	}
	if err := file.GetDb().NewTask(&file.Tunnel{
		Id:     22,
		Mode:   "tcp",
		Status: true,
		Client: clientB,
		Target: &file.Target{TargetStr: "127.0.0.1:81"},
		Flow:   &file.Flow{},
	}); err != nil {
		t.Fatalf("NewTask(clientB) error = %v", err)
	}
	if err := file.GetDb().NewHost(&file.Host{
		Id:      31,
		Host:    "a.example.com",
		Scheme:  "all",
		Client:  clientA,
		Target:  &file.Target{TargetStr: "127.0.0.1:90"},
		Flow:    &file.Flow{},
		IsClose: false,
	}); err != nil {
		t.Fatalf("NewHost(clientA) error = %v", err)
	}
	if err := file.GetDb().NewHost(&file.Host{
		Id:      32,
		Host:    "b.example.com",
		Scheme:  "all",
		Client:  clientB,
		Target:  &file.Target{TargetStr: "127.0.0.1:91"},
		Flow:    &file.Flow{},
		IsClose: false,
	}); err != nil {
		t.Fatalf("NewHost(clientB) error = %v", err)
	}
	file.GetDb().JsonDb.Tasks.Store(999, "bad-tunnel-entry")
	file.GetDb().JsonDb.Hosts.Store(998, "bad-host-entry")

	var rangedTunnel *file.Tunnel
	tunnelCount := 0
	defaultRepository{}.RangeTunnelsByClientID(7, func(tunnel *file.Tunnel) bool {
		rangedTunnel = tunnel
		tunnelCount++
		return true
	})
	if tunnelCount != 1 || rangedTunnel == nil || rangedTunnel.Client == nil || rangedTunnel.Client.Id != 7 {
		t.Fatalf("RangeTunnelsByClientID() count=%d tunnel=%+v, want one client 7 tunnel", tunnelCount, rangedTunnel)
	}
	storedTunnel, err := file.GetDb().GetTask(21)
	if err != nil {
		t.Fatalf("GetTask(21) error = %v", err)
	}
	if rangedTunnel == storedTunnel {
		t.Fatal("RangeTunnelsByClientID() should return a cloned tunnel, not the live pointer")
	}
	rangedTunnel.Flow.Add(1, 2)
	if storedTunnel.Flow == nil || storedTunnel.Flow.InletFlow != 0 || storedTunnel.Flow.ExportFlow != 0 {
		t.Fatalf("stored tunnel flow mutated = %+v", storedTunnel.Flow)
	}

	var rangedHost *file.Host
	hostCount := 0
	defaultRepository{}.RangeHostsByClientID(7, func(host *file.Host) bool {
		rangedHost = host
		hostCount++
		return true
	})
	if hostCount != 1 || rangedHost == nil || rangedHost.Client == nil || rangedHost.Client.Id != 7 {
		t.Fatalf("RangeHostsByClientID() count=%d host=%+v, want one client 7 host", hostCount, rangedHost)
	}
	storedHost, err := file.GetDb().GetHostById(31)
	if err != nil {
		t.Fatalf("GetHostById(31) error = %v", err)
	}
	if rangedHost == storedHost {
		t.Fatal("RangeHostsByClientID() should return a cloned host, not the live pointer")
	}
	rangedHost.Flow.Add(3, 4)
	if storedHost.Flow == nil || storedHost.Flow.InletFlow != 0 || storedHost.Flow.ExportFlow != 0 {
		t.Fatalf("stored host flow mutated = %+v", storedHost.Flow)
	}
}

func TestDefaultRepositoryGetTunnelReturnsWorkingCopy(t *testing.T) {
	resetBackendTestDB(t)

	if err := file.GetDb().NewClient(&file.Client{
		Id:        11,
		VerifyKey: "demo-vkey",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if err := file.GetDb().NewTask(&file.Tunnel{
		Id:     21,
		Port:   8080,
		Mode:   "tcp",
		Remark: "original-tunnel",
		Client: &file.Client{Id: 11},
		Target: &file.Target{TargetStr: "127.0.0.1:8080"},
		Flow:   &file.Flow{},
	}); err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}

	got, err := defaultRepository{}.GetTunnel(21)
	if err != nil {
		t.Fatalf("GetTunnel() error = %v", err)
	}
	stored, err := file.GetDb().GetTask(21)
	if err != nil {
		t.Fatalf("db GetTask() error = %v", err)
	}
	if got == stored {
		t.Fatal("GetTunnel() should return a cloned tunnel, not the live pointer")
	}

	got.Remark = "mutated-tunnel"
	got.Flow.Add(5, 6)
	got.Client.Remark = "mutated-client"
	got.Client.VerifyKey = "mutated-vkey"
	if stored.Remark != "original-tunnel" {
		t.Fatalf("stored tunnel remark = %q, want %q", stored.Remark, "original-tunnel")
	}
	if stored.Flow == nil || stored.Flow.InletFlow != 0 || stored.Flow.ExportFlow != 0 {
		t.Fatalf("stored tunnel flow mutated = %+v", stored.Flow)
	}
	if stored.Client == nil || stored.Client.Remark != "" || stored.Client.VerifyKey != "demo-vkey" {
		t.Fatalf("stored tunnel client mutated = %+v", stored.Client)
	}
}

func TestDefaultRepositoryGetTunnelRefreshesNestedClientRuntimeFields(t *testing.T) {
	resetBackendTestDB(t)

	oldBridge := server.Bridge
	server.Bridge = bridge.NewTunnel(false, nil, 0)
	t.Cleanup(func() {
		server.Bridge = oldBridge
	})

	if err := file.GetDb().NewClient(&file.Client{
		Id:        11,
		VerifyKey: "demo-vkey",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
		Addr:      "stale-addr",
		LocalAddr: "stale-local",
		Version:   "0.0.1",
	}); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if err := file.GetDb().NewTask(&file.Tunnel{
		Id:     21,
		Port:   8080,
		Mode:   "tcp",
		Remark: "runtime-tunnel",
		Client: &file.Client{Id: 11},
		Target: &file.Target{TargetStr: "127.0.0.1:8080"},
		Flow:   &file.Flow{},
	}); err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}

	node := bridge.NewNode("node-tunnel", "1.2.6", 4)
	node.AddSignal(conn.NewConn(backendRuntimeSignalConn{
		remote: backendRuntimeAddr("203.0.113.21:23000"),
		local:  backendRuntimeAddr("10.0.0.21:9300"),
	}))
	server.Bridge.Client.Store(11, bridge.NewClient(11, node))

	got, err := defaultRepository{}.GetTunnel(21)
	if err != nil {
		t.Fatalf("GetTunnel() error = %v", err)
	}
	if got.Client == nil || !got.Client.IsConnect {
		t.Fatalf("GetTunnel() client runtime state = %#v, want connected client", got.Client)
	}
	if got.Client.Version != "1.2.6" {
		t.Fatalf("GetTunnel() client version = %q, want 1.2.6", got.Client.Version)
	}
	if got.Client.Addr != "203.0.113.21" {
		t.Fatalf("GetTunnel() client addr = %q, want 203.0.113.21", got.Client.Addr)
	}
	if got.Client.LocalAddr != "10.0.0.21" {
		t.Fatalf("GetTunnel() client local addr = %q, want 10.0.0.21", got.Client.LocalAddr)
	}
}

func TestDefaultRepositoryGetHostReturnsWorkingCopy(t *testing.T) {
	resetBackendTestDB(t)

	if err := file.GetDb().NewClient(&file.Client{
		Id:        11,
		VerifyKey: "demo-vkey",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if err := file.GetDb().NewHost(&file.Host{
		Id:     31,
		Host:   "demo.example.com",
		Scheme: "http",
		Remark: "original-host",
		Client: &file.Client{Id: 11},
		Target: &file.Target{TargetStr: "127.0.0.1:9090"},
		Flow:   &file.Flow{},
	}); err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}

	got, err := defaultRepository{}.GetHost(31)
	if err != nil {
		t.Fatalf("GetHost() error = %v", err)
	}
	stored, err := file.GetDb().GetHostById(31)
	if err != nil {
		t.Fatalf("db GetHostById() error = %v", err)
	}
	if got == stored {
		t.Fatal("GetHost() should return a cloned host, not the live pointer")
	}

	got.Remark = "mutated-host"
	got.Flow.Add(7, 8)
	got.Client.Remark = "mutated-client"
	got.Client.VerifyKey = "mutated-vkey"
	if stored.Remark != "original-host" {
		t.Fatalf("stored host remark = %q, want %q", stored.Remark, "original-host")
	}
	if stored.Flow == nil || stored.Flow.InletFlow != 0 || stored.Flow.ExportFlow != 0 {
		t.Fatalf("stored host flow mutated = %+v", stored.Flow)
	}
	if stored.Client == nil || stored.Client.Remark != "" || stored.Client.VerifyKey != "demo-vkey" {
		t.Fatalf("stored host client mutated = %+v", stored.Client)
	}
}

func TestDefaultRepositoryGetHostRefreshesNestedClientRuntimeFields(t *testing.T) {
	resetBackendTestDB(t)

	oldBridge := server.Bridge
	server.Bridge = bridge.NewTunnel(false, nil, 0)
	t.Cleanup(func() {
		server.Bridge = oldBridge
	})

	if err := file.GetDb().NewClient(&file.Client{
		Id:        11,
		VerifyKey: "demo-vkey",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
		Addr:      "stale-addr",
		LocalAddr: "stale-local",
		Version:   "0.0.1",
	}); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if err := file.GetDb().NewHost(&file.Host{
		Id:     31,
		Host:   "demo.example.com",
		Scheme: "http",
		Remark: "runtime-host",
		Client: &file.Client{Id: 11},
		Target: &file.Target{TargetStr: "127.0.0.1:9090"},
		Flow:   &file.Flow{},
	}); err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}

	node := bridge.NewNode("node-host", "1.2.7", 4)
	node.AddSignal(conn.NewConn(backendRuntimeSignalConn{
		remote: backendRuntimeAddr("203.0.113.31:24000"),
		local:  backendRuntimeAddr("10.0.0.31:9400"),
	}))
	server.Bridge.Client.Store(11, bridge.NewClient(11, node))

	got, err := defaultRepository{}.GetHost(31)
	if err != nil {
		t.Fatalf("GetHost() error = %v", err)
	}
	if got.Client == nil || !got.Client.IsConnect {
		t.Fatalf("GetHost() client runtime state = %#v, want connected client", got.Client)
	}
	if got.Client.Version != "1.2.7" {
		t.Fatalf("GetHost() client version = %q, want 1.2.7", got.Client.Version)
	}
	if got.Client.Addr != "203.0.113.31" {
		t.Fatalf("GetHost() client addr = %q, want 203.0.113.31", got.Client.Addr)
	}
	if got.Client.LocalAddr != "10.0.0.31" {
		t.Fatalf("GetHost() client local addr = %q, want 10.0.0.31", got.Client.LocalAddr)
	}
}

func TestDefaultIndexServiceGetTunnelReflectsRuntimeRunStatus(t *testing.T) {
	resetBackendTestDB(t)

	if err := file.GetDb().NewClient(&file.Client{
		Id:        11,
		VerifyKey: "demo-vkey",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if err := file.GetDb().NewTask(&file.Tunnel{
		Id:       21,
		Mode:     "secret",
		Password: "secret-password",
		Remark:   "runtime-secret",
		Client:   &file.Client{Id: 11},
		Target:   &file.Target{TargetStr: "127.0.0.1:8080"},
		Flow:     &file.Flow{},
	}); err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}

	tunnel, err := file.GetDb().GetTask(21)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if err := server.AddTask(tunnel); err != nil {
		t.Fatalf("AddTask() error = %v", err)
	}
	t.Cleanup(func() {
		_ = server.DelTask(21)
	})

	got, err := DefaultIndexService{Backend: DefaultBackend()}.GetTunnel(21)
	if err != nil {
		t.Fatalf("GetTunnel() error = %v", err)
	}
	if !got.RunStatus {
		t.Fatal("GetTunnel() should reflect runtime running state")
	}
}

func TestDefaultRepositorySaveHostUsesDbUpdateSemantics(t *testing.T) {
	resetBackendTestDB(t)

	client := &file.Client{
		Id:        11,
		VerifyKey: "demo-vkey",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	host := &file.Host{
		Id:       31,
		Host:     "demo.example.com",
		Location: "/api",
		Scheme:   "https",
		Client:   client,
		Target:   &file.Target{TargetStr: "127.0.0.1:9090"},
		Flow:     &file.Flow{},
	}
	if err := file.GetDb().NewHost(host); err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}

	repo := defaultRepository{}
	updated, err := repo.GetHost(31)
	if err != nil {
		t.Fatalf("GetHost() error = %v", err)
	}
	cert := "-----BEGIN CERTIFICATE-----\nrepo-cert\n-----END CERTIFICATE-----"
	key := "-----BEGIN PRIVATE KEY-----\nrepo-key\n-----END PRIVATE KEY-----"
	updated.Host = "api.example.com"
	updated.Location = ""
	updated.Scheme = "invalid"
	updated.CertFile = cert
	updated.KeyFile = key

	if err := repo.SaveHost(updated, ""); err != nil {
		t.Fatalf("SaveHost() error = %v", err)
	}

	stored, err := file.GetDb().GetHostById(31)
	if err != nil {
		t.Fatalf("GetHostById() error = %v", err)
	}
	if stored.Host != "api.example.com" {
		t.Fatalf("stored.Host = %q, want api.example.com", stored.Host)
	}
	if stored.Location != "/" {
		t.Fatalf("stored.Location = %q, want /", stored.Location)
	}
	if stored.Scheme != "all" {
		t.Fatalf("stored.Scheme = %q, want all", stored.Scheme)
	}
	if stored.CertType != "text" {
		t.Fatalf("stored.CertType = %q, want text", stored.CertType)
	}
	expectedHash := crypt.FNV1a64("text", cert, key)
	if stored.CertHash != expectedHash {
		t.Fatalf("stored.CertHash = %q, want %q", stored.CertHash, expectedHash)
	}
	if ids := file.CurrentHostIndex().Lookup("demo.example.com"); containsRepositoryHostID(ids, 31) {
		t.Fatalf("HostIndex.Lookup(demo.example.com) = %v, want old id removed", ids)
	}
	if ids := file.CurrentHostIndex().Lookup("api.example.com"); !containsRepositoryHostID(ids, 31) {
		t.Fatalf("HostIndex.Lookup(api.example.com) = %v, want updated id present", ids)
	}
}

func containsRepositoryHostID(ids []int, want int) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func TestDefaultRepositoryClientOwnsTunnelDropsInvalidEntry(t *testing.T) {
	resetBackendTestDB(t)

	file.GetDb().JsonDb.Tasks.Store(21, "bad-tunnel-entry")

	if (defaultRepository{}).ClientOwnsTunnel(11, 21) {
		t.Fatal("ClientOwnsTunnel() = true for invalid task entry, want false")
	}
	if _, ok := file.GetDb().JsonDb.Tasks.Load(21); ok {
		t.Fatal("ClientOwnsTunnel() should remove invalid task entry")
	}
}

func TestDefaultRepositoryClientOwnsHostDropsInvalidEntry(t *testing.T) {
	resetBackendTestDB(t)

	file.GetDb().JsonDb.Hosts.Store(31, "bad-host-entry")

	if (defaultRepository{}).ClientOwnsHost(11, 31) {
		t.Fatal("ClientOwnsHost() = true for invalid host entry, want false")
	}
	if _, ok := file.GetDb().JsonDb.Hosts.Load(31); ok {
		t.Fatal("ClientOwnsHost() should remove invalid host entry")
	}
}
