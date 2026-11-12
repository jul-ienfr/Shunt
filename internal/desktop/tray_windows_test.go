//go:build windows

package desktop

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestTrayStartsAndStops is gated because it briefly adds a real tray icon.
func TestTrayStartsAndStops(t *testing.T) {
	if os.Getenv("SHUNT_TRAY_TEST") != "1" {
		t.Skip("set SHUNT_TRAY_TEST=1 to exercise the Windows tray")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- RunTray(ctx, TrayOptions{
			Title:    "shunt test",
			URL:      func() string { return "http://127.0.0.1:1/_shunt/ui" },
			Status:   func() string { return "local (manual)" },
			Profiles: func() []Profile { return []Profile{{ID: "local", Label: "Local"}} },
			Current:  func() string { return "local" },
			OnOpen:   func() error { return nil },
			OnSwitch: func(string) error { return nil },
			OnQuit:   cancel,
		})
	}()
	time.Sleep(1500 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("tray: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("tray did not stop after cancellation")
	}
}
