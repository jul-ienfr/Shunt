package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// RotatingWriter is an append-only log writer with size-based rotation and a
// fixed number of retained backups. It is safe for concurrent use.
type RotatingWriter struct {
	mu       sync.Mutex
	dir      string
	name     string
	maxBytes int64
	maxFiles int

	file *os.File
	size int64
}

// NewRotatingWriter opens (or creates) dir/name and rotates it once it would
// exceed maxBytes. maxFiles is the number of rotated backups to retain.
func NewRotatingWriter(dir, name string, maxBytes int64, maxFiles int) (*RotatingWriter, error) {
	if dir == "" || name == "" {
		return nil, fmt.Errorf("logging: empty log path")
	}
	if maxBytes <= 0 {
		maxBytes = 10 << 20
	}
	if maxFiles < 1 {
		maxFiles = 5
	}
	w := &RotatingWriter{dir: dir, name: name, maxBytes: maxBytes, maxFiles: maxFiles}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *RotatingWriter) path() string { return filepath.Join(w.dir, w.name) }

func (w *RotatingWriter) open() error {
	f, err := os.OpenFile(w.path(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	w.file = f
	w.size = info.Size()
	return nil
}

// Write appends p, rotating first when the file would exceed the limit.
func (w *RotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		if err := w.open(); err != nil {
			return 0, err
		}
	}
	if w.size > 0 && w.size+int64(len(p)) > w.maxBytes {
		if err := w.rotateLocked(); err != nil {
			return 0, err
		}
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *RotatingWriter) rotateLocked() error {
	if w.file != nil {
		if err := w.file.Close(); err != nil {
			w.file = nil
			return err
		}
		w.file = nil
	}
	base := w.path()
	for i := w.maxFiles; i >= 1; i-- {
		old := fmt.Sprintf("%s.%d", base, i)
		if i == w.maxFiles {
			_ = os.Remove(old)
			continue
		}
		_ = os.Rename(old, fmt.Sprintf("%s.%d", base, i+1))
	}
	if err := os.Rename(base, base+".1"); err != nil && !os.IsNotExist(err) {
		return err
	}
	return w.open()
}

// Close closes the current log file.
func (w *RotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}
