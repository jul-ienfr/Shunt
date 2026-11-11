package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"shunt/internal/cli/commands"
)

// Version is the binary version. It is intentionally small and stable.
const Version = "0.1.0"

// Execute parses global options, dispatches a subcommand, and returns the
// process exit code.
func Execute(args []string, stdout, stderr io.Writer) int {
	env := &commands.Env{
		Version:   Version,
		Stdout:    stdout,
		Stderr:    stderr,
		Stdin:     os.Stdin,
		LookupEnv: os.LookupEnv,
	}
	var rest []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--config":
			if i+1 >= len(args) {
				return envFail(env, "missing value for --config")
			}
			i++
			env.ConfigPath = args[i]
		case strings.HasPrefix(arg, "--config="):
			env.ConfigPath = strings.TrimPrefix(arg, "--config=")
		case arg == "--no-color":
			env.NoColor = true
		case arg == "--json":
			env.JSON = true
		case arg == "--version" || arg == "-V":
			return commands.PrintVersion(env, nil)
		default:
			rest = append(rest, arg)
		}
	}
	if len(rest) == 0 || rest[0] == "help" || rest[0] == "--help" || rest[0] == "-h" {
		printUsage(env)
		return commands.ExitOK
	}
	command := rest[0]
	rest = rest[1:]
	switch command {
	case "init":
		return commands.Init(env, rest)
	case "serve":
		return commands.Serve(env, rest)
	case "ui":
		return commands.UI(env, rest)
	case "use":
		return commands.Use(env, rest)
	case "switch":
		return commands.Switch(env, rest)
	case "mode":
		return commands.Mode(env, rest)
	case "status":
		return commands.Status(env, rest)
	case "doctor":
		return commands.Doctor(env, rest)
	case "models":
		return commands.Models(env, rest)
	case "config":
		return commands.Config(env, rest)
	case "install":
		return commands.Install(env, rest)
	case "uninstall":
		return commands.Uninstall(env, rest)
	case "version":
		return commands.PrintVersion(env, rest)
	default:
		return envFail(env, "unknown command %q; run shunt --help", command)
	}
}

func envFail(e *commands.Env, format string, args ...any) int {
	fmt.Fprintf(e.Stderr, format+"\n", args...)
	return commands.ExitUsage
}

func printUsage(e *commands.Env) {
	_, _ = io.WriteString(e.Stdout, `Shunt - a stable local relay that hot-switches upstreams

Usage:
  shunt [--config <path>] [--json] [--no-color] <command> [options]

Commands:
  init        Create the default configuration, state, and directories
  serve       Run the relay in the foreground
  ui          Print the GUI URL and optionally open it
  use         Select a profile in manual mode
  switch      switch on|off aliases for configured profiles
  mode        Switch between manual and automatic mode
  status      Show live or last-known relay status
  doctor      Diagnose configuration, bind, reachability, TLS, and auth
  models      Discover models and generate client configuration
  config      path|show|validate|restore
  install     Create a per-user login task
  uninstall   Delete the login task (keeps data)
  version     Print version, schema, Go version, and target

Global options:
  --config <path>   Configuration file (default: platform path or SHUNT_CONFIG)
  --json            Machine-readable output where supported
  --no-color        Disable ANSI color
  --help, -h        Show this help
  --version, -V     Print version information

No credential is accepted as a command-line argument. Tokens are read from
SHUNT_TOKEN, ANTHROPIC_API_KEY, ANTHROPIC_AUTH_TOKEN, or OPENAI_API_KEY.
`)
}
