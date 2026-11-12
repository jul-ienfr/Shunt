package upstream

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"shunt/internal/config"
)

func probeProfile(baseURL string) config.Profile {
	return config.Profile{
		BaseURL:                 baseURL,
		ProbeURL:                baseURL + "/",
		ProbeMethod:             http.MethodGet,
		ConnectTimeoutMS:        500,
		ResponseHeaderTimeoutMS: 1000,
	}
}

func TestProbeClassification(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		wantOK     bool
		wantStatus int
	}{
		{name: "ok", status: 200, wantOK: true, wantStatus: 200},
		{name: "unauthorized", status: 401, wantOK: true, wantStatus: 401},
		{name: "forbidden", status: 403, wantOK: true, wantStatus: 403},
		{name: "rate limited", status: 429, wantOK: true, wantStatus: 429},
		{name: "server error", status: 503, wantOK: false, wantStatus: 503},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()
			p := probeProfile(srv.URL)
			res := Probe(context.Background(), NewClient(p), p, time.Second)
			if res.OK != tc.wantOK {
				t.Fatalf("OK = %v, want %v (err %q)", res.OK, tc.wantOK, res.Err)
			}
			if res.Status != tc.wantStatus {
				t.Fatalf("status = %d, want %d", res.Status, tc.wantStatus)
			}
		})
	}
}

func TestProbeTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(250 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	p := probeProfile(srv.URL)
	res := Probe(context.Background(), NewClient(p), p, 40*time.Millisecond)
	if res.OK || !res.Timeout {
		t.Fatalf("expected timeout failure, got %+v", res)
	}
}

func TestProbeTLSError(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	p := probeProfile(srv.URL)
	res := Probe(context.Background(), NewClient(p), p, time.Second)
	if res.OK {
		t.Fatal("self-signed certificate must fail verification")
	}
	if res.Err == "" {
		t.Fatal("expected a TLS error message")
	}
}

func TestProbeDNSFailure(t *testing.T) {
	p := probeProfile("http://does-not-exist.invalid")
	res := Probe(context.Background(), NewClient(p), p, 2*time.Second)
	if res.OK || res.Err == "" {
		t.Fatalf("expected DNS failure, got %+v", res)
	}
}

func TestTrackerHysteresis(t *testing.T) {
	profiles := map[string]config.Profile{"local": probeProfile("http://127.0.0.1:1")}
	tr := NewTracker(profiles, 3, 2)
	ok := Result{OK: true, Status: 200}
	fail := Result{Status: 0, Err: "dial failed"}

	tr.Observe("local", ok)
	if h, known := tr.Healthy("local"); !known || !h {
		t.Fatalf("after first success: healthy=%v known=%v", h, known)
	}
	tr.Observe("local", fail)
	tr.Observe("local", fail)
	if h, _ := tr.Healthy("local"); !h {
		t.Fatal("two failures must not demote before the threshold")
	}
	tr.Observe("local", fail)
	if h, _ := tr.Healthy("local"); h {
		t.Fatal("third failure must demote")
	}
	tr.Observe("local", ok)
	if h, _ := tr.Healthy("local"); h {
		t.Fatal("one success must not recover before the threshold")
	}
	tr.Observe("local", ok)
	if h, _ := tr.Healthy("local"); !h {
		t.Fatal("second success must recover")
	}
}

func TestTrackerUnknownAndSnapshot(t *testing.T) {
	tr := NewTracker(map[string]config.Profile{"local": probeProfile("http://127.0.0.1:1")}, 3, 2)
	if h, known := tr.Healthy("local"); !h || known {
		t.Fatalf("unknown profile should be usable: healthy=%v known=%v", h, known)
	}
	tr.Observe("local", Result{OK: true, Status: 200, LatencyMS: 7})
	snap := tr.Snapshot()
	rec := snap["local"]
	if rec.Healthy == nil || !*rec.Healthy || rec.LastStatus == nil || *rec.LastStatus != 200 {
		t.Fatalf("snapshot = %+v", rec)
	}
	*rec.Healthy = false
	if h, _ := tr.Healthy("local"); !h {
		t.Fatal("snapshot must be a deep copy")
	}
	tr.UpdateProfile("local", "http://127.0.0.1:2", false)
	rec = tr.Snapshot()["local"]
	if rec.BaseURL != "http://127.0.0.1:2" || rec.Enabled {
		t.Fatalf("profile update not applied: %+v", rec)
	}
}
