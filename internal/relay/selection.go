package relay

import (
	"sync/atomic"

	"shunt/internal/config"
)

// Selection is an immutable routing decision held for the lifetime of one
// request. A later switch publishes a new Selection; it never mutates this one.
type Selection struct {
	Profile    string
	BaseURL    string
	Mode       string
	Revision   int64
	Generation uint64
}

// Selector holds the current immutable selection.
type Selector struct {
	current atomic.Pointer[Selection]
}

// NewSelector creates a selector initialized to the given selection.
func NewSelector(initial Selection) *Selector {
	s := &Selector{}
	s.Store(initial)
	return s
}

// Load returns the current selection. It is never nil.
func (s *Selector) Load() *Selection {
	if sel := s.current.Load(); sel != nil {
		return sel
	}
	return &Selection{}
}

// Store publishes a new immutable selection.
func (s *Selector) Store(sel Selection) {
	if sel.Generation == 0 {
		sel.Generation = 1
	}
	copy := sel
	s.current.Store(&copy)
}

// ProfileTable is an immutable map of profile definitions. It is swapped
// atomically when a profile endpoint changes so in-flight requests keep the
// profile they started with.
type ProfileTable struct {
	Profiles map[string]config.Profile
}

// NewProfileTable builds a table from a configuration.
func NewProfileTable(cfg config.Config) *ProfileTable {
	profiles := make(map[string]config.Profile, len(cfg.Profiles))
	for id, p := range cfg.Profiles {
		profiles[id] = p
	}
	return &ProfileTable{Profiles: profiles}
}

// Profile returns the named profile.
func (t *ProfileTable) Profile(id string) (config.Profile, bool) {
	if t == nil {
		return config.Profile{}, false
	}
	p, ok := t.Profiles[id]
	return p, ok
}

// TableHolder holds the current immutable profile table.
type TableHolder struct {
	current atomic.Pointer[ProfileTable]
}

// NewTableHolder creates a holder initialized with t.
func NewTableHolder(t *ProfileTable) *TableHolder {
	h := &TableHolder{}
	h.Store(t)
	return h
}

// Load returns the current table.
func (h *TableHolder) Load() *ProfileTable {
	return h.current.Load()
}

// Store publishes a new table.
func (h *TableHolder) Store(t *ProfileTable) {
	h.current.Store(t)
}
