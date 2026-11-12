package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain doubles as a fake tailscale executable. installFakeTailscale copies
// the test binary under a tailscale name so doctor sees a deterministic CLI.
func TestMain(m *testing.M) {
	base := strings.ToLower(filepath.Base(os.Args[0]))
	if strings.HasPrefix(base, "tailscale") {
		fmt.Print(os.Getenv("SHUNT_FAKE_TAILSCALE_OUTPUT"))
		os.Exit(0)
	}
	os.Exit(m.Run())
}
