package commands

import (
	"flag"
	"fmt"
	"sort"
)

// Status implements `shunt status [--json] [--live]`.
func Status(e *Env, args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(e.err())
	live := fs.Bool("live", false, "require a live relay response")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	cfg, _, paths, err := e.load()
	if err != nil {
		return e.fail(ExitUsage, "%v", err)
	}
	snap, reachable := e.currentSnapshot(cfg, paths)
	if !reachable && *live {
		return e.fail(ExitRelayNotRunning, "relay not running at %s", cfg.PrimaryListener().Listen)
	}
	intent, stateErr := e.readState(paths.StateFile)
	if e.JSON {
		doc := map[string]any{
			"schema": Schema,
			"relay":  "running",
			"status": snap,
			"state":  intent,
		}
		if !reachable {
			doc["relay"] = "not_running"
			if stateErr != nil {
				doc["state_error"] = stateErr.Error()
			}
		}
		if err := e.printJSON(doc); err != nil {
			return e.fail(ExitGeneric, "%v", err)
		}
		return ExitOK
	}
	if !reachable {
		e.printf("relay: not running\n")
		if snap.Listen != "" {
			e.printf("\nlast runtime snapshot:\n")
			printStatus(e, snap)
		}
		if stateErr == nil {
			e.printf("\npersisted intent: mode=%s selected=%s preferred=%s revision=%d\n", intent.Mode, deref(intent.Selected), deref(intent.Preferred), intent.Revision)
		} else {
			e.printf("\npersisted intent: unavailable (%v)\n", stateErr)
		}
		return ExitOK
	}
	printStatus(e, snap)
	return ExitOK
}

func printStatus(e *Env, snap runtimeSnapshot) {
	e.printf("relay: running v%s pid %d listen %s uptime %ds\n", snap.Version, snap.PID, snap.Listen, snap.UptimeS)
	e.printf("mode: %s  selected: %s  preferred: %s  effective: %s  last_good: %s\n",
		snap.Mode, orDash(snap.Selected), orDash(snap.Preferred), snap.Effective, orDash(snap.LastGood))
	e.printf("inflight: %d  requests: %d  errors: %d  switches: %d  bytes_in: %d  bytes_out: %d\n",
		snap.Inflight, snap.RequestsTotal, snap.ErrorsTotal, snap.SwitchesTotal, snap.BytesIn, snap.BytesOut)
	if snap.LastSwitch != nil {
		e.printf("last switch: %s -> %s at %s (reason %s, by %s)\n",
			snap.LastSwitch.From, snap.LastSwitch.To, snap.LastSwitch.At.Format("2006-01-02T15:04:05Z"), snap.LastSwitch.Reason, snap.LastSwitch.By)
	}
	if len(snap.Profiles) == 0 {
		return
	}
	ids := make([]string, 0, len(snap.Profiles))
	for id := range snap.Profiles {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	e.printf("\n%-12s %-12s %-8s %-8s %-8s %s\n", "PROFILE", "HEALTH", "ENABLED", "STATUS", "LATENCY", "LAST ERROR")
	for _, id := range ids {
		h := snap.Profiles[id]
		health := "unknown"
		if h.Healthy != nil {
			if *h.Healthy {
				health = "healthy"
			} else {
				health = "unhealthy"
			}
		}
		status := "-"
		if h.LastStatus != nil {
			status = fmt.Sprintf("%d", *h.LastStatus)
		}
		latency := "-"
		if h.LastProbeMS != nil {
			latency = fmt.Sprintf("%dms", *h.LastProbeMS)
		}
		lastErr := "-"
		if h.LastError != nil {
			lastErr = *h.LastError
		}
		e.printf("%-12s %-12s %-8t %-8s %-8s %s\n", id, health, h.Enabled, status, latency, lastErr)
	}
}

func orDash(v *string) string {
	if v == nil || *v == "" {
		return "-"
	}
	return *v
}
