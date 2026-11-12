package ui

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"shunt/internal/clients"
	"shunt/internal/config"
	"shunt/internal/models"
	"shunt/internal/platform"
)

type clientView struct {
	ID             string   `json:"id"`
	Label          string   `json:"label"`
	Target         string   `json:"target"`
	BaseURLMode    string   `json:"base_url_mode"`
	WriteSupported bool     `json:"write_supported"`
	Dialects       []string `json:"dialects"`
	Note           string   `json:"note,omitempty"`
}

type clientsView struct {
	Schema    int          `json:"schema"`
	Relay     string       `json:"relay"`
	BackupDir string       `json:"backup_dir"`
	Clients   []clientView `json:"clients"`
}

type clientConfigRequest struct {
	Client         string `json:"client"`
	Model          string `json:"model,omitempty"`
	AnthropicModel string `json:"anthropic_model,omitempty"`
	OpenAIModel    string `json:"openai_model,omitempty"`
	API            string `json:"api,omitempty"`
	Opus           string `json:"opus,omitempty"`
	Sonnet         string `json:"sonnet,omitempty"`
	Haiku          string `json:"haiku,omitempty"`
	Fable          string `json:"fable,omitempty"`
	Legacy         bool   `json:"legacy,omitempty"`
	Force          bool   `json:"force,omitempty"`
}

type clientConfigView struct {
	Schema         int      `json:"schema"`
	Client         string   `json:"client"`
	Label          string   `json:"label"`
	Target         string   `json:"target"`
	Content        string   `json:"content"`
	Snippet        string   `json:"snippet,omitempty"`
	Changed        []string `json:"changed"`
	Warnings       []string `json:"warnings"`
	WriteSupported bool     `json:"write_supported"`
	BackupDir      string   `json:"backup_dir"`
	Note           string   `json:"note,omitempty"`
}

type clientBackupView struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Modified string `json:"modified"`
}

type clientBackupsView struct {
	Schema         int                `json:"schema"`
	Client         string             `json:"client"`
	Label          string             `json:"label"`
	Target         string             `json:"target"`
	BackupDir      string             `json:"backup_dir"`
	WriteSupported bool               `json:"write_supported"`
	Backups        []clientBackupView `json:"backups"`
}

type clientRestoreRequest struct {
	Client string `json:"client"`
	Backup string `json:"backup"`
}

type clientConfigError struct {
	status  int
	code    string
	message string
}

func (e *clientConfigError) Error() string { return e.message }

type clientPlan struct {
	client         string
	label          string
	target         string
	content        []byte
	snippet        string
	merged         []byte
	changed        []string
	warnings       []string
	writeSupported bool
	note           string
}

func (h *Handler) handleClients(w http.ResponseWriter, r *http.Request) {
	cfg := h.server.Config()
	paths, err := config.ResolvePaths(cfg, h.server.ConfigFilePath())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "config_write_failed", err.Error(), "", "")
		return
	}
	h.writeJSON(w, http.StatusOK, clientsView{
		Schema:    1,
		Relay:     config.ListenOrigin(h.listen()),
		BackupDir: paths.BackupDir,
		Clients: []clientView{
			{
				ID:             "claude",
				Label:          "Claude Code",
				Target:         expandClientTarget(cfg.Clients.Claude.SettingsPath, "~/.claude/settings.json"),
				BaseURLMode:    cfg.Clients.Claude.BaseURLMode,
				WriteSupported: true,
				Dialects:       []string{models.DialectAnthropic},
				Note:           "Writes the non-secret ANTHROPIC_BASE_URL and model aliases into settings.json and keeps unrelated keys.",
			},
			{
				ID:             "codex",
				Label:          "Codex",
				Target:         expandClientTarget(cfg.Clients.Codex.ConfigPath, "~/.codex/config.toml"),
				BaseURLMode:    cfg.Clients.Codex.BaseURLMode,
				WriteSupported: true,
				Dialects:       []string{models.DialectOpenAI},
				Note:           "Adds or refreshes the [model_providers.shunt] block and keeps the rest of config.toml, including comments.",
			},
			{
				ID:             "dsh",
				Label:          "DeepSeek Harness",
				Target:         expandClientTarget(cfg.Clients.DSH.Home, "~/.dsh"),
				BaseURLMode:    "origin",
				WriteSupported: false,
				Dialects:       []string{models.DialectAnthropic, models.DialectOpenAI},
				Note:           "Preview only in v1: merge the fragment into the existing llm-pi-ai row in cordis.patch.yml.",
			},
		},
	})
}

func expandClientTarget(value, fallback string) string {
	target := strings.TrimSpace(value)
	if target == "" {
		target = fallback
	}
	expanded, err := platform.ExpandHome(target)
	if err != nil {
		return target
	}
	return expanded
}

// resolveClientTarget maps a client id to the configured destination. Targets
// always come from config.toml, never from the request.
func (h *Handler) resolveClientTarget(id string) (client, label, target string, supported bool, err error) {
	cfg := h.server.Config()
	switch strings.ToLower(strings.TrimSpace(id)) {
	case "claude":
		return "claude", "Claude Code", expandClientTarget(cfg.Clients.Claude.SettingsPath, "~/.claude/settings.json"), true, nil
	case "codex":
		return "codex", "Codex", expandClientTarget(cfg.Clients.Codex.ConfigPath, "~/.codex/config.toml"), true, nil
	case "dsh":
		return "dsh", "DeepSeek Harness", expandClientTarget(cfg.Clients.DSH.Home, "~/.dsh"), false, nil
	default:
		return "", "", "", false, &clientConfigError{http.StatusBadRequest, "invalid_client", `client must be "claude", "codex", or "dsh"`}
	}
}

func (h *Handler) handleClientBackups(w http.ResponseWriter, r *http.Request) {
	client, label, target, supported, err := h.resolveClientTarget(r.URL.Query().Get("client"))
	if err != nil {
		h.writeClientError(w, err)
		return
	}
	if !supported {
		writeError(w, http.StatusNotImplemented, "client_restore_unsupported", "Shunt cannot restore this client configuration; DSH is preview only", "client", client)
		return
	}
	paths, err := config.ResolvePaths(h.server.Config(), h.server.ConfigFilePath())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "config_write_failed", err.Error(), "", "")
		return
	}
	entries, err := os.ReadDir(filepath.Join(paths.BackupDir, client))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusInternalServerError, "backup_list_failed", err.Error(), "client", client)
		return
	}
	backups := []clientBackupView{}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		backups = append(backups, clientBackupView{
			Name:     entry.Name(),
			Size:     info.Size(),
			Modified: info.ModTime().UTC().Format(time.RFC3339),
		})
	}
	sort.Slice(backups, func(i, j int) bool { return backups[i].Name > backups[j].Name })
	h.writeJSON(w, http.StatusOK, clientBackupsView{
		Schema:         1,
		Client:         client,
		Label:          label,
		Target:         target,
		BackupDir:      paths.BackupDir,
		WriteSupported: supported,
		Backups:        backups,
	})
}

func (h *Handler) handleClientRestore(w http.ResponseWriter, r *http.Request) {
	var req clientRestoreRequest
	if !h.decode(w, r, &req) {
		return
	}
	h.clientsMu.Lock()
	defer h.clientsMu.Unlock()
	client, _, target, supported, err := h.resolveClientTarget(req.Client)
	if err != nil {
		h.writeClientError(w, err)
		return
	}
	if !supported {
		writeError(w, http.StatusNotImplemented, "client_restore_unsupported", "Shunt cannot restore this client configuration; DSH is preview only", "client", client)
		return
	}
	name := strings.TrimSpace(req.Backup)
	if name == "" || filepath.Base(name) != name || name == "." || name == ".." {
		writeError(w, http.StatusBadRequest, "backup_invalid", "backup must be a bare file name listed for this client", "backup", "")
		return
	}
	paths, err := config.ResolvePaths(h.server.Config(), h.server.ConfigFilePath())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "config_write_failed", err.Error(), "", "")
		return
	}
	source := filepath.Join(paths.BackupDir, client, name)
	info, err := os.Lstat(source)
	if err != nil || !info.Mode().IsRegular() {
		writeError(w, http.StatusBadRequest, "backup_invalid", "backup is not a regular file", "backup", name)
		return
	}
	safety, err := clients.Restore(paths.BackupDir, client, name, target)
	if err != nil {
		writeError(w, http.StatusBadRequest, "client_restore_failed", err.Error(), "backup", name)
		return
	}
	h.logger.Info("gui restored client configuration", "client", client, "target", target, "restored", name, "safety_backup", safety)
	h.writeJSON(w, http.StatusOK, map[string]any{
		"schema":   1,
		"client":   client,
		"target":   target,
		"restored": name,
		"backup":   safety,
		"notice":   "Configuration restored atomically. Restart the client to load the previous endpoint.",
	})
}

func (h *Handler) handleClientPreview(w http.ResponseWriter, r *http.Request) {
	var req clientConfigRequest
	if !h.decode(w, r, &req) {
		return
	}
	plan, err := h.buildClientPlan(req)
	if err != nil {
		h.writeClientError(w, err)
		return
	}
	paths, err := config.ResolvePaths(h.server.Config(), h.server.ConfigFilePath())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "config_write_failed", err.Error(), "", "")
		return
	}
	h.writeJSON(w, http.StatusOK, clientConfigView{
		Schema:         1,
		Client:         plan.client,
		Label:          plan.label,
		Target:         plan.target,
		Content:        string(plan.content),
		Snippet:        plan.snippet,
		Changed:        emptyIfNil(plan.changed),
		Warnings:       emptyIfNil(plan.warnings),
		WriteSupported: plan.writeSupported,
		BackupDir:      paths.BackupDir,
		Note:           plan.note,
	})
}

func (h *Handler) handleClientWrite(w http.ResponseWriter, r *http.Request) {
	var req clientConfigRequest
	if !h.decode(w, r, &req) {
		return
	}
	h.clientsMu.Lock()
	defer h.clientsMu.Unlock()
	plan, err := h.buildClientPlan(req)
	if err != nil {
		h.writeClientError(w, err)
		return
	}
	if !plan.writeSupported || len(plan.merged) == 0 {
		writeError(w, http.StatusNotImplemented, "client_write_unsupported", "Shunt cannot write this client configuration; copy the preview and merge it manually", "client", plan.client)
		return
	}
	paths, err := config.ResolvePaths(h.server.Config(), h.server.ConfigFilePath())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "config_write_failed", err.Error(), "", "")
		return
	}
	backup, err := clients.WriteWithBackup(paths.BackupDir, plan.client, plan.target, plan.merged)
	if err != nil {
		writeError(w, http.StatusConflict, "client_write_failed", err.Error(), "target", plan.target)
		return
	}
	h.logger.Info("gui wrote client configuration", "client", plan.client, "target", plan.target, "backup", backup, "changed", len(plan.changed))
	h.writeJSON(w, http.StatusOK, map[string]any{
		"schema":  1,
		"client":  plan.client,
		"target":  plan.target,
		"backup":  backup,
		"changed": emptyIfNil(plan.changed),
		"notice":  "Configuration written atomically. Restart the client to load the new endpoint.",
	})
}

func (h *Handler) writeClientError(w http.ResponseWriter, err error) {
	var ce *clientConfigError
	if errors.As(err, &ce) {
		writeError(w, ce.status, ce.code, ce.message, "client", "")
		return
	}
	writeError(w, http.StatusInternalServerError, "client_config_failed", err.Error(), "client", "")
}

func (h *Handler) buildClientPlan(req clientConfigRequest) (*clientPlan, error) {
	cfg := h.server.Config()
	origin := config.ListenOrigin(h.listen())
	warnings := []string{}
	switch strings.ToLower(strings.TrimSpace(req.Client)) {
	case "claude":
		rec, modelWarnings, err := h.pickModel(models.DialectAnthropic, firstNonEmpty(req.Model, req.AnthropicModel))
		if err != nil {
			return nil, err
		}
		warnings = append(warnings, modelWarnings...)
		opts := clients.ClaudeOptions{
			BaseURL:    origin,
			Model:      rec.ID,
			Opus:       req.Opus,
			Sonnet:     req.Sonnet,
			Haiku:      req.Haiku,
			Fable:      req.Fable,
			Legacy:     req.Legacy,
			PowerShell: runtime.GOOS == "windows",
		}
		target := expandClientTarget(cfg.Clients.Claude.SettingsPath, "~/.claude/settings.json")
		fragment, err := clients.ClaudeSettings(opts)
		if err != nil {
			return nil, err
		}
		existing, err := readClientTarget(target)
		if err != nil {
			return nil, err
		}
		merged, changed, err := clients.MergeClaudeSettings(existing, opts)
		if err != nil {
			return nil, &clientConfigError{http.StatusConflict, "client_merge_failed", err.Error()}
		}
		return &clientPlan{
			client:         "claude",
			label:          "Claude Code",
			target:         target,
			content:        fragment,
			snippet:        clients.ClaudeShell(opts),
			merged:         merged,
			changed:        changed,
			warnings:       warnings,
			writeSupported: true,
			note:           "Keep your existing ANTHROPIC_API_KEY. The settings file never receives a credential value.",
		}, nil
	case "codex":
		rec, modelWarnings, err := h.pickModel(models.DialectOpenAI, firstNonEmpty(req.Model, req.OpenAIModel))
		if err != nil {
			return nil, err
		}
		warnings = append(warnings, modelWarnings...)
		opts := clients.CodexOptions{RelayOrigin: origin, Model: rec.ID, Force: req.Force}
		target := expandClientTarget(cfg.Clients.Codex.ConfigPath, "~/.codex/config.toml")
		existing, err := readClientTarget(target)
		if err != nil {
			return nil, err
		}
		merged, changed, err := clients.MergeCodex(string(existing), opts)
		if err != nil {
			return nil, &clientConfigError{http.StatusConflict, "client_merge_failed", err.Error()}
		}
		return &clientPlan{
			client:         "codex",
			label:          "Codex",
			target:         target,
			content:        []byte(clients.CodexSnippet(opts)),
			merged:         []byte(merged),
			changed:        changed,
			warnings:       warnings,
			writeSupported: true,
			note:           "Set SHUNT_API_KEY in the client environment; the value stays outside config.toml.",
		}, nil
	case "dsh":
		api := normalizeDSHAPI(req.API)
		if api == "" {
			return nil, &clientConfigError{http.StatusUnprocessableEntity, "invalid_api", `api must be "anthropic", "openai", or "both"`}
		}
		opts := clients.DSHOptions{RelayOrigin: origin, API: api, Profile: cfg.Clients.DSH.Profile}
		if api == "anthropic" || api == "both" {
			rec, modelWarnings, err := h.pickModel(models.DialectAnthropic, firstNonEmpty(req.AnthropicModel, req.Model))
			if err != nil {
				return nil, err
			}
			warnings = append(warnings, modelWarnings...)
			opts.AnthropicID, opts.AnthropicName = rec.ID, rec.DisplayName
			if rec.ContextWindow != nil {
				opts.AnthropicCtx = *rec.ContextWindow
			}
		}
		if api == "openai" || api == "both" {
			rec, modelWarnings, err := h.pickModel(models.DialectOpenAI, firstNonEmpty(req.OpenAIModel, req.Model))
			if err != nil {
				return nil, err
			}
			warnings = append(warnings, modelWarnings...)
			opts.OpenAIID, opts.OpenAIName = rec.ID, rec.DisplayName
			if rec.ContextWindow != nil {
				opts.OpenAICtx = *rec.ContextWindow
			}
		}
		return &clientPlan{
			client:         "dsh",
			label:          "DeepSeek Harness",
			target:         expandClientTarget(cfg.Clients.DSH.Home, "~/.dsh"),
			content:        []byte(clients.DSHFragment(opts)),
			changed:        []string{"llm-pi-ai.config.providers"},
			warnings:       warnings,
			writeSupported: false,
			note:           "Merge-ready fragment only. Preserve the rest of the llm-pi-ai row; DSH writes are not supported in v1.",
		}, nil
	default:
		return nil, &clientConfigError{http.StatusBadRequest, "invalid_client", `client must be "claude", "codex", or "dsh"`}
	}
}

func normalizeDSHAPI(api string) string {
	switch strings.ToLower(strings.TrimSpace(api)) {
	case "", "both":
		return "both"
	case "anthropic":
		return "anthropic"
	case "openai":
		return "openai"
	default:
		return ""
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func readClientTarget(target string) ([]byte, error) {
	data, err := os.ReadFile(target)
	if err == nil {
		return data, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return nil, &clientConfigError{http.StatusConflict, "client_target_unreadable", fmt.Sprintf("read %s: %v", target, err)}
}

func (h *Handler) pickModel(dialect, explicit string) (models.Record, []string, error) {
	cat, cached := h.cachedCatalog(dialect)
	warnings := []string{}
	if explicit != "" {
		rec := models.Record{ID: explicit, DisplayName: explicit, Free: models.FreeUnknown}
		if cached {
			found := false
			for _, m := range cat.Models {
				if m.ID == explicit {
					rec = m
					found = true
					if m.Free == models.FreePaid {
						warnings = append(warnings, fmt.Sprintf("model %q is PAID", explicit))
					}
					break
				}
			}
			if !found {
				warnings = append(warnings, fmt.Sprintf("model %q is not in the cached %s catalog", explicit, dialect))
			}
			if cat.Stale {
				warnings = append(warnings, "the cached model catalog is stale")
			}
		}
		return rec, warnings, nil
	}
	if !cached {
		return models.Record{}, nil, &clientConfigError{
			http.StatusConflict,
			"model_catalog_missing",
			fmt.Sprintf("no cached %s catalog for the effective profile; load models in the Models tab or choose a model id", dialect),
		}
	}
	if cat.Stale {
		warnings = append(warnings, "the cached model catalog is stale")
	}
	for _, m := range cat.Models {
		if m.Free == models.FreeFree {
			return m, warnings, nil
		}
	}
	ids := make([]string, 0, len(cat.Models))
	for _, m := range cat.Models {
		ids = append(ids, m.ID)
	}
	sort.Strings(ids)
	return models.Record{}, nil, &clientConfigError{
		http.StatusUnprocessableEntity,
		"no_free_model",
		fmt.Sprintf("no free model in the %s catalog; choose one explicitly: %s", dialect, strings.Join(ids, ", ")),
	}
}

func (h *Handler) cachedCatalog(dialect string) (models.Catalog, bool) {
	cfg := h.server.Config()
	paths, err := config.ResolvePaths(cfg, h.server.ConfigFilePath())
	if err != nil {
		return models.Catalog{}, false
	}
	snap := h.server.Snapshot()
	profileID := snap.Effective
	if profileID == "" {
		profileID = cfg.DefaultProfile
	}
	profile, ok := cfg.Profiles[profileID]
	if !ok {
		return models.Catalog{}, false
	}
	cache := models.NewCache(paths.CacheDir, cfg.Models.CacheTTLHours, cfg.Models.StaleTTLHours)
	cat, state, err := cache.Load(profileID, dialect, models.ModelsURL(profile.BaseURL, dialect))
	if err != nil || (!state.Fresh && !state.Usable) {
		return models.Catalog{}, false
	}
	return cat, true
}

func emptyIfNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
