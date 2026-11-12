package relay

import (
	"testing"

	"shunt/internal/config"
)

func TestSelectionSnapshotIsImmutable(t *testing.T) {
	selector := NewSelector(Selection{Profile: "local", BaseURL: "http://127.0.0.1:4000", Mode: "manual", Revision: 1})
	held := selector.Load()
	selector.Store(Selection{Profile: "remote", BaseURL: "https://example.test", Mode: "manual", Revision: 2})
	if held.Profile != "local" || held.BaseURL != "http://127.0.0.1:4000" {
		t.Fatalf("held snapshot mutated: %+v", held)
	}
	current := selector.Load()
	if current.Profile != "remote" || current.Revision != 2 {
		t.Fatalf("current selection = %+v", current)
	}
	if current.Generation != 1 {
		t.Fatalf("generation = %d, want 1", current.Generation)
	}
}

func TestSelectorNeverNil(t *testing.T) {
	selector := NewSelector(Selection{})
	if got := selector.Load(); got == nil {
		t.Fatal("Load returned nil")
	}
	var empty Selector
	if got := empty.Load(); got == nil {
		t.Fatal("zero selector Load returned nil")
	}
}

func TestProfileTableIsImmutable(t *testing.T) {
	yes := true
	cfg := config.Defaults()
	cfg.Profiles = map[string]config.Profile{
		"local": {Label: "Local", BaseURL: "http://127.0.0.1:4000", Enabled: &yes},
	}
	table := NewProfileTable(cfg)
	cfg.Profiles["local"] = config.Profile{Label: "Changed"}

	profile, ok := table.Profile("local")
	if !ok {
		t.Fatal("profile missing")
	}
	if profile.Label != "Local" || profile.BaseURL != "http://127.0.0.1:4000" {
		t.Fatalf("table was not copied: %+v", profile)
	}
	holder := NewTableHolder(table)
	replacement := NewProfileTable(config.Config{Profiles: map[string]config.Profile{"remote": {Label: "Remote"}}})
	holder.Store(replacement)
	if _, ok := holder.Load().Profile("local"); ok {
		t.Fatal("holder did not publish the new table")
	}
	var nilTable *ProfileTable
	if _, ok := nilTable.Profile("local"); ok {
		t.Fatal("nil table returned a profile")
	}
}
