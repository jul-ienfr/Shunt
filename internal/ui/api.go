package ui

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"shunt/internal/config"
	"shunt/internal/models"
	"shunt/internal/relay"
	"shunt/internal/runtime"
	"shunt/internal/upstream"
)

type overviewDoc struct {
	Schema     int                  `json:"schema"`
	Relay      relayView            `json:"relay"`
	Mode       string               `json:"mode"`
	Selected   *string              `json:"selected"`
	Preferred  *string              `json:"preferred"`
	Effective  string               `json:"effective"`
	Inflight   int                  `json:"inflight"`
	LastSwitch *runtime.SwitchEvent `json:"last_switch"`
	Profiles   []profileView        `json:"profiles"`
}

type relayView struct {
	Version          string          `json:"version"`
	PID              int             `json:"pid"`
	UptimeS          int64           `json:"uptime_s"`
	Listen           string          `json:"listen"`
	IsLoopback       bool            `json:"is_loopback"`
	AllowNonLoopback bool            `json:"allow_non_loopback"`
	Interfaces       []interfaceView `json:"interfaces"`
}

type interfaceView struct {
	Name     string `json:"name"`
	Address  string `json:"address"`
	Loopback bool   `json:"loopback"`
}

type profileView struct {
	ID        string  `json:"id"`
	Label     string  `json:"label"`
	Scheme    string  `json:"scheme"`
	Host      string  `json:"host"`
	Port      int     `json:"port"`
	BaseURL   string  `json:"base_url"`
	Enabled   bool    `json:"enabled"`
	Health    string  `json:"health"`
	LatencyMS *int64  `json:"latency_ms"`
	LastError *string `json:"last_error"`
}

func (h *Handler) overview() overviewDoc {
	snap := h.server.Snapshot()
	snap.Profiles = h.server.Tracker().Snapshot()
	cfg := h.server.Config()
	listen := snap.Listen
	if listen == "" {
		listen = cfg.Relay.Listen
	}
	ip, _, _ := net.SplitHostPort(listen)
	parsed := net.ParseIP(strings.Trim(ip, "[]"))
	doc := overviewDoc{
		Schema:     1,
		Mode:       snap.Mode,
		Selected:   snap.Selected,
		Preferred:  snap.Preferred,
		Effective:  snap.Effective,
		Inflight:   h.server.Proxy().Inflight().Count(),
		LastSwitch: snap.LastSwitch,
		Relay: relayView{
			Version:          h.server.Version,
			PID:              snap.PID,
			UptimeS:          snap.UptimeS,
			Listen:           listen,
			IsLoopback:       parsed != nil && parsed.IsLoopback(),
			AllowNonLoopback: cfg.Relay.AllowNonLoopback,
		},
	}
	if addrs, err := config.LocalAddresses(); err == nil {
		for _, addr := range addrs {
			doc.Relay.Interfaces = append(doc.Relay.Interfaces, interfaceView{Name: addr.Name, Address: addr.Address, Loopback: addr.Loopback})
		}
	}
	if doc.Relay.Interfaces == nil {
		doc.Relay.Interfaces = []interfaceView{}
	}
	ids := make([]string, 0, len(cfg.Profiles))
	for id := range cfg.Profiles {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		p := cfg.Profiles[id]
		view := profileView{
			ID:      id,
			Label:   p.Label,
			BaseURL: p.BaseURL,
			Enabled: p.IsEnabled(),
			Health:  "unknown",
		}
		if u, err := url.Parse(p.BaseURL); err == nil {
			view.Scheme = u.Scheme
			view.Host = u.Hostname()
			view.Port = effectivePort(u)
		}
		if health, ok := snap.Profiles[id]; ok {
			view.LatencyMS = health.LastProbeMS
			view.LastError = health.LastError
			if health.Healthy != nil {
				if *health.Healthy {
					view.Health = "healthy"
				} else {
					view.Health = "unhealthy"
				}
			}
		}
		doc.Profiles = append(doc.Profiles, view)
	}
	if doc.Profiles == nil {
		doc.Profiles = []profileView{}
	}
	return doc
}

func (h *Handler) handleEntry(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IP         string `json:"ip"`
		Port       int    `json:"port"`
		ConfirmLAN bool   `json:"confirm_lan"`
	}
	if !h.decode(w, r, &req) {
		return
	}
	listen, generation, err := h.server.ApplyEntry(req.IP, req.Port, req.ConfirmLAN)
	if err != nil {
		h.writeApplyError(w, err)
		return
	}
	h.logger.Info("gui changed entry", "listen", listen, "generation", generation)
	h.writeJSON(w, http.StatusOK, map[string]any{
		"schema":     1,
		"listen":     listen,
		"url":        "http://" + listen + "/_shunt/ui",
		"generation": generation,
		"rebound":    true,
		"notice":     "The entry address changed. Update the base URL of every configured client; upstream switches do not move the entry.",
	})
}

func (h *Handler) handleProfileEndpoint(w http.ResponseWriter, r *http.Request, id string) {
	var req struct {
		Scheme string `json:"scheme"`
		Host   string `json:"host"`
		Port   int    `json:"port"`
	}
	if !h.decode(w, r, &req) {
		return
	}
	res, err := h.server.ApplyProfileEndpoint(id, req.Scheme, req.Host, req.Port)
	if err != nil {
		h.writeApplyError(w, err)
		return
	}
	cfg := h.server.Config()
	p := cfg.Profiles[id]
	h.logger.Info("gui changed profile endpoint", "profile", id, "base_url", p.BaseURL)
	h.writeJSON(w, http.StatusOK, map[string]any{
		"schema":   1,
		"profile":  id,
		"base_url": p.BaseURL,
		"probe": map[string]any{
			"ok":         res.OK,
			"latency_ms": res.LatencyMS,
			"status":     res.Status,
		},
		"applied": true,
		"notice":  "New requests use the new endpoint; in-flight requests finish on the previous one.",
	})
}

func (h *Handler) handleSwitch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Profile string `json:"profile"`
		Reason  string `json:"reason"`
		Probe   *bool  `json:"probe"`
		Force   bool   `json:"force"`
	}
	if !h.decode(w, r, &req) {
		return
	}
	probe := true
	if req.Probe != nil {
		probe = *req.Probe
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		reason = "gui"
	}
	if _, err := h.server.SwitchProfile(req.Profile, probe, req.Force, "gui", reason); err != nil {
		h.writeApplyError(w, err)
		return
	}
	h.logger.Info("gui switched profile", "profile", req.Profile, "probe", probe)
	h.writeJSON(w, http.StatusOK, h.overview())
}

func (h *Handler) handleMode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode      string `json:"mode"`
		Preferred string `json:"preferred"`
	}
	if !h.decode(w, r, &req) {
		return
	}
	if err := h.server.SetMode(req.Mode, req.Preferred, "gui"); err != nil {
		h.writeApplyError(w, err)
		return
	}
	h.logger.Info("gui changed mode", "mode", req.Mode, "preferred", req.Preferred)
	h.writeJSON(w, http.StatusOK, h.overview())
}

func (h *Handler) handleModels(w http.ResponseWriter, r *http.Request, forceRefresh bool) {
	cfg := h.server.Config()
	snap := h.server.Snapshot()
	query := r.URL.Query()
	via := strings.TrimSpace(query.Get("via"))
	profileID := snap.Effective
	if profileID == "" {
		profileID = cfg.DefaultProfile
	}
	if via != "" && via != "active" {
		profileID = via
	}
	profile, ok := cfg.Profiles[profileID]
	if !ok || !profile.IsEnabled() {
		writeError(w, http.StatusNotFound, "profile_not_found", "profile "+profileID+" does not exist or is disabled", "profile", "")
		return
	}
	dialect := strings.ToLower(strings.TrimSpace(query.Get("dialect")))
	if dialect == "" {
		dialect = cfg.Models.DefaultDialect
	}
	if dialect != models.DialectAnthropic && dialect != models.DialectOpenAI {
		writeError(w, http.StatusUnprocessableEntity, "invalid_dialect", "dialect must be anthropic or openai", "dialect", "")
		return
	}
	paths, err := config.ResolvePaths(cfg, h.server.ConfigFilePath())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "config_write_failed", err.Error(), "", "")
		return
	}
	cache := models.NewCache(paths.CacheDir, cfg.Models.CacheTTLHours, cfg.Models.StaleTTLHours)
	sourceURL := models.ModelsURL(profile.BaseURL, dialect)
	if forceRefresh || query.Get("refresh") == "true" {
		_ = cache.Invalidate(profileID, dialect)
	}
	var stale *models.Catalog
	if !forceRefresh && query.Get("refresh") != "true" {
		cat, state, cacheErr := cache.Load(profileID, dialect, sourceURL)
		if state.Fresh {
			h.writeJSON(w, http.StatusOK, cat)
			return
		}
		if state.Usable {
			stale = &cat
		}
		if cacheErr != nil {
			h.logger.Warn("ignoring model cache", "profile", profileID, "error", cacheErr)
		}
	}
	token, envName := models.ResolveToken(os.Getenv, "")
	if token == "" {
		writeError(w, http.StatusConflict, "credential_unavailable", "set SHUNT_TOKEN in the relay process environment to discover models", "token", "")
		return
	}
	client := upstream.NewClient(profile)
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	cat, fetchErr := models.Fetch(ctx, models.FetchOptions{
		Profile:    profileID,
		BaseURL:    profile.BaseURL,
		Dialect:    dialect,
		Token:      token,
		HTTPClient: client,
		PageSize:   cfg.Models.PageSize,
		MaxPages:   cfg.Models.MaxPages,
		Source:     "relay",
	})
	if fetchErr != nil {
		h.logger.Warn("model discovery failed", "profile", profileID, "dialect", dialect, "error", fetchErr, "token_env", envName)
		if stale != nil {
			h.writeJSON(w, http.StatusOK, stale)
			return
		}
		status, code := modelErrorCode(fetchErr)
		writeError(w, status, code, fetchErr.Error(), "models", "")
		return
	}
	if err := cache.Save(cat); err != nil {
		h.logger.Warn("model cache write failed", "profile", profileID, "error", err)
	}
	h.writeJSON(w, http.StatusOK, cat)
}

func modelErrorCode(err error) (int, string) {
	var fe *models.FetchError
	if errors.As(err, &fe) {
		switch fe.Kind {
		case models.KindAuth:
			return http.StatusBadGateway, "upstream_auth_failed"
		case models.KindUnreachable:
			return http.StatusBadGateway, "upstream_unreachable"
		case models.KindUpstream:
			return http.StatusBadGateway, "upstream_unreachable"
		default:
			return http.StatusBadGateway, "upstream_unreachable"
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return http.StatusGatewayTimeout, "upstream_timeout"
	}
	return http.StatusBadGateway, "upstream_unreachable"
}

func (h *Handler) writeApplyError(w http.ResponseWriter, err error) {
	var apply *relay.ApplyError
	if errors.As(err, &apply) {
		status := http.StatusUnprocessableEntity
		switch apply.Code {
		case relay.CodeLANNotConfirmed:
			status = http.StatusForbidden
		case relay.CodeBindFailed:
			status = http.StatusConflict
		case relay.CodeConfigWriteFailed:
			status = http.StatusInternalServerError
		case relay.CodeProfileNotFound:
			status = http.StatusNotFound
		case relay.CodeProfileDisabled, relay.CodeInvalidMode, relay.CodeInvalidEntry, relay.CodeInvalidEndpoint, relay.CodeProbeFailed:
			status = http.StatusUnprocessableEntity
		}
		writeError(w, status, apply.Code, apply.Error(), apply.Field, apply.Current)
		return
	}
	writeError(w, http.StatusInternalServerError, "config_write_failed", err.Error(), "", "")
}

var _ = strconv.Itoa
var _ = nowUTC
