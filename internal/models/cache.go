package models

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"shunt/internal/platform"
)

// Cache states.
type CacheState struct {
	Found  bool
	Fresh  bool
	Usable bool
	Stale  bool
}

// ErrCacheCorrupt reports a cache file that was deleted after failing to parse.
var ErrCacheCorrupt = errors.New("corrupt model cache")

// Cache stores one normalized catalog per (profile, dialect).
type Cache struct {
	Dir      string
	TTL      time.Duration
	StaleTTL time.Duration
}

// NewCache builds a cache rooted at dir.
func NewCache(dir string, ttlHours, staleHours int) *Cache {
	if ttlHours <= 0 {
		ttlHours = 12
	}
	if staleHours <= ttlHours {
		staleHours = 168
	}
	return &Cache{
		Dir:      dir,
		TTL:      time.Duration(ttlHours) * time.Hour,
		StaleTTL: time.Duration(staleHours) * time.Hour,
	}
}

// Path returns the cache file for a profile and dialect.
func (c *Cache) Path(profile, dialect string) string {
	return filepath.Join(c.Dir, "models."+profile+"."+dialect+".json")
}

// Load reads a cache entry. A missing entry is a miss with a nil error. A
// corrupt or schema-incompatible entry is deleted and reported as a miss.
func (c *Cache) Load(profile, dialect, sourceURL string) (Catalog, CacheState, error) {
	var state CacheState
	path := c.Path(profile, dialect)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Catalog{}, state, nil
		}
		return Catalog{}, state, err
	}
	var cat Catalog
	if err := json.Unmarshal(data, &cat); err != nil {
		_ = os.Remove(path)
		return Catalog{}, state, fmt.Errorf("%w: %v", ErrCacheCorrupt, err)
	}
	if cat.Schema != 1 || cat.Profile != profile || cat.Dialect != dialect || len(cat.Models) == 0 {
		_ = os.Remove(path)
		return Catalog{}, state, fmt.Errorf("%w: schema or key mismatch", ErrCacheCorrupt)
	}
	if sourceURL != "" && cat.SourceURL != sourceURL {
		_ = os.Remove(path)
		return Catalog{}, state, nil
	}
	now := time.Now().UTC()
	expires := cat.FetchedAt.Add(c.TTL)
	staleUntil := cat.FetchedAt.Add(c.StaleTTL)
	state.Found = true
	state.Fresh = now.Before(expires)
	state.Usable = now.Before(staleUntil)
	state.Stale = !state.Fresh && state.Usable
	cat.Stale = state.Stale
	if state.Stale {
		reason := fmt.Sprintf("cache older than %s", c.TTL)
		cat.StaleReason = &reason
	} else {
		cat.StaleReason = nil
	}
	return cat, state, nil
}

// Save writes a catalog atomically after serializing a lock. A failed write
// leaves the previous cache intact.
func (c *Cache) Save(cat Catalog) error {
	if len(cat.Models) == 0 {
		return fmt.Errorf("refusing to cache an empty catalog")
	}
	if cat.Schema == 0 {
		cat.Schema = 1
	}
	cat.Stale = false
	cat.StaleReason = nil
	cat.Recount()
	if err := os.MkdirAll(c.Dir, 0o700); err != nil {
		return err
	}
	unlock, err := c.lock(cat.Profile, cat.Dialect)
	if err != nil {
		return err
	}
	defer unlock()
	return platform.WriteJSON(c.Path(cat.Profile, cat.Dialect), cat)
}

// Invalidate removes a cache entry.
func (c *Cache) Invalidate(profile, dialect string) error {
	err := os.Remove(c.Path(profile, dialect))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// lock serializes writers with an exclusive lock file. A lock older than 30
// seconds is treated as abandoned.
func (c *Cache) lock(profile, dialect string) (func(), error) {
	path := c.Path(profile, dialect) + ".lock"
	deadline := time.Now().Add(2 * time.Second)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = f.WriteString(time.Now().UTC().Format(time.RFC3339Nano))
			_ = f.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if info, statErr := os.Stat(path); statErr == nil && time.Since(info.ModTime()) > 30*time.Second {
			_ = os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("model cache is locked by another process")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
