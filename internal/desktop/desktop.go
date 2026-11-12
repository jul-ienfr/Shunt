// Package desktop provides optional Windows desktop integration: a system tray
// icon and a chromeless dashboard window. It uses only the standard library so
// release builds stay CGO-free and keep a single external module.
package desktop

import "log/slog"

// Profile is one switchable upstream shown in the tray menu.
type Profile struct {
	ID    string
	Label string
}

// TrayOptions configures the tray icon and its callbacks. URL, OnOpen, and
// OnQuit are required; the other callbacks are optional.
type TrayOptions struct {
	Title    string
	Logger   *slog.Logger
	URL      func() string
	Status   func() string
	Profiles func() []Profile
	Current  func() string
	OnOpen   func() error
	OnSwitch func(string) error
	OnQuit   func()
}
