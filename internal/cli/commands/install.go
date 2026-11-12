package commands

import (
	"flag"
	"os"

	"shunt/internal/platform"
)

// Install implements `shunt install [--start-at-login] [--start-now]`.
func Install(e *Env, args []string) int {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(e.err())
	startAtLogin := fs.Bool("start-at-login", true, "create the per-user login task")
	startNow := fs.Bool("start-now", false, "start the task immediately")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if !*startAtLogin {
		e.printf("no task created (--start-at-login=false); run shunt serve manually\n")
		return ExitOK
	}
	exe, err := os.Executable()
	if err != nil {
		return e.fail(ExitGeneric, "resolve executable: %v", err)
	}
	manual, err := platform.RunServiceTask("install", exe)
	if err != nil {
		e.printf("startup entry was not created (%s). Run this command manually:\n%s\n", platform.StartupKind(), manual)
		return e.fail(ExitGeneric, "%v", err)
	}
	e.printf("created per-user startup entry %q for %s (%s)\n", platform.ServiceTaskName, exe, platform.StartupKind())
	if *startNow {
		if manual, err := platform.RunServiceTask("run", exe); err != nil {
			e.printf("startup entry was not started. Run this command manually:\n%s\n", manual)
			return e.fail(ExitGeneric, "%v", err)
		}
		e.printf("started startup entry %q\n", platform.ServiceTaskName)
	}
	e.printf("no elevation was requested; configuration, state, caches, and logs are untouched\n")
	return ExitOK
}

// Uninstall implements `shunt uninstall`.
func Uninstall(e *Env, args []string) int {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	fs.SetOutput(e.err())
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	manual, err := platform.RunServiceTask("delete", "")
	if err != nil {
		e.printf("task was not deleted. Run this command manually:\n%s\n", manual)
		return e.fail(ExitGeneric, "%v", err)
	}
	e.printf("deleted per-user startup entry %q\n", platform.ServiceTaskName)
	e.printf("configuration, state, caches, and logs were not deleted\n")
	return ExitOK
}
