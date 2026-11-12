package util

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync/atomic"
	"time"
)

var requestCounter atomic.Uint64

// NewRequestID returns an identifier of the form shunt-<epoch_ms>-<counter>.
// It is unique within a process and uses random bytes when available to make
// collisions across processes unlikely.
func NewRequestID() string {
	n := requestCounter.Add(1)
	var b [6]byte
	if _, err := rand.Read(b[:]); err == nil {
		return fmt.Sprintf("shunt-%d-%d-%s", time.Now().UnixMilli(), n, hex.EncodeToString(b[:]))
	}
	return fmt.Sprintf("shunt-%d-%d", time.Now().UnixMilli(), n)
}

// HumanBytes formats a byte count with binary units.
func HumanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// Millis converts a duration to whole milliseconds.
func Millis(d time.Duration) int64 {
	return d.Milliseconds()
}
