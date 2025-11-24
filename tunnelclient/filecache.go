package tunnelclient

import (
	"container/list"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// fileCache is a size bounded LRU of file contents, so a directory served over a tunnel does not hit the disk for every request.
// It is keyed by absolute path and validated on every read against the file's size and modification time, so an edited file is picked up immediately rather than served stale.
type fileCache struct {
	maxBytes int64
	// maxEntryBytes keeps one large file from evicting everything else.
	maxEntryBytes int64

	mu      sync.Mutex
	entries map[string]*list.Element
	order   *list.List // front is most recently used
	used    int64

	hits, misses, evictions atomic.Uint64
}

type cacheEntry struct {
	path    string
	data    []byte
	modTime time.Time
	size    int64
}

// newFileCache returns a cache holding at most maxBytes of file content.
// A maxBytes of zero disables caching entirely, and every read goes to the disk.
func newFileCache(maxBytes int64) *fileCache {
	if maxBytes < 0 {
		maxBytes = 0
	}
	// A single file may take at most an eighth of the budget, so one large asset cannot flush the working set.
	// Below 8 MB that rule is too strict to be useful, so the whole budget is allowed instead.
	perEntry := maxBytes / 8
	if maxBytes < 8<<20 {
		perEntry = maxBytes
	}
	return &fileCache{
		maxBytes:      maxBytes,
		maxEntryBytes: perEntry,
		entries:       map[string]*list.Element{},
		order:         list.New(),
	}
}

// enabled reports whether anything is cached at all.
func (c *fileCache) enabled() bool { return c != nil && c.maxBytes > 0 }

// cacheable reports whether a file of this size is worth holding.
func (c *fileCache) cacheable(size int64) bool {
	return c.enabled() && size > 0 && size <= c.maxEntryBytes
}

// get returns the cached contents of a file whose stat matches what was stored.
// A file that has changed on disk is dropped and reported as a miss.
func (c *fileCache) get(path string, info os.FileInfo) ([]byte, bool) {
	if !c.enabled() {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	el, ok := c.entries[path]
	if !ok {
		c.misses.Add(1)
		return nil, false
	}
	entry := el.Value.(*cacheEntry)
	if entry.size != info.Size() || !entry.modTime.Equal(info.ModTime()) {
		c.removeLocked(el)
		c.misses.Add(1)
		return nil, false
	}
	c.order.MoveToFront(el)
	c.hits.Add(1)
	return entry.data, true
}

// put stores a file's contents, evicting least recently used entries to stay within the budget.
func (c *fileCache) put(path string, data []byte, info os.FileInfo) {
	if !c.cacheable(int64(len(data))) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if el, ok := c.entries[path]; ok {
		c.removeLocked(el)
	}
	entry := &cacheEntry{path: path, data: data, modTime: info.ModTime(), size: info.Size()}
	c.entries[path] = c.order.PushFront(entry)
	c.used += int64(len(data))

	for c.used > c.maxBytes {
		oldest := c.order.Back()
		if oldest == nil {
			break
		}
		c.removeLocked(oldest)
		c.evictions.Add(1)
	}
}

func (c *fileCache) removeLocked(el *list.Element) {
	entry := el.Value.(*cacheEntry)
	c.order.Remove(el)
	delete(c.entries, entry.path)
	c.used -= int64(len(entry.data))
}

// stats is a snapshot for logging.
type cacheStats struct {
	Entries   int
	UsedBytes int64
	MaxBytes  int64
	Hits      uint64
	Misses    uint64
	Evictions uint64
}

func (c *fileCache) stats() cacheStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return cacheStats{
		Entries:   len(c.entries),
		UsedBytes: c.used,
		MaxBytes:  c.maxBytes,
		Hits:      c.hits.Load(),
		Misses:    c.misses.Load(),
		Evictions: c.evictions.Load(),
	}
}
