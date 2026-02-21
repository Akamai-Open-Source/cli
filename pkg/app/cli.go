// Package app bootstraps each Akamai CLI binary with shared configuration,
// global flags, proxy handling, daemon mode, and branded help templates.
//
// The global flags defined in this package (--edgerc, --section, --accountkey)
// form the credential portion of the Akamai plugin contract. They are
// propagated to every plugin subcommand by prepareCommand in
// pkg/commands/command_subcommand.go, ensuring that credential context is
// always available to plugins without each plugin re-implementing flag
// parsing.
//
// Environment variable bindings:
//   - AKAMAI_EDGERC           → --edgerc flag
//   - AKAMAI_EDGERC_SECTION   → --section flag
//   - AKAMAI_EDGERC_ACCOUNT_KEY → --accountkey flag
//   - AKAMAI_CLI_DAEMON       → --daemon flag (hidden)
//
// See docs/plugin-contract.md for the full plugin contract specification.
package app

import (
	"context"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	"github.com/akamai/cli/v2/pkg/apphelp"
	"github.com/akamai/cli/v2/pkg/autocomplete"
	"github.com/akamai/cli/v2/pkg/terminal"
	"github.com/akamai/cli/v2/pkg/tools"
	"github.com/akamai/cli/v2/pkg/version"

	"github.com/kardianos/osext"
	"github.com/mitchellh/go-homedir"
	"github.com/urfave/cli/v2"
)

// sleep24HDuration is the interval used by daemon mode to keep the CLI
// process alive indefinitely via time.Sleep. When the --daemon flag is set,
// the Before hook enters an infinite loop sleeping for this duration.
const sleep24HDuration = time.Hour * 24

// CreateApp creates the root Akamai CLI application with all global flags,
// proxy handling, daemon mode, and shell autocomplete support.
//
// In addition to the credential flags inherited from createAppTemplate
// (--edgerc, --section, --accountkey), CreateApp registers:
//   - --bash and --zsh: emit shell completion scripts to stdout
//   - --proxy: set HTTP_PROXY / HTTPS_PROXY for the process
//   - --daemon (hidden, env: AKAMAI_CLI_DAEMON): enter an infinite sleep
//     loop to keep the process alive (useful for Docker containers)
//
// The app.Before hook normalises the --proxy value by prepending "http://"
// when no scheme is present and propagates it to both HTTP_PROXY and
// HTTPS_PROXY environment variables. If --daemon is set, it enters the
// infinite sleep loop instead of running any command.
//
// The app.Action delegates to defaultAction, which outputs the requested
// shell completion script (--bash / --zsh) or, if neither is set, displays
// the application help and exits with code 0.
//
// Credential flags (--edgerc, --section, --accountkey) are always present
// because createAppTemplate adds them unconditionally. They are propagated
// to plugin subcommands by prepareCommand in
// pkg/commands/command_subcommand.go.
//
// Parameters:
//   - ctx: application context carrying terminal and logging configuration.
//
// Returns a fully configured *cli.App ready for command registration and
// execution.
func CreateApp(ctx context.Context) *cli.App {
	app := createAppTemplate(ctx, "", "Akamai CLI", "", version.Version, false)
	app.Flags = append(app.Flags,
		&cli.BoolFlag{
			Name:  "bash",
			Usage: "Output bash auto-complete",
		},
		&cli.BoolFlag{
			Name:  "zsh",
			Usage: "Output zsh auto-complete",
		},
		&cli.StringFlag{
			Name:  "proxy",
			Usage: "Set a proxy to use",
		},
		&cli.BoolFlag{
			Name:    "daemon",
			Usage:   "Keep Akamai CLI running in the background, particularly useful for Docker containers",
			Hidden:  true,
			EnvVars: []string{"AKAMAI_CLI_DAEMON"},
		},
	)

	app.Action = func(c *cli.Context) error {
		return defaultAction(c)
	}

	app.Before = func(c *cli.Context) error {
		if c.IsSet("proxy") {
			proxy := c.String("proxy")
			if !strings.HasPrefix(proxy, "http://") && !strings.HasPrefix(proxy, "https://") {
				proxy = fmt.Sprintf("http://%s", proxy)
			}
			if err := os.Setenv("HTTP_PROXY", proxy); err != nil {
				return err
			}
			if err := os.Setenv("HTTPS_PROXY", proxy); err != nil {
				return err
			}
		}

		if c.IsSet("daemon") {
			for {
				time.Sleep(sleep24HDuration)
			}
		}
		return nil
	}

	return app
}

// CreateAppTemplate creates a CLI app template intended for individual Akamai
// plugin binaries (e.g., akamai-<commandName>). It delegates to
// createAppTemplate with useDefaults=true, so the returned app ships with
// a default .edgerc path (~/.edgerc) and section ("default").
//
// When the AKAMAI_CLI environment variable is set (i.e., the binary is
// invoked as a plugin inside the Akamai CLI), the app name becomes
// "akamai <commandName>"; otherwise it is "akamai-<commandName>".
//
// The returned app includes the credential flags (--edgerc, --section,
// --accountkey) with their environment variable bindings. These flags are
// part of the plugin contract documented in docs/plugin-contract.md.
//
// Parameters:
//   - ctx: application context carrying terminal and logging configuration.
//   - commandName: plugin name used for app naming and help text.
//   - usage: short one-line usage description.
//   - description: longer description shown in help output.
//   - version: plugin version string displayed by --version.
//
// Returns a configured *cli.App ready for the plugin to add its own
// commands and run.
func CreateAppTemplate(ctx context.Context, commandName, usage, description, version string) *cli.App {
	return createAppTemplate(ctx, commandName, usage, description, version, true)
}

// createAppTemplate is the internal factory that builds a *cli.App with the
// shared Akamai CLI configuration used by both the root CLI binary and
// individual plugin binaries.
//
// The useDefaults parameter controls credential flag defaults: when true the
// user's home directory is resolved to set the default .edgerc path
// (~/.edgerc) and section ("default"); when false the credential flag values
// start empty so that the root CLI does not override user-supplied values.
//
// App naming logic:
//   - commandName == "": name is "akamai" (root CLI binary).
//   - commandName != "" and AKAMAI_CLI env NOT set: "akamai-<commandName>"
//     (standalone plugin binary).
//   - commandName != "" and AKAMAI_CLI env IS set: "akamai <commandName>"
//     (plugin running inside the CLI).
//
// Credential flags registered here are part of the plugin contract:
//   - --edgerc  (alias -e, env: AKAMAI_EDGERC)
//   - --section (alias -s, env: AKAMAI_EDGERC_SECTION)
//   - --accountkey (alias --account-key, env: AKAMAI_EDGERC_ACCOUNT_KEY)
//
// These flags are always passed to plugin subcommands by prepareCommand in
// pkg/commands/command_subcommand.go — this is a core part of the plugin
// contract.
//
// Global urfave/cli flags (VersionFlag, BashCompletionFlag, HelpFlag) are
// overridden to match Akamai naming conventions and visibility requirements.
// Finally, apphelp.Setup wires the branded help templates.
//
// This function's signature MUST NOT change; it is an internal contract.
func createAppTemplate(ctx context.Context, commandName, usage, description, version string, useDefaults bool) *cli.App {
	// Check the AKAMAI_CLI sentinel to determine whether this binary is
	// running as a plugin inside the Akamai CLI. The sentinel affects app
	// naming: plugins show "akamai <cmd>" instead of "akamai-<cmd>".
	_, inCli := os.LookupEnv("AKAMAI_CLI")
	term := terminal.Get(ctx)

	appName := "akamai"
	if commandName != "" {
		appName = "akamai-" + commandName
		if inCli {
			appName = "akamai " + commandName
		}
	}

	app := cli.NewApp()
	app.Name = appName
	app.HelpName = appName
	app.Usage = usage
	app.Description = description
	app.Version = version

	app.Copyright = "Copyright (C) Akamai Technologies, Inc"
	app.Writer = term
	app.ErrWriter = term.Error()
	app.EnableBashCompletion = true
	app.BashComplete = autocomplete.Default

	// Credential defaults are conditional: standalone plugin binaries
	// (useDefaults=true) need sensible defaults so that users can run them
	// without explicit --edgerc/--section; the root CLI (useDefaults=false)
	// leaves them empty to avoid overriding user-supplied configuration.
	var edgercpath, section string
	if useDefaults {
		edgercpath, _ = homedir.Dir()
		edgercpath = path.Join(edgercpath, ".edgerc")

		section = "default"
	}

	// Plugin contract: credential flags. These three flags are present on
	// every Akamai CLI app and are propagated to plugin subcommands by
	// prepareCommand (pkg/commands/command_subcommand.go). Changing their
	// names or semantics would break the plugin contract.
	app.Flags = []cli.Flag{
		&cli.StringFlag{
			Name:    "edgerc",
			Aliases: []string{"e"},
			Usage:   "Location of the credentials file",
			Value:   edgercpath,
			EnvVars: []string{"AKAMAI_EDGERC"},
		},
		&cli.StringFlag{
			Name:    "section",
			Aliases: []string{"s"},
			Usage:   "Section of the credentials file",
			Value:   section,
			EnvVars: []string{"AKAMAI_EDGERC_SECTION"},
		},
		&cli.StringFlag{
			Name:    "accountkey",
			Aliases: []string{"account-key"},
			Usage:   "Account switch key",
			EnvVars: []string{"AKAMAI_EDGERC_ACCOUNT_KEY"},
		},
	}

	// Override urfave/cli's built-in global flags to match Akamai naming
	// conventions and visibility requirements. BashCompletionFlag is hidden
	// because it is an internal mechanism used by shell completion scripts,
	// not a user-facing flag.
	cli.VersionFlag = &cli.BoolFlag{
		Name:  "version",
		Usage: "Output CLI version",
	}
	cli.BashCompletionFlag = &cli.BoolFlag{
		Name:   "generate-bash-completion",
		Hidden: true,
	}
	cli.HelpFlag = &cli.BoolFlag{
		Name:  "help",
		Usage: "show help",
	}

	apphelp.Setup(app)

	return app
}

// defaultAction handles the root CLI invocation when no subcommand is
// specified. If the --bash flag is set it emits a bash completion script; if
// --zsh is set it emits a zsh completion script. Both scripts reference the
// current executable path resolved via osext.Executable with a tools.Self
// fallback. When neither flag is set, the application help is displayed and
// the process exits with code 0.
func defaultAction(c *cli.Context) error {
	cmd, err := osext.Executable()
	if err != nil {
		cmd = tools.Self()
	}

	zshScript := `set -k
# To enable zsh auto-completion, run: eval "$(` + cmd + ` --zsh)"
# We recommend adding this to your .zshrc file
autoload -U compinit && compinit
autoload -U bashcompinit && bashcompinit`

	bashComments := `# To enable bash auto-completion, run: eval "$(` + cmd + ` --bash)"
# We recommend adding this to your .bashrc or .bash_profile file`

	bashScript := `_akamai_cli_bash_autocomplete() {
	local cur opts base
	COMPREPLY=()
	cur="${COMP_WORDS[COMP_CWORD]}"
	opts=$( ${COMP_WORDS[@]:0:$COMP_CWORD} --generate-bash-completion )
	COMPREPLY=( $(compgen -W "${opts}" -- ${cur}) )
	return 0
}

complete -F _akamai_cli_bash_autocomplete ` + tools.Self()

	term := terminal.Get(c.Context)

	if c.Bool("bash") {
		if _, err = term.Writeln(bashComments); err != nil {
			return err
		}
		if _, err = term.Writeln(bashScript); err != nil {
			return err
		}
		return nil
	}

	if c.Bool("zsh") {
		if _, err = term.Writeln(zshScript); err != nil {
			return err
		}
		if _, err = term.Writeln(bashScript); err != nil {
			return err
		}
		return nil
	}

	cli.ShowAppHelpAndExit(c, 0)
	return nil
}
