package proxy

import (
	"net"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
)

func TestP2PServerCloseWaitsForActiveProbeWorkers(t *testing.T) {
	server := NewP2PServer(0, false)
	server.workers = make(chan struct{}, 1)

	probeStarted := make(chan struct{})
	releaseProbe := make(chan struct{})
	server.probeHook = func(*net.UDPConn, int, *net.UDPAddr, []byte) {
		close(probeStarted)
		<-releaseProbe
	}

	buf := common.BufPoolUdp.Get()
	ingress := p2pProbeIngress{
		addr: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 32001},
		data: buf[:1],
	}
	if !server.acquireWorker() {
		releaseP2PProbeIngress(ingress)
		t.Fatal("acquireWorker() = false, want true")
	}
	server.dispatchProbeIngress(nil, 0, ingress)

	select {
	case <-probeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("probe worker did not start")
	}

	closeDone := make(chan struct{})
	go func() {
		_ = server.Close()
		close(closeDone)
	}()

	select {
	case <-closeDone:
		t.Fatal("Close() returned before active probe worker exited")
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseProbe)

	select {
	case <-closeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Close() did not finish after active probe worker exit")
	}
}
