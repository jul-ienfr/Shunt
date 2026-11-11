package commands

import (
	"flag"
	"runtime"
)

// PrintVersion implements `shunt version [--json]`.
func PrintVersion(e *Env, args []string) int {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(e.err())
	jsonOut := fs.Bool("json", e.JSON, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	info := map[string]any{
		"version": e.Version,
		"schema":  Schema,
		"go":      runtime.Version(),
		"target":  runtime.GOOS + "/" + runtime.GOARCH,
	}
	if *jsonOut {
		if err := e.printJSON(info); err != nil {
			return e.fail(ExitGeneric, "%v", err)
		}
		return ExitOK
	}
	e.printf("shunt %s\n", e.Version)
	e.printf("schema: %d\n", Schema)
	e.printf("go: %s\n", runtime.Version())
	e.printf("target: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	return ExitOK
}
