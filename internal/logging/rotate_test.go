package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestRotatingWriterBoundsAndRetainsFiles(t *testing.T) {
	dir := t.TempDir()
	w, err := NewRotatingWriter(dir, "shunt.log", 64, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	for i := 0; i < 12; i++ {
		if _, err := fmt.Fprintf(w, "line-%02d-padding-padding\n", i); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	current, err := os.ReadFile(filepath.Join(dir, "shunt.log"))
	if err != nil {
		t.Fatal(err)
	}
	if len(current) > 64 {
		t.Fatalf("current log size = %d, want <= 64", len(current))
	}
	if !strings.Contains(string(current), "line-11") {
		t.Fatalf("current log is missing the newest line:\n%s", current)
	}
	for _, name := range []string{"shunt.log.1", "shunt.log.2"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("missing rotated file %s: %v", name, err)
		}
		if len(data) == 0 || len(data) > 64 {
			t.Fatalf("%s size = %d", name, len(data))
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "shunt.log.3")); !os.IsNotExist(err) {
		t.Fatalf("retention kept shunt.log.3: %v", err)
	}
}

func TestRotatingWriterConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	w, err := NewRotatingWriter(dir, "shunt.log", 256, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, _ = fmt.Fprintf(w, "goroutine=%d line=%d padding\n", id, j)
			}
		}(i)
	}
	wg.Wait()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no log files were written")
	}
}
