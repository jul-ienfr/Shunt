package commands

import (
	"flag"

	"shunt/internal/state"
)

// Mode implements `shunt mode manual|auto [--prefer <profile>]`.
func Mode(e *Env, args []string) int {
	fs := flag.NewFlagSet("mode", flag.ContinueOnError)
	fs.SetOutput(e.err())
	prefer := fs.String("prefer", "", "preferred profile in auto mode")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	mode := fs.Arg(0)
	if mode != state.ModeManual && mode != state.ModeAuto {
		return e.fail(ExitUsage, "usage: shunt mode manual|auto [--prefer <profile>]")
	}
	cfg, _, paths, err := e.load()
	if err != nil {
		return e.fail(ExitUsage, "%v", err)
	}
	current := state.LoadOr(paths.StateFile, cfg.DefaultProfile)
	var intent state.Intent
	if mode == state.ModeManual {
		selected := ""
		if current.Selected != nil {
			selected = *current.Selected
		}
		if p, ok := cfg.Profiles[selected]; !ok || !p.IsEnabled() {
			selected = cfg.DefaultProfile
		}
		intent = state.Select(selected, "cli", "operator")
		intent.Preferred = current.Preferred
	} else {
		preferred := *prefer
		if preferred == "" && current.Preferred != nil {
			preferred = *current.Preferred
		}
		if preferred == "" {
			preferred = cfg.DefaultProfile
		}
		if p, ok := cfg.Profiles[preferred]; !ok || !p.IsEnabled() {
			return e.fail(ExitUsage, "profile %q does not exist or is disabled", preferred)
		}
		intent = state.SetAuto(preferred, current.Selected, "cli", "operator")
	}
	saved, err := state.Save(paths.StateFile, intent)
	if err != nil {
		return e.fail(ExitGeneric, "write state: %v", err)
	}
	if saved.Mode == state.ModeAuto {
		e.printf("mode auto; preferred %s (revision %d)\n", deref(saved.Preferred), saved.Revision)
	} else {
		e.printf("mode manual; selected %s (revision %d)\n", deref(saved.Selected), saved.Revision)
	}
	return ExitOK
}
