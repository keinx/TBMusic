package cache

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func openTest(t *testing.T, dir string, limit int64) *Cache {
	t.Helper()
	c, err := Open(dir, limit)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func putTest(t *testing.T, c *Cache, key string, n int) {
	t.Helper()
	if err := c.Set(key, []byte(strings.Repeat(key[:1], n)), time.Hour, ""); err != nil {
		t.Fatal(err)
	}
}
func checkSize(t *testing.T, c *Cache) {
	t.Helper()
	files, _ := os.ReadDir(c.dir)
	var total int64
	for _, f := range files {
		st, err := f.Info()
		if err != nil {
			t.Fatal(err)
		}
		total += st.Size()
	}
	st := c.Stats()
	if total != st.Bytes || total > st.Limit {
		t.Fatalf("disk=%d stats=%+v", total, st)
	}
}
func TestLFUPersistsAndSharesCapacity(t *testing.T) {
	dir := t.TempDir()
	c := openTest(t, dir, 800)
	putTest(t, c, "search|hot", 250)
	putTest(t, c, "cover|cold", 250)
	for i := 0; i < 8; i++ {
		if _, ok := c.Get("search|hot"); !ok {
			t.Fatal("missing hot item")
		}
	}
	c = openTest(t, dir, 800) // Persist both values and hit counters across restart.
	putTest(t, c, "lyric|new", 250)
	if _, ok := c.Get("cover|cold"); ok {
		t.Fatal("least-used item was retained")
	}
	if _, ok := c.Get("search|hot"); !ok {
		t.Fatal("hot item was evicted")
	}
	if _, ok := c.Get("lyric|new"); !ok {
		t.Fatal("new item was not admitted")
	}
	checkSize(t, c)
}
func TestExpirationPrecedesFrequencyEviction(t *testing.T) {
	c := openTest(t, t.TempDir(), 800)
	putTest(t, c, "expired", 250)
	putTest(t, c, "valid", 250)
	for i := 0; i < 10; i++ {
		c.Get("expired")
	}
	c.entries[digest("expired")].expires = time.Now().Add(-time.Second).UnixNano()
	putTest(t, c, "new", 250)
	if _, ok := c.Get("valid"); !ok {
		t.Fatal("valid cold entry evicted before expired hot entry")
	}
	checkSize(t, c)
}
func TestOversizeReplacementAndTagInvalidation(t *testing.T) {
	c := openTest(t, t.TempDir(), 800)
	putTest(t, c, "safe", 200)
	if err := c.Set("huge", make([]byte, 801), time.Hour, ""); err == nil {
		t.Fatal("accepted oversize entry")
	}
	if _, ok := c.Get("safe"); !ok {
		t.Fatal("oversize request evicted useful data")
	}
	for _, key := range []string{"url|320", "url|128"} {
		if err := c.Set(key, []byte("url"), time.Hour, "expired-link"); err != nil {
			t.Fatal(err)
		}
	}
	c.DeleteTag("expired-link")
	if _, ok := c.Get("url|128"); ok {
		t.Fatal("stale URL retained")
	}
	putTest(t, c, "safe", 100)
	checkSize(t, c)
}
func TestConcurrentReadsWritesAndStartupRecovery(t *testing.T) {
	dir := t.TempDir()
	c := openTest(t, dir, 4096)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := strings.Repeat("x", i+1)
			for j := 0; j < 10; j++ {
				_ = c.Set(key, make([]byte, 300), time.Hour, "")
				c.Get(key)
			}
		}(i)
	}
	wg.Wait()
	checkSize(t, c)
	if err := os.WriteFile(filepath.Join(dir, ".tbmusic-cache-interrupted"), []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, digest("broken")+".cache"), []byte("bad"), 0600); err != nil {
		t.Fatal(err)
	}
	c = openTest(t, dir, 1000)
	checkSize(t, c)
	if _, err := os.Stat(filepath.Join(dir, ".tbmusic-cache-interrupted")); !os.IsNotExist(err) {
		t.Fatal("temporary file was not cleaned")
	}
}

func TestResizeEvictsLFUAndCanGrowAboveDefault(t *testing.T) {
	c := openTest(t, t.TempDir(), 1500)
	putTest(t, c, "search|hot", 250)
	putTest(t, c, "cover|cold", 250)
	putTest(t, c, "lyric|warm", 250)
	c.Get("lyric|warm")
	c.Get("search|hot")
	c.Get("search|hot")
	if err := c.Resize(700); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get("cover|cold"); ok {
		t.Fatal("shrinking retained the least-used entry")
	}
	for _, key := range []string{"search|hot", "lyric|warm"} {
		if _, ok := c.Get(key); !ok {
			t.Fatalf("shrinking evicted %s", key)
		}
	}
	checkSize(t, c)
	if err := c.Resize(4 << 30); err != nil {
		t.Fatal(err)
	}
	if c.Stats().Limit != 4<<30 {
		t.Fatal("capacity was clamped to the old 3 GB maximum")
	}
	if err := c.Resize(0); err == nil || c.Stats().Limit != 4<<30 {
		t.Fatal("invalid capacity changed the cache")
	}
	c = openTest(t, c.dir, 4<<30)
	if c.Stats().Limit != 4<<30 || c.Stats().Entries != 2 {
		t.Fatal("reopening lost the configured capacity or entries")
	}
}

func TestClearPreservesOtherFilesAndRejectsOldLoads(t *testing.T) {
	c := openTest(t, t.TempDir(), 800)
	putTest(t, c, "cover|image", 250)
	c.Get("cover|image")
	other := filepath.Join(c.dir, "keep.txt")
	if err := os.WriteFile(other, []byte("unrelated"), 0600); err != nil {
		t.Fatal(err)
	}
	generation := c.Generation()
	if err := c.Clear(); err != nil {
		t.Fatal(err)
	}
	if err := c.SetIfGeneration("search|old", []byte("old"), time.Hour, "", generation); err != nil {
		t.Fatal(err)
	}
	st := c.Stats()
	if st.Bytes != 0 || st.Entries != 0 || st.Hits != 0 || st.Limit != 800 {
		t.Fatalf("clear did not reset cached content: %+v", st)
	}
	if b, err := os.ReadFile(other); err != nil || string(b) != "unrelated" {
		t.Fatal("clear touched an unrelated file")
	}
	if err := c.SetIfGeneration("search|new", []byte("new"), time.Hour, "", c.Generation()); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get("search|new"); !ok {
		t.Fatal("new requests no longer cache after clear")
	}
}
