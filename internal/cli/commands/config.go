package commands

import (
	"encoding/json"
	"flag"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"

	"shunt/internal/clients"
)

// Config implements `shunt config path|show|validate|restore`.
func Config(e *Env, args []string) int {
	if len(args) == 0 {
		return e.fail(ExitUsage, "usage: shunt config path|show|validate|restore <backup>")
	}
	switch args[0] {
	case "path":
		cfg, actual, paths, err := e.load()
		if err != nil {
			return e.fail(ExitUsage, "%v", err)
		}
		_ = cfg
		e.printf("config:  %s\n", actual)
		e.printf("state:   %s\n", paths.StateFile)
		e.printf("runtime: %s\n", paths.RuntimeFile)
		e.printf("cache:   %s\n", paths.CacheDir)
		e.printf("logs:    %s\n", paths.LogDir)
		e.printf("backups: %s\n", paths.BackupDir)
		return ExitOK
	case "show":
		cfg, _, _, err := e.load()
		if err != nil {
			return e.fail(ExitUsage, "%v", err)
		}
		if e.JSON {
			data, err := json.MarshalIndent(cfg, "", "  ")
			if err != nil {
				return e.fail(ExitGeneric, "%v", err)
			}
			e.printf("%s\n", data)
			return ExitOK
		}
		data, err := toml.Marshal(cfg)
		if err != nil {
			return e.fail(ExitGeneric, "%v", err)
		}
		e.printf("%s", data)
		return ExitOK
	case "validate":
		_, actual, _, err := e.load()
		if err != nil {
			return e.fail(ExitUsage, "%v", err)
		}
		e.printf("configuration valid: %s\n", actual)
		return ExitOK
	case "restore":
		return configRestore(e, args[1:])
	default:
		return e.fail(ExitUsage, "unknown config subcommand %q", args[0])
	}
}

func configRestore(e *Env, args []string) int {
	fs := flag.NewFlagSet("config restore", flag.ContinueOnError)
	fs.SetOutput(e.err())
	client := fs.String("client", "", "claude or codex")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	backupName := fs.Arg(0)
	if backupName == "" {
		return e.fail(ExitUsage, "usage: shunt config restore <backup>")
	}
	cfg, _, paths, err := e.load()
	if err != nil {
		return e.fail(ExitUsage, "%v", err)
	}
	which := *client
	if which == "" {
		which = backupClientFromName(backupName)
	}
	if which == "" {
		return e.fail(ExitUsage, "cannot infer the target client from %q; pass --client claude|codex", backupName)
	}
	var target string
	switch which {
	case "claude":
		target, err = expandPath(cfg.Clients.Claude.SettingsPath)
	case "codex":
		target, err = expandPath(cfg.Clients.Codex.ConfigPath)
	case "dsh":
		return e.fail(ExitWriteRefused, "DSH configuration is not written by shunt in v1")
	default:
		return e.fail(ExitUsage, "unknown client %q", which)
	}
	if err != nil {
		return e.fail(ExitUsage, "%v", err)
	}
	if target == "" {
		return e.fail(ExitUsage, "no configured path for client %q", which)
	}
	name := backupName
	if !filepath.IsAbs(name) {
		name = filepath.Base(name)
	}
	safety, err := clients.Restore(paths.BackupDir, which, name, target)
	if err != nil {
		return e.fail(ExitWriteRefused, "%v", err)
	}
	e.printf("restored %s from backup %s\n", target, name)
	if safety != "" {
		e.printf("safety backup: %s\n", safety)
	}
	return ExitOK
}
