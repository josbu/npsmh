package mux

import (
	"sync"
)

const (
	poolSizeBuffer = 4096
	//poolSizeBuffer = 4096 * 10                           // a mux packager total length
	poolSizeWindow       = poolSizeBuffer - 2 - 4 - 4 - 1 // content length
	poolSizeWindowBuffer = poolSizeBuffer
)

func normalizeForPut(buf []byte, size int) ([]byte, bool) {
	if cap(buf) != size {
		return nil, false
	}
	return buf[:size:size], true
}

type windowBufferPool struct {
	pool sync.Pool
}

func newWindowBufferPool() *windowBufferPool {
	return &windowBufferPool{
		pool: sync.Pool{
			New: func() any {
				return new([poolSizeWindowBuffer]byte)
			},
		},
	}
}

func (p *windowBufferPool) Get() []byte {
	return p.pool.Get().(*[poolSizeWindowBuffer]byte)[:]
}

func (p *windowBufferPool) Put(buf []byte) {
	b, ok := normalizeForPut(buf, poolSizeWindowBuffer)
	if !ok {
		return
	}
	p.pool.Put((*[poolSizeWindowBuffer]byte)(b))
}

type muxPackagerPool struct {
	pool sync.Pool
}

func newMuxPackagerPool() *muxPackagerPool {
	return &muxPackagerPool{
		pool: sync.Pool{
			New: func() any {
				pack := muxPackager{}
				return &pack
			},
		},
	}
}

func (p *muxPackagerPool) Get() *muxPackager {
	return p.pool.Get().(*muxPackager)
}

func (p *muxPackagerPool) Put(pack *muxPackager) {
	pack.reset()
	p.pool.Put(pack)
}

type listElementPool struct {
	pool sync.Pool
}

func newListElementPool() *listElementPool {
	return &listElementPool{
		pool: sync.Pool{
			New: func() any {
				element := listElement{}
				return &element
			},
		},
	}
}

func (p *listElementPool) Get() *listElement {
	return p.pool.Get().(*listElement)
}

func (p *listElementPool) Put(element *listElement) {
	element.Reset()
	p.pool.Put(element)
}

var (
	muxPack    = newMuxPackagerPool()
	windowBuff = newWindowBufferPool()
	listEle    = newListElementPool()
)
