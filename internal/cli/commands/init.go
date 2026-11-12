package commands

import (
	"flag"
	"sort"
	"strings"

	"shunt/internal/config"
	"shunt/internal/platform"
	"shunt/internal/state"
)

// Init implements `shunt init [--force] [--local-url URL] [--remote-url URL]`.
func Init(e *Env, args []string) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(e.err())
	force := fs.Bool("force", false, "overwrite an existing configuration")
	localURL := fs.String("local-url", "", "base URL for the local profile")
	remoteURL := fs.String("remote-url", "", "base URL for the remote profile")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	cfg, paths, err := e.defaults()
	if err != nil {
		return e.fail(ExitGeneric, "resolve paths: %v", err)
	}
	urls := config.InitURLs{
		Local:  firstSet(*localURL, e.getenv("SHUNT_LOCAL_URL"), config.DefaultLocalURL),
		Remote: firstSet(*remoteURL, e.getenv("SHUNT_REMOTE_URL"), config.DefaultRemoteURL),
	}
	if err := config.SaveDefaultWith(paths.ConfigFile, *force, urls); err != nil {
		return e.fail(ExitUsage, "%v", err)
	}
	cfg, _, err = config.Load(paths.ConfigFile)
	if err != nil {
		return e.fail(ExitUsage, "%v", err)
	}
	paths, err = config.ResolvePaths(cfg, paths.ConfigFile)
	if err != nil {
		return e.fail(ExitGeneric, "resolve paths: %v", err)
	}
	if err := paths.Ensure(); err != nil {
		return e.fail(ExitGeneric, "create directories: %v", err)
	}
	intent := state.Default(cfg.DefaultProfile)
	if _, err := state.Save(paths.StateFile, intent); err != nil {
		return e.fail(ExitGeneric, "write state: %v", err)
	}
	e.printf("configuration: %s\n", paths.ConfigFile)
	e.printf("state:         %s\n", paths.StateFile)
	e.printf("runtime:       %s\n", paths.RuntimeFile)
	e.printf("cache:         %s\n", paths.CacheDir)
	e.printf("logs:          %s\n", paths.LogDir)
	e.printf("backups:       %s\n", paths.BackupDir)
	e.printf("\ndefault profile: %s\n", cfg.DefaultProfile)
	ids := make([]string, 0, len(cfg.Profiles))
	for id := range cfg.Profiles {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		p := cfg.Profiles[id]
		e.printf("profile %-8s %-20s %s\n", id, p.Label, p.BaseURL)
	}
	if p, ok := cfg.Profiles["remote"]; ok && p.BaseURL == config.DefaultRemoteURL {
		e.printf("\nnote: profile \"remote\" uses the reserved placeholder %s.\n", config.DefaultRemoteURL)
		e.printf("      point it at your real upstream with:\n")
		e.printf("        shunt init --force --remote-url https://<host>.ts.net\n")
		e.printf("      or edit the endpoint live in the GUI.\n")
	}
	return ExitOK
}

// firstSet returns the first non-blank value.
func firstSet(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

var _ = platform.ExpandHome
