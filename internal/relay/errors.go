package relay

import (
	"encoding/json"
	"net/http"
)

// Stable relay error codes.
const (
	CodeUpstreamUnavailable = "shunt_upstream_unavailable"
	CodeUpstreamTimeout     = "shunt_upstream_timeout"
	CodeOverloaded          = "shunt_overloaded"
	CodeNoHealthyUpstream   = "shunt_no_healthy_upstream"
)

// ErrorBody is the relay-generated error document. It carries both the
// Anthropic-style top-level type and the OpenAI-style nested error object.
type ErrorBody struct {
	Type      string    `json:"type"`
	Error     ErrorInfo `json:"error"`
	RequestID string    `json:"request_id"`
}

// ErrorInfo is the nested error detail.
type ErrorInfo struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Code    string `json:"code"`
	Profile string `json:"profile,omitempty"`
}

// WriteRelayError writes a structured relay error.
func WriteRelayError(w http.ResponseWriter, status int, code, message, profile, requestID string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if requestID != "" {
		w.Header().Set("X-Shunt-Request-Id", requestID)
	}
	if profile != "" {
		w.Header().Set("X-Shunt-Profile", profile)
	}
	w.WriteHeader(status)
	body := ErrorBody{
		Type: "error",
		Error: ErrorInfo{
			Type:    code,
			Message: message,
			Code:    code,
			Profile: profile,
		},
		RequestID: requestID,
	}
	_ = json.NewEncoder(w).Encode(body)
}
