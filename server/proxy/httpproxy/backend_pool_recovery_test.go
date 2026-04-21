package httpproxy

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/djylb/nps/lib/index"
	serverproxy "github.com/djylb/nps/server/proxy"
)

type backendPoolInvalidCacheStub struct {
	closeCalls atomic.Int32
}

func (s *backendPoolInvalidCacheStub) CloseIdleConnections() {
	s.closeCalls.Add(1)
}

func TestHttpServerBackendTransportPoolCoalescesConcurrentInvalidCacheRecovery(t *testing.T) {
	cache := index.NewAnyIntIndex()
	invalid := &backendPoolInvalidCacheStub{}
	const hostID = 41
	cache.Add(hostID, invalid)

	server := &HttpServer{
		HttpProxy: &HttpProxy{
			BaseServer:     serverproxy.NewBaseServer(nil, nil),
			HttpProxyCache: cache,
		},
	}

	const callers = 32
	results := make([]*backendTransportPool, callers)
	var start sync.WaitGroup
	start.Add(1)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			start.Wait()
			results[idx] = server.backendTransportPool(hostID)
		}(i)
	}
	start.Done()
	wg.Wait()

	if results[0] == nil {
		t.Fatal("backendTransportPool() returned nil pool")
	}
	for i := 1; i < len(results); i++ {
		if results[i] != results[0] {
			t.Fatal("concurrent invalid-cache recovery should converge on one shared backend pool")
		}
	}

	cached, ok := cache.Get(hostID)
	if !ok {
		t.Fatal("backend pool was not cached after recovery")
	}
	pool, ok := cached.(*backendTransportPool)
	if !ok || pool == nil {
		t.Fatalf("cached pool type = %T, want *backendTransportPool", cached)
	}
	if pool != results[0] {
		t.Fatal("cached backend pool should match the recovered shared pool")
	}
	if got := invalid.closeCalls.Load(); got != 1 {
		t.Fatalf("invalid cache close calls = %d, want 1", got)
	}
}
