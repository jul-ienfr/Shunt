package models

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func sampleCatalog(profile, dialect, sourceURL string, fetchedAt time.Time) Catalog {
	return Catalog{
		Schema:    1,
		Profile:   profile,
		Dialect:   dialect,
		SourceURL: sourceURL,
		FetchedAt: fetchedAt,
		Models:    []Record{{ID: "m", Free: FreeFree}},
	}
}

func TestCacheFreshAndStale(t *testing.T) {
	dir := t.TempDir()
	cache := NewCache(dir, 12, 168)
	url := "http://127.0.0.1:4000/v1/models"

	fresh := sampleCatalog("local", DialectAnthropic, url, time.Now().UTC().Add(-time.Hour))
	if err := cache.Save(fresh); err != nil {
		t.Fatal(err)
	}
	cat, state, err := cache.Load("local", DialectAnthropic, url)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Found || !state.Fresh || !state.Usable || state.Stale {
		t.Fatalf("state = %+v", state)
	}
	if cat.Stale || cat.StaleReason != nil {
		t.Fatalf("fresh catalog marked stale: %+v", cat)
	}

	stale := sampleCatalog("local", DialectAnthropic, url, time.Now().UTC().Add(-13*time.Hour))
	if err := cache.Save(stale); err != nil {
		t.Fatal(err)
	}
	cat, state, err = cache.Load("local", DialectAnthropic, url)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Found || state.Fresh || !state.Usable || !state.Stale {
		t.Fatalf("state = %+v", state)
	}
	if !cat.Stale || cat.StaleReason == nil || *cat.StaleReason == "" {
		t.Fatalf("stale marker = %+v", cat)
	}
}

func TestCacheExpired(t *testing.T) {
	dir := t.TempDir()
	cache := NewCache(dir, 12, 24)
	url := "http://127.0.0.1:4000/v1/models"
	old := sampleCatalog("local", DialectOpenAI, url, time.Now().UTC().Add(-48*time.Hour))
	if err := cache.Save(old); err != nil {
		t.Fatal(err)
	}
	_, state, err := cache.Load("local", DialectOpenAI, url)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Found || state.Fresh || state.Usable {
		t.Fatalf("state = %+v", state)
	}
}

func TestCacheSourceURLMismatch(t *testing.T) {
	dir := t.TempDir()
	cache := NewCache(dir, 12, 168)
	cat := sampleCatalog("local", DialectAnthropic, "http://old/v1/models", time.Now().UTC())
	if err := cache.Save(cat); err != nil {
		t.Fatal(err)
	}
	_, state, err := cache.Load("local", DialectAnthropic, "http://new/v1/models")
	if err != nil {
		t.Fatalf("mismatch should be a quiet miss: %v", err)
	}
	if state.Found {
		t.Fatalf("state = %+v", state)
	}
	if _, err := os.Stat(cache.Path("local", DialectAnthropic)); !os.IsNotExist(err) {
		t.Fatalf("mismatched cache was not removed: %v", err)
	}
}

func TestCacheCorruptIsRemoved(t *testing.T) {
	dir := t.TempDir()
	cache := NewCache(dir, 12, 168)
	path := cache.Path("local", DialectAnthropic)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, state, err := cache.Load("local", DialectAnthropic, "")
	if !errors.Is(err, ErrCacheCorrupt) {
		t.Fatalf("error = %v", err)
	}
	if state.Found {
		t.Fatalf("state = %+v", state)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("corrupt cache was not deleted: %v", statErr)
	}
}

func TestCacheSaveRejectsEmpty(t *testing.T) {
	cache := NewCache(t.TempDir(), 12, 168)
	if err := cache.Save(Catalog{Profile: "local", Dialect: DialectAnthropic}); err == nil {
		t.Fatal("empty catalog must not be cached")
	}
}

func TestCacheInvalidate(t *testing.T) {
	cache := NewCache(t.TempDir(), 12, 168)
	cat := sampleCatalog("local", DialectAnthropic, "http://x/v1/models", time.Now().UTC())
	if err := cache.Save(cat); err != nil {
		t.Fatal(err)
	}
	if err := cache.Invalidate("local", DialectAnthropic); err != nil {
		t.Fatal(err)
	}
	if _, state, _ := cache.Load("local", DialectAnthropic, ""); state.Found {
		t.Fatal("entry still present after invalidate")
	}
	if err := cache.Invalidate("local", DialectAnthropic); err != nil {
		t.Fatalf("idempotent invalidate: %v", err)
	}
}

func TestCatalogRecount(t *testing.T) {
	cat := Catalog{Models: []Record{{Free: FreeFree}, {Free: FreePaid}, {Free: FreeFree}, {Free: FreeUnknown}}}
	cat.Recount()
	if cat.Counts != (Counts{Total: 4, Free: 2, Paid: 1, Unknown: 1}) {
		t.Fatalf("counts = %+v", cat.Counts)
	}
}
