package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"shunt/internal/platform"
)

const (
	Schema     = 1
	ModeManual = "manual"
	ModeAuto   = "auto"
)

// Intent is the durable operator intent stored in state.json. It is written by
// the control plane (CLI or authenticated GUI) and only read by the relay.
type Intent struct {
	Schema    int       `json:"schema"`
	Revision  int64     `json:"revision"`
	Mode      string    `json:"mode"`
	Selected  *string   `json:"selected"`
	Preferred *string   `json:"preferred"`
	UpdatedAt time.Time `json:"updated_at"`
	UpdatedBy string    `json:"updated_by,omitempty"`
	Reason    string    `json:"reason,omitempty"`
}

// Default returns a safe manual intent.
func Default(selected string) Intent {
	in := Intent{
		Schema:    Schema,
		Revision:  0,
		Mode:      ModeManual,
		UpdatedAt: time.Now().UTC(),
		UpdatedBy: "migration",
	}
	if selected != "" {
		in.Selected = &selected
	}
	return in
}

// Load reads operator intent. A missing, truncated, or corrupt file is
// tolerated: a safe manual intent is returned with a non-nil error describing
// the condition. Callers that only need a usable value should use LoadOr.
func Load(path string) (Intent, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Default(""), fmt.Errorf("state file missing: %w", err)
		}
		return Default(""), err
	}
	var in Intent
	if err := json.Unmarshal(data, &in); err != nil {
		return Default(""), fmt.Errorf("parse state: %w", err)
	}
	if in.Schema != Schema {
		return Default(""), fmt.Errorf("unsupported state schema %d", in.Schema)
	}
	if in.Mode != ModeManual && in.Mode != ModeAuto {
		return Default(""), fmt.Errorf("invalid state mode %q", in.Mode)
	}
	if in.Mode == ModeManual && in.Selected == nil {
		return Default(""), fmt.Errorf("manual state has no selected profile")
	}
	if in.Mode == ModeAuto && in.Preferred == nil {
		return Default(""), fmt.Errorf("auto state has no preferred profile")
	}
	if in.UpdatedAt.IsZero() {
		in.UpdatedAt = time.Now().UTC()
	}
	return in, nil
}

// LoadOr returns a usable intent, falling back to manual mode with the given
// default profile when the stored file is missing or invalid.
func LoadOr(path, defaultProfile string) Intent {
	in, err := Load(path)
	if err != nil {
		return Default(defaultProfile)
	}
	return in
}

// Save writes intent atomically, incrementing the revision from the last
// readable value. A corrupt existing file is treated as revision zero.
func Save(path string, in Intent) (Intent, error) {
	if in.Schema == 0 {
		in.Schema = Schema
	}
	if in.Schema != Schema {
		return Intent{}, fmt.Errorf("unsupported state schema %d", in.Schema)
	}
	if in.Mode != ModeManual && in.Mode != ModeAuto {
		return Intent{}, fmt.Errorf("invalid mode %q", in.Mode)
	}
	if in.Mode == ModeManual && in.Selected == nil {
		return Intent{}, fmt.Errorf("manual mode requires a selected profile")
	}
	if in.Mode == ModeAuto && in.Preferred == nil {
		return Intent{}, fmt.Errorf("auto mode requires a preferred profile")
	}
	prev, err := Load(path)
	var revision int64
	if err == nil {
		revision = prev.Revision
	}
	in.Revision = revision + 1
	in.UpdatedAt = time.Now().UTC()
	if in.UpdatedBy == "" {
		in.UpdatedBy = "cli"
	}
	in.Reason = sanitize(in.Reason)
	if err := platform.WriteJSON(path, in); err != nil {
		return Intent{}, err
	}
	return in, nil
}

// Select returns a manual intent selecting profile.
func Select(profile, by, reason string) Intent {
	return Intent{
		Schema:    Schema,
		Mode:      ModeManual,
		Selected:  &profile,
		UpdatedBy: by,
		Reason:    reason,
	}
}

// SetAuto returns an auto intent preferring profile, retaining the last manual
// selection when provided.
func SetAuto(profile string, selected *string, by, reason string) Intent {
	return Intent{
		Schema:    Schema,
		Mode:      ModeAuto,
		Preferred: &profile,
		Selected:  selected,
		UpdatedBy: by,
		Reason:    reason,
	}
}

func sanitize(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 256 {
		s = s[:256]
	}
	return strings.TrimSpace(s)
}
