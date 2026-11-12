package logging

import (
	"bytes"
	"strings"
	"testing"
)

func TestLoggerRedactsSensitiveAttributes(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			var buf bytes.Buffer
			logger := New(Options{Level: "debug", Format: format, Writer: &buf})
			logger.Info("request",
				"authorization", "Bearer super-secret",
				"X-Api-Key", "sk-live-secret",
				"proxy-authorization", "Basic cHJveHk=",
				"cookie", "session=secret",
				"set-cookie", "session=secret",
				"token", "tok_secret",
				"access_token", "at_secret",
				"request_id", "req-1",
			)
			out := buf.String()
			if !strings.Contains(out, "[REDACTED]") {
				t.Fatalf("no redaction marker in %s log: %s", format, out)
			}
			for _, secret := range []string{
				"super-secret", "sk-live-secret", "cHJveHk=", "session=secret",
				"tok_secret", "at_secret",
			} {
				if strings.Contains(out, secret) {
					t.Fatalf("%s log leaked %q: %s", format, secret, out)
				}
			}
			if !strings.Contains(out, "req-1") {
				t.Fatalf("%s log dropped a safe attribute: %s", format, out)
			}
		})
	}
}

func TestLoggerLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	logger := New(Options{Level: "warn", Format: "text", Writer: &buf})
	logger.Info("hidden", "authorization", "Bearer secret")
	if buf.Len() != 0 {
		t.Fatalf("info log passed a warn filter: %s", buf.String())
	}
	logger.Warn("visible", "profile", "local")
	if out := buf.String(); !strings.Contains(out, "visible") || !strings.Contains(out, "local") {
		t.Fatalf("warn log missing: %s", out)
	}
}
