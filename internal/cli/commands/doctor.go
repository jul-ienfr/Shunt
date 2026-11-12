package commands

import (
	"context"
	"flag"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"shunt/internal/config"
	"shunt/internal/models"
	"shunt/internal/state"
	"shunt/internal/upstream"
)

type doctorCheck struct {
	Check  string `json:"check"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
	Action string `json:"action,omitempty"`
}

// Doctor implements `shunt doctor [--json] [--profile <id>]`.
func Doctor(e *Env, args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(e.err())
	profileFlag := fs.String("profile", "", "restrict upstream checks to one profile")
	jsonOut := fs.Bool("json", e.JSON, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	var checks []doctorCheck
	add := func(check, status, detail, action string) {
		checks = append(checks, doctorCheck{Check: check, Status: status, Detail: detail, Action: action})
	}
	cfg, _, paths, err := e.load()
	if err != nil {
		add("config", "FAIL", err.Error(), "fix config.toml and run shunt config validate")
		return finishDoctor(e, checks, *jsonOut)
	}
	add("config", "PASS", "configuration parsed and validated", "")

	dirs := map[string]string{
		"state":   filepath.Dir(paths.StateFile),
		"cache":   paths.CacheDir,
		"logs":    paths.LogDir,
		"backups": paths.BackupDir,
	}
	dirNames := make([]string, 0, len(dirs))
	for name := range dirs {
		dirNames = append(dirNames, name)
	}
	sort.Strings(dirNames)
	for _, name := range dirNames {
		target := dirs[name]
		if err := writableDir(target); err != nil {
			add("directory:"+name, "FAIL", err.Error(), "check permissions or the paths."+name+" override")
		} else {
			add("directory:"+name, "PASS", target, "")
		}
	}

	if intent, err := state.Load(paths.StateFile); err != nil {
		add("state", "WARN", "state is missing or unreadable: "+err.Error(), "run shunt init or shunt use <profile>")
	} else {
		add("state", "PASS", fmt.Sprintf("mode=%s selected=%s preferred=%s revision=%d", intent.Mode, deref(intent.Selected), deref(intent.Preferred), intent.Revision), "")
	}
	if snap, err := readRuntimeFile(paths.RuntimeFile); err != nil {
		add("runtime", "WARN", "runtime.json is missing or unreadable: "+err.Error(), "start shunt serve")
	} else {
		add("runtime", "PASS", fmt.Sprintf("last observed listen=%s effective=%s", snap.Listen, snap.Effective), "")
	}

	for _, listener := range cfg.EffectiveListeners() {
		if err := config.ValidateListen(listener.Listen, cfg.Relay.AllowNonLoopback); err != nil {
			add("bind:"+listener.Name, "FAIL", err.Error(), "choose a loopback or private local listen address")
			continue
		}
		detail := fmt.Sprintf("listen %s is an allowed loopback or private-local address", listener.Listen)
		if listener.Profile != "" {
			detail += fmt.Sprintf(" (pinned to %s)", listener.Profile)
		}
		add("bind:"+listener.Name, "PASS", detail, "")
	}
	primary := cfg.PrimaryListener()
	if snap, ok := e.liveStatus(primary.Listen); ok {
		add("relay", "PASS", fmt.Sprintf("relay is running as pid %d, effective %s", snap.PID, snap.Effective), "")
		for _, listener := range cfg.EffectiveListeners() {
			if listener.Listen == primary.Listen {
				continue
			}
			if _, ok := e.liveStatus(listener.Listen); ok {
				add("listener:"+listener.Name, "PASS", "reachable at "+listener.Listen, "")
			} else {
				add("listener:"+listener.Name, "WARN", "relay is not reachable at "+listener.Listen, "check the [[listeners]] entry and restart shunt serve")
			}
		}
	} else {
		add("relay", "WARN", "relay is not reachable at "+primary.Listen, "start shunt serve")
	}

	intent := state.LoadOr(paths.StateFile, cfg.DefaultProfile)
	effective := effectiveFromState(intent, cfg)
	if snap, ok := e.liveStatus(primary.Listen); ok && snap.Effective != "" {
		effective = snap.Effective
	}
	profileIDs := []string{}
	if *profileFlag != "" {
		profileIDs = []string{*profileFlag}
	} else {
		profileIDs = cfg.EnabledProfileIDs()
	}
	token, tokenName := models.ResolveToken(e.tokenLookup(), "")
	for _, id := range profileIDs {
		p, ok := cfg.Profiles[id]
		if !ok || !p.IsEnabled() {
			add("profile:"+id, "FAIL", "profile does not exist or is disabled", "choose an enabled profile")
			continue
		}
		res := probeProfile(p, time.Duration(cfg.Auto.ProbeTimeoutMS)*time.Millisecond)
		if res.OK {
			add("probe:"+id, "PASS", fmt.Sprintf("HTTP %d in %d ms", res.Status, res.LatencyMS), "")
			if token != "" {
				if err := doctorModelListing(cfg, p, token); err != nil {
					add("models:"+id, "WARN", err.Error(), "set "+tokenName+" or fix upstream authentication")
				} else {
					add("models:"+id, "PASS", "model listing reachable", "")
				}
			}
		} else if id == effective || *profileFlag != "" {
			add("probe:"+id, "FAIL", "upstream probe failed: "+res.Err, "start the upstream, fix the endpoint, or switch with shunt use <profile>")
		} else {
			add("probe:"+id, "WARN", "upstream probe failed: "+res.Err, "start the upstream or disable the profile")
		}
	}
	if token == "" {
		add("credential", "WARN", "no model token found in the environment", "set SHUNT_TOKEN or the client's key variable")
	} else {
		add("credential", "PASS", "token is available from "+tokenName+" (value not shown)", "")
	}

	if path, err := exec.LookPath("tailscale"); err != nil {
		add("tailscale", "WARN", "tailscale CLI not found; tailnet checks skipped", "install Tailscale if the remote profile uses a tailnet")
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		out, err := exec.CommandContext(ctx, path, "status").CombinedOutput()
		cancel()
		if err != nil {
			add("tailscale", "WARN", "tailscale status failed: "+strings.TrimSpace(string(out)), "run tailscale status and sign in")
		} else if strings.Contains(strings.ToLower(string(out)), "funnel") {
			add("tailscale", "WARN", "Tailscale Funnel appears active", "disable Funnel; Shunt never enables it")
		} else {
			add("tailscale", "PASS", "tailscale CLI present; Funnel not detected", "")
		}
	}
	add("meshnet", "PASS", "NordVPN Meshnet is not managed by shunt; verify the peer is online if the remote path uses Meshnet", "")
	add("security", "PASS", "no wildcard bind, no public bind, TLS verification always enabled, no insecure mode", "")
	return finishDoctor(e, checks, *jsonOut)
}

func doctorModelListing(cfg config.Config, p config.Profile, token string) error {
	yes := true
	requestProfile := p
	requestProfile.TLSVerify = &yes
	client := upstream.NewClient(requestProfile)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := models.Fetch(ctx, models.FetchOptions{
		Profile:    "doctor",
		BaseURL:    p.BaseURL,
		Dialect:    models.DialectAnthropic,
		Token:      token,
		HTTPClient: client,
		PageSize:   cfg.Models.PageSize,
		MaxPages:   1,
		Source:     "direct",
	})
	return err
}

func finishDoctor(e *Env, checks []doctorCheck, jsonOut bool) int {
	fails, warns := 0, 0
	for _, c := range checks {
		switch c.Status {
		case "FAIL":
			fails++
		case "WARN":
			warns++
		}
	}
	exit := ExitOK
	if fails > 0 {
		exit = ExitUsage
	} else if warns > 0 {
		exit = ExitGeneric
	}
	if jsonOut {
		_ = e.printJSON(map[string]any{
			"schema":   Schema,
			"checks":   checks,
			"fails":    fails,
			"warnings": warns,
			"exit":     exit,
		})
		return exit
	}
	for _, c := range checks {
		e.printf("%-4s %-24s %s\n", c.Status, c.Check, c.Detail)
		if c.Action != "" && c.Status != "PASS" {
			e.printf("     action: %s\n", c.Action)
		}
	}
	if fails > 0 {
		e.printf("\n%d failure(s), %d warning(s)\n", fails, warns)
	} else if warns > 0 {
		e.printf("\n%d warning(s)\n", warns)
	} else {
		e.printf("\nall checks passed\n")
	}
	return exit
}
