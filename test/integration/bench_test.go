package integration

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"shunt/test/support"
)

const benchPath = "/v1/messages"

var benchPayload = []byte(`{"model":"bench","messages":[{"role":"user","content":"ping"}],"stream":false}`)

func newBenchClient(b *testing.B) *http.Client {
	transport := &http.Transport{
		MaxIdleConns:        256,
		MaxIdleConnsPerHost: 64,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  true,
	}
	b.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 10 * time.Second}
}

func newBenchUpstream(b *testing.B, handler http.HandlerFunc) *httptest.Server {
	server := httptest.NewServer(handler)
	b.Cleanup(server.Close)
	return server
}

func benchJSON(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"ok":true}`)
}

func benchStream(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	_, _ = io.WriteString(w, "data: one\n\n")
	if flusher != nil {
		flusher.Flush()
	}
	_, _ = io.WriteString(w, "data: two\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

func sendNonStreamed(client *http.Client, url string) error {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(benchPayload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(io.Discard, resp.Body)
	closeErr := resp.Body.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status = %d", resp.StatusCode)
	}
	return nil
}

func readFirstStreamedByte(client *http.Client, url string) (time.Duration, error) {
	start := time.Now()
	resp, err := client.Get(url)
	if err != nil {
		return 0, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return 0, fmt.Errorf("status = %d", resp.StatusCode)
	}
	var first [1]byte
	if _, err := io.ReadFull(resp.Body, first[:]); err != nil {
		_ = resp.Body.Close()
		return 0, err
	}
	elapsed := time.Since(start)
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		_ = resp.Body.Close()
		return 0, err
	}
	if err := resp.Body.Close(); err != nil {
		return 0, err
	}
	return elapsed, nil
}

func reportPercentiles(b *testing.B, samples []time.Duration) {
	b.Helper()
	if len(samples) == 0 {
		return
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	pick := func(quantile float64) time.Duration {
		index := int(math.Ceil(quantile*float64(len(samples)))) - 1
		if index < 0 {
			index = 0
		}
		return samples[index]
	}
	b.ReportMetric(float64(pick(0.50))/float64(time.Microsecond), "p50-us")
	b.ReportMetric(float64(pick(0.95))/float64(time.Microsecond), "p95-us")
}

func benchNonStreamed(b *testing.B, client *http.Client, url string) {
	b.Helper()
	b.ReportAllocs()
	if err := sendNonStreamed(client, url); err != nil {
		b.Fatalf("warm-up: %v", err)
	}
	b.ResetTimer()
	samples := make([]time.Duration, b.N)
	for i := 0; i < b.N; i++ {
		start := time.Now()
		if err := sendNonStreamed(client, url); err != nil {
			b.Fatal(err)
		}
		samples[i] = time.Since(start)
	}
	reportPercentiles(b, samples)
}

func benchFirstStreamedByte(b *testing.B, client *http.Client, url string) {
	b.Helper()
	b.ReportAllocs()
	if _, err := readFirstStreamedByte(client, url); err != nil {
		b.Fatalf("warm-up: %v", err)
	}
	b.ResetTimer()
	samples := make([]time.Duration, b.N)
	for i := 0; i < b.N; i++ {
		elapsed, err := readFirstStreamedByte(client, url)
		if err != nil {
			b.Fatal(err)
		}
		samples[i] = elapsed
	}
	reportPercentiles(b, samples)
}

// BenchmarkNonStreamedLatency compares direct upstream requests with the same
// request through the relay. Run it on the target workstation to evaluate
// SC-003; the contract tests cover behavior, not numeric latency.
func BenchmarkNonStreamedLatency(b *testing.B) {
	upstream := newBenchUpstream(b, benchJSON)
	relay := support.NewRelay(b, support.RelayOptions{
		Profiles: map[string]string{"local": upstream.URL},
		Selected: "local",
	})
	client := newBenchClient(b)

	b.Run("direct", func(b *testing.B) {
		benchNonStreamed(b, client, upstream.URL+benchPath)
	})
	b.Run("relay", func(b *testing.B) {
		benchNonStreamed(b, client, relay.URL(benchPath))
	})
}

// BenchmarkFirstStreamedByteLatency compares time-to-first-byte for a short
// server-sent-event stream, directly and through the relay.
func BenchmarkFirstStreamedByteLatency(b *testing.B) {
	upstream := newBenchUpstream(b, benchStream)
	relay := support.NewRelay(b, support.RelayOptions{
		Profiles: map[string]string{"local": upstream.URL},
		Selected: "local",
	})
	client := newBenchClient(b)

	b.Run("direct", func(b *testing.B) {
		benchFirstStreamedByte(b, client, upstream.URL+benchPath)
	})
	b.Run("relay", func(b *testing.B) {
		benchFirstStreamedByte(b, client, relay.URL(benchPath))
	})
}
