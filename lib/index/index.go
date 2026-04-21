package index

import "sync"

type idleConnectionCloser interface {
	CloseIdleConnections()
}

type StringIDIndex struct {
	mu   sync.RWMutex
	data map[string]int
}

func NewStringIDIndex(initialCapacity ...int) *StringIDIndex {
	var cap0 int
	if len(initialCapacity) > 0 && initialCapacity[0] > 0 {
		cap0 = initialCapacity[0]
	}
	idx := &StringIDIndex{}
	if cap0 > 0 {
		idx.data = make(map[string]int, cap0)
	} else {
		idx.data = make(map[string]int)
	}
	return idx
}

func (idx *StringIDIndex) Add(key string, id int) {
	idx.mu.Lock()
	idx.data[key] = id
	idx.mu.Unlock()
}

func (idx *StringIDIndex) Get(key string) (id int, ok bool) {
	idx.mu.RLock()
	id, ok = idx.data[key]
	idx.mu.RUnlock()
	return
}

func (idx *StringIDIndex) Remove(key string) {
	idx.mu.Lock()
	delete(idx.data, key)
	idx.mu.Unlock()
}

func (idx *StringIDIndex) Clear() {
	idx.mu.Lock()
	clear(idx.data)
	idx.mu.Unlock()
}

type StringIndex struct {
	mu   sync.RWMutex
	data map[string]string
}

func NewStringIndex(initialCapacity ...int) *StringIndex {
	var cap0 int
	if len(initialCapacity) > 0 && initialCapacity[0] > 0 {
		cap0 = initialCapacity[0]
	}
	idx := &StringIndex{}
	if cap0 > 0 {
		idx.data = make(map[string]string, cap0)
	} else {
		idx.data = make(map[string]string)
	}
	return idx
}

func (idx *StringIndex) Add(key, value string) {
	idx.mu.Lock()
	idx.data[key] = value
	idx.mu.Unlock()
}

func (idx *StringIndex) Get(key string) (value string, ok bool) {
	idx.mu.RLock()
	value, ok = idx.data[key]
	idx.mu.RUnlock()
	return
}

func (idx *StringIndex) Remove(key string) {
	idx.mu.Lock()
	delete(idx.data, key)
	idx.mu.Unlock()
}

func (idx *StringIndex) Clear() {
	idx.mu.Lock()
	clear(idx.data)
	idx.mu.Unlock()
}

type AnyStringIndex struct {
	data sync.Map // map[string]interface{}
}

func NewAnyStringIndex() *AnyStringIndex {
	return &AnyStringIndex{}
}

func (idx *AnyStringIndex) Add(key string, value interface{}) {
	idx.data.Store(key, value)
}

func (idx *AnyStringIndex) Get(key string) (value interface{}, ok bool) {
	return idx.data.Load(key)
}

func (idx *AnyStringIndex) Remove(key string) {
	idx.data.Delete(key)
}

func (idx *AnyStringIndex) Clear() {
	idx.data.Clear()
}

type AnyIntIndex struct {
	data sync.Map // map[int]interface{}
}

func NewAnyIntIndex() *AnyIntIndex {
	return &AnyIntIndex{}
}

func (idx *AnyIntIndex) Add(key int, value interface{}) {
	idx.data.Store(key, value)
}

func (idx *AnyIntIndex) LoadOrStore(key int, value interface{}) (actual interface{}, loaded bool) {
	return idx.data.LoadOrStore(key, value)
}

func (idx *AnyIntIndex) CompareAndDelete(key int, expected interface{}) bool {
	if idx == nil {
		return false
	}
	return idx.data.CompareAndDelete(key, expected)
}

func (idx *AnyIntIndex) Get(key int) (value interface{}, ok bool) {
	return idx.data.Load(key)
}

func (idx *AnyIntIndex) Remove(key int) {
	if idx == nil {
		return
	}
	if value, ok := idx.data.LoadAndDelete(key); ok {
		if closer, ok := value.(idleConnectionCloser); ok {
			closer.CloseIdleConnections()
		}
	}
}

func (idx *AnyIntIndex) Range(fn func(int, interface{}) bool) {
	if idx == nil || fn == nil {
		return
	}
	idx.data.Range(func(key, value interface{}) bool {
		intKey, ok := key.(int)
		if !ok {
			return true
		}
		return fn(intKey, value)
	})
}

func (idx *AnyIntIndex) Clear() {
	if idx == nil {
		return
	}
	closers := make([]idleConnectionCloser, 0)
	idx.data.Range(func(_, value interface{}) bool {
		if closer, ok := value.(idleConnectionCloser); ok {
			closers = append(closers, closer)
		}
		return true
	})
	idx.data.Clear()
	for _, closer := range closers {
		closer.CloseIdleConnections()
	}
}
