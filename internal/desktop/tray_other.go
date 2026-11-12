//go:build !windows

package desktop

import "context"

// RunTray is a no-op outside Windows. It blocks until the context is canceled
// so callers can keep the same lifecycle on every platform.
func RunTray(ctx context.Context, _ TrayOptions) error {
	<-ctx.Done()
	return nil
}
