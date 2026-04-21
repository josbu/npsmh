package cache

import (
	"container/list"
	"sync"
)

// Cache is an LRU cache. It is safe for concurrent access.
type Cache struct {
	// MaxEntries is the maximum number of cache entries before
	// an item is evicted. Zero means no limit.
	MaxEntries int

	//Execute this callback function when an element is culled
	OnEvicted func(key Key, value interface{})

	mu    sync.Mutex
	ll    *list.List //list
	cache map[Key]*list.Element
}

// A Key may be any value that is comparable. See http://golang.org/ref/spec#Comparison_operators
type Key interface{}

type entry struct {
	key   Key
	value interface{}
}

// New creates a new Cache.
// If maxEntries is 0, the cache has no length limit.
// that eviction is done by the caller.
func New(maxEntries int) *Cache {
	return &Cache{
		MaxEntries: maxEntries,
		ll:         list.New(),
		cache:      make(map[Key]*list.Element),
	}
}

// Add If the key value already exists, move the key to the front
func (c *Cache) Add(key Key, value interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.ll == nil {
		c.ll = list.New()
	}
	if c.cache == nil {
		c.cache = make(map[Key]*list.Element)
	}
	if ee, ok := c.cache[key]; ok {
		c.ll.MoveToFront(ee) // move to the front
		ee.Value.(*entry).value = value
		return
	}
	ele := c.ll.PushFront(&entry{key, value})
	c.cache[key] = ele
	if c.MaxEntries != 0 && c.ll.Len() > c.MaxEntries { // Remove the oldest element if the limit is exceeded
		c.removeOldestLocked()
	}
}

// Get looks up a key's value from the cache.
func (c *Cache) Get(key Key) (value interface{}, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.ll == nil {
		return nil, false
	}
	if ele, hit := c.cache[key]; hit {
		c.ll.MoveToFront(ele)
		return ele.Value.(*entry).value, true
	}
	return
}

// Remove removes the provided key from the cache.
func (c *Cache) Remove(key Key) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if ele, hit := c.cache[key]; hit {
		c.removeElementLocked(ele)
	}
}

// RemoveOldest removes the oldest item from the cache.
func (c *Cache) RemoveOldest() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.removeOldestLocked()
}

func (c *Cache) removeOldestLocked() {
	if c.ll == nil {
		return
	}
	ele := c.ll.Back()
	if ele != nil {
		c.removeElementLocked(ele)
	}
}

func (c *Cache) removeElementLocked(e *list.Element) {
	if e == nil {
		return
	}
	c.ll.Remove(e)
	kv := e.Value.(*entry)
	delete(c.cache, kv.key)
	e.Value = nil
	if c.OnEvicted != nil {
		c.OnEvicted(kv.key, kv.value)
	}
}

// Len returns the number of items in the cache.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.ll == nil {
		return 0
	}
	return c.ll.Len()
}

// Clear purges all stored items from the cache.
func (c *Cache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.OnEvicted != nil {
		for _, value := range c.cache {
			kv := value.Value.(*entry)
			c.OnEvicted(kv.key, kv.value)
		}
	}
	clear(c.cache)
	if c.ll == nil {
		c.ll = list.New()
		return
	}
	c.ll.Init()
}
