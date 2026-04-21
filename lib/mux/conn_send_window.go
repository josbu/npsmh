package mux

import (
	"errors"
	"fmt"
	"io"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

type sendWindow struct {
	window
	buf         []byte
	setSizeCh   chan struct{}
	setSizeOnce sync.Once
	timeoutMu   sync.RWMutex
	timeout     time.Time
	deadlineCh  chan struct{}
	priority    bool
	// send window receive the receiving window max size and read size
	// done size store the size send window has sent, send and read will be totally equal
	// so send minus read, send window can get the current window size remaining
}

func (Self *sendWindow) New(mux *Mux) {
	Self.priority = false
	Self.setSizeCh = make(chan struct{}, 1)
	Self.deadlineCh = make(chan struct{}, 1)
	Self.maxSizeDone = Self.pack(defaultInitialConnWindow, 0, false)
	Self.mux = mux
	Self.window.New()
}

func (Self *sendWindow) SetSendBuf(buf []byte) {
	// send window buff from conn write method, set it to send window
	Self.buf = buf
	Self.off = 0
}

func (Self *sendWindow) remainingSize(maxSize, send uint32) uint32 {
	l := int64(maxSize&mask31) - int64(send&mask31)
	if l > 0 {
		return uint32(l)
	}
	return 0
}

func (Self *sendWindow) safeCloseSetSizeCh() {
	Self.setSizeOnce.Do(func() {
		close(Self.setSizeCh)
	})
}

func (Self *sendWindow) SetSize(currentMaxSizeDone uint64) (closed bool) {
	// set the window size from receive window
	defer func() {
		if r := recover(); r != nil {
			closed = true
		}
	}()
	if Self.IsClosed() {
		Self.safeCloseSetSizeCh()
		return true
	}
	var maxsize, send uint32
	var wait, newWait bool
	currentMaxSize, read, _ := Self.unpack(currentMaxSizeDone)
	for {
		ptrs := atomic.LoadUint64(&Self.maxSizeDone)
		maxsize, send, wait = Self.unpack(ptrs)
		if read > send {
			_ = Self.mux.closeWithReason(fmt.Sprintf(
				"invalid send window update max=%d read=%d outstanding=%d",
				currentMaxSize,
				read,
				send,
			))
			return true
		}
		if read == 0 && currentMaxSize == maxsize {
			return
		}
		outstanding := (send - read) & mask31
		remain := Self.remainingSize(currentMaxSize, outstanding)
		nextWait := remain == 0 && wait

		// remain > 0, change wait to false. or remain == 0, wait is false, just keep it
		if atomic.CompareAndSwapUint64(&Self.maxSizeDone, ptrs, Self.pack(currentMaxSize, outstanding, nextWait)) {
			newWait = nextWait
			break
		}
		// another goroutine change wait status or window size
	}
	if wait && !newWait {
		// send window into the wait status, need notice the channel
		Self.allow()
	}
	// send window not into the wait status, so just do slide
	return false
}

func (Self *sendWindow) allow() {
	if Self.IsClosed() {
		return
	}
	defer func() { _ = recover() }()
	select {
	case Self.setSizeCh <- struct{}{}:
	default:
	}
}

func (Self *sendWindow) sent(sentSize uint32) {
	var maxSie, send uint32
	var wait bool
	for {
		ptrs := atomic.LoadUint64(&Self.maxSizeDone)
		maxSie, send, wait = Self.unpack(ptrs)
		if (send+sentSize)&mask31 < send {
			// overflow
			runtime.Gosched()
			continue
		}
		if atomic.CompareAndSwapUint64(&Self.maxSizeDone, ptrs, Self.pack(maxSie, send+sentSize, wait)) {
			// set the send size
			break
		}
	}
}

func (Self *sendWindow) rollbackSent(sentSize uint32) {
	if sentSize == 0 {
		return
	}
	if Self.off >= sentSize {
		Self.off -= sentSize
	} else {
		Self.off = 0
	}
	for {
		ptrs := atomic.LoadUint64(&Self.maxSizeDone)
		maxSize, send, wait := Self.unpack(ptrs)
		if send < sentSize {
			sentSize = send
		}
		if atomic.CompareAndSwapUint64(&Self.maxSizeDone, ptrs, Self.pack(maxSize, send-sentSize, wait)) {
			return
		}
	}
}

func (Self *sendWindow) WriteTo() (p []byte, sendSize uint32, part bool, err error) {
	// returns buf segments, return only one segments, need a loop outside
	// until err = io.EOF
	if Self.IsClosed() {
		return nil, 0, false, errors.New("conn.writeWindow: window closed")
	}
	if Self.off >= uint32(len(Self.buf)) {
		return nil, 0, false, io.EOF
	}
	var maxSize, send uint32
start:
	ptrs := atomic.LoadUint64(&Self.maxSizeDone)
	maxSize, send, _ = Self.unpack(ptrs)
	remain := Self.remainingSize(maxSize, send)
	if remain == 0 {
		if !atomic.CompareAndSwapUint64(&Self.maxSizeDone, ptrs, Self.pack(maxSize, send, true)) {
			// just change the status wait status
			goto start // another goroutine change the window, try again
		}
		// into the wait status
		err = Self.waitReceiveWindow()
		if err != nil {
			return nil, 0, false, err
		}
		goto start
	}

	if Self.off > uint32(len(Self.buf)) {
		return nil, 0, false, io.EOF
	}

	if len(Self.buf[Self.off:]) > maximumSegmentSize {
		sendSize = maximumSegmentSize
	} else {
		sendSize = uint32(len(Self.buf[Self.off:]))
	}
	if remain < sendSize {
		// usable window size is smaller than
		// window MAXIMUM_SEGMENT_SIZE or send buf left
		sendSize = remain
	}
	if sendSize < uint32(len(Self.buf[Self.off:])) {
		part = true
	}
	p = Self.buf[Self.off : sendSize+Self.off]
	Self.off += sendSize
	Self.sent(sendSize)
	return
}

func (Self *sendWindow) waitReceiveWindow() (err error) {
	for {
		deadline := Self.getTimeOut()
		if deadline.IsZero() {
			select {
			case _, ok := <-Self.setSizeCh:
				if !ok {
					return errors.New("conn.writeWindow: window closed")
				}
				return nil
			case <-Self.closeOpCh:
				return errors.New("conn.writeWindow: window closed")
			case <-Self.deadlineCh:
				continue
			}
		}

		t := time.Until(deadline)
		if t <= 0 {
			return errSendWindowTimeout
		}
		timer := time.NewTimer(t)
		select {
		case _, ok := <-Self.setSizeCh:
			stopTimer(timer)
			if !ok {
				return errors.New("conn.writeWindow: window closed")
			}
			return nil
		case <-timer.C:
			return errSendWindowTimeout
		case <-Self.closeOpCh:
			stopTimer(timer)
			return errors.New("conn.writeWindow: window closed")
		case <-Self.deadlineCh:
			stopTimer(timer)
		}
	}
}

func (Self *sendWindow) WriteFull(buf []byte, id int32) (n int, err error) {
	Self.SetSendBuf(buf) // set the buf to send window
	var bufSeg []byte
	var part bool
	var l uint32
	for {
		bufSeg, l, part, err = Self.WriteTo()
		// get the buf segments from send window
		if bufSeg == nil && !part && err == io.EOF {
			// send window is drain, break the loop
			err = nil
			break
		}
		if err != nil {
			break
		}
		// Only report bytes once the frame has been accepted by the mux queue.
		if part {
			err = Self.mux.sendInfo(muxNewMsgPart, id, Self.priority, bufSeg)
		} else {
			err = Self.mux.sendInfo(muxNewMsg, id, Self.priority, bufSeg)
		}
		if err != nil {
			Self.rollbackSent(l)
			break
		}
		n += int(l)
		// send to other side, not send nil data to other side
	}
	return
}

func (Self *sendWindow) SetTimeOut(t time.Time) {
	// waiting for receive a receiving window size
	Self.timeoutMu.Lock()
	Self.timeout = t
	Self.timeoutMu.Unlock()
	notifySignal(Self.deadlineCh)
}

func (Self *sendWindow) getTimeOut() time.Time {
	Self.timeoutMu.RLock()
	defer Self.timeoutMu.RUnlock()
	return Self.timeout
}
