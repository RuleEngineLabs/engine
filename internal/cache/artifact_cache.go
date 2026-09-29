package cache

import (
	"container/list"
	"encoding/json"
	"sync"

	"github.com/RuleEngineLabs/engine/internal/compiler"
)

const DefaultMaxBytes = 10 * 1024 * 1024 // 10 MB

// ArtifactCache is an LRU cache for compiled artifacts with singleflight de-duplication.
// It is safe for concurrent use.
type ArtifactCache struct {
	mu       sync.Mutex
	maxBytes int
	curBytes int
	lru      *list.List
	items    map[string]*list.Element
	// singleflight: concurrent compilations for the same key share one call
	flights map[string]*call
}

type entry struct {
	key      string
	artifact *compiler.Artifact
	size     int
}

type call struct {
	wg  sync.WaitGroup
	val *compiler.Artifact
	err error
}

// New creates an ArtifactCache with the given max size in bytes.
func New(maxBytes int) *ArtifactCache {
	return &ArtifactCache{
		maxBytes: maxBytes,
		lru:      list.New(),
		items:    make(map[string]*list.Element),
		flights:  make(map[string]*call),
	}
}

// GetOrCompile returns the cached artifact for key, or calls compile() exactly once
// (singleflight) if it is absent, then caches and returns the result.
func (c *ArtifactCache) GetOrCompile(key string, compile func() (*compiler.Artifact, error)) (*compiler.Artifact, error) {
	if art, ok := c.get(key); ok {
		return art, nil
	}

	c.mu.Lock()
	// Check again while holding lock to prevent races
	if art, ok := c.getUnlocked(key); ok {
		c.mu.Unlock()
		return art, nil
	}

	// Singleflight: if a compile is already in progress, join it
	if f, ok := c.flights[key]; ok {
		c.mu.Unlock()
		f.wg.Wait()
		return f.val, f.err
	}

	f := &call{}
	f.wg.Add(1)
	c.flights[key] = f
	c.mu.Unlock()

	f.val, f.err = compile()
	f.wg.Done()

	c.mu.Lock()
	delete(c.flights, key)
	if f.err == nil {
		c.setUnlocked(key, f.val)
	}
	c.mu.Unlock()

	return f.val, f.err
}

func (c *ArtifactCache) get(key string) (*compiler.Artifact, bool) {
	c.mu.Lock()
	art, ok := c.getUnlocked(key)
	c.mu.Unlock()
	return art, ok
}

func (c *ArtifactCache) getUnlocked(key string) (*compiler.Artifact, bool) {
	el, ok := c.items[key]
	if !ok {
		return nil, false
	}
	c.lru.MoveToFront(el)
	return el.Value.(*entry).artifact, true
}

func (c *ArtifactCache) setUnlocked(key string, art *compiler.Artifact) {
	size := estimateSize(art)
	for c.curBytes+size > c.maxBytes && c.lru.Len() > 0 {
		c.evictLRU()
	}
	el := c.lru.PushFront(&entry{key: key, artifact: art, size: size})
	c.items[key] = el
	c.curBytes += size
}

func (c *ArtifactCache) evictLRU() {
	el := c.lru.Back()
	if el == nil {
		return
	}
	e := el.Value.(*entry)
	c.lru.Remove(el)
	delete(c.items, e.key)
	c.curBytes -= e.size
}

// Invalidate removes the entry for the given key from the cache.
// Any goroutine already holding the artifact pointer for that key continues
// to use it safely until Go's GC reclaims it (blue-green safety).
func (c *ArtifactCache) Invalidate(key string) {
	c.mu.Lock()
	if el, ok := c.items[key]; ok {
		e := el.Value.(*entry)
		c.lru.Remove(el)
		delete(c.items, key)
		c.curBytes -= e.size
	}
	c.mu.Unlock()
}

// Len returns the number of cached entries.
func (c *ArtifactCache) Len() int {
	c.mu.Lock()
	n := c.lru.Len()
	c.mu.Unlock()
	return n
}

func estimateSize(art *compiler.Artifact) int {
	b, err := json.Marshal(art.Policy)
	if err != nil {
		return 4096
	}
	return len(b)
}
