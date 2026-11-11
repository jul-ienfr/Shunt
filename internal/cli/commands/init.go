package commands

import (
	"flag"
	"sort"

	"shunt/internal/config"
	"shunt/internal/platform"
	"shunt/internal/state"
)

// Init implements `shunt init [--force]`.
func Init(e *Env, args []string) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(e.err())
	force := fs.Bool("force", false, "overwrite an existing configuration")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	cfg, paths, err := e.defaults()
	if err != nil {
		return e.fail(ExitGeneric, "resolve paths: %v", err)
	}
	if err := config.SaveDefault(paths.ConfigFile, *force); err != nil {
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
	return ExitOK
}

var _ = platform.ExpandHome
