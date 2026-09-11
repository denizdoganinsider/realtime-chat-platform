package edgecache

import (
	"fmt"
	"testing"
)

func entry(size int) *Entry {
	return &Entry{Body: make([]byte, size), ContentType: "image/png", ETag: `"x"`}
}

func TestPutThenGet(t *testing.T) {
	c := New(1000, 500)

	if !c.Put("a", entry(100)) {
		t.Fatal("Put refused an entry that fits")
	}

	got, ok := c.Get("a")
	if !ok || len(got.Body) != 100 {
		t.Errorf("Get = %v/%v, want the stored entry", got, ok)
	}

	stats := c.Stats()
	if stats.Hits != 1 || stats.Misses != 0 || stats.UsedBytes != 100 || stats.Entries != 1 {
		t.Errorf("Stats = %+v", stats)
	}
}

func TestMissIsCounted(t *testing.T) {
	c := New(1000, 500)

	if _, ok := c.Get("nope"); ok {
		t.Fatal("Get returned an entry that was never stored")
	}
	if c.Stats().Misses != 1 {
		t.Errorf("Misses = %d, want 1", c.Stats().Misses)
	}
}

// Byte-bounded, least-recently-used out first. "a" is touched after "b" is
// stored, so "b" is the oldest when "c" needs the room.
func TestEvictsLeastRecentlyUsedByBytes(t *testing.T) {
	c := New(250, 250)

	c.Put("a", entry(100))
	c.Put("b", entry(100))
	c.Get("a") // a is now more recent than b

	c.Put("c", entry(100)) // needs 300 > 250: evict b

	if _, ok := c.Get("b"); ok {
		t.Error("b survived; it was the least recently used")
	}
	if _, ok := c.Get("a"); !ok {
		t.Error("a was evicted; it was recently used")
	}
	if _, ok := c.Get("c"); !ok {
		t.Error("c was not stored")
	}
	if used := c.Stats().UsedBytes; used != 200 {
		t.Errorf("UsedBytes = %d, want 200", used)
	}
}

// A single object over the per-entry cap must not flush everything else to
// make room for itself.
func TestRefusesOversizedEntryWithoutEvicting(t *testing.T) {
	c := New(1000, 300)
	c.Put("small", entry(100))

	if c.Put("huge", entry(301)) {
		t.Fatal("Put accepted an entry over the per-object cap")
	}
	if _, ok := c.Get("small"); !ok {
		t.Error("existing entry was evicted for an object that was then refused")
	}
}

func TestUsedBytesStaysConsistentUnderChurn(t *testing.T) {
	c := New(1000, 1000)

	for i := range 100 {
		c.Put(fmt.Sprintf("k%d", i), entry(150))
	}

	stats := c.Stats()
	if stats.UsedBytes > 1000 {
		t.Errorf("UsedBytes = %d exceeds the cap", stats.UsedBytes)
	}
	if stats.UsedBytes != int64(stats.Entries)*150 {
		t.Errorf("UsedBytes = %d does not match %d entries of 150", stats.UsedBytes, stats.Entries)
	}
}
