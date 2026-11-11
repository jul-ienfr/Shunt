package ui

import (
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"shunt/internal/relay"
)

//go:embed assets/index.html assets/app.js assets/i18n.js assets/style.css assets/theme.js assets/favicon.svg
var assets embed.FS

// Handler serves the embedded GUI and its authenticated API.
type Handler struct {
	server    *relay.Server
	token     string
	maxBody   int64
	logger    *slog.Logger
	clientsMu sync.Mutex
}

// New builds a GUI handler with a per-process 256-bit token.
func New(server *relay.Server, maxBody int64, logger *slog.Logger) (*Handler, error) {
	if server == nil {
		return nil, errors.New("ui: nil server")
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, err
	}
	if maxBody <= 0 {
		maxBody = 65536
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		server:  server,
		token:   hex.EncodeToString(raw[:]),
		maxBody: maxBody,
		logger:  logger,
	}, nil
}

// Token returns the per-run token. It is intentionally not persisted.
func (h *Handler) Token() string { return h.token }

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setSecurityHeaders(w)
	switch {
	case r.URL.Path == "/_shunt/ui" || r.URL.Path == "/_shunt/ui/":
		h.serveIndex(w, r)
		return
	case r.URL.Path == "/_shunt/ui/app.js":
		h.serveAsset(w, r, "assets/app.js", "application/javascript; charset=utf-8")
		return
	case r.URL.Path == "/_shunt/ui/i18n.js":
		h.serveAsset(w, r, "assets/i18n.js", "application/javascript; charset=utf-8")
		return
	case r.URL.Path == "/_shunt/ui/theme.js":
		h.serveAsset(w, r, "assets/theme.js", "application/javascript; charset=utf-8")
		return
	case r.URL.Path == "/_shunt/ui/style.css":
		h.serveAsset(w, r, "assets/style.css", "text/css; charset=utf-8")
		return
	case r.URL.Path == "/_shunt/ui/favicon.svg":
		h.serveAsset(w, r, "assets/favicon.svg", "image/svg+xml")
		return
	case strings.HasPrefix(r.URL.Path, "/_shunt/api/"):
		h.serveAPI(w, r)
		return
	}
	http.NotFound(w, r)
}

func setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
}

func (h *Handler) serveIndex(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, "GET, HEAD")
		return
	}
	page, err := assets.ReadFile("assets/index.html")
	if err != nil {
		http.Error(w, "ui asset missing", http.StatusInternalServerError)
		return
	}
	page = []byte(strings.ReplaceAll(string(page), "__SHUNT_UI_TOKEN__", h.token))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(page)
}

func (h *Handler) serveAsset(w http.ResponseWriter, r *http.Request, name, contentType string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, "GET, HEAD")
		return
	}
	data, err := assets.ReadFile(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(data)
}

func (h *Handler) serveAPI(w http.ResponseWriter, r *http.Request) {
	if !h.hostAllowed(r) {
		writeError(w, http.StatusForbidden, "ui_origin_invalid", "request host does not match the relay listener", "host", "")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/_shunt/api/")
	switch {
	case path == "overview" && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		h.writeJSON(w, http.StatusOK, h.overview())
	case path == "models" && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		h.handleModels(w, r, false)
	case path == "models/refresh" && r.Method == http.MethodPost:
		if !h.authorize(w, r) {
			return
		}
		h.handleModels(w, r, true)
	case path == "clients" && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		h.handleClients(w, r)
	case path == "clients/preview" && r.Method == http.MethodPost:
		if !h.authorize(w, r) {
			return
		}
		h.handleClientPreview(w, r)
	case path == "clients/write" && r.Method == http.MethodPost:
		if !h.authorize(w, r) {
			return
		}
		h.handleClientWrite(w, r)
	case path == "clients/backups" && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		h.handleClientBackups(w, r)
	case path == "clients/restore" && r.Method == http.MethodPost:
		if !h.authorize(w, r) {
			return
		}
		h.handleClientRestore(w, r)
	case path == "entry" && r.Method == http.MethodPost:
		if !h.authorize(w, r) {
			return
		}
		h.handleEntry(w, r)
	case path == "switch" && r.Method == http.MethodPost:
		if !h.authorize(w, r) {
			return
		}
		h.handleSwitch(w, r)
	case path == "mode" && r.Method == http.MethodPost:
		if !h.authorize(w, r) {
			return
		}
		h.handleMode(w, r)
	case strings.HasPrefix(path, "profiles/") && strings.HasSuffix(path, "/endpoint") && r.Method == http.MethodPost:
		if !h.authorize(w, r) {
			return
		}
		id := strings.TrimSuffix(strings.TrimPrefix(path, "profiles/"), "/endpoint")
		h.handleProfileEndpoint(w, r, id)
	case path == "overview" || path == "models" || path == "clients" || path == "clients/backups":
		methodNotAllowed(w, "GET, HEAD")
	default:
		writeError(w, http.StatusNotFound, "not_found", "unknown GUI API route", "", "")
	}
}

func (h *Handler) authorize(w http.ResponseWriter, r *http.Request) bool {
	if !h.hostAllowed(r) {
		writeError(w, http.StatusForbidden, "ui_origin_invalid", "request host does not match the relay listener", "host", "")
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" && !h.originAllowed(origin) {
		writeError(w, http.StatusForbidden, "ui_origin_invalid", "request origin is not the relay origin", "origin", "")
		return false
	}
	switch strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site"))) {
	case "", "same-origin", "none":
	default:
		writeError(w, http.StatusForbidden, "ui_origin_invalid", "cross-site requests are not allowed", "origin", "")
		return false
	}
	provided := r.Header.Get("X-Shunt-UI-Token")
	if subtle.ConstantTimeCompare([]byte(provided), []byte(h.token)) != 1 {
		writeError(w, http.StatusForbidden, "ui_token_invalid", "missing or invalid UI token", "token", "")
		return false
	}
	return true
}

func (h *Handler) listen() string {
	listen := h.server.Snapshot().Listen
	if listen == "" {
		listen = h.server.Config().Relay.Listen
	}
	return listen
}

func (h *Handler) hostAllowed(r *http.Request) bool {
	host := strings.TrimSpace(r.Host)
	listen := h.listen()
	if host == listen {
		return true
	}
	if listen == "" {
		return false
	}
	hostName, hostPort, err := net.SplitHostPort(host)
	if err != nil {
		return false
	}
	listenIP, listenPort, err := net.SplitHostPort(listen)
	if err != nil {
		return false
	}
	if hostPort != listenPort {
		return false
	}
	ip := net.ParseIP(strings.Trim(listenIP, "[]"))
	if ip != nil && ip.IsLoopback() {
		return strings.EqualFold(hostName, "localhost") || strings.EqualFold(hostName, "127.0.0.1") || hostName == "::1" || hostName == "[::1]"
	}
	return false
}

func (h *Handler) originAllowed(origin string) bool {
	origin = strings.TrimRight(origin, "/")
	listen := h.listen()
	if origin == "http://"+listen {
		return true
	}
	ip, port, err := net.SplitHostPort(listen)
	if err == nil {
		parsed := net.ParseIP(strings.Trim(ip, "[]"))
		if parsed != nil && parsed.IsLoopback() {
			return origin == "http://localhost:"+port || origin == "http://127.0.0.1:"+port || origin == "http://[::1]:"+port
		}
	}
	return false
}

func (h *Handler) decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, h.maxBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "request body exceeds ui.max_request_bytes", "body", "")
			return false
		}
		writeError(w, http.StatusBadRequest, "invalid_payload", "invalid JSON payload: "+err.Error(), "body", "")
		return false
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid_payload", "request body must contain one JSON document", "body", "")
		return false
	}
	return true
}

func (h *Handler) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type apiError struct {
	Schema int         `json:"schema"`
	Error  apiErrorDoc `json:"error"`
}

type apiErrorDoc struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field,omitempty"`
	Current string `json:"current,omitempty"`
}

func writeError(w http.ResponseWriter, status int, code, message, field, current string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(apiError{Schema: 1, Error: apiErrorDoc{Code: code, Message: message, Field: field, Current: current}})
}

func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", "", "")
}

func parsePort(raw string) (int, error) {
	return strconv.Atoi(raw)
}

func effectivePort(u *url.URL) int {
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err == nil {
			return n
		}
	}
	if u.Scheme == "https" {
		return 443
	}
	return 80
}

func nowUTC() time.Time { return time.Now().UTC() }
