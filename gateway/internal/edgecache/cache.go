// Package edgecache is the in-memory "edge" in front of media-service: the
// part of a CDN that a single machine can honestly demonstrate. Not the
// geography - one process, one region - but the HTTP mechanics a CDN runs on:
// content-addressed keys, hit/miss headers, conditional requests answered at
// the edge, and invalidation by URL rather than by purge.
package edgecache

import (
	"container/list"
	"sync"
)

// Entry is one cached object: the bytes and the headers needed to replay the
// origin's response without asking it again.
type Entry struct {
	Body        []byte
	ContentType string
	ETag        string
}

// Cache is a byte-bounded LRU. Bounded in bytes, not entries, because the
// entries are images of very different sizes and "1000 objects" says nothing
// about memory.
type Cache struct {
	mu       sync.Mutex
	maxBytes int64
	maxEntry int64
	used     int64
	order    *list.List
	items    map[string]*list.Element
	hits     uint64
	misses   uint64
}

type item struct {
	key   string
	entry *Entry
}

// maxEntry caps a single object: something larger than the whole cache, or a
// large fraction of it, would evict everything else for one hit.
func New(maxBytes int64, maxEntry int64) *Cache {
	return &Cache{
		maxBytes: maxBytes,
		maxEntry: maxEntry,
		order:    list.New(),
		items:    make(map[string]*list.Element),
	}
}

func (c *Cache) Get(key string) (*Entry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	element, ok := c.items[key]
	if !ok {
		c.misses++
		return nil, false
	}

	c.order.MoveToFront(element)
	c.hits++

	return element.Value.(*item).entry, true
}

// Put stores an entry, evicting least-recently-used ones until it fits.
// Returns false (and stores nothing) if the entry alone is over the cap.
func (c *Cache) Put(key string, entry *Entry) bool {
	size := int64(len(entry.Body))
	if size > c.maxEntry || size > c.maxBytes {
		return false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if element, ok := c.items[key]; ok {
		// Content-addressed keys never change value, but be correct anyway.
		c.used -= int64(len(element.Value.(*item).entry.Body))
		c.order.Remove(element)
		delete(c.items, key)
	}

	for c.used+size > c.maxBytes {
		oldest := c.order.Back()
		if oldest == nil {
			break
		}
		c.evict(oldest)
	}

	element := c.order.PushFront(&item{key: key, entry: entry})
	c.items[key] = element
	c.used += size

	return true
}

func (c *Cache) evict(element *list.Element) {
	it := element.Value.(*item)
	c.used -= int64(len(it.entry.Body))
	c.order.Remove(element)
	delete(c.items, it.key)
}

type Stats struct {
	Entries   int    `json:"entries"`
	UsedBytes int64  `json:"used_bytes"`
	MaxBytes  int64  `json:"max_bytes"`
	Hits      uint64 `json:"hits"`
	Misses    uint64 `json:"misses"`
}

func (c *Cache) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()

	return Stats{
		Entries:   len(c.items),
		UsedBytes: c.used,
		MaxBytes:  c.maxBytes,
		Hits:      c.hits,
		Misses:    c.misses,
	}
}
