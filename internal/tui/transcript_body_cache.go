package tui

import (
	"container/list"
	"sync"
)

const defaultTranscriptBodyHeightCacheMaxEntries = 512

type transcriptBodyHeightCache struct {
	mu          sync.Mutex
	baseEntries int
	maxEntries  int
	items       map[string]*list.Element
	lru         *list.List
}

type transcriptBodyHeightCacheEntry struct {
	key    string
	height int
}

func newTranscriptBodyHeightCache(maxEntries int) *transcriptBodyHeightCache {
	return &transcriptBodyHeightCache{
		baseEntries: maxEntries,
		maxEntries:  maxEntries,
		items:       map[string]*list.Element{},
		lru:         list.New(),
	}
}

// retain keeps one complete active layout resident and drops obsolete keys.
// Without this, measuring a transcript larger than the fixed cache in
// oldest-to-newest order evicts entries that the same scan needs next, turning
// every frame into a full render. Reconciling to active keys also releases old
// widths and replaced transcripts instead of retaining the largest thread seen.
func (c *transcriptBodyHeightCache) retain(keys map[string]struct{}) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.maxEntries = maxInt(c.baseEntries, len(keys))
	for key, element := range c.items {
		if _, ok := keys[key]; ok {
			continue
		}
		delete(c.items, key)
		c.lru.Remove(element)
	}
}

func (c *transcriptBodyHeightCache) get(key string) (int, bool) {
	if c == nil || key == "" {
		return 0, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.items[key]
	if !ok {
		return 0, false
	}
	c.lru.MoveToFront(element)
	return element.Value.(*transcriptBodyHeightCacheEntry).height, true
}

func (c *transcriptBodyHeightCache) set(key string, height int) {
	if c == nil || key == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.maxEntries <= 0 {
		return
	}
	if element, ok := c.items[key]; ok {
		element.Value.(*transcriptBodyHeightCacheEntry).height = height
		c.lru.MoveToFront(element)
		return
	}
	c.items[key] = c.lru.PushFront(&transcriptBodyHeightCacheEntry{key: key, height: height})
	for len(c.items) > c.maxEntries {
		element := c.lru.Back()
		if element == nil {
			return
		}
		entry := element.Value.(*transcriptBodyHeightCacheEntry)
		delete(c.items, entry.key)
		c.lru.Remove(element)
	}
}
