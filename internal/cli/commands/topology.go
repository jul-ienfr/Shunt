package commands

import (
	"flag"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"

	"shunt/internal/config"
	"shunt/internal/platform"
)

// Profile implements `shunt profile list|add|remove|set-url`. It manages the
// upstream side of the relay; several profiles can be active at once behind
// different listener ports.
func Profile(e *Env, args []string) int {
	if len(args) == 0 {
		return e.fail(ExitUsage, "usage: shunt profile list|add|remove|set-url ...")
	}
	switch args[0] {
	case "list", "ls":
		return profileList(e, args[1:])
	case "add":
		return profileAdd(e, args[1:])
	case "remove", "rm":
		return profileRemove(e, args[1:])
	case "set-url":
		return profileSetURL(e, args[1:])
	default:
		return e.fail(ExitUsage, "unknown profile action %q; use list, add, remove, or set-url", args[0])
	}
}

// Listener implements `shunt listener list|add|remove|set-listen`.
func Listener(e *Env, args []string) int {
	if len(args) == 0 {
		return e.fail(ExitUsage, "usage: shunt listener list|add|remove|set-listen ...")
	}
	switch args[0] {
	case "list", "ls":
		return listenerList(e, args[1:])
	case "add":
		return listenerAdd(e, args[1:])
	case "remove", "rm":
		return listenerRemove(e, args[1:])
	case "set-listen":
		return listenerSetListen(e, args[1:])
	default:
		return e.fail(ExitUsage, "unknown listener action %q; use list, add, remove, or set-listen", args[0])
	}
}

func profileList(e *Env, args []string) int {
	fs := flag.NewFlagSet("profile list", flag.ContinueOnError)
	fs.SetOutput(e.err())
	jsonOut := fs.Bool("json", e.JSON, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	cfg, _, _, err := e.load()
	if err != nil {
		return e.fail(ExitUsage, "%v", err)
	}
	if *jsonOut {
		type profileDoc struct {
			ID      string   `json:"id"`
			Label   string   `json:"label"`
			BaseURL string   `json:"base_url"`
			Enabled bool     `json:"enabled"`
			Tags    []string `json:"tags,omitempty"`
		}
		docs := make([]profileDoc, 0, len(cfg.Profiles))
		all := make([]string, 0, len(cfg.Profiles))
		for id := range cfg.Profiles {
			all = append(all, id)
		}
		sort.Strings(all)
		for _, id := range all {
			p := cfg.Profiles[id]
			docs = append(docs, profileDoc{ID: id, Label: p.Label, BaseURL: p.BaseURL, Enabled: p.IsEnabled(), Tags: p.Tags})
		}
		return e.encodeJSON(map[string]any{"schema": Schema, "profiles": docs})
	}
	e.printf("%-12s %-8s %-28s %s\n", "PROFILE", "ENABLED", "BASE URL", "LISTENERS")
	for id, p := range cfg.Profiles {
		var pinned []string
		for _, listener := range cfg.Listeners {
			if listener.Profile == id {
				pinned = append(pinned, listener.Name)
			}
		}
		sort.Strings(pinned)
		enabled := "yes"
		if !p.IsEnabled() {
			enabled = "no"
		}
		column := "-"
		if len(pinned) > 0 {
			column = strings.Join(pinned, ",")
		}
		e.printf("%-12s %-8s %-28s %s\n", id, enabled, p.BaseURL, column)
	}
	return ExitOK
}

func profileAdd(e *Env, args []string) int {
	fs := flag.NewFlagSet("profile add", flag.ContinueOnError)
	fs.SetOutput(e.err())
	rawURL := fs.String("url", "", "upstream http(s) origin")
	probeURL := fs.String("probe-url", "", "probe URL (default: origin + /)")
	label := fs.String("label", "", "human-readable label")
	if err := parseInterspersed(fs, args, "url", "probe-url", "label"); err != nil {
		return ExitUsage
	}
	if fs.NArg() != 1 {
		return e.fail(ExitUsage, "usage: shunt profile add <id> --url https://host[:port]")
	}
	id := fs.Arg(0)
	origin, err := originURL(*rawURL)
	if err != nil {
		return e.fail(ExitUsage, "%v", err)
	}
	if *probeURL == "" {
		*probeURL = origin + "/"
	}
	path, err := e.editableConfig()
	if err != nil {
		return e.fail(ExitUsage, "%v", err)
	}
	if err := e.editConfig(path, func(text string) (string, error) {
		return config.AddProfile(text, id, *label, origin, *probeURL)
	}); err != nil {
		return e.fail(ExitUsage, "%v", err)
	}
	e.printf("added profile %q -> %s\n", id, origin)
	e.noteReload()
	return ExitOK
}

func profileRemove(e *Env, args []string) int {
	fs := flag.NewFlagSet("profile remove", flag.ContinueOnError)
	fs.SetOutput(e.err())
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if fs.NArg() != 1 {
		return e.fail(ExitUsage, "usage: shunt profile remove <id>")
	}
	id := fs.Arg(0)
	path, err := e.editableConfig()
	if err != nil {
		return e.fail(ExitUsage, "%v", err)
	}
	if err := e.editConfig(path, func(text string) (string, error) {
		return config.RemoveProfile(text, id)
	}); err != nil {
		return e.fail(ExitUsage, "%v", err)
	}
	e.printf("removed profile %q\n", id)
	e.noteReload()
	return ExitOK
}

func profileSetURL(e *Env, args []string) int {
	fs := flag.NewFlagSet("profile set-url", flag.ContinueOnError)
	fs.SetOutput(e.err())
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if fs.NArg() != 2 {
		return e.fail(ExitUsage, "usage: shunt profile set-url <id> https://host[:port]")
	}
	id := fs.Arg(0)
	origin, err := originURL(fs.Arg(1))
	if err != nil {
		return e.fail(ExitUsage, "%v", err)
	}
	path, err := e.editableConfig()
	if err != nil {
		return e.fail(ExitUsage, "%v", err)
	}
	if err := e.editConfig(path, func(text string) (string, error) {
		cfg, err := config.Parse([]byte(text))
		if err != nil {
			return "", err
		}
		if _, ok := cfg.Profiles[id]; !ok {
			return "", fmt.Errorf("profile %q not found", id)
		}
		return config.SetProfileBaseURL(text, id, origin, origin+"/")
	}); err != nil {
		return e.fail(ExitUsage, "%v", err)
	}
	e.printf("profile %q now points at %s\n", id, origin)
	e.noteReload()
	return ExitOK
}

func listenerList(e *Env, args []string) int {
	fs := flag.NewFlagSet("listener list", flag.ContinueOnError)
	fs.SetOutput(e.err())
	jsonOut := fs.Bool("json", e.JSON, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	cfg, _, _, err := e.load()
	if err != nil {
		return e.fail(ExitUsage, "%v", err)
	}
	listeners := cfg.EffectiveListeners()
	if *jsonOut {
		type listenerDoc struct {
			Name     string `json:"name"`
			Listen   string `json:"listen"`
			Profile  string `json:"profile"`
			Pinned   bool   `json:"pinned"`
			Explicit bool   `json:"explicit"`
		}
		docs := make([]listenerDoc, 0, len(listeners))
		for _, l := range listeners {
			docs = append(docs, listenerDoc{Name: l.Name, Listen: l.Listen, Profile: l.Profile, Pinned: l.Profile != "", Explicit: cfg.ExplicitListeners()})
		}
		return e.encodeJSON(map[string]any{"schema": Schema, "listeners": docs})
	}
	e.printf("%-12s %-24s %s\n", "LISTENER", "LISTEN", "PROFILE")
	for _, l := range listeners {
		profile := l.Profile
		if profile == "" {
			profile = "(global switch)"
		}
		e.printf("%-12s %-24s %s\n", l.Name, l.Listen, profile)
	}
	if !cfg.ExplicitListeners() {
		e.printf("\nusing the legacy single entry from [relay] listen; `shunt listener add` materializes it as \"default\"\n")
	}
	return ExitOK
}

func listenerAdd(e *Env, args []string) int {
	fs := flag.NewFlagSet("listener add", flag.ContinueOnError)
	fs.SetOutput(e.err())
	listen := fs.String("listen", "", "local entry address, for example 127.0.0.1:8788")
	profile := fs.String("profile", "", "pin this port to one profile (default: follow the global switch)")
	if err := parseInterspersed(fs, args, "listen", "profile"); err != nil {
		return ExitUsage
	}
	if fs.NArg() != 1 {
		return e.fail(ExitUsage, "usage: shunt listener add <name> --listen 127.0.0.1:8788 [--profile remote]")
	}
	name := fs.Arg(0)
	addr, err := config.NormalizeListen(*listen)
	if err != nil {
		return e.fail(ExitUsage, "%v", err)
	}
	path, err := e.editableConfig()
	if err != nil {
		return e.fail(ExitUsage, "%v", err)
	}
	if err := e.editConfig(path, func(text string) (string, error) {
		cfg, err := config.Parse([]byte(text))
		if err != nil {
			return "", err
		}
		if !cfg.ExplicitListeners() {
			// Materialize the legacy entry so adding a port never removes the
			// existing one.
			legacy := cfg.Relay.Listen
			if name == "default" {
				return config.AddListener(text, name, addr, *profile)
			}
			text, err = config.AddListener(text, "default", legacy, "")
			if err != nil {
				return "", err
			}
		}
		return config.AddListener(text, name, addr, *profile)
	}); err != nil {
		return e.fail(ExitUsage, "%v", err)
	}
	e.printf("added listener %q on %s\n", name, addr)
	e.noteReload()
	return ExitOK
}

func listenerRemove(e *Env, args []string) int {
	fs := flag.NewFlagSet("listener remove", flag.ContinueOnError)
	fs.SetOutput(e.err())
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if fs.NArg() != 1 {
		return e.fail(ExitUsage, "usage: shunt listener remove <name>")
	}
	name := fs.Arg(0)
	path, err := e.editableConfig()
	if err != nil {
		return e.fail(ExitUsage, "%v", err)
	}
	if err := e.editConfig(path, func(text string) (string, error) {
		cfg, err := config.Parse([]byte(text))
		if err != nil {
			return "", err
		}
		if len(cfg.EffectiveListeners()) <= 1 {
			return "", fmt.Errorf("cannot remove the last listener; edit [relay] listen instead")
		}
		return config.RemoveListener(text, name)
	}); err != nil {
		return e.fail(ExitUsage, "%v", err)
	}
	e.printf("removed listener %q\n", name)
	e.noteReload()
	return ExitOK
}

func listenerSetListen(e *Env, args []string) int {
	fs := flag.NewFlagSet("listener set-listen", flag.ContinueOnError)
	fs.SetOutput(e.err())
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if fs.NArg() != 2 {
		return e.fail(ExitUsage, "usage: shunt listener set-listen <name> 127.0.0.1:8788")
	}
	name := fs.Arg(0)
	addr, err := config.NormalizeListen(fs.Arg(1))
	if err != nil {
		return e.fail(ExitUsage, "%v", err)
	}
	path, err := e.editableConfig()
	if err != nil {
		return e.fail(ExitUsage, "%v", err)
	}
	if err := e.editConfig(path, func(text string) (string, error) {
		cfg, err := config.Parse([]byte(text))
		if err != nil {
			return "", err
		}
		if !cfg.ExplicitListeners() {
			return "", fmt.Errorf("no [[listeners]] table; edit [relay] listen instead")
		}
		return config.SetListenerListen(text, name, addr)
	}); err != nil {
		return e.fail(ExitUsage, "%v", err)
	}
	e.printf("listener %q now listens on %s\n", name, addr)
	e.noteReload()
	return ExitOK
}

// editableConfig returns the configuration path, refusing when the file does
// not exist so the operator runs init first.
func (e *Env) editableConfig() (string, error) {
	path := e.configPath()
	if path == "" {
		return "", fmt.Errorf("no configuration path")
	}
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("read config %s: %w; run shunt init first", path, err)
	}
	return path, nil
}

// editConfig applies an edit, validates the whole result, and only then writes
// it atomically. A rejected edit never truncates the running configuration.
func (e *Env) editConfig(path string, edit func(string) (string, error)) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	out, err := edit(string(data))
	if err != nil {
		return err
	}
	if _, err := config.Parse([]byte(out)); err != nil {
		return fmt.Errorf("edit rejected: %w", err)
	}
	return platform.WriteFileAtomic(path, []byte(out), 0o600)
}

func (e *Env) noteReload() {
	e.printf("a running relay reloads the file within relay.config_poll_ms\n")
}

func (e *Env) encodeJSON(v any) int {
	if err := e.printJSON(v); err != nil {
		return e.fail(ExitGeneric, "encode json: %v", err)
	}
	return ExitOK
}

// parseInterspersed accepts flags before or after positional arguments. The
// standard flag package stops at the first positional, which makes the natural
// `shunt listener add edge --listen ...` fail.
func parseInterspersed(fs *flag.FlagSet, args []string, valueFlags ...string) error {
	takesValue := make(map[string]bool, len(valueFlags))
	for _, name := range valueFlags {
		takesValue[name] = true
	}
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if len(arg) < 2 || !strings.HasPrefix(arg, "-") {
			positional = append(positional, arg)
			continue
		}
		flags = append(flags, arg)
		name := strings.TrimLeft(arg, "-")
		if strings.Contains(name, "=") {
			continue
		}
		if takesValue[name] && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return fs.Parse(append(flags, positional...))
}

// originURL validates an upstream origin and strips trailing slashes.
func originURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("--url is required")
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Host == "" {
		return "", fmt.Errorf("url must be an absolute http(s) origin")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("url scheme must be http or https")
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", fmt.Errorf("url must not contain a path, query, fragment, or credentials")
	}
	return u.Scheme + "://" + u.Host, nil
}
