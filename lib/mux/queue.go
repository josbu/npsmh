package mux

import (
	"runtime"
	"sync"
	"sync/atomic"
)

type priorityQueue struct {
	length          uint64
	controlHead     *muxPackager
	controlTail     *muxPackager
	priorityStreams streamBucketSet
	streams         streamBucketSet
	priorityBurst   uint8
	highWater       uint64
	lowWater        uint64
	stop            uint32
	mu              sync.Mutex
	cond            *sync.Cond
}

func (Self *priorityQueue) New() {
	Self.priorityStreams.init()
	Self.streams.init()
	Self.highWater, Self.lowWater = normalizeWriteQueueWatermarks(0, 0)
	locker := new(sync.Mutex)
	Self.cond = sync.NewCond(locker)
}

func (Self *priorityQueue) ConfigureWatermarks(high, low uint64) {
	Self.highWater, Self.lowWater = normalizeWriteQueueWatermarks(high, low)
}

func (Self *priorityQueue) Push(packager *muxPackager) bool {
	Self.mu.Lock()
	if atomic.LoadUint32(&Self.stop) == 1 {
		Self.mu.Unlock()
		return false
	}
	atomic.AddUint64(&Self.length, 1)
	Self.push(packager)
	Self.mu.Unlock()

	Self.cond.L.Lock()
	Self.cond.Signal()
	Self.cond.L.Unlock()
	return true
}

func (Self *priorityQueue) push(packager *muxPackager) {
	if isMuxStreamOrderedFlag(packager.flag) {
		if packager.priority {
			Self.priorityStreams.push(packager)
		} else {
			Self.streams.push(packager)
		}
		return
	}
	Self.pushControl(packager)
}

func (Self *priorityQueue) Len() uint64 {
	return atomic.LoadUint64(&Self.length)
}

func (Self *priorityQueue) HighWater() uint64 {
	return Self.highWater
}

func (Self *priorityQueue) LowWater() uint64 {
	return Self.lowWater
}

func (Self *priorityQueue) afterPop() {
	newLen := atomic.AddUint64(&Self.length, ^uint64(0))
	if newLen < Self.lowWater {
		oldLen := newLen + 1
		if oldLen >= Self.lowWater {
			Self.cond.L.Lock()
			Self.cond.Broadcast()
			Self.cond.L.Unlock()
		}
	}
}

const maxPriorityBurst uint8 = 8

func (Self *priorityQueue) Pop() (packager *muxPackager) {
	var iter bool
	for {
		packager = Self.TryPop()
		if packager != nil {
			return
		}
		if atomic.LoadUint32(&Self.stop) == 1 {
			return
		}
		if iter {
			break
		}
		iter = true
		runtime.Gosched()
	}
	Self.cond.L.Lock()
	defer Self.cond.L.Unlock()
	for packager = Self.TryPop(); packager == nil; {
		if atomic.LoadUint32(&Self.stop) == 1 {
			return
		}
		Self.cond.Wait()
		packager = Self.TryPop()
	}
	return
}

func (Self *priorityQueue) TryPop() (packager *muxPackager) {
	Self.mu.Lock()
	defer Self.mu.Unlock()

	if packager = Self.popControl(); packager != nil {
		Self.afterPop()
		return
	}
	if packager = Self.popStream(); packager != nil {
		Self.afterPop()
		return
	}
	return
}

func (Self *priorityQueue) pushControl(packager *muxPackager) {
	packager.queueNext = nil
	if Self.controlTail == nil {
		Self.controlHead = packager
		Self.controlTail = packager
		return
	}
	Self.controlTail.queueNext = packager
	Self.controlTail = packager
}

func (Self *priorityQueue) popControl() *muxPackager {
	if Self.controlHead == nil {
		return nil
	}
	packager := Self.controlHead
	Self.controlHead = packager.queueNext
	if Self.controlHead == nil {
		Self.controlTail = nil
	}
	packager.queueNext = nil
	return packager
}

func (Self *priorityQueue) popStream() *muxPackager {
	priorityReady := Self.priorityStreams.hasPending()
	normalReady := Self.streams.hasPending()

	if priorityReady && (!normalReady || Self.priorityBurst < maxPriorityBurst) {
		packager := Self.priorityStreams.pop()
		if packager != nil {
			if normalReady {
				Self.priorityBurst++
			} else {
				Self.priorityBurst = 0
			}
			return packager
		}
		priorityReady = false
	}

	if normalReady {
		packager := Self.streams.pop()
		if packager != nil {
			Self.priorityBurst = 0
			return packager
		}
	}

	if priorityReady {
		packager := Self.priorityStreams.pop()
		if packager != nil {
			Self.priorityBurst = 0
			return packager
		}
	}

	return nil
}

func (Self *priorityQueue) Stop() {
	Self.mu.Lock()
	atomic.StoreUint32(&Self.stop, 1)
	Self.mu.Unlock()
	Self.cond.L.Lock()
	Self.cond.Broadcast()
	Self.cond.L.Unlock()
}
