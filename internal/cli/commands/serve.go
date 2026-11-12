package commands

import (
	"context"
	"errors"
	"flag"
	"io"
	iofs "io/fs"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"shunt/internal/config"
	"shunt/internal/desktop"
	"shunt/internal/logging"
	"shunt/internal/relay"
	"shunt/internal/ui"
)

// Serve implements `shunt serve [--foreground] [--no-ui]`.
func Serve(e *Env, args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(e.err())
	_ = fs.Bool("foreground", false, "run in the foreground (the only mode)")
	noUI := fs.Bool("no-ui", false, "disable the embedded GUI for this run")
	noTray := fs.Bool("no-tray", false, "disable the system tray icon")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	cfg, actual, paths, err := e.load()
	if err != nil {
		if errors.Is(err, iofs.ErrNotExist) {
			return e.fail(ExitUsage, "%v\nrun `shunt init` to create a configuration, or pass --config <path>", err)
		}
		return e.fail(ExitUsage, "%v", err)
	}
	if err := paths.Ensure(); err != nil {
		return e.fail(ExitGeneric, "create directories: %v", err)
	}
	for _, listener := range cfg.EffectiveListeners() {
		if _, err := config.NormalizeListen(listener.Listen); err != nil {
			return e.fail(ExitUsage, "listener %s: %v", listener.Name, err)
		}
	}
	logWriter := e.err()
	rotating, rotErr := logging.NewRotatingWriter(paths.LogDir, "shunt.log", 10<<20, 5)
	if rotErr != nil {
		e.printf("warning: file logging disabled: %v\n", rotErr)
	} else {
		logWriter = io.MultiWriter(e.err(), rotating)
		defer rotating.Close()
	}
	logger := logging.New(logging.Options{Level: cfg.Relay.LogLevel, Format: cfg.Relay.LogFormat, Writer: logWriter})
	srv, err := relay.NewServer(cfg, actual, paths.StateFile, paths.RuntimeFile, e.Version, logger)
	if err != nil {
		return e.fail(ExitUsage, "%v", err)
	}
	if !*noUI && cfg.UI.Enabled {
		handler, err := ui.New(srv, int64(cfg.UI.MaxRequestBytes), logger)
		if err != nil {
			return e.fail(ExitGeneric, "initialize GUI: %v", err)
		}
		srv.SetUI(handler)
	}
	selected := srv.Selector().Load().Profile
	for _, listener := range cfg.EffectiveListeners() {
		effective := selected
		if listener.Profile != "" {
			effective = listener.Profile + ", pinned"
		}
		e.printf("shunt %s listening on %s (listener %s, profile %s)\n", e.Version, config.ListenOrigin(listener.Listen), listener.Name, effective)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if !*noTray && !*noUI && cfg.UI.Enabled {
		trayCtx, cancelTray := context.WithCancel(ctx)
		trayDone := make(chan struct{})
		defer func() {
			cancelTray()
			select {
			case <-trayDone:
			case <-time.After(3 * time.Second):
			}
		}()
		dashboardURL := func() string {
			listen := srv.Snapshot().Listen
			if listen == "" {
				listen = srv.Config().PrimaryListener().Listen
			}
			return config.ListenOrigin(listen) + "/_shunt/ui"
		}
		opts := desktop.TrayOptions{
			Title:  "Shunt " + e.Version,
			Logger: logger,
			URL:    dashboardURL,
			Status: func() string {
				sel := srv.Selector().Load()
				mode := sel.Mode
				if mode == "" {
					mode = "manual"
				}
				return sel.Profile + " (" + mode + ")"
			},
			Profiles: func() []desktop.Profile {
				cfg := srv.Config()
				ids := make([]string, 0, len(cfg.Profiles))
				for id, p := range cfg.Profiles {
					if p.IsEnabled() {
						ids = append(ids, id)
					}
				}
				sort.Strings(ids)
				profiles := make([]desktop.Profile, 0, len(ids))
				for _, id := range ids {
					label := cfg.Profiles[id].Label
					if label == "" {
						label = id
					}
					profiles = append(profiles, desktop.Profile{ID: id, Label: label})
				}
				return profiles
			},
			Current: func() string { return srv.Selector().Load().Profile },
			OnOpen:  func() error { return desktop.OpenWindow(dashboardURL()) },
			OnSwitch: func(id string) error {
				_, err := srv.SwitchProfile(id, false, false, "tray", "tray")
				return err
			},
			OnQuit: stop,
		}
		go func() {
			defer close(trayDone)
			if err := desktop.RunTray(trayCtx, opts); err != nil {
				logger.Warn("tray unavailable", "error", err)
			}
		}()
	}
	if err := srv.Run(ctx); err != nil {
		if isAddrInUse(err) {
			return e.fail(ExitAlreadyRunning, "cannot start relay: %v", err)
		}
		return e.fail(ExitGeneric, "relay stopped: %v", err)
	}
	return ExitOK
}

func isAddrInUse(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "address already in use") ||
		strings.Contains(msg, "only one usage of each socket address") ||
		strings.Contains(msg, "eaddrinuse")
}
