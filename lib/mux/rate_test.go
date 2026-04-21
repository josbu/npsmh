package mux

import (
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

func TestRateGetDisabledReturnsImmediately(t *testing.T) {
	r := NewRate(0)

	start := time.Now()
	r.Get(1024)
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("Get() elapsed = %s, want immediate return for disabled rate", elapsed)
	}
}

func TestRateStopIsIdempotent(t *testing.T) {
	r := NewRate(1024)
	r.Stop()
	r.Stop()
}

func TestRateGetHandlesRequestLargerThanBucket(t *testing.T) {
	r := NewRate(4)
	atomic.StoreInt64(&r.bucketSurplusSize, r.bucketSize)

	done := make(chan struct{})
	go func() {
		r.Get(r.bucketSize + 1)
		close(done)
	}()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(&r.bucketSurplusSize) == 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if got := atomic.LoadInt64(&r.bucketSurplusSize); got != 0 {
		t.Fatalf("bucketSurplusSize = %d, want first bucket-sized chunk consumed", got)
	}

	select {
	case <-done:
		t.Fatal("Get() returned before the final chunk was available")
	default:
	}

	r.ReturnBucket(1)

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Get() did not complete after the final chunk was returned")
	}
}

func TestRateCurrentRateHandlesNilReceiver(t *testing.T) {
	var r *Rate
	if got := r.CurrentRate(); got != 0 {
		t.Fatalf("CurrentRate() = %d, want 0 for nil receiver", got)
	}
}

func TestRateConnExposesWrappedConnCapabilities(t *testing.T) {
	left, right := net.Pipe()
	defer func() { _ = left.Close() }()
	defer func() { _ = right.Close() }()

	closeCh := make(chan struct{})
	adapted := AdaptConn(left, ConnCapabilities{
		RawConn:   left,
		CloseChan: closeCh,
	})
	wrapped := NewRateConn(nil, adapted)

	if got := unwrapSocketConn(wrapped); got != left {
		t.Fatalf("unwrapSocketConn(rateConn) = %p, want wrapped conn %p", got, left)
	}

	done := make(chan string, 1)
	watchConnDone(wrapped, func(reason string) {
		done <- reason
	})

	close(closeCh)

	select {
	case reason := <-done:
		if reason != "underlying connection closed" {
			t.Fatalf("watchConnDone(rateConn) reason = %q, want close-chan reason", reason)
		}
	case <-time.After(time.Second):
		t.Fatal("watchConnDone(rateConn) did not react")
	}
}

func TestRateConnHandlesNilConnAndNilRate(t *testing.T) {
	rc := NewRateConn(nil, nil)
	if _, err := rc.Write([]byte("x")); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Write() error = %v, want net.ErrClosed", err)
	}

	conn := &flushWriterTestConn{}
	rc = NewRateConn(nil, conn)
	if n, err := rc.Write([]byte("ok")); err != nil || n != 2 {
		t.Fatalf("Write() = %d, %v, want 2, nil", n, err)
	}
}
