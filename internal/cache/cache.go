// Package cache provides a shared, persistent, byte-bounded LFU cache.
package cache

import (
	"container/heap"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const DefaultBytes int64 = 3 << 30
const maxEntryBytes int64 = 16 << 20
const headerSize = 48
const magic = "TBMusic1"

type metadata struct {
	Key string `json:"key"`
	Tag string `json:"tag,omitempty"`
}
type entry struct {
	metadata
	id       string
	size     int64
	expires  int64
	hits     uint64
	accessed int64
	metaSize int
	index    int
}
type frequencyHeap []*entry

func (h frequencyHeap) Len() int { return len(h) }
func (h frequencyHeap) Less(i, j int) bool {
	if h[i].hits != h[j].hits {
		return h[i].hits < h[j].hits
	}
	if h[i].accessed != h[j].accessed {
		return h[i].accessed < h[j].accessed
	}
	return h[i].id < h[j].id
}
func (h frequencyHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i]; h[i].index = i; h[j].index = j }
func (h *frequencyHeap) Push(x any)   { e := x.(*entry); e.index = len(*h); *h = append(*h, e) }
func (h *frequencyHeap) Pop() any     { old := *h; e := old[len(old)-1]; *h = old[:len(old)-1]; return e }

type Cache struct {
	mu                      sync.Mutex
	dir                     string
	limit, used             int64
	entries                 map[string]*entry
	order                   frequencyHeap
	hits, misses, evictions uint64
	generation              uint64
}
type Stats struct {
	Bytes      int64          `json:"bytes"`
	Limit      int64          `json:"limit"`
	Entries    int            `json:"entries"`
	Hits       uint64         `json:"hits"`
	Misses     uint64         `json:"misses"`
	Evictions  uint64         `json:"evictions"`
	Categories map[string]int `json:"categories"`
}

func digest(s string) string           { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }
func (c *Cache) path(id string) string { return filepath.Join(c.dir, id+".cache") }

// Open indexes only our own regular cache files. The full file size (including
// metadata) counts against the limit. Hits survive restarts in a fixed header.
func Open(dir string, limit int64) (*Cache, error) {
	if limit <= 0 {
		limit = DefaultBytes
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	c := &Cache{dir: dir, limit: limit, entries: map[string]*entry{}}
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if !f.Type().IsRegular() {
			continue
		}
		name := f.Name()
		if strings.HasPrefix(name, ".tbmusic-cache-") {
			if err := os.Remove(filepath.Join(dir, name)); err != nil {
				return nil, err
			}
			continue
		}
		if len(name) != 64+6 || !strings.HasSuffix(name, ".cache") {
			continue
		}
		id := strings.TrimSuffix(name, ".cache")
		if _, err := hex.DecodeString(id); err != nil {
			continue
		}
		e, err := readEntry(c.path(id))
		if err != nil || e.expires <= time.Now().UnixNano() || digest(e.Key) != id {
			if err := os.Remove(c.path(id)); err != nil {
				return nil, err
			}
			continue
		}
		e.id = id
		c.entries[id] = e
		c.used += e.size
		heap.Push(&c.order, e)
	}
	if err := c.makeRoom(0); err != nil {
		return nil, err
	}
	return c, nil
}
func readEntry(path string) (*entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	var h [headerSize]byte
	if _, err = io.ReadFull(f, h[:]); err != nil || string(h[:8]) != magic {
		return nil, errors.New("invalid cache header")
	}
	n := int(binary.LittleEndian.Uint32(h[32:36]))
	size := int64(binary.LittleEndian.Uint64(h[40:48]))
	if n <= 0 || n > 16384 || size < 0 || size > maxEntryBytes || st.Size() != headerSize+int64(n)+size {
		return nil, errors.New("invalid cache size")
	}
	b := make([]byte, n)
	if _, err = io.ReadFull(f, b); err != nil {
		return nil, err
	}
	e := &entry{size: st.Size(), metaSize: n, expires: int64(binary.LittleEndian.Uint64(h[8:16])), hits: binary.LittleEndian.Uint64(h[16:24]), accessed: int64(binary.LittleEndian.Uint64(h[24:32]))}
	if err = json.Unmarshal(b, &e.metadata); err != nil {
		return nil, err
	}
	return e, nil
}
func (c *Cache) remove(e *entry) error {
	if err := os.Remove(c.path(e.id)); err != nil && !os.IsNotExist(err) {
		return err
	}
	delete(c.entries, e.id)
	c.used -= e.size
	heap.Remove(&c.order, e.index)
	return nil
}
func (c *Cache) makeRoom(size int64) error {
	if c.used+size > c.limit {
		now := time.Now().UnixNano()
		for _, e := range c.entries {
			if e.expires <= now {
				if err := c.remove(e); err != nil {
					return err
				}
			}
		}
	}
	for c.used+size > c.limit {
		if len(c.order) == 0 {
			return errors.New("cache entry exceeds capacity")
		}
		if err := c.remove(c.order[0]); err != nil {
			return err
		}
		c.evictions++
	}
	return nil
}
func (c *Cache) Get(key string) ([]byte, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entries[digest(key)]
	if e == nil {
		c.misses++
		return nil, false
	}
	if e.expires <= time.Now().UnixNano() {
		_ = c.remove(e)
		c.misses++
		return nil, false
	}
	f, err := os.OpenFile(c.path(e.id), os.O_RDWR, 0)
	if err != nil {
		c.misses++
		return nil, false
	}
	defer f.Close()
	b := make([]byte, e.size-headerSize-int64(e.metaSize))
	if _, err = f.ReadAt(b, headerSize+int64(e.metaSize)); err != nil {
		c.misses++
		return nil, false
	}
	e.hits++
	e.accessed = time.Now().UnixNano()
	c.hits++
	heap.Fix(&c.order, e.index)
	var touch [16]byte
	binary.LittleEndian.PutUint64(touch[:8], e.hits)
	binary.LittleEndian.PutUint64(touch[8:], uint64(e.accessed))
	_, _ = f.WriteAt(touch[:], 16)
	return b, true
}

// Set reserves space before writing; even the temporary file fits within limit.
// Failed or over-sized loads are returned to callers without entering the cache.
func (c *Cache) Set(key string, data []byte, ttl time.Duration, tag string) error {
	return c.set(key, data, ttl, tag, nil)
}

// Generation lets in-flight requests avoid repopulating a cache cleared while loading.
func (c *Cache) Generation() uint64 {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.generation
}

func (c *Cache) SetIfGeneration(key string, data []byte, ttl time.Duration, tag string, generation uint64) error {
	return c.set(key, data, ttl, tag, &generation)
}

func (c *Cache) set(key string, data []byte, ttl time.Duration, tag string, generation *uint64) error {
	if c == nil {
		return nil
	}
	if len(data) > int(maxEntryBytes) || ttl <= 0 || len(key) > 8192 {
		return errors.New("invalid cache entry")
	}
	meta, _ := json.Marshal(metadata{Key: key, Tag: tag})
	size := int64(headerSize + len(meta) + len(data))
	c.mu.Lock()
	defer c.mu.Unlock()
	if generation != nil && *generation != c.generation {
		return nil
	}
	if size > c.limit {
		return errors.New("cache entry exceeds capacity")
	}
	id := digest(key)
	if old := c.entries[id]; old != nil {
		if err := c.remove(old); err != nil {
			return err
		}
	}
	if err := c.makeRoom(size); err != nil {
		return err
	}
	e := &entry{metadata: metadata{Key: key, Tag: tag}, id: id, size: size, metaSize: len(meta), expires: time.Now().Add(ttl).UnixNano(), accessed: time.Now().UnixNano()}
	var h [headerSize]byte
	copy(h[:8], magic)
	binary.LittleEndian.PutUint64(h[8:16], uint64(e.expires))
	binary.LittleEndian.PutUint64(h[24:32], uint64(e.accessed))
	binary.LittleEndian.PutUint32(h[32:36], uint32(len(meta)))
	binary.LittleEndian.PutUint64(h[40:48], uint64(len(data)))
	f, err := os.CreateTemp(c.dir, ".tbmusic-cache-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	_, err = f.Write(h[:])
	if err == nil {
		_, err = f.Write(meta)
	}
	if err == nil {
		_, err = f.Write(data)
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(tmp, c.path(id)); err != nil {
		return err
	}
	c.entries[id] = e
	c.used += size
	heap.Push(&c.order, e)
	return nil
}

// Resize applies LFU eviction immediately when lowering the limit.
func (c *Cache) Resize(limit int64) error {
	if c == nil || limit <= 0 {
		return errors.New("invalid cache capacity")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	previous := c.limit
	c.limit = limit
	if err := c.makeRoom(0); err != nil {
		c.limit = previous
		return err
	}
	return nil
}

// Clear removes only indexed cache files, never other files in the directory.
func (c *Cache) Clear() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	for _, e := range c.entries {
		if err := c.remove(e); err != nil {
			return err
		}
	}
	c.hits, c.misses, c.evictions = 0, 0, 0
	return nil
}
func (c *Cache) Delete(key string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.entries[digest(key)]; e != nil {
		_ = c.remove(e)
	}
}
func (c *Cache) DeleteTag(tag string) {
	if c == nil || tag == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.entries {
		if e.Tag == tag {
			_ = c.remove(e)
		}
	}
}
func (c *Cache) Stats() Stats {
	if c == nil {
		return Stats{Limit: DefaultBytes, Categories: map[string]int{}}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	st := Stats{Bytes: c.used, Limit: c.limit, Entries: len(c.entries), Hits: c.hits, Misses: c.misses, Evictions: c.evictions, Categories: map[string]int{}}
	for _, e := range c.entries {
		kind, _, _ := strings.Cut(e.Key, "|")
		st.Categories[kind]++
	}
	return st
}
