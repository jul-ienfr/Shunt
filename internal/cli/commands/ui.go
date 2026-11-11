package commands

import (
	"flag"

	"shunt/internal/config"
	"shunt/internal/desktop"
)

// UI implements `shunt ui [--open] [--webview]`.
func UI(e *Env, args []string) int {
	fs := flag.NewFlagSet("ui", flag.ContinueOnError)
	fs.SetOutput(e.err())
	open := fs.Bool("open", false, "open the GUI in the default browser")
	webview := fs.Bool("webview", false, "open the GUI in a chromeless app window")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	cfg, _, _, err := e.load()
	if err != nil {
		return e.fail(ExitUsage, "%v", err)
	}
	snap, ok := e.liveStatus(cfg.Relay.Listen)
	if !ok {
		return e.fail(ExitRelayNotRunning, "relay not running at %s", cfg.Relay.Listen)
	}
	listen := snap.Listen
	if listen == "" {
		listen = cfg.Relay.Listen
	}
	target := config.ListenOrigin(listen) + "/_shunt/ui"
	e.printf("%s\n", target)
	if *webview {
		if err := desktop.OpenWindow(target); err != nil {
			return e.fail(ExitGeneric, "open window: %v", err)
		}
		return ExitOK
	}
	if *open || cfg.UI.OpenBrowser {
		if err := openBrowser(target); err != nil {
			return e.fail(ExitGeneric, "open browser: %v", err)
		}
	}
	return ExitOK
}
