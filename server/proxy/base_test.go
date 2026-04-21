package proxy

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/rate"
)

func TestIn(t *testing.T) {
	tests := []struct {
		name   string
		target string
		list   []string
		want   bool
	}{
		{name: "finds existing element", target: "b", list: []string{"c", "a", "b"}, want: true},
		{name: "returns false for missing element", target: "d", list: []string{"c", "a", "b"}, want: false},
		{name: "handles empty slice", target: "a", list: []string{}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := in(tt.target, tt.list); got != tt.want {
				t.Fatalf("in(%q, %v) = %v, want %v", tt.target, tt.list, got, tt.want)
			}
		})
	}
}

func TestInDoesNotMutateInputOrder(t *testing.T) {
	list := []string{"c", "a", "b"}
	original := append([]string(nil), list...)

	if !in("b", list) {
		t.Fatal("in() should still find existing element")
	}
	if strings.Join(list, ",") != strings.Join(original, ",") {
		t.Fatalf("in() mutated input slice order: got %v want %v", list, original)
	}
}

func TestCheckFlowAndConnNum(t *testing.T) {
	s := &BaseServer{}

	t.Run("expired service", func(t *testing.T) {
		client := &file.Client{Flow: &file.Flow{TimeLimit: time.Now().Add(-time.Second)}}
		_, err := s.CheckFlowAndConnNum(client, nil, nil)
		if err == nil || err.Error() != "service access expired" {
			t.Fatalf("CheckFlowAndConnNum() error = %v, want service access expired", err)
		}
	})

	t.Run("traffic limit exceeded", func(t *testing.T) {
		client := &file.Client{Flow: &file.Flow{FlowLimit: 1, ExportFlow: 1 << 20, InletFlow: 1}, MaxConn: 10}
		_, err := s.CheckFlowAndConnNum(client, nil, nil)
		if err == nil || err.Error() != "traffic limit exceeded" {
			t.Fatalf("CheckFlowAndConnNum() error = %v, want traffic limit exceeded", err)
		}
	})

	t.Run("connection limit exceeded", func(t *testing.T) {
		client := &file.Client{Flow: &file.Flow{}, MaxConn: 1, NowConn: 1}
		_, err := s.CheckFlowAndConnNum(client, nil, nil)
		if err == nil || err.Error() != "connection limit exceeded" {
			t.Fatalf("CheckFlowAndConnNum() error = %v, want connection limit exceeded", err)
		}
	})

	t.Run("success increments connection count", func(t *testing.T) {
		client := &file.Client{Flow: &file.Flow{}, MaxConn: 2, NowConn: 0}
		lease, err := s.CheckFlowAndConnNum(client, nil, nil)
		if err != nil {
			t.Fatalf("CheckFlowAndConnNum() unexpected error = %v", err)
		}
		if client.NowConn != 1 {
			t.Fatalf("client.NowConn = %d, want 1", client.NowConn)
		}
		lease.Release()
		if client.NowConn != 0 {
			t.Fatalf("client.NowConn after release = %d, want 0", client.NowConn)
		}
	})
}

func TestFlowAddAndFlowAddHost(t *testing.T) {
	taskFlow := &file.Flow{}
	hostFlow := &file.Flow{}
	s := &BaseServer{Task: &file.Tunnel{Flow: taskFlow}}
	h := &file.Host{Flow: hostFlow}

	s.FlowAdd(10, 20)
	s.FlowAddHost(h, 30, 40)

	if taskFlow.InletFlow != 10 || taskFlow.ExportFlow != 20 {
		t.Fatalf("task flow mismatch: inlet=%d export=%d", taskFlow.InletFlow, taskFlow.ExportFlow)
	}
	if hostFlow.InletFlow != 30 || hostFlow.ExportFlow != 40 {
		t.Fatalf("host flow mismatch: inlet=%d export=%d", hostFlow.InletFlow, hostFlow.ExportFlow)
	}
}

func TestAuth(t *testing.T) {
	t.Run("auth success", func(t *testing.T) {
		s := &BaseServer{}
		r := httptest.NewRequest("GET", "http://example.com", nil)
		r.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("user:pass")))

		if err := s.Auth(r, nil, "user", "pass", nil, nil); err != nil {
			t.Fatalf("Auth() unexpected error = %v", err)
		}
	})

	t.Run("auth failure writes unauthorized bytes and closes conn", func(t *testing.T) {
		s := &BaseServer{}
		r := httptest.NewRequest("GET", "http://example.com", nil)
		r.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("bad:creds")))

		serverSide, clientSide := net.Pipe()
		defer func() { _ = clientSide.Close() }()
		c := conn.NewConn(serverSide)
		errCh := make(chan error, 1)
		go func() {
			errCh <- s.Auth(r, c, "user", "pass", nil, nil)
		}()

		buf := make([]byte, len(common.UnauthorizedBytes))
		if _, err := io.ReadFull(clientSide, buf); err != nil {
			t.Fatalf("Read() unauthorized bytes error = %v", err)
		}
		if got := string(buf); got != common.UnauthorizedBytes {
			t.Fatalf("unauthorized bytes = %q, want %q", got, common.UnauthorizedBytes)
		}

		err := <-errCh
		if !errors.Is(err, errProxyUnauthorized) {
			t.Fatalf("Auth() error = %v, want %v", err, errProxyUnauthorized)
		}
	})
}

func TestWriteConnFail(t *testing.T) {
	s := &BaseServer{ErrorContent: []byte("detail")}
	serverSide, clientSide := net.Pipe()
	defer func() { _ = clientSide.Close() }()
	go s.writeConnFail(serverSide)

	buf := make([]byte, len(common.ConnectionFailBytes)+len(s.ErrorContent))
	if _, err := io.ReadFull(clientSide, buf); err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	want := common.ConnectionFailBytes + "detail"
	if got := string(buf); got != want {
		t.Fatalf("writeConnFail() bytes = %q, want %q", got, want)
	}

	_ = serverSide.Close()
}

func TestCheckFlowAndConnNumRejectsTrafficLimitBoundary(t *testing.T) {
	s := &BaseServer{}
	client := &file.Client{
		Flow:          &file.Flow{FlowLimit: 1, ExportFlow: 1 << 20, InletFlow: 0},
		BridgeTraffic: &file.TrafficStats{ExportBytes: 1 << 20},
		MaxConn:       2,
	}

	lease, err := s.CheckFlowAndConnNum(client, nil, nil)
	if err == nil || err.Error() != "traffic limit exceeded" {
		t.Fatalf("CheckFlowAndConnNum() error = %v, want traffic limit exceeded", err)
	}
	if lease != nil {
		lease.Release()
	}
}

func TestCheckFlowAndConnNumAllowsUnownedClient(t *testing.T) {
	s := &BaseServer{}
	client := &file.Client{
		Id:      17,
		Status:  true,
		Flow:    &file.Flow{},
		MaxConn: 1,
	}

	lease, err := s.CheckFlowAndConnNum(client, nil, nil)
	if err != nil {
		t.Fatalf("CheckFlowAndConnNum() error = %v, want nil for unowned client", err)
	}
	if client.NowConn != 1 {
		t.Fatalf("client.NowConn = %d, want 1", client.NowConn)
	}
	lease.Release()
}

func TestAuthWithMultiAccount(t *testing.T) {
	s := &BaseServer{}
	r := httptest.NewRequest("GET", "http://example.com", nil)
	r.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("worker:p@ss")))
	multi := &file.MultiAccount{AccountMap: map[string]string{"worker": "p@ss"}}

	if err := s.Auth(r, nil, "", "", multi, nil); err != nil {
		t.Fatalf("Auth() with multi-account unexpected error = %v", err)
	}
}

func TestAuthWithMultiAccountContentFallback(t *testing.T) {
	s := &BaseServer{}
	r := httptest.NewRequest("GET", "http://example.com", nil)
	r.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("worker:p@ss")))
	multi := &file.MultiAccount{Content: "worker=p@ss\n"}

	if err := s.Auth(r, nil, "", "", multi, nil); err != nil {
		t.Fatalf("Auth() with content-only multi-account unexpected error = %v", err)
	}
}

func TestWriteConnFailWithNilErrorContent(t *testing.T) {
	s := &BaseServer{}
	serverSide, clientSide := net.Pipe()
	defer func() { _ = clientSide.Close() }()
	go s.writeConnFail(serverSide)

	buf := make([]byte, len(common.ConnectionFailBytes))
	if _, err := io.ReadFull(clientSide, buf); err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if got := string(buf); !strings.HasPrefix(got, common.ConnectionFailBytes) {
		t.Fatalf("writeConnFail() bytes = %q, want prefix %q", got, common.ConnectionFailBytes)
	}

	_ = serverSide.Close()
}

func TestCheckFlowAndConnNumUsesEffectiveClientLifecycleFields(t *testing.T) {
	s := &BaseServer{}

	t.Run("client expire_at is enforced", func(t *testing.T) {
		client := &file.Client{
			ExpireAt: time.Now().Add(-time.Second).Unix(),
			Flow:     &file.Flow{},
			MaxConn:  1,
		}
		_, err := s.CheckFlowAndConnNum(client, nil, nil)
		if err == nil || err.Error() != "service access expired" {
			t.Fatalf("CheckFlowAndConnNum() error = %v, want service access expired", err)
		}
	})

	t.Run("client flow_limit bytes are enforced", func(t *testing.T) {
		client := &file.Client{
			FlowLimit: 1024,
			Flow: &file.Flow{
				ExportFlow: 1024,
				InletFlow:  1,
			},
			MaxConn: 1,
		}
		_, err := s.CheckFlowAndConnNum(client, nil, nil)
		if err == nil || err.Error() != "traffic limit exceeded" {
			t.Fatalf("CheckFlowAndConnNum() error = %v, want traffic limit exceeded", err)
		}
	})
}

func TestCheckFlowAndConnNumAcquiresHierarchicalLease(t *testing.T) {
	s := &BaseServer{}
	user := &file.User{
		Id:             3,
		Status:         1,
		TotalFlow:      &file.Flow{},
		MaxConnections: 2,
	}
	file.InitializeUserRuntime(user)
	client := &file.Client{
		Id:      7,
		UserId:  user.Id,
		Status:  true,
		Flow:    &file.Flow{},
		MaxConn: 2,
	}
	client.BindOwnerUser(user)
	tunnel := &file.Tunnel{
		Id:      9,
		Flow:    &file.Flow{},
		MaxConn: 2,
	}
	file.InitializeTunnelRuntime(tunnel)

	lease, err := s.CheckFlowAndConnNum(client, tunnel, nil)
	if err != nil {
		t.Fatalf("CheckFlowAndConnNum() error = %v", err)
	}
	if user.NowConn != 1 || client.NowConn != 1 || tunnel.NowConn != 1 {
		t.Fatalf("hierarchical counts = user:%d client:%d tunnel:%d, want 1/1/1", user.NowConn, client.NowConn, tunnel.NowConn)
	}

	lease.Release()
	if user.NowConn != 0 || client.NowConn != 0 || tunnel.NowConn != 0 {
		t.Fatalf("hierarchical counts after release = user:%d client:%d tunnel:%d, want 0/0/0", user.NowConn, client.NowConn, tunnel.NowConn)
	}
}

func TestCheckFlowAndConnNumRejectsResourceConnectionLimit(t *testing.T) {
	s := &BaseServer{}
	client := &file.Client{
		Id:      7,
		Status:  true,
		Flow:    &file.Flow{},
		MaxConn: 2,
	}
	tunnel := &file.Tunnel{
		Id:      9,
		Flow:    &file.Flow{},
		MaxConn: 1,
		NowConn: 1,
	}
	file.InitializeTunnelRuntime(tunnel)

	_, err := s.CheckFlowAndConnNum(client, tunnel, nil)
	if err != errProxyConnectionLimit {
		t.Fatalf("CheckFlowAndConnNum() error = %v, want %v", err, errProxyConnectionLimit)
	}
}

func TestServiceRateLimiterBuildsHierarchicalLimiter(t *testing.T) {
	clientRate := rate.NewRate(64 * 1024)
	clientRate.Start()
	user := &file.User{
		Id:         3,
		Status:     1,
		RateLimit:  128,
		TotalFlow:  &file.Flow{},
		MaxClients: 0,
	}
	file.InitializeUserRuntime(user)
	client := &file.Client{
		Id:      7,
		UserId:  3,
		Flow:    &file.Flow{},
		MaxConn: 1,
		Rate:    clientRate,
	}

	oldStore := file.GlobalStore
	file.GlobalStore = proxyTestStore{
		user:    user,
		clients: []*file.Client{client},
	}
	defer func() {
		file.GlobalStore = oldStore
	}()

	tunnelRate := rate.NewRate(32 * 1024)
	tunnelRate.Start()
	tunnel := &file.Tunnel{
		Id:        11,
		Flow:      &file.Flow{},
		RateLimit: 32,
		Rate:      tunnelRate,
	}
	limiter := (&BaseServer{}).ServiceRateLimiter(client, tunnel, nil)
	if _, ok := limiter.(*rate.HierarchicalLimiter); !ok {
		t.Fatalf("ServiceRateLimiter() type = %T, want *rate.HierarchicalLimiter", limiter)
	}
}

func TestResolveClientUserCachesOwnerUser(t *testing.T) {
	user := &file.User{
		Id:        3,
		Status:    1,
		RateLimit: 64,
		TotalFlow: &file.Flow{},
	}
	file.InitializeUserRuntime(user)
	client := &file.Client{
		Id:      7,
		UserId:  3,
		Flow:    &file.Flow{},
		MaxConn: 1,
	}

	oldStore := file.GlobalStore
	file.GlobalStore = proxyTestStore{
		user:    user,
		clients: []*file.Client{client},
	}
	defer func() {
		file.GlobalStore = oldStore
	}()

	resolved := resolveClientUser(client)
	if resolved == nil || resolved.Id != user.Id {
		t.Fatalf("resolveClientUser() = %+v, want user %d", resolved, user.Id)
	}

	file.GlobalStore = proxyTestStore{}
	cached := resolveClientUser(client)
	if cached == nil || cached.Id != user.Id {
		t.Fatalf("resolveClientUser() cached = %+v, want user %d", cached, user.Id)
	}
}

func TestClientSourceAccessDeniedIncludesUserAndTunnelEntryACL(t *testing.T) {
	user := &file.User{
		Id:            3,
		Status:        1,
		TotalFlow:     &file.Flow{},
		EntryAclMode:  file.AclWhitelist,
		EntryAclRules: "192.168.0.0/16",
	}
	file.InitializeUserRuntime(user)
	client := &file.Client{
		Id:            7,
		UserId:        3,
		Flow:          &file.Flow{},
		MaxConn:       1,
		EntryAclMode:  file.AclBlacklist,
		EntryAclRules: "10.0.0.0/8",
	}
	client.CompileSourcePolicy()
	task := &file.Tunnel{
		Id:            8,
		EntryAclMode:  file.AclWhitelist,
		EntryAclRules: "192.168.1.0/24",
	}
	task.CompileEntryACL()

	oldStore := file.GlobalStore
	file.GlobalStore = proxyTestStore{
		user:    user,
		clients: []*file.Client{client},
	}
	defer func() {
		file.GlobalStore = oldStore
	}()

	if !isClientSourceAccessDenied(client, task, "10.0.0.8:443") {
		t.Fatal("client blacklist should deny matching source")
	}
	if !isClientSourceAccessDenied(client, task, "172.16.0.8:443") {
		t.Fatal("user whitelist should deny non-matching source")
	}
	if !isClientSourceAccessDenied(client, task, "192.168.2.8:443") {
		t.Fatal("tunnel whitelist should deny non-matching source")
	}
	if isClientSourceAccessDenied(client, task, "192.168.1.8:443") {
		t.Fatal("matching user+tunnel whitelist should allow source")
	}
}

func TestHostSourceAccessDeniedIncludesHostEntryACL(t *testing.T) {
	user := &file.User{
		Id:            5,
		Status:        1,
		TotalFlow:     &file.Flow{},
		EntryAclMode:  file.AclWhitelist,
		EntryAclRules: "2001:db8::/32",
	}
	file.InitializeUserRuntime(user)
	client := &file.Client{
		Id:      9,
		UserId:  5,
		Flow:    &file.Flow{},
		MaxConn: 1,
	}
	host := &file.Host{
		Id:            11,
		Host:          "demo.example.com",
		Client:        client,
		EntryAclMode:  file.AclWhitelist,
		EntryAclRules: "2001:db8:1::/48",
	}
	host.CompileEntryACL()

	oldStore := file.GlobalStore
	file.GlobalStore = proxyTestStore{
		user:    user,
		clients: []*file.Client{client},
	}
	defer func() {
		file.GlobalStore = oldStore
	}()

	if !IsHostSourceAccessDenied(host, "[2001:db9::1]:443") {
		t.Fatal("user whitelist should deny unmatched IPv6 source")
	}
	if !IsHostSourceAccessDenied(host, "[2001:db8:2::1]:443") {
		t.Fatal("host whitelist should deny unmatched IPv6 source")
	}
	if IsHostSourceAccessDenied(host, "[2001:db8:1::1]:443") {
		t.Fatal("matching IPv6 source should be allowed")
	}
}

func TestClientDestinationAccessDeniedIncludesUserAndTunnelACL(t *testing.T) {
	user := &file.User{
		Id:           3,
		Status:       1,
		TotalFlow:    &file.Flow{},
		DestAclMode:  file.AclWhitelist,
		DestAclRules: "full:db.internal.example\nfull:blocked.internal.example",
	}
	file.InitializeUserRuntime(user)
	client := &file.Client{
		Id:     7,
		UserId: 3,
		Flow:   &file.Flow{},
	}
	client.BindOwnerUser(user)
	task := &file.Tunnel{
		Id:           8,
		Mode:         "mixProxy",
		DestAclMode:  file.AclBlacklist,
		DestAclRules: "full:blocked.internal.example",
	}
	task.CompileDestACL()

	if !IsClientDestinationAccessDenied(client, task, "api.public.example:443") {
		t.Fatal("user destination whitelist should deny unmatched host")
	}
	if !IsClientDestinationAccessDenied(client, task, "blocked.internal.example:443") {
		t.Fatal("tunnel destination blacklist should deny matched host")
	}
	if IsClientDestinationAccessDenied(client, task, "db.internal.example:443") {
		t.Fatal("matching user destination whitelist and non-blocked tunnel should allow host")
	}
}

func TestClientDestinationAccessDeniedNonMixProxyUsesIPOnlyRules(t *testing.T) {
	user := &file.User{
		Id:           3,
		Status:       1,
		TotalFlow:    &file.Flow{},
		DestAclMode:  file.AclWhitelist,
		DestAclRules: "full:db.internal.example\n10.0.0.0/8",
	}
	file.InitializeUserRuntime(user)
	client := &file.Client{
		Id:     7,
		UserId: 3,
		Flow:   &file.Flow{},
	}
	client.BindOwnerUser(user)
	task := &file.Tunnel{
		Id:           8,
		Mode:         "tcp",
		DestAclMode:  file.AclBlacklist,
		DestAclRules: "192.168.0.0/16",
	}
	task.CompileDestACL()

	if IsClientDestinationAccessDenied(client, task, "10.1.2.3:443") {
		t.Fatal("non-mixProxy should still allow matching IP destination rules")
	}
	if !IsClientDestinationAccessDenied(client, task, "db.internal.example:443") {
		t.Fatal("non-mixProxy should ignore domain rules and deny unmatched IP destination")
	}
	if !IsClientDestinationAccessDenied(client, task, "192.168.1.8:443") {
		t.Fatal("non-mixProxy should apply tunnel IP blacklist")
	}
}

func TestHasActiveDestinationACLIncludesUser(t *testing.T) {
	user := &file.User{
		Id:           3,
		Status:       1,
		TotalFlow:    &file.Flow{},
		DestAclMode:  file.AclBlacklist,
		DestAclRules: "10.0.0.0/8",
	}
	file.InitializeUserRuntime(user)
	client := &file.Client{
		Id:     7,
		UserId: 3,
		Flow:   &file.Flow{},
	}
	client.BindOwnerUser(user)

	if !HasActiveDestinationACL(client, nil) {
		t.Fatal("active user destination ACL should make destination ACL guard active")
	}
}

func TestCheckFlowAndConnNumUsesCanonicalTunnelCounterForRuntimeRouteView(t *testing.T) {
	s := &BaseServer{}
	client := &file.Client{
		Id:      7,
		Status:  true,
		Flow:    &file.Flow{},
		MaxConn: 2,
	}
	tunnel := &file.Tunnel{
		Id:      9,
		Flow:    &file.Flow{},
		MaxConn: 1,
		Target:  &file.Target{TargetStr: "base:9"},
	}
	file.InitializeTunnelRuntime(tunnel)
	tunnel.BindRuntimeOwner("node-a", &file.Tunnel{Target: &file.Target{TargetStr: "owner-a:9"}})

	selected := tunnel.SelectRuntimeRoute()
	if selected == nil {
		t.Fatal("SelectRuntimeRoute() = nil")
	}

	lease, err := s.CheckFlowAndConnNum(client, selected, nil)
	if err != nil {
		t.Fatalf("CheckFlowAndConnNum() error = %v", err)
	}
	if tunnel.NowConn != 1 {
		t.Fatalf("canonical tunnel NowConn = %d, want 1", tunnel.NowConn)
	}
	lease.Release()
	if tunnel.NowConn != 0 {
		t.Fatalf("canonical tunnel NowConn after release = %d, want 0", tunnel.NowConn)
	}
}

func TestCheckFlowAndConnNumUsesCanonicalHostCounterForRuntimeRouteView(t *testing.T) {
	s := &BaseServer{}
	client := &file.Client{
		Id:      7,
		Status:  true,
		Flow:    &file.Flow{},
		MaxConn: 2,
	}
	host := &file.Host{
		Id:      10,
		Flow:    &file.Flow{},
		MaxConn: 1,
		Target:  &file.Target{TargetStr: "base:10"},
	}
	file.InitializeHostRuntime(host)
	host.BindRuntimeOwner("node-a", &file.Host{Target: &file.Target{TargetStr: "owner-a:10"}})

	selected := host.SelectRuntimeRoute()
	if selected == nil {
		t.Fatal("SelectRuntimeRoute() = nil")
	}

	lease, err := s.CheckFlowAndConnNum(client, nil, selected)
	if err != nil {
		t.Fatalf("CheckFlowAndConnNum() error = %v", err)
	}
	if host.NowConn != 1 {
		t.Fatalf("canonical host NowConn = %d, want 1", host.NowConn)
	}
	lease.Release()
	if host.NowConn != 0 {
		t.Fatalf("canonical host NowConn after release = %d, want 0", host.NowConn)
	}
}

type proxyRouteRuntimeCollectorStub struct {
	addCalls      []string
	cutCalls      []string
	bridgeTotals  [2]int64
	serviceTotals [2]int64
}

type proxyBoundNodeRuntimeStub struct {
	addCount      int
	cutCount      int
	bridgeTotals  [2]int64
	serviceTotals [2]int64
}

type proxyRouteRuntimeBindingCollectorStub struct {
	proxyRouteRuntimeCollectorStub
	nodes     map[string]*proxyBoundNodeRuntimeStub
	loadCalls []string
}

type proxyRouteRuntimeBindingBridgeLinkOpenerStub struct {
	*proxyRouteRuntimeBindingCollectorStub
}

type proxyLinkOpenerNodeCountStub struct {
	count  int
	rotate bool
}

type proxyLinkOpenerMultiNodeStub struct {
	multi  bool
	rotate bool
}

type proxyTypedNilLinkOpenerStub struct{}
type proxyTypedNilRouteConnStub struct{}

func (s proxyLinkOpenerNodeCountStub) SendLinkInfo(int, *conn.Link, *file.Tunnel) (net.Conn, error) {
	return nil, nil
}

func (s proxyLinkOpenerNodeCountStub) ClientOnlineNodeCount(int) int {
	return s.count
}

func (s proxyLinkOpenerNodeCountStub) ClientSelectionCanRotate() bool {
	return s.rotate
}

func (s proxyLinkOpenerMultiNodeStub) SendLinkInfo(int, *conn.Link, *file.Tunnel) (net.Conn, error) {
	return nil, nil
}

func (s proxyLinkOpenerMultiNodeStub) ClientHasMultipleOnlineNodes(int) bool {
	return s.multi
}

func (s proxyLinkOpenerMultiNodeStub) ClientSelectionCanRotate() bool {
	return s.rotate
}

func (s *proxyTypedNilLinkOpenerStub) SendLinkInfo(int, *conn.Link, *file.Tunnel) (net.Conn, error) {
	panic("unexpected SendLinkInfo call on typed nil opener")
}

func (s *proxyTypedNilLinkOpenerStub) ClientOnlineNodeCount(int) int {
	panic("unexpected ClientOnlineNodeCount call on typed nil opener")
}

func (s *proxyTypedNilLinkOpenerStub) ClientSelectionCanRotate() bool {
	panic("unexpected ClientSelectionCanRotate call on typed nil opener")
}

func (s *proxyTypedNilLinkOpenerStub) SelectClientRouteUUID(int) string {
	panic("unexpected SelectClientRouteUUID call on typed nil opener")
}

func (s *proxyTypedNilLinkOpenerStub) AddClientNodeConn(int, string) {
	panic("unexpected AddClientNodeConn call on typed nil opener")
}

func (s *proxyTypedNilLinkOpenerStub) CutClientNodeConn(int, string) {
	panic("unexpected CutClientNodeConn call on typed nil opener")
}

func (s *proxyTypedNilLinkOpenerStub) ObserveClientNodeBridgeTraffic(int, string, int64, int64) {
	panic("unexpected ObserveClientNodeBridgeTraffic call on typed nil opener")
}

func (s *proxyTypedNilLinkOpenerStub) ObserveClientNodeServiceTraffic(int, string, int64, int64) {
	panic("unexpected ObserveClientNodeServiceTraffic call on typed nil opener")
}

func (s *proxyTypedNilRouteConnStub) Read([]byte) (int, error) {
	panic("unexpected Read call on typed nil conn")
}

func (s *proxyTypedNilRouteConnStub) Write([]byte) (int, error) {
	panic("unexpected Write call on typed nil conn")
}

func (s *proxyTypedNilRouteConnStub) Close() error {
	panic("unexpected Close call on typed nil conn")
}

func (s *proxyTypedNilRouteConnStub) LocalAddr() net.Addr {
	panic("unexpected LocalAddr call on typed nil conn")
}

func (s *proxyTypedNilRouteConnStub) RemoteAddr() net.Addr {
	panic("unexpected RemoteAddr call on typed nil conn")
}

func (s *proxyTypedNilRouteConnStub) SetDeadline(time.Time) error {
	panic("unexpected SetDeadline call on typed nil conn")
}

func (s *proxyTypedNilRouteConnStub) SetReadDeadline(time.Time) error {
	panic("unexpected SetReadDeadline call on typed nil conn")
}

func (s *proxyTypedNilRouteConnStub) SetWriteDeadline(time.Time) error {
	panic("unexpected SetWriteDeadline call on typed nil conn")
}

func (s *proxyTypedNilRouteConnStub) RuntimeRouteUUID() string {
	panic("unexpected RuntimeRouteUUID call on typed nil conn")
}

func (s *proxyRouteRuntimeCollectorStub) AddClientNodeConn(clientID int, uuid string) {
	s.addCalls = append(s.addCalls, fmt.Sprintf("%d:%s", clientID, uuid))
}

func (s *proxyRouteRuntimeCollectorStub) CutClientNodeConn(clientID int, uuid string) {
	s.cutCalls = append(s.cutCalls, fmt.Sprintf("%d:%s", clientID, uuid))
}

func (s *proxyRouteRuntimeCollectorStub) ObserveClientNodeBridgeTraffic(_ int, _ string, in, out int64) {
	s.bridgeTotals[0] += in
	s.bridgeTotals[1] += out
}

func (s *proxyRouteRuntimeCollectorStub) ObserveClientNodeServiceTraffic(_ int, _ string, in, out int64) {
	s.serviceTotals[0] += in
	s.serviceTotals[1] += out
}

func (s *proxyBoundNodeRuntimeStub) AddConn() {
	s.addCount++
}

func (s *proxyBoundNodeRuntimeStub) CutConn() {
	s.cutCount++
}

func (s *proxyBoundNodeRuntimeStub) ObserveBridgeTraffic(in, out int64) {
	s.bridgeTotals[0] += in
	s.bridgeTotals[1] += out
}

func (s *proxyBoundNodeRuntimeStub) ObserveServiceTraffic(in, out int64) {
	s.serviceTotals[0] += in
	s.serviceTotals[1] += out
}

func (s *proxyRouteRuntimeBindingCollectorStub) LoadClientNodeRuntime(clientID int, uuid string) any {
	s.loadCalls = append(s.loadCalls, fmt.Sprintf("%d:%s", clientID, uuid))
	if s.nodes == nil {
		return nil
	}
	return s.nodes[uuid]
}

func (s proxyRouteRuntimeBindingBridgeLinkOpenerStub) SendLinkInfo(int, *conn.Link, *file.Tunnel) (net.Conn, error) {
	return nil, nil
}

func TestProxyLimitRuntimeTracksRouteConnectionsWithoutAffectingSharedLimits(t *testing.T) {
	client := &file.Client{
		Id:      7,
		Status:  true,
		Flow:    &file.Flow{},
		MaxConn: 2,
	}
	tunnel := &file.Tunnel{
		Id:      9,
		Flow:    &file.Flow{},
		MaxConn: 2,
		Target:  &file.Target{TargetStr: "base:9"},
	}
	file.InitializeTunnelRuntime(tunnel)
	tunnel.BindRuntimeOwner("node-a", &file.Tunnel{Target: &file.Target{TargetStr: "owner-a:9"}})
	selected := tunnel.SelectRuntimeRoute()
	if selected == nil {
		t.Fatal("SelectRuntimeRoute() = nil")
	}

	collector := &proxyRouteRuntimeCollectorStub{}
	runtime := proxyLimitRuntime{
		now:        time.Now,
		userQuota:  proxyRateLimiterUserQuotaStub{},
		clientLife: proxyClientLifecycleRuntime{},
		rateLimits: proxyRateLimitRuntime{},
	}

	lease, err := runtime.CheckFlowAndConnNum(client, selected, nil)
	if err != nil {
		t.Fatalf("CheckFlowAndConnNum() error = %v", err)
	}
	if len(collector.addCalls) != 0 {
		t.Fatalf("route add calls = %#v, want nil; runtime instance conn count should track actual bridge links only", collector.addCalls)
	}
	if client.NowConn != 1 || tunnel.NowConn != 1 {
		t.Fatalf("shared limits should still use canonical client/tunnel counters, got client=%d tunnel=%d", client.NowConn, tunnel.NowConn)
	}

	lease.Release()
	if len(collector.cutCalls) != 0 {
		t.Fatalf("route cut calls = %#v, want nil; runtime instance conn count should track actual bridge links only", collector.cutCalls)
	}
}

func TestProxyLimitRuntimeResolvesMissingProvidersFromCurrentRootOncePerCall(t *testing.T) {
	oldRuntime := currentProxyRuntimeRoot
	defer func() {
		currentProxyRuntimeRoot = oldRuntime
	}()

	rootCalls := 0
	currentProxyRuntimeRoot = func() proxyRuntimeContext {
		rootCalls++
		userQuota := proxyUserQuotaRuntime{}
		return proxyRuntimeContext{
			userQuota:       userQuota,
			clientLifecycle: proxyClientLifecycleRuntime{},
			rateLimiters: proxyRateLimitRuntime{
				userQuota: userQuota,
			},
		}
	}

	runtime := proxyLimitRuntime{}
	client := &file.Client{Flow: &file.Flow{}, MaxConn: 2}

	lease, err := runtime.CheckFlowAndConnNum(client, nil, nil)
	if err != nil {
		t.Fatalf("CheckFlowAndConnNum() error = %v", err)
	}
	lease.Release()
	if rootCalls != 1 {
		t.Fatalf("CheckFlowAndConnNum() runtime root calls = %d, want 1", rootCalls)
	}

	rootCalls = 0
	if limiter := runtime.ServiceRateLimiter(client, nil, nil); limiter != nil {
		t.Fatalf("ServiceRateLimiter() = %#v, want nil for zero-value default limiters", limiter)
	}
	if rootCalls != 1 {
		t.Fatalf("ServiceRateLimiter() runtime root calls = %d, want 1", rootCalls)
	}

	rootCalls = 0
	if limiter := runtime.BridgeRateLimiter(client); limiter != nil {
		t.Fatalf("BridgeRateLimiter() = %#v, want nil for zero-value default limiters", limiter)
	}
	if rootCalls != 1 {
		t.Fatalf("BridgeRateLimiter() runtime root calls = %d, want 1", rootCalls)
	}
}

func TestClientNeedsPerRequestBackendUsesRuntimeNodeCount(t *testing.T) {
	server := &BaseServer{
		linkOpener: proxyLinkOpenerNodeCountStub{count: 2, rotate: true},
	}
	client := &file.Client{Id: 7}
	if !server.ClientNeedsPerRequestBackend(client, "") {
		t.Fatal("multiple online runtime nodes without bound route should require per-request backend selection")
	}
	if server.ClientNeedsPerRequestBackend(client, "node-a") {
		t.Fatal("bound runtime route should not force per-request backend selection")
	}
	if server.ClientNeedsPerRequestBackend(&file.Client{Id: 0}, "") {
		t.Fatal("invalid client id should not force per-request backend selection")
	}
	server.linkOpener = proxyLinkOpenerNodeCountStub{count: 2, rotate: false}
	if server.ClientNeedsPerRequestBackend(client, "") {
		t.Fatal("non-rotating client selection should keep keep-alive reuse enabled")
	}
}

func TestClientNeedsPerRequestBackendPrefersMultipleOnlineNodeChecker(t *testing.T) {
	server := &BaseServer{
		linkOpener: proxyLinkOpenerMultiNodeStub{multi: true, rotate: true},
	}
	client := &file.Client{Id: 7}
	if !server.ClientNeedsPerRequestBackend(client, "") {
		t.Fatal("multiple-online-node checker should enable per-request backend selection")
	}
	server.linkOpener = proxyLinkOpenerMultiNodeStub{multi: false, rotate: true}
	if server.ClientNeedsPerRequestBackend(client, "") {
		t.Fatal("single-online-node checker should keep keep-alive reuse enabled")
	}
}

func TestBaseServerRouteRuntimeHelpersIgnoreTypedNilLinkOpener(t *testing.T) {
	var opener *proxyTypedNilLinkOpenerStub
	server := &BaseServer{linkOpener: opener}
	client := &file.Client{Id: 7}

	if collector := routeStatsCollectorFromLinkOpener(server.linkOpener); collector != nil {
		t.Fatalf("routeStatsCollectorFromLinkOpener() = %#v, want nil", collector)
	}
	if server.ClientNeedsPerRequestBackend(client, "") {
		t.Fatal("ClientNeedsPerRequestBackend() = true, want false for typed nil opener")
	}
	if got := server.SelectClientRouteUUID(client, ""); got != "" {
		t.Fatalf("SelectClientRouteUUID() = %q, want empty", got)
	}

	left, right := net.Pipe()
	defer func() {
		_ = left.Close()
		_ = right.Close()
	}()
	routeRuntime := server.NewRouteRuntimeContext(client, "node-a")
	if routeRuntime == nil {
		t.Fatal("NewRouteRuntimeContext() = nil")
	}
	if wrapped := routeRuntime.TrackConn(left); wrapped != left {
		t.Fatalf("TrackConn() = %#v, want original conn when collector is absent", wrapped)
	}
}

func TestRouteRuntimeHelpersIgnoreTypedNilConn(t *testing.T) {
	var target *proxyTypedNilRouteConnStub
	collector := &proxyRouteRuntimeCollectorStub{}
	routeRuntime := newRouteRuntimeContext(collector, &file.Client{Id: 7}, "node-a")

	if got := RuntimeRouteUUIDFromConn(target); got != "" {
		t.Fatalf("RuntimeRouteUUIDFromConn() = %q, want empty", got)
	}
	if wrapped := WrapRuntimeRouteConn(target, "node-a"); wrapped != nil {
		t.Fatalf("WrapRuntimeRouteConn() = %#v, want nil", wrapped)
	}
	if wrapped := wrapRouteTrackedConn(target, collector, 7, "node-a"); wrapped != nil {
		t.Fatalf("wrapRouteTrackedConn() = %#v, want nil", wrapped)
	}
	if got := len(collector.addCalls); got != 0 {
		t.Fatalf("route add calls = %#v, want none", collector.addCalls)
	}
	if wrapped := routeRuntime.TrackConn(target); wrapped != nil {
		t.Fatalf("RouteRuntimeContext.TrackConn() = %#v, want nil", wrapped)
	}
}

func TestWrapRouteTrackedConnTracksActualBridgeConnectionLifecycle(t *testing.T) {
	collector := &proxyRouteRuntimeCollectorStub{}
	left, right := net.Pipe()
	defer func() { _ = right.Close() }()

	wrapped := wrapRouteTrackedConn(left, collector, 7, "node-a")
	if len(collector.addCalls) != 1 || collector.addCalls[0] != "7:node-a" {
		t.Fatalf("route add calls = %#v, want [\"7:node-a\"]", collector.addCalls)
	}
	if err := wrapped.Close(); err != nil {
		t.Fatalf("wrapped.Close() error = %v", err)
	}
	if len(collector.cutCalls) != 1 || collector.cutCalls[0] != "7:node-a" {
		t.Fatalf("route cut calls = %#v, want [\"7:node-a\"]", collector.cutCalls)
	}
}

func TestWrapRouteTrackedConnUsesBoundNodeRuntimeWhenAvailable(t *testing.T) {
	boundNode := &proxyBoundNodeRuntimeStub{}
	collector := &proxyRouteRuntimeBindingCollectorStub{
		nodes: map[string]*proxyBoundNodeRuntimeStub{
			"node-a": boundNode,
		},
	}
	left, right := net.Pipe()
	defer func() { _ = right.Close() }()

	wrapped := wrapRouteTrackedConn(left, collector, 7, "node-a")
	if boundNode.addCount != 1 {
		t.Fatalf("bound node add count = %d, want 1", boundNode.addCount)
	}
	if len(collector.addCalls) != 0 {
		t.Fatalf("collector add calls = %#v, want none when bound node runtime exists", collector.addCalls)
	}
	if len(collector.loadCalls) != 1 || collector.loadCalls[0] != "7:node-a" {
		t.Fatalf("collector load calls = %#v, want [\"7:node-a\"]", collector.loadCalls)
	}
	if err := wrapped.Close(); err != nil {
		t.Fatalf("wrapped.Close() error = %v", err)
	}
	if boundNode.cutCount != 1 {
		t.Fatalf("bound node cut count = %d, want 1", boundNode.cutCount)
	}
	if len(collector.cutCalls) != 0 {
		t.Fatalf("collector cut calls = %#v, want none when bound node runtime exists", collector.cutCalls)
	}
}

func TestWithRouteRuntimeTraceCapturesTaggedConnectionRoute(t *testing.T) {
	routeRuntime := newRouteRuntimeContext(nil, &file.Client{Id: 7}, "")
	ctx := WithRouteRuntimeTrace(context.Background(), routeRuntime)
	trace := httptrace.ContextClientTrace(ctx)
	if trace == nil || trace.GotConn == nil {
		t.Fatal("WithRouteRuntimeTrace() should install GotConn callback")
	}

	left, right := net.Pipe()
	defer func() {
		_ = left.Close()
		_ = right.Close()
	}()
	trace.GotConn(httptrace.GotConnInfo{Conn: WrapRuntimeRouteConn(left, "node-a")})
	if routeRuntime.RouteUUID() != "node-a" {
		t.Fatalf("routeRuntime.RouteUUID() = %q, want %q", routeRuntime.RouteUUID(), "node-a")
	}
}

func TestWithRouteRuntimeTraceHandlesNilContext(t *testing.T) {
	routeRuntime := newRouteRuntimeContext(nil, &file.Client{Id: 7}, "")
	var parent context.Context
	ctx := WithRouteRuntimeTrace(parent, routeRuntime)
	if ctx == nil {
		t.Fatal("WithRouteRuntimeTrace() returned nil context")
	}
	trace := httptrace.ContextClientTrace(ctx)
	if trace == nil || trace.GotConn == nil {
		t.Fatal("WithRouteRuntimeTrace() should install GotConn callback for nil parent context")
	}
}

func TestRouteRuntimeContextUpdateFromLinkKeepsExistingRouteUUIDWhenLinkIsEmpty(t *testing.T) {
	routeRuntime := newRouteRuntimeContext(nil, &file.Client{Id: 7}, "node-a")
	routeRuntime.UpdateFromLink(&conn.Link{})
	if got := routeRuntime.RouteUUID(); got != "node-a" {
		t.Fatalf("UpdateFromLink(empty) route uuid = %q, want %q", got, "node-a")
	}

	routeRuntime.UpdateFromLink(&conn.Link{Option: conn.Options{RouteUUID: "node-b"}})
	if got := routeRuntime.RouteUUID(); got != "node-b" {
		t.Fatalf("UpdateFromLink(non-empty) route uuid = %q, want %q", got, "node-b")
	}
}

func TestRouteRuntimeContextObserveServiceTrafficUsesUpdatedRouteUUID(t *testing.T) {
	collector := &proxyRouteRuntimeCollectorStub{}
	client := &file.Client{Id: 7, Flow: &file.Flow{}}
	file.InitializeClientRuntime(client)

	routeRuntime := newRouteRuntimeContext(collector, client, "")
	if err := routeRuntime.ObserveServiceTraffic(client, nil, nil, 3, 4); err != nil {
		t.Fatalf("ObserveServiceTraffic() before route update error = %v", err)
	}
	if collector.serviceTotals != [2]int64{} {
		t.Fatalf("collector totals before route update = %#v, want zero totals", collector.serviceTotals)
	}

	routeRuntime.UpdateFromLink(&conn.Link{Option: conn.Options{RouteUUID: "node-b"}})
	if err := routeRuntime.ObserveServiceTraffic(client, nil, nil, 5, 6); err != nil {
		t.Fatalf("ObserveServiceTraffic() after route update error = %v", err)
	}
	if collector.serviceTotals != [2]int64{5, 6} {
		t.Fatalf("collector totals after route update = %#v, want %#v", collector.serviceTotals, [2]int64{5, 6})
	}
}

func TestRouteRuntimeContextUsesBoundNodeRuntimeAcrossRouteChanges(t *testing.T) {
	nodeA := &proxyBoundNodeRuntimeStub{}
	nodeB := &proxyBoundNodeRuntimeStub{}
	collector := &proxyRouteRuntimeBindingCollectorStub{
		nodes: map[string]*proxyBoundNodeRuntimeStub{
			"node-a": nodeA,
			"node-b": nodeB,
		},
	}
	client := &file.Client{Id: 7, Flow: &file.Flow{}}
	file.InitializeClientRuntime(client)

	routeRuntime := newRouteRuntimeContext(collector, client, "node-a")
	if len(collector.loadCalls) != 1 || collector.loadCalls[0] != "7:node-a" {
		t.Fatalf("collector load calls after init = %#v, want [\"7:node-a\"]", collector.loadCalls)
	}
	if err := routeRuntime.ObserveServiceTraffic(client, nil, nil, 5, 6); err != nil {
		t.Fatalf("ObserveServiceTraffic(node-a) error = %v", err)
	}
	if nodeA.serviceTotals != [2]int64{5, 6} {
		t.Fatalf("node-a service totals = %#v, want %#v", nodeA.serviceTotals, [2]int64{5, 6})
	}
	if collector.serviceTotals != [2]int64{} {
		t.Fatalf("collector service totals = %#v, want zero when bound node runtime exists", collector.serviceTotals)
	}
	if err := routeRuntime.ObserveBridgeTraffic(client, 3, 4); err != nil {
		t.Fatalf("ObserveBridgeTraffic(node-a) error = %v", err)
	}
	if nodeA.bridgeTotals != [2]int64{3, 4} {
		t.Fatalf("node-a bridge totals = %#v, want %#v", nodeA.bridgeTotals, [2]int64{3, 4})
	}
	if collector.bridgeTotals != [2]int64{} {
		t.Fatalf("collector bridge totals = %#v, want zero when bound node runtime exists", collector.bridgeTotals)
	}

	routeRuntime.UpdateFromLink(&conn.Link{Option: conn.Options{RouteUUID: "node-b"}})
	if len(collector.loadCalls) != 2 || collector.loadCalls[1] != "7:node-b" {
		t.Fatalf("collector load calls after route switch = %#v, want second bind to node-b", collector.loadCalls)
	}
	if err := routeRuntime.ObserveServiceTraffic(client, nil, nil, 7, 8); err != nil {
		t.Fatalf("ObserveServiceTraffic(node-b) error = %v", err)
	}
	if nodeB.serviceTotals != [2]int64{7, 8} {
		t.Fatalf("node-b service totals = %#v, want %#v", nodeB.serviceTotals, [2]int64{7, 8})
	}
	if nodeA.serviceTotals != [2]int64{5, 6} {
		t.Fatalf("node-a service totals after route switch = %#v, want unchanged %#v", nodeA.serviceTotals, [2]int64{5, 6})
	}
}

func TestTrafficObserversUseBoundNodeRuntimeWhenAvailable(t *testing.T) {
	boundNode := &proxyBoundNodeRuntimeStub{}
	collector := &proxyRouteRuntimeBindingCollectorStub{
		nodes: map[string]*proxyBoundNodeRuntimeStub{
			"node-a": boundNode,
		},
	}
	client := &file.Client{Id: 7, Flow: &file.Flow{}}
	file.InitializeClientRuntime(client)

	bridgeObserver := bridgeTrafficObserver(client, collector, "node-a")
	if err := bridgeObserver.OnRead(2); err != nil {
		t.Fatalf("bridge observer OnRead() error = %v", err)
	}
	if err := bridgeObserver.OnWrite(3); err != nil {
		t.Fatalf("bridge observer OnWrite() error = %v", err)
	}
	if boundNode.bridgeTotals != [2]int64{2, 3} {
		t.Fatalf("bound node bridge totals = %#v, want %#v", boundNode.bridgeTotals, [2]int64{2, 3})
	}
	if collector.bridgeTotals != [2]int64{} {
		t.Fatalf("collector bridge totals = %#v, want zero when bound node runtime exists", collector.bridgeTotals)
	}

	serviceObserver := serviceTrafficObserver(client, nil, nil, collector, "node-a")
	if err := serviceObserver.OnRead(4); err != nil {
		t.Fatalf("service observer OnRead() error = %v", err)
	}
	if err := serviceObserver.OnWrite(5); err != nil {
		t.Fatalf("service observer OnWrite() error = %v", err)
	}
	if boundNode.serviceTotals != [2]int64{4, 5} {
		t.Fatalf("bound node service totals = %#v, want %#v", boundNode.serviceTotals, [2]int64{4, 5})
	}
	if collector.serviceTotals != [2]int64{} {
		t.Fatalf("collector service totals = %#v, want zero when bound node runtime exists", collector.serviceTotals)
	}
	if len(collector.loadCalls) != 2 {
		t.Fatalf("collector load calls = %#v, want one bind per observer", collector.loadCalls)
	}
}

func TestBaseServerBridgeTrafficObserverUsesResolvedRouteBinding(t *testing.T) {
	boundNode := &proxyBoundNodeRuntimeStub{}
	collector := &proxyRouteRuntimeBindingCollectorStub{
		nodes: map[string]*proxyBoundNodeRuntimeStub{
			"node-a": boundNode,
		},
	}
	base := &BaseServer{
		linkOpener: proxyRouteRuntimeBindingBridgeLinkOpenerStub{collector},
	}
	client := &file.Client{Id: 7, Flow: &file.Flow{}}
	file.InitializeClientRuntime(client)

	observer := base.BridgeTrafficObserver(client, "node-a")
	if err := observer.OnRead(6); err != nil {
		t.Fatalf("BridgeTrafficObserver().OnRead() error = %v", err)
	}
	if err := observer.OnWrite(7); err != nil {
		t.Fatalf("BridgeTrafficObserver().OnWrite() error = %v", err)
	}
	if boundNode.bridgeTotals != [2]int64{6, 7} {
		t.Fatalf("bound node bridge totals = %#v, want %#v", boundNode.bridgeTotals, [2]int64{6, 7})
	}
	if collector.bridgeTotals != [2]int64{} {
		t.Fatalf("collector bridge totals = %#v, want zero when BaseServer binding resolves bound node", collector.bridgeTotals)
	}
	if len(collector.loadCalls) != 1 || collector.loadCalls[0] != "7:node-a" {
		t.Fatalf("collector load calls = %#v, want [\"7:node-a\"]", collector.loadCalls)
	}
}

func TestBaseServerServiceTrafficObserverUsesResolvedRouteBinding(t *testing.T) {
	boundNode := &proxyBoundNodeRuntimeStub{}
	collector := &proxyRouteRuntimeBindingCollectorStub{
		nodes: map[string]*proxyBoundNodeRuntimeStub{
			"node-a": boundNode,
		},
	}
	base := &BaseServer{
		linkOpener: proxyRouteRuntimeBindingBridgeLinkOpenerStub{collector},
	}
	client := &file.Client{Id: 7, Flow: &file.Flow{}}
	file.InitializeClientRuntime(client)
	tunnel := &file.Tunnel{
		Id:     9,
		Client: client,
		Flow:   &file.Flow{},
		Target: &file.Target{TargetStr: "owner-a:9"},
	}
	file.InitializeTunnelRuntime(tunnel)
	tunnel.BindRuntimeOwner("node-a", &file.Tunnel{
		Id:     9,
		Client: client,
		Flow:   &file.Flow{},
		Target: &file.Target{TargetStr: "owner-a:9"},
	})
	selected := tunnel.SelectRuntimeRoute()
	if selected == nil {
		t.Fatal("SelectRuntimeRoute() = nil")
	}

	observer := base.ServiceTrafficObserver(client, selected, nil)
	if err := observer.OnRead(8); err != nil {
		t.Fatalf("ServiceTrafficObserver().OnRead() error = %v", err)
	}
	if err := observer.OnWrite(9); err != nil {
		t.Fatalf("ServiceTrafficObserver().OnWrite() error = %v", err)
	}
	if boundNode.serviceTotals != [2]int64{8, 9} {
		t.Fatalf("bound node service totals = %#v, want %#v", boundNode.serviceTotals, [2]int64{8, 9})
	}
	if collector.serviceTotals != [2]int64{} {
		t.Fatalf("collector service totals = %#v, want zero when BaseServer binding resolves bound node", collector.serviceTotals)
	}
	if len(collector.loadCalls) != 1 || collector.loadCalls[0] != "7:node-a" {
		t.Fatalf("collector load calls = %#v, want [\"7:node-a\"]", collector.loadCalls)
	}
}

type recordingConn struct {
	writes bytes.Buffer
	closed bool
}

func (c *recordingConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (c *recordingConn) Write(b []byte) (int, error)      { return c.writes.Write(b) }
func (c *recordingConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (c *recordingConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (c *recordingConn) SetDeadline(time.Time) error      { return nil }
func (c *recordingConn) SetReadDeadline(time.Time) error  { return nil }
func (c *recordingConn) SetWriteDeadline(time.Time) error { return nil }
func (c *recordingConn) Close() error {
	c.closed = true
	return nil
}

func TestProxyTransportRuntimePipeClientConnCountsBufferedIngressAsServiceTraffic(t *testing.T) {
	client := &file.Client{
		Id:   7,
		Cnf:  &file.Config{},
		Flow: &file.Flow{},
	}
	file.InitializeClientRuntime(client)
	task := &file.Tunnel{
		Id:     9,
		Client: client,
		Flow:   &file.Flow{},
		Target: &file.Target{TargetStr: "example.com:443"},
	}
	file.InitializeTunnelRuntime(task)

	collector := &proxyRouteRuntimeCollectorStub{}
	runtime := proxyTransportRuntime{routeStats: collector}
	target := &recordingConn{}

	serviceSide, peerSide := net.Pipe()
	_ = peerSide.Close()
	link := conn.NewLink(common.CONN_TCP, "example.com:443", false, false, "198.51.100.40:4000", false, conn.WithRouteUUID("node-a"))
	runtime.PipeClientConn(target, conn.NewConn(serviceSide), link, client, []*file.Flow{task.Flow, client.Flow}, 0, []byte("prefetch"), task, false, common.CONN_TCP)

	if got := target.writes.String(); got != "prefetch" {
		t.Fatalf("target buffered write = %q, want %q", got, "prefetch")
	}
	in, out, total := task.ServiceTrafficTotals()
	if in != int64(len("prefetch")) || out != 0 || total != int64(len("prefetch")) {
		t.Fatalf("task service traffic = (%d, %d, %d), want (%d, 0, %d)", in, out, total, len("prefetch"), len("prefetch"))
	}
	clientIn, clientOut, clientTotal := client.ServiceTrafficTotals()
	if clientIn != int64(len("prefetch")) || clientOut != 0 || clientTotal != int64(len("prefetch")) {
		t.Fatalf("client service traffic = (%d, %d, %d), want (%d, 0, %d)", clientIn, clientOut, clientTotal, len("prefetch"), len("prefetch"))
	}
	if collector.serviceTotals != [2]int64{int64(len("prefetch")), 0} {
		t.Fatalf("route service totals = %#v, want %#v", collector.serviceTotals, [2]int64{int64(len("prefetch")), 0})
	}
}

type proxyTestStore struct {
	user    *file.User
	clients []*file.Client
}

func (s proxyTestStore) GetUser(id int) (*file.User, error) {
	if s.user != nil && s.user.Id == id {
		return s.user, nil
	}
	return nil, errors.New("not found")
}

func (proxyTestStore) GetUserByUsername(string) (*file.User, error) {
	return nil, errors.New("not implemented")
}
func (proxyTestStore) CreateUser(*file.User) error { return errors.New("not implemented") }
func (proxyTestStore) UpdateUser(*file.User) error { return errors.New("not implemented") }
func (proxyTestStore) GetClient(string) (*file.Client, error) {
	return nil, errors.New("not implemented")
}
func (proxyTestStore) GetClientByID(int) (*file.Client, error) {
	return nil, errors.New("not implemented")
}
func (proxyTestStore) UpdateClient(*file.Client) error { return errors.New("not implemented") }
func (s proxyTestStore) GetAllClients() []*file.Client { return s.clients }
func (s proxyTestStore) GetClientsByUserId(userId int) []*file.Client {
	if s.user == nil || s.user.Id != userId {
		return nil
	}
	return s.clients
}
func (proxyTestStore) GetTunnelsByUserId(int) int   { return 0 }
func (proxyTestStore) GetHostsByUserId(int) int     { return 0 }
func (proxyTestStore) AddTraffic(int, int64, int64) {}
func (proxyTestStore) Flush() error                 { return nil }
