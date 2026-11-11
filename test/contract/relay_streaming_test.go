package contract

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"shunt/test/support"
)

func TestRelayStreamsFirstChunkBeforeUpstreamFinishes(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)

	first := "event: start\ndata: one\n\n"
	second := "event: done\ndata: two\n\n"
	upstream := support.New(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("upstream response is not flushable")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, first)
		flusher.Flush()
		<-release
		_, _ = io.WriteString(w, second)
		flusher.Flush()
	})
	defer upstream.Close()

	relay := support.NewRelay(t, support.RelayOptions{
		Profiles: map[string]string{"local": upstream.Origin()},
		Selected: "local",
	})

	resp, err := http.Get(relay.URL("/v1/messages"))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content type = %q", ct)
	}

	got := make(chan []byte, 1)
	go func() {
		buf := make([]byte, len(first))
		if _, err := io.ReadFull(resp.Body, buf); err != nil {
			got <- nil
			return
		}
		got <- buf
	}()
	select {
	case buf := <-got:
		if string(buf) != first {
			t.Fatalf("first chunk = %q", buf)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first chunk was not streamed before the upstream finished")
	}

	unblock()
	rest, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read rest: %v", err)
	}
	if string(rest) != second {
		t.Fatalf("rest = %q", rest)
	}
}

func TestRelayPassesCompressedBodiesUntouched(t *testing.T) {
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	if _, err := zw.Write([]byte(`{"message":"compressed payload"}`)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	original := compressed.Bytes()

	upstream := support.New(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(original)
	})
	defer upstream.Close()

	relay := support.NewRelay(t, support.RelayOptions{
		Profiles: map[string]string{"local": upstream.Origin()},
		Selected: "local",
	})

	client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	req, err := http.NewRequest(http.MethodGet, relay.URL("/compressed"), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("content-encoding = %q", got)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, original) {
		t.Fatalf("compressed body altered: %d bytes vs %d", len(body), len(original))
	}
}

func TestRelayPassesRequestBodyAndToleratesIdleGaps(t *testing.T) {
	payload := bytes.Repeat([]byte("shunt-body-"), 32*1024)

	upstream := support.New(func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "chunk-1")
		flusher.Flush()
		time.Sleep(250 * time.Millisecond)
		_, _ = io.WriteString(w, "chunk-2")
		flusher.Flush()
	})
	defer upstream.Close()

	relay := support.NewRelay(t, support.RelayOptions{
		Profiles:                map[string]string{"local": upstream.Origin()},
		Selected:                "local",
		ResponseHeaderTimeoutMS: 5000,
	})

	resp, err := http.Post(relay.URL("/echo"), "application/octet-stream", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != "chunk-1chunk-2" {
		t.Fatalf("streamed body = %q", body)
	}

	got, ok := upstream.LastRequest()
	if !ok {
		t.Fatal("upstream received no request")
	}
	if !bytes.Equal(got.Body, payload) {
		t.Fatalf("request body altered: %d bytes vs %d", len(got.Body), len(payload))
	}
}

func TestRelayFlushesEachFrame(t *testing.T) {
	chunks := []string{"a", "b", "c", "d", "e"}
	handler := support.SSE(chunks, 20*time.Millisecond, false)
	fake := support.New(handler)
	defer fake.Close()

	relay := support.NewRelay(t, support.RelayOptions{
		Profiles: map[string]string{"local": fake.Origin()},
		Selected: "local",
	})

	resp, err := http.Get(relay.URL("/stream"))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)
	var received strings.Builder
	for {
		b, err := reader.ReadByte()
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("read stream: %v", err)
		}
		received.WriteByte(b)
	}
	if received.String() != strings.Join(chunks, "") {
		t.Fatalf("stream = %q", received.String())
	}
}
