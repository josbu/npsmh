package server

import (
	"sync"
	"testing"
	"time"

	"github.com/djylb/nps/lib/file"
)

func TestSortClientListUsesEffectiveLifecycleFields(t *testing.T) {
	list := []*file.Client{
		{Id: 1, FlowLimit: 3 * 1024 * 1024, ExpireAt: time.Date(2026, time.March, 23, 10, 0, 0, 0, time.UTC).Unix(), Flow: &file.Flow{}},
		{Id: 2, FlowLimit: 1 * 1024 * 1024, ExpireAt: time.Date(2026, time.March, 21, 10, 0, 0, 0, time.UTC).Unix(), Flow: &file.Flow{}},
		{Id: 3, Flow: &file.Flow{FlowLimit: 2, TimeLimit: time.Date(2026, time.March, 22, 10, 0, 0, 0, time.UTC)}},
	}

	SortClientList(list, "FlowLimit", "asc")
	if list[0].Id != 2 || list[1].Id != 3 || list[2].Id != 1 {
		t.Fatalf("SortClientList(flow_limit asc) ids = [%d %d %d], want [2 3 1]", list[0].Id, list[1].Id, list[2].Id)
	}

	SortClientList(list, "ExpireAt", "asc")
	if list[0].Id != 2 || list[1].Id != 3 || list[2].Id != 1 {
		t.Fatalf("SortClientList(expire_at asc) ids = [%d %d %d], want [2 3 1]", list[0].Id, list[1].Id, list[2].Id)
	}
}

func TestSortClientListDefaultsToIDAscending(t *testing.T) {
	list := []*file.Client{
		{Id: 9, Flow: &file.Flow{}},
		{Id: 3, Flow: &file.Flow{}},
		{Id: 5, Flow: &file.Flow{}},
	}

	SortClientList(list, "", "")
	if list[0].Id != 3 || list[1].Id != 5 || list[2].Id != 9 {
		t.Fatalf("SortClientList(default) ids = [%d %d %d], want [3 5 9]", list[0].Id, list[1].Id, list[2].Id)
	}
}

func TestSortClientListUsesIDAsTieBreakerForEqualValues(t *testing.T) {
	list := []*file.Client{
		{Id: 9, Remark: "same", Flow: &file.Flow{}},
		{Id: 3, Remark: "same", Flow: &file.Flow{}},
		{Id: 5, Remark: "same", Flow: &file.Flow{}},
	}

	SortClientList(list, "Remark", "asc")
	if list[0].Id != 3 || list[1].Id != 5 || list[2].Id != 9 {
		t.Fatalf("SortClientList(remark tie) ids = [%d %d %d], want [3 5 9]", list[0].Id, list[1].Id, list[2].Id)
	}
}

func TestGetTunnelWithoutClientOrTypeReturnsAllTunnels(t *testing.T) {
	restoreDB := setupServerJsonDB(t)
	oldBridge := Bridge
	Bridge = nil
	defer func() {
		restoreDB()
		Bridge = oldBridge
	}()

	clientA := &file.Client{Id: 11, VerifyKey: "vk-11", Status: true, Cnf: &file.Config{}, Flow: &file.Flow{}}
	clientB := &file.Client{Id: 12, VerifyKey: "vk-12", Status: true, Cnf: &file.Config{}, Flow: &file.Flow{}}
	if err := file.GetDb().NewClient(clientA); err != nil {
		t.Fatalf("NewClient(clientA) error = %v", err)
	}
	if err := file.GetDb().NewClient(clientB); err != nil {
		t.Fatalf("NewClient(clientB) error = %v", err)
	}
	if err := file.GetDb().NewTask(&file.Tunnel{Id: 21, Port: 18081, Mode: "tcp", Status: true, Client: clientA, Target: &file.Target{TargetStr: "127.0.0.1:80"}}); err != nil {
		t.Fatalf("NewTask(21) error = %v", err)
	}
	if err := file.GetDb().NewTask(&file.Tunnel{Id: 22, Port: 18082, Mode: "udp", Status: true, Client: clientB, Target: &file.Target{TargetStr: "127.0.0.1:81"}}); err != nil {
		t.Fatalf("NewTask(22) error = %v", err)
	}

	list, total := GetTunnel(0, 0, "", 0, "", "", "")
	if total != 2 || len(list) != 2 {
		t.Fatalf("GetTunnel() total=%d len=%d, want 2/2", total, len(list))
	}
}

func TestGetTunnelUsesRuntimeSnapshotWithoutMutatingStoredObjects(t *testing.T) {
	restoreDB := setupServerJsonDB(t)
	oldBridge := Bridge
	Bridge = nil
	defer func() {
		restoreDB()
		Bridge = oldBridge
	}()

	RunList = sync.Map{}
	defer func() {
		RunList = sync.Map{}
	}()

	clientA := &file.Client{Id: 21, VerifyKey: "vk-21", Status: true, Cnf: &file.Config{}, Flow: &file.Flow{}}
	clientB := &file.Client{Id: 22, VerifyKey: "vk-22", Status: true, Cnf: &file.Config{}, Flow: &file.Flow{}}
	if err := file.GetDb().NewClient(clientA); err != nil {
		t.Fatalf("NewClient(clientA) error = %v", err)
	}
	if err := file.GetDb().NewClient(clientB); err != nil {
		t.Fatalf("NewClient(clientB) error = %v", err)
	}
	taskA := &file.Tunnel{Id: 31, Port: 19081, Mode: "tcp", Status: true, Client: clientA, Target: &file.Target{TargetStr: "127.0.0.1:80"}}
	taskB := &file.Tunnel{Id: 32, Port: 19082, Mode: "tcp", Status: true, Client: clientB, Target: &file.Target{TargetStr: "127.0.0.1:81"}}
	if err := file.GetDb().NewTask(taskA); err != nil {
		t.Fatalf("NewTask(taskA) error = %v", err)
	}
	if err := file.GetDb().NewTask(taskB); err != nil {
		t.Fatalf("NewTask(taskB) error = %v", err)
	}
	RunList.Store(taskB.Id, struct{}{})

	list, total := GetTunnel(0, 0, "", 0, "", "RunStatus", "asc")
	if total != 2 || len(list) != 2 {
		t.Fatalf("GetTunnel() total=%d len=%d, want 2/2", total, len(list))
	}
	if list[0].Id != taskB.Id || !list[0].RunStatus {
		t.Fatalf("GetTunnel() first item = %+v, want task %d with RunStatus=true", list[0], taskB.Id)
	}
	if taskA.RunStatus || taskB.RunStatus {
		t.Fatalf("stored tasks were mutated: taskA.RunStatus=%v taskB.RunStatus=%v", taskA.RunStatus, taskB.RunStatus)
	}
	if taskA.Client.IsConnect || taskB.Client.IsConnect {
		t.Fatalf("stored clients were mutated: clientA.IsConnect=%v clientB.IsConnect=%v", taskA.Client.IsConnect, taskB.Client.IsConnect)
	}
}

func TestGetTunnelReturnsDetachedSnapshots(t *testing.T) {
	restoreDB := setupServerJsonDB(t)
	oldBridge := Bridge
	Bridge = nil
	defer func() {
		restoreDB()
		Bridge = oldBridge
	}()

	client := &file.Client{
		Id:        71,
		VerifyKey: "vk-71",
		Status:    true,
		RateLimit: 32,
		Cnf:       &file.Config{U: "tunnel-user"},
		Flow:      &file.Flow{},
	}
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	tunnel := &file.Tunnel{
		Id:        81,
		Port:      18081,
		Mode:      "tcp",
		Status:    true,
		RateLimit: 16,
		Client:    client,
		Flow:      &file.Flow{InletFlow: 5, ExportFlow: 6},
		Target:    &file.Target{TargetStr: "127.0.0.1:90", TargetArr: []string{"127.0.0.1:90"}},
		UserAuth:  &file.MultiAccount{Content: "user:pass", AccountMap: map[string]string{"user": "pass"}},
	}
	if err := file.GetDb().NewTask(tunnel); err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}

	list, total := GetTunnel(0, 0, "", 0, "", "", "")
	if total != 1 || len(list) != 1 {
		t.Fatalf("GetTunnel() total=%d len=%d, want 1/1", total, len(list))
	}

	list[0].Flow.InletFlow = 99
	list[0].Target.TargetStr = "mutated-target"
	list[0].Client.Cnf.U = "mutated-user"
	list[0].UserAuth.Content = "mutated-auth"
	list[0].UserAuth.AccountMap["user"] = "mutated-pass"

	stored, err := file.GetDb().GetTask(tunnel.Id)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if list[0].Rate == stored.Rate || list[0].ServiceMeter == stored.ServiceMeter {
		t.Fatal("GetTunnel() should detach tunnel runtime limiter and meter pointers")
	}
	if list[0].Client == nil || list[0].Client.Rate == nil || stored.Client == nil || stored.Client.Rate == nil {
		t.Fatal("GetTunnel() client snapshots should keep runtime limiters available")
	}
	if list[0].Client.Rate == stored.Client.Rate {
		t.Fatal("GetTunnel() should detach nested client runtime limiter pointers")
	}
	list[0].Rate.ResetLimit(0)
	if got := stored.Rate.Limit(); got != 16*1024 {
		t.Fatalf("stored tunnel rate limit after snapshot mutation = %d, want %d", got, 16*1024)
	}
	if stored.Flow == nil || stored.Flow.InletFlow != 5 {
		t.Fatalf("stored tunnel flow inlet = %d, want 5", stored.Flow.InletFlow)
	}
	if stored.Target == nil || stored.Target.TargetStr != "127.0.0.1:90" {
		t.Fatalf("stored tunnel target = %q, want %q", stored.Target.TargetStr, "127.0.0.1:90")
	}
	if stored.Client == nil || stored.Client.Cnf == nil || stored.Client.Cnf.U != "tunnel-user" {
		t.Fatalf("stored tunnel client username = %q, want %q", stored.Client.Cnf.U, "tunnel-user")
	}
	if stored.UserAuth == nil || stored.UserAuth.Content != "user:pass" || stored.UserAuth.AccountMap["user"] != "pass" {
		t.Fatalf("stored tunnel auth = %+v, want original credentials", stored.UserAuth)
	}
}

func TestGetTunnelDropsInvalidEntriesAndHandlesNilPointers(t *testing.T) {
	restore := setupServerJsonDB(t)
	defer restore()

	db := file.GetDb()
	client := &file.Client{Id: 101, VerifyKey: "vk-101", Status: true, Cnf: &file.Config{}, Flow: &file.Flow{}}
	if err := db.NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	validTask := &file.Tunnel{Id: 201, Port: 18201, Mode: "tcp", Status: true, Client: client, Target: &file.Target{TargetStr: "127.0.0.1:80"}, Flow: &file.Flow{}}
	if err := db.NewTask(validTask); err != nil {
		t.Fatalf("NewTask(validTask) error = %v", err)
	}

	taskNilTarget := &file.Tunnel{Id: 202, Port: 18202, Mode: "tcp", Status: true, Client: client, Flow: &file.Flow{}}
	db.JsonDb.Tasks.Store(taskNilTarget.Id, taskNilTarget)
	taskNilClient := &file.Tunnel{Id: 203, Port: 18203, Mode: "tcp", Status: true, Flow: &file.Flow{}}
	db.JsonDb.Tasks.Store(taskNilClient.Id, taskNilClient)

	db.JsonDb.Tasks.Store("bad-task-key", "invalid")
	db.JsonDb.Tasks.Store(204, "invalid")

	list, total := GetTunnel(0, 0, "", 0, "", "Target.TargetStr", "asc")
	if total != 3 || len(list) != 3 {
		t.Fatalf("GetTunnel() total=%d len=%d, want 3 surviving tasks", total, len(list))
	}
	if list[0].Id != taskNilTarget.Id || list[1].Id != taskNilClient.Id || list[2].Id != validTask.Id {
		t.Fatalf("GetTunnel() ids=%v, want [%d %d %d]", tunnelListIDs(list), taskNilTarget.Id, taskNilClient.Id, validTask.Id)
	}
	if _, ok := db.JsonDb.Tasks.Load("bad-task-key"); ok {
		t.Fatal("GetTunnel() should drop invalid task key entry")
	}
	if _, ok := db.JsonDb.Tasks.Load(204); ok {
		t.Fatal("GetTunnel() should drop invalid task value entry")
	}
}

func TestGetTunnelSearchMatchesClientVerifyKey(t *testing.T) {
	restore := setupServerJsonDB(t)
	defer restore()

	db := file.GetDb()
	clientA := &file.Client{Id: 301, VerifyKey: "vk-search-a", Status: true, Cnf: &file.Config{}, Flow: &file.Flow{}}
	clientB := &file.Client{Id: 302, VerifyKey: "vk-search-b", Status: true, Cnf: &file.Config{}, Flow: &file.Flow{}}
	if err := db.NewClient(clientA); err != nil {
		t.Fatalf("NewClient(clientA) error = %v", err)
	}
	if err := db.NewClient(clientB); err != nil {
		t.Fatalf("NewClient(clientB) error = %v", err)
	}
	if err := db.NewTask(&file.Tunnel{Id: 401, Port: 18401, Mode: "tcp", Status: true, Client: clientA, Remark: "alpha", Flow: &file.Flow{}, Target: &file.Target{TargetStr: "10.0.0.1:80"}}); err != nil {
		t.Fatalf("NewTask(401) error = %v", err)
	}
	if err := db.NewTask(&file.Tunnel{Id: 402, Port: 18402, Mode: "tcp", Status: true, Client: clientB, Remark: "beta", Flow: &file.Flow{}, Target: &file.Target{TargetStr: "10.0.0.2:80"}}); err != nil {
		t.Fatalf("NewTask(402) error = %v", err)
	}

	list, total := GetTunnel(0, 0, "", 0, "vk-search-b", "", "")
	if total != 1 || len(list) != 1 {
		t.Fatalf("GetTunnel(search by verify_key) total=%d len=%d, want 1/1", total, len(list))
	}
	if list[0].Id != 402 {
		t.Fatalf("GetTunnel(search by verify_key) first id=%d, want 402", list[0].Id)
	}
}

func TestGetTunnelByClientIDsDropsTouchedInvalidEntriesAndFiltersVisibleClients(t *testing.T) {
	restore := setupServerJsonDB(t)
	defer restore()

	db := file.GetDb()
	clientA := &file.Client{Id: 311, VerifyKey: "vk-client-a", Status: true, Cnf: &file.Config{}, Flow: &file.Flow{}}
	clientB := &file.Client{Id: 312, VerifyKey: "vk-client-b", Status: true, Cnf: &file.Config{}, Flow: &file.Flow{}}
	if err := db.NewClient(clientA); err != nil {
		t.Fatalf("NewClient(clientA) error = %v", err)
	}
	if err := db.NewClient(clientB); err != nil {
		t.Fatalf("NewClient(clientB) error = %v", err)
	}
	taskA := &file.Tunnel{Id: 411, Port: 18411, Mode: "tcp", Status: true, Client: clientA, Remark: "alpha", Flow: &file.Flow{}, Target: &file.Target{TargetStr: "10.0.1.1:80"}}
	taskB := &file.Tunnel{Id: 412, Port: 18412, Mode: "tcp", Status: true, Client: clientB, Remark: "beta", Flow: &file.Flow{}, Target: &file.Target{TargetStr: "10.0.1.2:80"}}
	taskInvalid := &file.Tunnel{Id: 413, Port: 18413, Mode: "tcp", Status: true, Client: clientB, Remark: "stale", Flow: &file.Flow{}, Target: &file.Target{TargetStr: "10.0.1.3:80"}}
	if err := db.NewTask(taskA); err != nil {
		t.Fatalf("NewTask(taskA) error = %v", err)
	}
	if err := db.NewTask(taskB); err != nil {
		t.Fatalf("NewTask(taskB) error = %v", err)
	}
	if err := db.NewTask(taskInvalid); err != nil {
		t.Fatalf("NewTask(taskInvalid) error = %v", err)
	}
	taskInvalid.Client = nil

	list, total := GetTunnelByClientIDs(0, 0, "tcp", []int{clientB.Id}, "vk-client-b", "Client.VerifyKey", "asc")
	if total != 1 || len(list) != 1 {
		t.Fatalf("GetTunnelByClientIDs() total=%d len=%d, want 1/1", total, len(list))
	}
	if list[0].Id != taskB.Id {
		t.Fatalf("GetTunnelByClientIDs() first id=%d, want %d", list[0].Id, taskB.Id)
	}
	if _, ok := db.JsonDb.Tasks.Load(taskInvalid.Id); ok {
		t.Fatal("GetTunnelByClientIDs() should drop indexed task entries whose client reference is now invalid")
	}
}

func TestSortTunnelAndHostListSupportsNowRate(t *testing.T) {
	tunnelSlow := &file.Tunnel{Id: 1, Flow: &file.Flow{}, Target: &file.Target{TargetStr: "127.0.0.1:80"}}
	tunnelFast := &file.Tunnel{Id: 2, Flow: &file.Flow{}, Target: &file.Target{TargetStr: "127.0.0.1:81"}}
	hostSlow := &file.Host{Id: 3, Flow: &file.Flow{}, Target: &file.Target{TargetStr: "127.0.0.1:82"}}
	hostFast := &file.Host{Id: 4, Flow: &file.Flow{}, Target: &file.Target{TargetStr: "127.0.0.1:83"}}

	tunnelSlow.EnsureRuntimeTraffic()
	tunnelFast.EnsureRuntimeTraffic()
	hostSlow.EnsureRuntimeTraffic()
	hostFast.EnsureRuntimeTraffic()

	time.Sleep(1100 * time.Millisecond)

	if err := tunnelSlow.ObserveServiceTraffic(0, 8*1024); err != nil {
		t.Fatalf("tunnelSlow.ObserveServiceTraffic() error = %v", err)
	}
	if err := tunnelFast.ObserveServiceTraffic(0, 64*1024); err != nil {
		t.Fatalf("tunnelFast.ObserveServiceTraffic() error = %v", err)
	}
	if err := hostSlow.ObserveServiceTraffic(0, 8*1024); err != nil {
		t.Fatalf("hostSlow.ObserveServiceTraffic() error = %v", err)
	}
	if err := hostFast.ObserveServiceTraffic(0, 64*1024); err != nil {
		t.Fatalf("hostFast.ObserveServiceTraffic() error = %v", err)
	}

	tunnels := []*file.Tunnel{tunnelFast, tunnelSlow}
	sortTunnelList(tunnels, "NowRate", "asc")
	if tunnels[0].Id != tunnelSlow.Id || tunnels[1].Id != tunnelFast.Id {
		t.Fatalf("sortTunnelList(now_rate asc) ids = [%d %d], want [%d %d]", tunnels[0].Id, tunnels[1].Id, tunnelSlow.Id, tunnelFast.Id)
	}
	sortTunnelList(tunnels, "NowRate", "desc")
	if tunnels[0].Id != tunnelFast.Id || tunnels[1].Id != tunnelSlow.Id {
		t.Fatalf("sortTunnelList(now_rate desc) ids = [%d %d], want [%d %d]", tunnels[0].Id, tunnels[1].Id, tunnelFast.Id, tunnelSlow.Id)
	}

	hosts := []*file.Host{hostFast, hostSlow}
	sortHostList(hosts, "NowRate", "asc")
	if hosts[0].Id != hostSlow.Id || hosts[1].Id != hostFast.Id {
		t.Fatalf("sortHostList(now_rate asc) ids = [%d %d], want [%d %d]", hosts[0].Id, hosts[1].Id, hostSlow.Id, hostFast.Id)
	}
	sortHostList(hosts, "NowRate", "desc")
	if hosts[0].Id != hostFast.Id || hosts[1].Id != hostSlow.Id {
		t.Fatalf("sortHostList(now_rate desc) ids = [%d %d], want [%d %d]", hosts[0].Id, hosts[1].Id, hostFast.Id, hostSlow.Id)
	}
}

func TestSortTunnelAndHostListDefaultsToAscendingWhenOrderEmpty(t *testing.T) {
	tunnels := []*file.Tunnel{
		{Id: 2, Port: 20002, Target: &file.Target{TargetStr: "127.0.0.1:82"}},
		{Id: 1, Port: 20001, Target: &file.Target{TargetStr: "127.0.0.1:81"}},
	}
	hosts := []*file.Host{
		{Id: 4, Host: "b.example.com", Target: &file.Target{TargetStr: "127.0.0.1:84"}},
		{Id: 3, Host: "a.example.com", Target: &file.Target{TargetStr: "127.0.0.1:83"}},
	}

	sortTunnelList(tunnels, "Port", "")
	if tunnels[0].Id != 1 || tunnels[1].Id != 2 {
		t.Fatalf("sortTunnelList(port, empty order) ids = [%d %d], want [1 2]", tunnels[0].Id, tunnels[1].Id)
	}

	sortHostList(hosts, "Host", "")
	if hosts[0].Id != 3 || hosts[1].Id != 4 {
		t.Fatalf("sortHostList(host, empty order) ids = [%d %d], want [3 4]", hosts[0].Id, hosts[1].Id)
	}
}

func TestSortHostListUsesIDAsTieBreakerForEqualValues(t *testing.T) {
	hosts := []*file.Host{
		{Id: 9, Host: "same.example.com", Flow: &file.Flow{}},
		{Id: 3, Host: "same.example.com", Flow: &file.Flow{}},
		{Id: 5, Host: "same.example.com", Flow: &file.Flow{}},
	}

	sortHostList(hosts, "Host", "asc")
	if hosts[0].Id != 3 || hosts[1].Id != 5 || hosts[2].Id != 9 {
		t.Fatalf("sortHostList(host tie) ids = [%d %d %d], want [3 5 9]", hosts[0].Id, hosts[1].Id, hosts[2].Id)
	}
}

func TestSortTunnelAndHostListFlowRemainKeepsUnlimitedLast(t *testing.T) {
	tunnelLow := &file.Tunnel{Id: 1, FlowLimit: 2 * 1024 * 1024, Flow: &file.Flow{InletFlow: 1 * 1024}, Target: &file.Target{TargetStr: "127.0.0.1:80"}}
	tunnelHigh := &file.Tunnel{Id: 2, FlowLimit: 3 * 1024 * 1024, Flow: &file.Flow{InletFlow: 1 * 1024}, Target: &file.Target{TargetStr: "127.0.0.1:81"}}
	tunnelUnlimited := &file.Tunnel{Id: 3, Flow: &file.Flow{InletFlow: 1 * 1024}, Target: &file.Target{TargetStr: "127.0.0.1:82"}}
	hostLow := &file.Host{Id: 4, FlowLimit: 2 * 1024 * 1024, Flow: &file.Flow{InletFlow: 1 * 1024}, Target: &file.Target{TargetStr: "127.0.0.1:83"}}
	hostHigh := &file.Host{Id: 5, FlowLimit: 3 * 1024 * 1024, Flow: &file.Flow{InletFlow: 1 * 1024}, Target: &file.Target{TargetStr: "127.0.0.1:84"}}
	hostUnlimited := &file.Host{Id: 6, Flow: &file.Flow{InletFlow: 1 * 1024}, Target: &file.Target{TargetStr: "127.0.0.1:85"}}

	tunnels := []*file.Tunnel{tunnelUnlimited, tunnelHigh, tunnelLow}
	sortTunnelList(tunnels, "FlowRemain", "asc")
	if tunnels[0].Id != tunnelLow.Id || tunnels[1].Id != tunnelHigh.Id || tunnels[2].Id != tunnelUnlimited.Id {
		t.Fatalf("sortTunnelList(flow_remain asc) ids = [%d %d %d], want [%d %d %d]", tunnels[0].Id, tunnels[1].Id, tunnels[2].Id, tunnelLow.Id, tunnelHigh.Id, tunnelUnlimited.Id)
	}
	sortTunnelList(tunnels, "FlowRemain", "desc")
	if tunnels[0].Id != tunnelHigh.Id || tunnels[1].Id != tunnelLow.Id || tunnels[2].Id != tunnelUnlimited.Id {
		t.Fatalf("sortTunnelList(flow_remain desc) ids = [%d %d %d], want [%d %d %d]", tunnels[0].Id, tunnels[1].Id, tunnels[2].Id, tunnelHigh.Id, tunnelLow.Id, tunnelUnlimited.Id)
	}

	hosts := []*file.Host{hostUnlimited, hostHigh, hostLow}
	sortHostList(hosts, "FlowRemain", "asc")
	if hosts[0].Id != hostLow.Id || hosts[1].Id != hostHigh.Id || hosts[2].Id != hostUnlimited.Id {
		t.Fatalf("sortHostList(flow_remain asc) ids = [%d %d %d], want [%d %d %d]", hosts[0].Id, hosts[1].Id, hosts[2].Id, hostLow.Id, hostHigh.Id, hostUnlimited.Id)
	}
	sortHostList(hosts, "FlowRemain", "desc")
	if hosts[0].Id != hostHigh.Id || hosts[1].Id != hostLow.Id || hosts[2].Id != hostUnlimited.Id {
		t.Fatalf("sortHostList(flow_remain desc) ids = [%d %d %d], want [%d %d %d]", hosts[0].Id, hosts[1].Id, hosts[2].Id, hostHigh.Id, hostLow.Id, hostUnlimited.Id)
	}
}

func TestSortTunnelAndHostListUnknownFieldFallsBackToIDAscending(t *testing.T) {
	tunnels := []*file.Tunnel{
		{Id: 9, Target: &file.Target{TargetStr: "127.0.0.1:80"}},
		{Id: 3, Target: &file.Target{TargetStr: "127.0.0.1:81"}},
		{Id: 5, Target: &file.Target{TargetStr: "127.0.0.1:82"}},
	}
	hosts := []*file.Host{
		{Id: 8, Target: &file.Target{TargetStr: "127.0.0.1:83"}},
		{Id: 2, Target: &file.Target{TargetStr: "127.0.0.1:84"}},
		{Id: 6, Target: &file.Target{TargetStr: "127.0.0.1:85"}},
	}

	sortTunnelList(tunnels, "UnknownField", "asc")
	if tunnels[0].Id != 3 || tunnels[1].Id != 5 || tunnels[2].Id != 9 {
		t.Fatalf("sortTunnelList(unknown) ids = [%d %d %d], want [3 5 9]", tunnels[0].Id, tunnels[1].Id, tunnels[2].Id)
	}

	sortHostList(hosts, "UnknownField", "asc")
	if hosts[0].Id != 2 || hosts[1].Id != 6 || hosts[2].Id != 8 {
		t.Fatalf("sortHostList(unknown) ids = [%d %d %d], want [2 6 8]", hosts[0].Id, hosts[1].Id, hosts[2].Id)
	}
}

func tunnelListIDs(list []*file.Tunnel) []int {
	ids := make([]int, 0, len(list))
	for _, item := range list {
		if item != nil {
			ids = append(ids, item.Id)
		}
	}
	return ids
}
