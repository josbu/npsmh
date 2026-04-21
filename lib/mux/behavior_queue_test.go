package mux

import (
	"bytes"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSetWriteDeadlineWakesBlockedWrite(t *testing.T) {
	left, right := newConnPair(t)
	defer func() { _ = left.Close() }()
	defer func() { _ = right.Close() }()

	done := make(chan error, 1)
	payload := bytes.Repeat([]byte("x"), maximumSegmentSize*80)
	go func() {
		_, err := left.Write(payload)
		done <- err
	}()

	time.Sleep(50 * time.Millisecond)
	if err := left.SetWriteDeadline(time.Now().Add(80 * time.Millisecond)); err != nil {
		t.Fatalf("SetWriteDeadline() error = %v", err)
	}

	select {
	case err := <-done:
		var netErr net.Error
		if !errors.As(err, &netErr) || !netErr.Timeout() {
			t.Fatalf("Write() error = %v, want timeout net.Error", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Write() did not unblock after write deadline")
	}
}

func TestConnConcurrentWritesPreserveByteCount(t *testing.T) {
	left, right := newConnPair(t)
	defer func() { _ = right.Close() }()

	const (
		writers   = 16
		chunkSize = 8 * 1024
	)

	expected := writers * chunkSize
	readDone := make(chan []byte, 1)
	go func() {
		buf := make([]byte, expected)
		_, err := io.ReadFull(right, buf)
		if err != nil {
			readDone <- nil
			return
		}
		readDone <- buf
	}()

	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(seed byte) {
			defer wg.Done()
			payload := bytes.Repeat([]byte{seed}, chunkSize)
			if _, err := left.Write(payload); err != nil {
				t.Errorf("Write() error = %v", err)
			}
		}(byte(i + 1))
	}
	wg.Wait()

	data := <-readDone
	if len(data) != expected {
		t.Fatalf("read length = %d, want %d", len(data), expected)
	}
	_ = left.Close()
}

func TestBusyStreamWriteDoesNotStarveInteractiveStream(t *testing.T) {
	m := newQueueOnlyMuxForTest()
	defer drainWriteQueueForTest(m)

	busy := newQueueOnlyConnForTest(m, 1)
	interactive := newQueueOnlyConnForTest(m, 3)
	t.Cleanup(func() { busy.closeLocal() })
	t.Cleanup(func() { interactive.closeLocal() })

	busyPayload := bytes.Repeat([]byte("b"), maximumSegmentSize*4)
	busyErrCh := make(chan error, 1)
	go func() {
		_, err := busy.Write(busyPayload)
		busyErrCh <- err
	}()

	waitForCondition(t, time.Second, func() bool {
		return m.WriteQueueLen() >= 4
	})

	if _, err := interactive.Write([]byte("ping")); err != nil {
		t.Fatalf("interactive Write() error = %v", err)
	}

	select {
	case err := <-busyErrCh:
		if err != nil {
			t.Fatalf("busy Write() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("busy Write() did not finish queuing data")
	}

	first := popQueuedPackOrFatal(t, m)
	second := popQueuedPackOrFatal(t, m)
	third := popQueuedPackOrFatal(t, m)
	defer recycleMuxPackForTest(first)
	defer recycleMuxPackForTest(second)
	defer recycleMuxPackForTest(third)

	if first.id != busy.connId || !isMuxStreamDataFlag(first.flag) {
		t.Fatalf("first queued pack = (id=%d flag=%d), want busy stream data", first.id, first.flag)
	}
	if second.id != interactive.connId || !isMuxStreamDataFlag(second.flag) {
		t.Fatalf("second queued pack = (id=%d flag=%d), want interactive stream data before draining busy stream", second.id, second.flag)
	}
	if third.id != busy.connId || !isMuxStreamDataFlag(third.flag) {
		t.Fatalf("third queued pack = (id=%d flag=%d), want busy stream data after interactive stream gets service", third.id, third.flag)
	}
}

func TestControlFrameBypassesBusyStreamBacklogAtMuxLevel(t *testing.T) {
	m := newQueueOnlyMuxForTest()
	defer drainWriteQueueForTest(m)

	busy := newQueueOnlyConnForTest(m, 1)
	t.Cleanup(func() { busy.closeLocal() })

	if _, err := busy.Write(bytes.Repeat([]byte("b"), maximumSegmentSize*3)); err != nil {
		t.Fatalf("busy Write() error = %v", err)
	}

	m.sendInfo(muxMsgSendOk, busy.connId, false, busy.receiveWindow.pack(defaultInitialConnWindow, 0, false))

	first := popQueuedPackOrFatal(t, m)
	second := popQueuedPackOrFatal(t, m)
	defer recycleMuxPackForTest(first)
	defer recycleMuxPackForTest(second)

	if first.flag != muxMsgSendOk {
		t.Fatalf("first queued pack flag = %d, want control frame %d", first.flag, muxMsgSendOk)
	}
	if second.id != busy.connId || !isMuxStreamDataFlag(second.flag) {
		t.Fatalf("second queued pack = (id=%d flag=%d), want busy stream data after control frame", second.id, second.flag)
	}
}

func TestFlushWriterPropagatesWriteError(t *testing.T) {
	wantErr := errors.New("boom")
	conn := &flushWriterTestConn{writeErr: wantErr}
	fw := NewFlushWriter(conn)

	if _, err := fw.Write([]byte("hello")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := fw.Flush(); !errors.Is(err, wantErr) {
		t.Fatalf("Flush() error = %v, want %v", err, wantErr)
	}
	if _, err := fw.Write([]byte("again")); !errors.Is(err, wantErr) {
		t.Fatalf("Write() after failure error = %v, want %v", err, wantErr)
	}
	if err := fw.Close(); !errors.Is(err, wantErr) {
		t.Fatalf("Close() error = %v, want %v", err, wantErr)
	}
}

func TestFlushWriterAppliesConfiguredWriteDeadline(t *testing.T) {
	conn := &flushWriterTestConn{}
	fw := NewFlushWriterWithTimeout(conn, 50*time.Millisecond)

	if _, err := fw.Write([]byte("hello")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := fw.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if conn.writeDeadlineHits < 2 {
		t.Fatalf("SetWriteDeadline() hit count = %d, want at least 2 (set and clear)", conn.writeDeadlineHits)
	}
	if !conn.lastWriteDeadline.IsZero() {
		t.Fatalf("last write deadline = %v, want cleared zero deadline", conn.lastWriteDeadline)
	}
}

func TestSendInfoFailsWhenWriteQueueStopped(t *testing.T) {
	m := newQueueOnlyMuxForTest()
	m.writeQueue.Stop()

	err := m.sendInfo(muxPeerHello, 1, true, nil)
	if err == nil {
		t.Fatal("sendInfo() error = nil, want queue stopped error")
	}
	if !strings.Contains(err.Error(), "write queue stopped") {
		t.Fatalf("sendInfo() error = %q, want queue stopped detail", err.Error())
	}
	if got := m.WriteQueueLen(); got != 0 {
		t.Fatalf("WriteQueueLen() = %d, want 0 after rejected send", got)
	}
}

func TestConnWriteFailsWhenWriteQueueStopped(t *testing.T) {
	m := newQueueOnlyMuxForTest()
	conn := newQueueOnlyConnForTest(m, 1)
	t.Cleanup(func() { conn.closeLocal() })
	m.writeQueue.Stop()

	n, err := conn.Write([]byte("hello"))
	if err == nil {
		t.Fatal("Write() error = nil, want queue stopped error")
	}
	if !strings.Contains(err.Error(), "write queue stopped") {
		t.Fatalf("Write() error = %q, want queue stopped detail", err.Error())
	}
	if n != 0 {
		t.Fatalf("Write() bytes = %d, want 0 when frame was not queued", n)
	}
	if got := m.WriteQueueLen(); got != 0 {
		t.Fatalf("WriteQueueLen() = %d, want 0 after failed write", got)
	}
}

func TestSessionReceiveAccountingTracksBufferedBytes(t *testing.T) {
	oldConnWindow := MaxConnReceiveWindow
	oldSessionWindow := MaxSessionReceiveWindow
	MaxConnReceiveWindow = defaultInitialConnWindow * 4
	MaxSessionReceiveWindow = uint64(defaultInitialConnWindow * 4)
	defer func() {
		MaxConnReceiveWindow = oldConnWindow
		MaxSessionReceiveWindow = oldSessionWindow
	}()

	left, rightNet := newConnPair(t)
	defer func() { _ = left.Close() }()
	right := rightNet.(*Conn)
	defer func() { _ = right.Close() }()

	payload := bytes.Repeat([]byte("z"), maximumSegmentSize*4)
	writeDone := make(chan error, 1)
	go func() {
		_, err := left.Write(payload)
		writeDone <- err
	}()

	waitForCondition(t, 2*time.Second, func() bool {
		return right.receiveWindow.bufferedBytes() >= uint32(len(payload)) &&
			right.receiveWindow.mux.SessionReceiveQueued() >= uint64(len(payload))
	})

	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Write()")
	}

	buf := make([]byte, len(payload))
	if _, err := io.ReadFull(right, buf); err != nil {
		t.Fatalf("ReadFull() error = %v", err)
	}

	waitForCondition(t, 2*time.Second, func() bool {
		return right.receiveWindow.bufferedBytes() == 0 && right.receiveWindow.mux.SessionReceiveQueued() == 0
	})
}
