package commands

import (
	"context"
	"flag"
	"net/http"
	"time"

	"shunt/internal/config"
	"shunt/internal/state"
	"shunt/internal/upstream"
)

// Use implements `shunt use <profile> [--force] [--no-probe]`.
func Use(e *Env, args []string) int {
	fs := flag.NewFlagSet("use", flag.ContinueOnError)
	fs.SetOutput(e.err())
	force := fs.Bool("force", false, "switch even when the probe fails")
	noProbe := fs.Bool("no-probe", false, "do not probe before switching")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	profile := fs.Arg(0)
	if profile == "" {
		return e.fail(ExitUsage, "usage: shunt use <profile> [--force] [--no-probe]")
	}
	return useProfile(e, profile, *force, *noProbe)
}

// Switch implements `shunt switch on|off [--force]`.
func Switch(e *Env, args []string) int {
	fs := flag.NewFlagSet("switch", flag.ContinueOnError)
	fs.SetOutput(e.err())
	force := fs.Bool("force", false, "switch even when the probe fails")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	which := fs.Arg(0)
	cfg, _, _, err := e.load()
	if err != nil {
		return e.fail(ExitUsage, "%v", err)
	}
	var profile string
	switch which {
	case "on":
		profile = cfg.Switch.OnProfile
	case "off":
		profile = cfg.Switch.OffProfile
	default:
		return e.fail(ExitUsage, "usage: shunt switch on|off [--force]")
	}
	return useProfile(e, profile, *force, false)
}

func useProfile(e *Env, profile string, force, noProbe bool) int {
	cfg, _, paths, err := e.load()
	if err != nil {
		return e.fail(ExitUsage, "%v", err)
	}
	p, ok := cfg.Profiles[profile]
	if !ok {
		return e.fail(ExitUsage, "profile %q does not exist", profile)
	}
	if !p.IsEnabled() {
		return e.fail(ExitUsage, "profile %q is disabled", profile)
	}
	if !noProbe {
		res := probeProfile(p, time.Duration(cfg.Auto.ProbeTimeoutMS)*time.Millisecond)
		if !res.OK {
			if !force {
				return e.fail(ExitUpstream, "probe of %q failed: %s (use --force to switch anyway)", profile, res.Err)
			}
			e.printf("warning: probe of %q failed: %s (forced)\n", profile, res.Err)
		}
	}
	saved, err := state.Save(paths.StateFile, state.Select(profile, "cli", "operator"))
	if err != nil {
		return e.fail(ExitGeneric, "write state: %v", err)
	}
	e.printf("selected %s in manual mode (revision %d)\n", profile, saved.Revision)
	if snap, reachable := e.liveStatus(cfg.Relay.Listen); reachable {
		if final, ok := e.waitEffective(cfg.Relay.Listen, profile, time.Second); ok {
			e.printf("relay effective profile: %s\n", final.Effective)
			return ExitOK
		}
		e.printf("warning: relay still reports effective profile %q; observed state: mode=%s selected=%s\n", snap.Effective, snap.Mode, deref(snap.Selected))
		return ExitOK
	}
	e.printf("relay is not running; the selection will apply on the next start\n")
	return ExitOK
}

func probeProfile(p config.Profile, timeout time.Duration) upstream.Result {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	tr := upstream.NewTransport(p)
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr}
	return upstream.Probe(context.Background(), client, p, timeout)
}

func deref(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
