package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingIsTolerated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	in, err := Load(path)
	if err == nil {
		t.Fatal("expected an error for a missing file")
	}
	if in.Mode != ModeManual || in.Schema != Schema {
		t.Fatalf("unsafe default: %+v", in)
	}
	if in.Selected != nil {
		t.Fatalf("default should not select a profile: %+v", in)
	}
}

func TestLoadOrFallsBack(t *testing.T) {
	in := LoadOr(filepath.Join(t.TempDir(), "missing.json"), "local")
	if in.Mode != ModeManual || in.Selected == nil || *in.Selected != "local" {
		t.Fatalf("fallback = %+v", in)
	}
}

func TestSaveIncrementsRevision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	first, err := Save(path, Select("local", "cli", "operator"))
	if err != nil {
		t.Fatal(err)
	}
	if first.Revision != 1 {
		t.Fatalf("first revision = %d", first.Revision)
	}
	second, err := Save(path, Select("remote", "gui", "lane"))
	if err != nil {
		t.Fatal(err)
	}
	if second.Revision != 2 {
		t.Fatalf("second revision = %d", second.Revision)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Revision != 2 || loaded.Selected == nil || *loaded.Selected != "remote" {
		t.Fatalf("loaded = %+v", loaded)
	}
	if loaded.Reason != "lane" || loaded.UpdatedBy != "gui" {
		t.Fatalf("metadata = %+v", loaded)
	}
}

func TestSaveCorruptResetsRevision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	saved, err := Save(path, Select("local", "cli", "operator"))
	if err != nil {
		t.Fatal(err)
	}
	if saved.Revision != 1 {
		t.Fatalf("revision after corrupt file = %d", saved.Revision)
	}
}

func TestLoadTruncated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte(`{"schema":1,"mode":"manual","selected":"local"`), 0o600); err != nil {
		t.Fatal(err)
	}
	in, err := Load(path)
	if err == nil {
		t.Fatal("expected truncated file error")
	}
	if in.Mode != ModeManual {
		t.Fatalf("unsafe default: %+v", in)
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	cases := []string{
		`{"schema":2,"mode":"manual","selected":"local"}`,
		`{"schema":1,"mode":"sideways","selected":"local"}`,
		`{"schema":1,"mode":"manual"}`,
		`{"schema":1,"mode":"auto"}`,
	}
	for _, body := range cases {
		path := filepath.Join(t.TempDir(), "state.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Errorf("expected error for %s", body)
		}
	}
}

func TestSaveRejectsInvalidIntent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	manual := Select("local", "cli", "")
	manual.Mode = ModeAuto
	if _, err := Save(path, manual); err == nil {
		t.Fatal("auto intent without preferred should fail")
	}
	auto := SetAuto("local", nil, "cli", "")
	auto.Preferred = nil
	if _, err := Save(path, auto); err == nil {
		t.Fatal("auto intent without preferred should fail")
	}
}

func TestSetAutoKeepsSelected(t *testing.T) {
	selected := "local"
	in := SetAuto("remote", &selected, "cli", "operator")
	if in.Mode != ModeAuto || in.Preferred == nil || *in.Preferred != "remote" {
		t.Fatalf("auto intent = %+v", in)
	}
	if in.Selected == nil || *in.Selected != "local" {
		t.Fatalf("selected not retained: %+v", in)
	}
}
