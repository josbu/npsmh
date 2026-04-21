package mux

import (
	"errors"
	"io"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

type listElement struct {
	Buf  []byte
	L    uint16
	Part bool
}

func (Self *listElement) Reset() {
	Self.L = 0
	Self.Buf = nil
	Self.Part = false
}

func newListElement(buf []byte, l uint16, part bool) (element *listElement, err error) {
	if uint16(len(buf)) < l {
		err = errors.New("listElement: buf length not match")
		return
	}
	element = listEle.Get()
	element.Buf = buf
	element.L = l
	element.Part = part
	return
}

type receiveWindowQueue struct {
	lengthWait uint64
	chain      *bufChain
	stopOp     chan struct{}
	stopOnce   sync.Once
	readOp     chan struct{}
	mu         sync.Mutex
	timeoutMu  sync.RWMutex
	deadlineCh chan struct{}
	// https://golang.org/pkg/sync/atomic/#pkg-note-BUG
	// On non-Linux ARM, the 64-bit functions use instructions unavailable before the ARMv6k core.
	// On ARM, x86-32, and 32-bit MIPS, it is the caller's responsibility
	// to arrange for 64-bit alignment of 64-bit words accessed atomically.
	// The first word in a variable or in an allocated struct, array, or slice can be relied upon to be 64-bit aligned.

	// if there are implicit struct, careful the first word
	timeout time.Time
}

func newReceiveWindowQueue() *receiveWindowQueue {
	queue := receiveWindowQueue{
		chain:      new(bufChain),
		stopOp:     make(chan struct{}),
		readOp:     make(chan struct{}),
		deadlineCh: make(chan struct{}, 1),
	}
	queue.chain.new(64)
	return &queue
}

func (Self *receiveWindowQueue) Push(element *listElement) {
	var length, wait uint32
	Self.mu.Lock()
	for {
		ptrs := atomic.LoadUint64(&Self.lengthWait)
		length, wait = Self.chain.head.unpack(ptrs)
		length += uint32(element.L)
		if atomic.CompareAndSwapUint64(&Self.lengthWait, ptrs, Self.chain.head.pack(length, 0)) {
			break
		}
		// another goroutine change the length or into wait, make sure
	}
	Self.chain.pushHead(unsafe.Pointer(element))
	Self.mu.Unlock()
	if wait == 1 {
		Self.allowPop()
	}
}

func (Self *receiveWindowQueue) Pop() (element *listElement, err error) {
	var length uint32
startPop:
	ptrs := atomic.LoadUint64(&Self.lengthWait)
	length, _ = Self.chain.head.unpack(ptrs)
	if length == 0 {
		if !atomic.CompareAndSwapUint64(&Self.lengthWait, ptrs, Self.chain.head.pack(0, 1)) {
			goto startPop // another goroutine is pushing
		}
		err = Self.waitPush()
		// there is no more data in queue, wait for it
		if err != nil {
			return
		}
		goto startPop // wait finish, trying to Get the New status
	}
	// length is not zero, so try to pop
	for {
		element = Self.TryPop()
		if element != nil {
			return
		}
		runtime.Gosched() // another goroutine is still pushing
	}
}

func (Self *receiveWindowQueue) TryPop() (element *listElement) {
	Self.mu.Lock()
	defer Self.mu.Unlock()

	ptr, ok := Self.chain.popTail()
	if ok {
		element = (*listElement)(ptr)
		atomic.AddUint64(&Self.lengthWait, ^(uint64(element.L)<<dequeueBits - 1))
		return
	}
	return nil
}

func (Self *receiveWindowQueue) allowPop() (closed bool) {
	select {
	case Self.readOp <- struct{}{}:
		return false
	case <-Self.stopOp:
		return true
	}
}

func (Self *receiveWindowQueue) waitPush() (err error) {
	for {
		deadline := Self.getTimeOut()
		if deadline.IsZero() {
			// not Set the timeout, so wait for it without timeout, just like a tcp connection
			select {
			case <-Self.readOp:
				return nil
			case <-Self.stopOp:
				err = io.EOF
				return
			case <-Self.deadlineCh:
				continue
			}
		}

		t := time.Until(deadline)
		if t <= 0 {
			return errReceiveWindowTimeout
		}
		timer := time.NewTimer(t)
		select {
		case <-Self.readOp:
			stopTimer(timer)
			return nil
		case <-Self.stopOp:
			stopTimer(timer)
			err = io.EOF
			return
		case <-timer.C:
			return errReceiveWindowTimeout
		case <-Self.deadlineCh:
			stopTimer(timer)
		}
	}
}

func (Self *receiveWindowQueue) Len() (n uint32) {
	ptrs := atomic.LoadUint64(&Self.lengthWait)
	n, _ = Self.chain.head.unpack(ptrs)
	// just for unpack method use
	return
}

func (Self *receiveWindowQueue) Stop() {
	Self.stopOnce.Do(func() {
		close(Self.stopOp)
	})
}

func (Self *receiveWindowQueue) SetTimeOut(t time.Time) {
	Self.timeoutMu.Lock()
	Self.timeout = t
	Self.timeoutMu.Unlock()
	notifySignal(Self.deadlineCh)
}

func (Self *receiveWindowQueue) getTimeOut() time.Time {
	Self.timeoutMu.RLock()
	defer Self.timeoutMu.RUnlock()
	return Self.timeout
}
