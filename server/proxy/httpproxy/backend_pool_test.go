package httpproxy

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/index"
	serverproxy "github.com/djylb/nps/server/proxy"
)

type httpBackendBridgeStub struct {
	selectedRoute string
	lastClientID  int
	lastLink      *conn.Link
}

func (s *httpBackendBridgeStub) SendLinkInfo(clientID int, link *conn.Link, task *file.Tunnel) (net.Conn, error) {
	s.lastClientID = clientID
	s.lastLink = link
	serverSide, peerSide := net.Pipe()
	go func() {
		_ = peerSide.Close()
	}()
	return serverSide, nil
}

func (s *httpBackendBridgeStub) IsServer() bool {
	return false
}

func (s *httpBackendBridgeStub) CliProcess(_ *conn.Conn, _ string) {}

func (s *httpBackendBridgeStub) SelectClientRouteUUID(clientID int) string {
	return s.selectedRoute
}

func TestBackendTransportPoolCachesPerBackendKey(t *testing.T) {
	pool := newBackendTransportPool()
	created := 0
	create := func() *http.Transport {
		created++
		return &http.Transport{}
	}

	first := pool.GetOrCreate(backendSelection{routeUUID: "node-a", targetAddr: "127.0.0.1:80"}, create)
	second := pool.GetOrCreate(backendSelection{routeUUID: "node-a", targetAddr: "127.0.0.1:80"}, create)
	third := pool.GetOrCreate(backendSelection{routeUUID: "node-b", targetAddr: "127.0.0.1:80"}, create)

	if first == nil || second == nil || third == nil {
		t.Fatal("GetOrCreate() returned nil transport")
	}
	if first != second {
		t.Fatal("same backend key should reuse the same transport")
	}
	if first == third {
		t.Fatal("different backend key should use a different transport")
	}
	if created != 2 {
		t.Fatalf("transport create count = %d, want 2", created)
	}
	if got := pool.Size(); got != 2 {
		t.Fatalf("pool size = %d, want 2", got)
	}
}

func TestBackendTransportPoolDefersExpiredSweepUntilScheduled(t *testing.T) {
	pool := newBackendTransportPool()
	pool.idleTTL = time.Second
	staleSelection := backendSelection{routeUUID: "stale-node", targetAddr: "127.0.0.1:81"}
	staleKey := staleSelection.key()
	now := time.Now()
	pool.items[staleKey] = newBackendTransportEntry(&http.Transport{}, now.Add(-2*time.Second))
	pool.nextPruneAt = now.Add(time.Minute)

	freshSelection := backendSelection{routeUUID: "fresh-node", targetAddr: "127.0.0.1:82"}
	pool.GetOrCreate(freshSelection, func() *http.Transport { return &http.Transport{} })

	if _, ok := pool.items[staleKey]; !ok {
		t.Fatal("stale entry should be retained until the scheduled prune time")
	}

	pool.nextPruneAt = time.Time{}
	nextSelection := backendSelection{routeUUID: "next-node", targetAddr: "127.0.0.1:83"}
	pool.GetOrCreate(nextSelection, func() *http.Transport { return &http.Transport{} })

	if _, ok := pool.items[staleKey]; ok {
		t.Fatal("stale entry should be pruned once the scheduled prune time is reached")
	}
}

func TestBackendTransportPoolCoalescesConcurrentCreateAfterUnlockedBuild(t *testing.T) {
	pool := newBackendTransportPool()
	selection := backendSelection{routeUUID: "node-a", targetAddr: "127.0.0.1:80"}

	var created atomic.Int32
	ready := make(chan struct{}, 2)
	release := make(chan struct{})
	create := func() *http.Transport {
		created.Add(1)
		ready <- struct{}{}
		<-release
		return &http.Transport{}
	}

	results := make([]*http.Transport, 2)
	var start sync.WaitGroup
	start.Add(1)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			start.Wait()
			results[idx] = pool.GetOrCreate(selection, create)
		}(i)
	}

	start.Done()
	<-ready
	<-ready
	close(release)
	wg.Wait()

	if created.Load() != 2 {
		t.Fatalf("transport create count = %d, want 2 concurrent unlocked builds", created.Load())
	}
	if results[0] == nil || results[1] == nil {
		t.Fatalf("GetOrCreate() results = %#v, want non-nil shared transport", results)
	}
	if results[0] != results[1] {
		t.Fatal("concurrent GetOrCreate() should converge on one cached transport")
	}
	if got := pool.Size(); got != 1 {
		t.Fatalf("pool size = %d, want 1 after concurrent coalescing", got)
	}
}

func TestWithHTTPProxyBackendSelectionHandlesNilContext(t *testing.T) {
	ctx := withHTTPProxyBackendSelection(nil, backendSelection{
		routeUUID:  " node-a ",
		targetAddr: " 127.0.0.1:80 ",
	})
	if ctx == nil {
		t.Fatal("withHTTPProxyBackendSelection() returned nil context")
	}

	selection, ok := httpProxyContextBackendSelection(ctx)
	if !ok {
		t.Fatal("httpProxyContextBackendSelection() did not recover stored backend selection")
	}
	if selection.routeUUID != "node-a" {
		t.Fatalf("selection.routeUUID = %q, want %q", selection.routeUUID, "node-a")
	}
	if selection.targetAddr != "127.0.0.1:80" {
		t.Fatalf("selection.targetAddr = %q, want %q", selection.targetAddr, "127.0.0.1:80")
	}
}

func TestHttpServerResolveBackendSelectionUsesBridgeRouteSelector(t *testing.T) {
	bridge := &httpBackendBridgeStub{selectedRoute: "node-b"}
	server := &HttpServer{
		HttpProxy: &HttpProxy{
			BaseServer:     serverproxy.NewBaseServer(bridge, nil),
			HttpProxyCache: index.NewAnyIntIndex(),
		},
	}
	host := &file.Host{
		Id:     7,
		Client: &file.Client{Id: 12, Cnf: &file.Config{}, Flow: &file.Flow{}},
		Flow:   &file.Flow{},
		Target: &file.Target{TargetStr: "127.0.0.1:80\n127.0.0.1:81"},
	}

	first, err := server.resolveBackendSelection(host)
	if err != nil {
		t.Fatalf("resolveBackendSelection() error = %v", err)
	}
	second, err := server.resolveBackendSelection(host)
	if err != nil {
		t.Fatalf("resolveBackendSelection() second error = %v", err)
	}

	if first.routeUUID != "node-b" || second.routeUUID != "node-b" {
		t.Fatalf("resolveBackendSelection() routeUUID = %q / %q, want %q", first.routeUUID, second.routeUUID, "node-b")
	}
	if first.targetAddr != "127.0.0.1:81" {
		t.Fatalf("first target = %q, want %q", first.targetAddr, "127.0.0.1:81")
	}
	if second.targetAddr != "127.0.0.1:80" {
		t.Fatalf("second target = %q, want %q", second.targetAddr, "127.0.0.1:80")
	}
}

func TestHttpServerResolveBackendSelectionRejectsMalformedHost(t *testing.T) {
	tests := []struct {
		name string
		host *file.Host
	}{
		{
			name: "missing client",
			host: &file.Host{
				Id:     1,
				Flow:   &file.Flow{},
				Target: &file.Target{TargetStr: "127.0.0.1:80"},
			},
		},
		{
			name: "missing client config",
			host: &file.Host{
				Id:     2,
				Flow:   &file.Flow{},
				Target: &file.Target{TargetStr: "127.0.0.1:80"},
				Client: &file.Client{Id: 7, Flow: &file.Flow{}},
			},
		},
		{
			name: "missing client flow",
			host: &file.Host{
				Id:     3,
				Flow:   &file.Flow{},
				Target: &file.Target{TargetStr: "127.0.0.1:80"},
				Client: &file.Client{Id: 7, Cnf: &file.Config{}},
			},
		},
		{
			name: "missing host flow",
			host: &file.Host{
				Id:     4,
				Target: &file.Target{TargetStr: "127.0.0.1:80"},
				Client: &file.Client{Id: 7, Cnf: &file.Config{}, Flow: &file.Flow{}},
			},
		},
		{
			name: "missing target",
			host: &file.Host{
				Id:     5,
				Flow:   &file.Flow{},
				Client: &file.Client{Id: 7, Cnf: &file.Config{}, Flow: &file.Flow{}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := &HttpServer{
				HttpProxy: &HttpProxy{
					BaseServer:     serverproxy.NewBaseServer(&httpBackendBridgeStub{}, nil),
					HttpProxyCache: index.NewAnyIntIndex(),
				},
			}

			_, err := server.resolveBackendSelection(tt.host)
			if !errors.Is(err, errHTTPProxyInvalidBackend) {
				t.Fatalf("resolveBackendSelection() error = %v, want %v", err, errHTTPProxyInvalidBackend)
			}
		})
	}
}

func TestHttpServerTransportForBackendCachesPerHostAndBackend(t *testing.T) {
	loadHTTPProxyTestConfig(t, map[string]any{})

	server := &HttpServer{
		HttpProxy: &HttpProxy{
			BaseServer:     serverproxy.NewBaseServer(&httpBackendBridgeStub{}, nil),
			HttpProxyCache: index.NewAnyIntIndex(),
		},
	}
	host := &file.Host{
		Id:     15,
		Client: &file.Client{Id: 31, Cnf: &file.Config{}, Flow: &file.Flow{}},
		Flow:   &file.Flow{},
		Target: &file.Target{TargetStr: "127.0.0.1:80"},
	}
	firstSelection := backendSelection{routeUUID: "node-a", targetAddr: "127.0.0.1:80"}
	secondSelection := backendSelection{routeUUID: "node-b", targetAddr: "127.0.0.1:80"}

	first := server.transportForBackend(host, firstSelection)
	second := server.transportForBackend(host, firstSelection)
	third := server.transportForBackend(host, secondSelection)

	if first == nil || second == nil || third == nil {
		t.Fatal("transportForBackend() returned nil transport")
	}
	if first != second {
		t.Fatal("same host/backend should reuse the cached transport")
	}
	if first == third {
		t.Fatal("different backend should use a different cached transport")
	}

	cached, ok := server.HttpProxyCache.Get(host.Id)
	if !ok {
		t.Fatal("transport pool was not cached by host id")
	}
	pool, ok := cached.(*backendTransportPool)
	if !ok || pool == nil {
		t.Fatalf("cached pool type = %T, want *backendTransportPool", cached)
	}
	if got := pool.Size(); got != 2 {
		t.Fatalf("cached pool size = %d, want 2", got)
	}

	server.removeBackendTransport(host.Id, firstSelection)
	if got := pool.Size(); got != 1 {
		t.Fatalf("cached pool size after remove = %d, want 1", got)
	}
}

func TestHttpServerDialContextUsesPreselectedBackend(t *testing.T) {
	bridge := &httpBackendBridgeStub{selectedRoute: "node-a"}
	server := &HttpServer{
		HttpProxy: &HttpProxy{
			BaseServer:     serverproxy.NewBaseServer(bridge, nil),
			HttpProxyCache: index.NewAnyIntIndex(),
		},
	}
	host := &file.Host{
		Id:     9,
		Client: &file.Client{Id: 21, Cnf: &file.Config{}, Flow: &file.Flow{}},
		Flow:   &file.Flow{},
		Target: &file.Target{TargetStr: "127.0.0.1:80"},
	}
	routeRuntime := server.NewRouteRuntimeContext(host.Client, "")
	ctx := context.Background()
	ctx = context.WithValue(ctx, ctxRemoteAddr, "203.0.113.1:5000")
	ctx = context.WithValue(ctx, ctxHost, host)
	ctx = context.WithValue(ctx, ctxRouteRuntime, routeRuntime)
	ctx = withHTTPProxyBackendSelection(ctx, backendSelection{
		routeUUID:  "node-fixed",
		targetAddr: "127.0.0.1:9001",
	})

	c, err := server.DialContext(ctx, "tcp", "ignored")
	if err != nil {
		t.Fatalf("DialContext() error = %v", err)
	}
	_ = c.Close()

	if bridge.lastLink == nil {
		t.Fatal("DialContext() did not open a bridge link")
	}
	if bridge.lastClientID != host.Client.Id {
		t.Fatalf("bridge client id = %d, want %d", bridge.lastClientID, host.Client.Id)
	}
	if bridge.lastLink.Host != "127.0.0.1:9001" {
		t.Fatalf("bridge target = %q, want %q", bridge.lastLink.Host, "127.0.0.1:9001")
	}
	if bridge.lastLink.Option.RouteUUID != "node-fixed" {
		t.Fatalf("bridge route uuid = %q, want %q", bridge.lastLink.Option.RouteUUID, "node-fixed")
	}
}

func TestHttpServerResolveHTTPProxyBackendStateUsesPreselectedBackend(t *testing.T) {
	bridge := &httpBackendBridgeStub{selectedRoute: "node-a"}
	server := &HttpServer{
		HttpProxy: &HttpProxy{
			BaseServer:     serverproxy.NewBaseServer(bridge, nil),
			HttpProxyCache: index.NewAnyIntIndex(),
		},
	}
	host := &file.Host{
		Id:            10,
		Client:        &file.Client{Id: 22, Cnf: &file.Config{}, Flow: &file.Flow{}},
		Flow:          &file.Flow{},
		Target:        &file.Target{TargetStr: "127.0.0.1:80"},
		TargetIsHttps: false,
	}
	routeRuntime := server.NewRouteRuntimeContext(host.Client, "")
	req := httptest.NewRequest(http.MethodGet, "http://example.com/ws", nil)
	ctx := context.WithValue(req.Context(), ctxRemoteAddr, "203.0.113.2:6000")
	ctx = context.WithValue(ctx, ctxHost, host)
	ctx = context.WithValue(ctx, ctxRouteRuntime, routeRuntime)
	ctx = withHTTPProxyBackendSelection(ctx, backendSelection{
		routeUUID:  "node-fixed-ws",
		targetAddr: "127.0.0.1:9002",
	})
	req = req.WithContext(ctx)

	resolvedCtx, resolved, err := server.resolveHTTPProxyBackendState(req.Context(), req, host, backendSelection{})
	if err != nil {
		t.Fatalf("resolveHTTPProxyBackendState() error = %v", err)
	}
	selection := resolved.selection
	resolvedRuntime := resolved.routeRuntime
	if selection.routeUUID != "node-fixed-ws" {
		t.Fatalf("selection.routeUUID = %q, want %q", selection.routeUUID, "node-fixed-ws")
	}
	if selection.targetAddr != "127.0.0.1:9002" {
		t.Fatalf("selection.targetAddr = %q, want %q", selection.targetAddr, "127.0.0.1:9002")
	}
	if resolvedRuntime == nil || resolvedRuntime.RouteUUID() != "node-fixed-ws" {
		t.Fatalf("resolved runtime route uuid = %v, want %q", resolvedRuntime, "node-fixed-ws")
	}

	c, err := server.dialResolvedBackendContext(resolvedCtx, resolved)
	if err != nil {
		t.Fatalf("dialResolvedBackendContext() error = %v", err)
	}
	_ = c.Close()

	if bridge.lastLink == nil {
		t.Fatal("dialResolvedBackendContext() did not open a bridge link")
	}
	if bridge.lastLink.Host != "127.0.0.1:9002" {
		t.Fatalf("bridge target = %q, want %q", bridge.lastLink.Host, "127.0.0.1:9002")
	}
	if bridge.lastLink.Option.RouteUUID != "node-fixed-ws" {
		t.Fatalf("bridge route uuid = %q, want %q", bridge.lastLink.Option.RouteUUID, "node-fixed-ws")
	}
}

func TestHttpServerResolveHTTPProxyBackendStateBackfillsCombinedRequestPayload(t *testing.T) {
	bridge := &httpBackendBridgeStub{selectedRoute: "node-a"}
	server := &HttpServer{
		HttpProxy: &HttpProxy{
			BaseServer:     serverproxy.NewBaseServer(bridge, nil),
			HttpProxyCache: index.NewAnyIntIndex(),
		},
	}
	host := &file.Host{
		Id:         28,
		HostChange: "backend.example.com",
		Client:     &file.Client{Id: 41, Cnf: &file.Config{}, Flow: &file.Flow{}},
		Flow:       &file.Flow{},
		Target:     &file.Target{TargetStr: "127.0.0.1:80"},
	}
	req := httptest.NewRequest(http.MethodGet, "http://example.com/ws", nil)
	req.RemoteAddr = "203.0.113.44:6040"

	resolvedCtx, resolved, err := server.resolveHTTPProxyBackendState(req.Context(), req, host, backendSelection{})
	if err != nil {
		t.Fatalf("resolveHTTPProxyBackendState() error = %v", err)
	}
	selection := resolved.selection
	routeRuntime := resolved.routeRuntime
	if got, ok := httpProxyContextString(resolvedCtx, ctxRemoteAddr); !ok || got != req.RemoteAddr {
		t.Fatalf("httpProxyContextString(remote) = %q, %v, want %q/true", got, ok, req.RemoteAddr)
	}
	if got, ok := httpProxyContextString(resolvedCtx, ctxSNI); !ok || got != host.HostChange {
		t.Fatalf("httpProxyContextString(sni) = %q, %v, want %q/true", got, ok, host.HostChange)
	}
	if got, ok := httpProxyContextHost(resolvedCtx); !ok || got != host {
		t.Fatalf("httpProxyContextHost() = %#v, %v, want original host/true", got, ok)
	}
	if got, ok := httpProxyContextRouteRuntime(resolvedCtx); !ok || got != routeRuntime {
		t.Fatalf("httpProxyContextRouteRuntime() = %#v, %v, want returned route runtime/true", got, ok)
	}
	if got, ok := httpProxyContextBackendSelection(resolvedCtx); !ok || got != selection {
		t.Fatalf("httpProxyContextBackendSelection() = %#v, %v, want returned selection/true", got, ok)
	}
}

func TestHttpServerResolveHTTPProxyBackendStateUsesPreselectedState(t *testing.T) {
	bridge := &httpBackendBridgeStub{selectedRoute: "node-a"}
	server := &HttpServer{
		HttpProxy: &HttpProxy{
			BaseServer:     serverproxy.NewBaseServer(bridge, nil),
			HttpProxyCache: index.NewAnyIntIndex(),
		},
	}
	host := &file.Host{
		Id:         31,
		HostChange: "backend.example.com",
		Client:     &file.Client{Id: 44, Cnf: &file.Config{}, Flow: &file.Flow{}},
		Flow:       &file.Flow{},
		Target:     &file.Target{TargetStr: "127.0.0.1:80"},
	}
	routeRuntime := server.NewRouteRuntimeContext(host.Client, "")
	req := httptest.NewRequest(http.MethodGet, "http://example.com/api", nil)
	req.RemoteAddr = "203.0.113.55:8040"
	ctx := context.WithValue(req.Context(), ctxSNI, "ctx.backend.example.com")
	ctx = context.WithValue(ctx, ctxRouteRuntime, routeRuntime)
	ctx = withHTTPProxyBackendSelection(ctx, backendSelection{
		routeUUID:  "node-fixed-http",
		targetAddr: "127.0.0.1:9004",
	})
	req = req.WithContext(ctx)

	resolvedCtx, resolved, err := server.resolveHTTPProxyBackendState(req.Context(), req, host, backendSelection{})
	if err != nil {
		t.Fatalf("resolveHTTPProxyBackendState() error = %v", err)
	}
	selection := resolved.selection
	resolvedRuntime := resolved.routeRuntime
	if selection.routeUUID != "node-fixed-http" {
		t.Fatalf("selection.routeUUID = %q, want %q", selection.routeUUID, "node-fixed-http")
	}
	if selection.targetAddr != "127.0.0.1:9004" {
		t.Fatalf("selection.targetAddr = %q, want %q", selection.targetAddr, "127.0.0.1:9004")
	}
	if resolvedRuntime != routeRuntime || resolvedRuntime.RouteUUID() != "node-fixed-http" {
		t.Fatalf("resolved runtime = %#v, route=%q, want original runtime with route %q", resolvedRuntime, resolvedRuntime.RouteUUID(), "node-fixed-http")
	}
	if got, ok := httpProxyContextString(resolvedCtx, ctxRemoteAddr); !ok || got != req.RemoteAddr {
		t.Fatalf("httpProxyContextString(remote) = %q, %v, want %q/true", got, ok, req.RemoteAddr)
	}
	if got, ok := httpProxyContextString(resolvedCtx, ctxSNI); !ok || got != "ctx.backend.example.com" {
		t.Fatalf("httpProxyContextString(sni) = %q, %v, want %q/true", got, ok, "ctx.backend.example.com")
	}
	if got, ok := httpProxyContextHost(resolvedCtx); !ok || got != host {
		t.Fatalf("httpProxyContextHost() = %#v, %v, want original host/true", got, ok)
	}
	if got, ok := httpProxyContextRouteRuntime(resolvedCtx); !ok || got != routeRuntime {
		t.Fatalf("httpProxyContextRouteRuntime() = %#v, %v, want original route runtime/true", got, ok)
	}
	if got, ok := httpProxyContextBackendSelection(resolvedCtx); !ok || got != selection {
		t.Fatalf("httpProxyContextBackendSelection() = %#v, %v, want returned selection/true", got, ok)
	}
}

func TestHttpServerResolveHTTPProxyServeRequestUsesPreselectedStateAndTrace(t *testing.T) {
	bridge := &httpBackendBridgeStub{selectedRoute: "node-a"}
	server := &HttpServer{
		HttpProxy: &HttpProxy{
			BaseServer:     serverproxy.NewBaseServer(bridge, nil),
			HttpProxyCache: index.NewAnyIntIndex(),
		},
	}
	host := &file.Host{
		Id:         32,
		HostChange: "backend.example.com",
		Client:     &file.Client{Id: 45, Cnf: &file.Config{}, Flow: &file.Flow{}},
		Flow:       &file.Flow{},
		Target:     &file.Target{TargetStr: "127.0.0.1:80"},
	}
	routeRuntime := server.NewRouteRuntimeContext(host.Client, "")
	req := httptest.NewRequest(http.MethodGet, "http://example.com/api", nil)
	req.RemoteAddr = "203.0.113.56:8041"
	ctx := context.WithValue(req.Context(), ctxSNI, "ctx.backend.example.com")
	ctx = context.WithValue(ctx, ctxRouteRuntime, routeRuntime)
	ctx = withHTTPProxyBackendSelection(ctx, backendSelection{
		routeUUID:  "node-fixed-serve",
		targetAddr: "127.0.0.1:9005",
	})
	req = req.WithContext(ctx)

	resolved, err := server.resolveHTTPProxyServeRequest(req, host)
	if err != nil {
		t.Fatalf("resolveHTTPProxyServeRequest() error = %v", err)
	}
	if resolved.backend.selection.routeUUID != "node-fixed-serve" {
		t.Fatalf("selection.routeUUID = %q, want %q", resolved.backend.selection.routeUUID, "node-fixed-serve")
	}
	if resolved.backend.selection.targetAddr != "127.0.0.1:9005" {
		t.Fatalf("selection.targetAddr = %q, want %q", resolved.backend.selection.targetAddr, "127.0.0.1:9005")
	}
	if resolved.backend.routeRuntime != routeRuntime || resolved.backend.routeRuntime.RouteUUID() != "node-fixed-serve" {
		t.Fatalf("resolved runtime = %#v, route=%q, want original runtime with route %q", resolved.backend.routeRuntime, resolved.backend.routeRuntime.RouteUUID(), "node-fixed-serve")
	}
	if got, ok := httpProxyContextString(resolved.request.Context(), ctxRemoteAddr); !ok || got != req.RemoteAddr {
		t.Fatalf("httpProxyContextString(remote) = %q, %v, want %q/true", got, ok, req.RemoteAddr)
	}
	if got, ok := httpProxyContextString(resolved.request.Context(), ctxSNI); !ok || got != "ctx.backend.example.com" {
		t.Fatalf("httpProxyContextString(sni) = %q, %v, want %q/true", got, ok, "ctx.backend.example.com")
	}
	if got, ok := httpProxyContextHost(resolved.request.Context()); !ok || got != host {
		t.Fatalf("httpProxyContextHost() = %#v, %v, want original host/true", got, ok)
	}
	if got, ok := httpProxyContextBackendSelection(resolved.request.Context()); !ok || got != resolved.backend.selection {
		t.Fatalf("httpProxyContextBackendSelection() = %#v, %v, want resolved selection/true", got, ok)
	}
	if trace := httptrace.ContextClientTrace(resolved.request.Context()); trace == nil || trace.GotConn == nil {
		t.Fatal("resolveHTTPProxyServeRequest() should attach route runtime trace")
	}
}

func TestHttpServerTransportForBackendUsesPerRequestHostState(t *testing.T) {
	loadHTTPProxyTestConfig(t, map[string]any{})

	bridge := &httpBackendBridgeStub{selectedRoute: "node-a"}
	server := &HttpServer{
		HttpProxy: &HttpProxy{
			BaseServer:     serverproxy.NewBaseServer(bridge, nil),
			HttpProxyCache: index.NewAnyIntIndex(),
		},
	}
	baseHost := &file.Host{
		Id:     18,
		Client: &file.Client{Id: 40, Cnf: &file.Config{Crypt: false}, Flow: &file.Flow{}},
		Flow:   &file.Flow{},
		Target: &file.Target{TargetStr: "127.0.0.1:80"},
	}
	selection := backendSelection{routeUUID: "node-a", targetAddr: "127.0.0.1:80"}

	transport := server.transportForBackend(baseHost, selection)
	if transport == nil {
		t.Fatal("transportForBackend() returned nil transport")
	}

	firstCtx := context.WithValue(context.Background(), ctxRemoteAddr, "203.0.113.40:5000")
	firstCtx = context.WithValue(firstCtx, ctxHost, baseHost)
	firstCtx = context.WithValue(firstCtx, ctxRouteRuntime, server.NewRouteRuntimeContext(baseHost.Client, selection.routeUUID))
	firstCtx = withHTTPProxyBackendSelection(firstCtx, selection)
	firstConn, err := transport.DialContext(firstCtx, "tcp", "")
	if err != nil {
		t.Fatalf("first DialContext() error = %v", err)
	}
	_ = firstConn.Close()
	if bridge.lastLink == nil {
		t.Fatal("first DialContext() did not open a bridge link")
	}
	if bridge.lastLink.Crypt {
		t.Fatal("first bridge link should preserve the initial non-crypt host state")
	}

	updatedHost := &file.Host{
		Id:     baseHost.Id,
		Client: &file.Client{Id: baseHost.Client.Id, Cnf: &file.Config{Crypt: true}, Flow: &file.Flow{}},
		Flow:   &file.Flow{},
		Target: &file.Target{TargetStr: "127.0.0.1:80"},
	}
	secondCtx := context.WithValue(context.Background(), ctxRemoteAddr, "203.0.113.41:6000")
	secondCtx = context.WithValue(secondCtx, ctxHost, updatedHost)
	secondCtx = context.WithValue(secondCtx, ctxRouteRuntime, server.NewRouteRuntimeContext(updatedHost.Client, selection.routeUUID))
	secondCtx = withHTTPProxyBackendSelection(secondCtx, selection)
	secondConn, err := transport.DialContext(secondCtx, "tcp", "")
	if err != nil {
		t.Fatalf("second DialContext() error = %v", err)
	}
	_ = secondConn.Close()
	if bridge.lastLink == nil {
		t.Fatal("second DialContext() did not open a bridge link")
	}
	if !bridge.lastLink.Crypt {
		t.Fatal("second bridge link should use the per-request host state instead of the cached host snapshot")
	}
}

func TestHttpServerResolveHTTPProxyBackendStateRejectsMalformedHost(t *testing.T) {
	server := &HttpServer{
		HttpProxy: &HttpProxy{
			BaseServer:     serverproxy.NewBaseServer(&httpBackendBridgeStub{}, nil),
			HttpProxyCache: index.NewAnyIntIndex(),
		},
	}
	host := &file.Host{
		Id:   12,
		Flow: &file.Flow{},
		Client: &file.Client{
			Id:   24,
			Cnf:  &file.Config{},
			Flow: &file.Flow{},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "http://example.com/ws", nil)

	_, _, err := server.resolveHTTPProxyBackendState(req.Context(), req, host, backendSelection{})
	if !errors.Is(err, errHTTPProxyInvalidBackend) {
		t.Fatalf("resolveHTTPProxyBackendState() error = %v, want %v", err, errHTTPProxyInvalidBackend)
	}
}

func TestHttpServerDialSelectedBackendContextRejectsMalformedHost(t *testing.T) {
	bridge := &httpBackendBridgeStub{selectedRoute: "node-a"}
	server := &HttpServer{
		HttpProxy: &HttpProxy{
			BaseServer:     serverproxy.NewBaseServer(bridge, nil),
			HttpProxyCache: index.NewAnyIntIndex(),
		},
	}
	host := &file.Host{
		Id:   11,
		Flow: &file.Flow{},
		Client: &file.Client{
			Id:   23,
			Cnf:  &file.Config{},
			Flow: &file.Flow{},
		},
	}
	ctx := context.WithValue(context.Background(), ctxRemoteAddr, "203.0.113.3:7000")
	selection := backendSelection{
		routeUUID:  "node-fixed",
		targetAddr: "127.0.0.1:9003",
	}

	c, err := server.dialSelectedBackendContext(ctx, host, selection)
	if !errors.Is(err, errHTTPProxyInvalidBackend) {
		t.Fatalf("dialSelectedBackendContext() error = %v, want %v", err, errHTTPProxyInvalidBackend)
	}
	if c != nil {
		t.Fatalf("dialSelectedBackendContext() conn = %#v, want nil", c)
	}
	if bridge.lastLink != nil {
		t.Fatal("dialSelectedBackendContext() should not open a bridge link for malformed host")
	}
}

func TestHttpsServerHandleProxyRejectsMalformedHost(t *testing.T) {
	tests := []struct {
		name   string
		handle func(*HttpsServer, *file.Host, net.Conn)
	}{
		{
			name: "https proxy",
			handle: func(server *HttpsServer, host *file.Host, c net.Conn) {
				server.handleHttpsProxy(host, c, nil, "example.com")
			},
		},
		{
			name: "tls proxy",
			handle: func(server *HttpsServer, host *file.Host, c net.Conn) {
				server.handleTlsProxy(host, c, "example.com")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := &HttpsServer{
				HttpServer: &HttpServer{
					HttpProxy: &HttpProxy{
						BaseServer: serverproxy.NewBaseServer(&httpProxyNoCallBridge{}, &file.Tunnel{Flow: &file.Flow{}}),
					},
				},
			}
			host := &file.Host{
				Id:   13,
				Flow: &file.Flow{},
				Client: &file.Client{
					Id:   25,
					Cnf:  &file.Config{},
					Flow: &file.Flow{},
				},
			}
			serverConn, clientConn := net.Pipe()
			defer func() { _ = clientConn.Close() }()

			done := make(chan struct{})
			go func() {
				tt.handle(server, host, serverConn)
				close(done)
			}()

			_ = clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
			buf := make([]byte, 1)
			_, err := clientConn.Read(buf)
			if err == nil {
				t.Fatal("malformed host should close connection")
			}

			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("handler did not return after rejecting malformed host")
			}
		})
	}
}

func TestHttpsServerResolveHTTPSProxyBackendUsesSelectedRuntimeRoute(t *testing.T) {
	server := &HttpsServer{
		HttpServer: &HttpServer{
			HttpProxy: &HttpProxy{
				BaseServer: serverproxy.NewBaseServer(&httpBackendBridgeStub{}, &file.Tunnel{Flow: &file.Flow{}}),
				HttpsPort:  443,
			},
		},
	}
	host := &file.Host{
		Id:     14,
		Client: newHTTPProxyTestClient(26),
		Flow:   &file.Flow{},
		Target: &file.Target{TargetStr: "base:443"},
	}
	host.BindRuntimeOwner("node-a", &file.Host{
		Target: &file.Target{TargetStr: "127.0.0.1:8443"},
	})

	resolved, err := server.resolveHTTPSProxyBackend(host)
	if err != nil {
		t.Fatalf("resolveHTTPSProxyBackend() error = %v", err)
	}
	t.Cleanup(resolved.releaseLease)

	if resolved.host == nil {
		t.Fatal("resolveHTTPSProxyBackend() returned nil host")
	}
	if got := resolved.host.RuntimeRouteUUID(); got != "node-a" {
		t.Fatalf("resolved host route uuid = %q, want %q", got, "node-a")
	}
	if resolved.targetAddr != "127.0.0.1:8443" {
		t.Fatalf("resolved target = %q, want %q", resolved.targetAddr, "127.0.0.1:8443")
	}
	if resolved.task == nil {
		t.Fatal("resolveHTTPSProxyBackend() returned nil task")
	}
	if got := resolved.task.RuntimeRouteUUID(); got != "node-a" {
		t.Fatalf("resolved task route uuid = %q, want %q", got, "node-a")
	}
	if resolved.task.Target == nil || resolved.task.Target.TargetStr != "127.0.0.1:8443" {
		t.Fatalf("resolved task target = %+v, want selected runtime target", resolved.task.Target)
	}
}

func TestHttpsServerResolveHTTPSProxyBackendReleasesLeaseOnTargetError(t *testing.T) {
	server := &HttpsServer{
		HttpServer: &HttpServer{
			HttpProxy: &HttpProxy{
				BaseServer: serverproxy.NewBaseServer(&httpBackendBridgeStub{}, &file.Tunnel{Flow: &file.Flow{}}),
				HttpsPort:  443,
			},
		},
	}
	client := newHTTPProxyTestClient(27)
	host := &file.Host{
		Id:     15,
		Client: client,
		Flow:   &file.Flow{},
		Target: &file.Target{},
	}

	_, err := server.resolveHTTPSProxyBackend(host)
	if err == nil {
		t.Fatal("resolveHTTPSProxyBackend() error = nil, want target selection failure")
	}
	if got := host.NowConn; got != 0 {
		t.Fatalf("host.NowConn = %d, want 0 after target selection failure", got)
	}
	if got := client.NowConn; got != 0 {
		t.Fatalf("client.NowConn = %d, want 0 after target selection failure", got)
	}
}
