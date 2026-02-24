// Copyright 2018. Akamai Technologies, Inc
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package apphelp

import (
	"os"

	"github.com/urfave/cli/v2"
)

// cmdHelp routes help requests for the Akamai CLI, handling built-in commands and
// plugin (external) commands through different rendering paths.
//
// Routing logic:
//   - No arguments: delegates to cli.ShowAppHelp to display the app-level help
//     showing all available commands. Users should first configure their .edgerc
//     credentials file, then use "akamai help <command>" for command-specific guidance.
//   - With a command name: resolves the command object; falls back to app help if the
//     command is unknown.
//   - Subcommand or command with subcommands: rewrites os.Args to append "--help" and
//     re-runs the app so urfave/cli renders nested help.
//   - Built-in command: appends cli.HelpFlag if needed and calls cli.ShowCommandHelp.
//   - Plugin (external) command: rewrites os.Args to include "help" token and invokes
//     via c.App.RunContext so the plugin's own help handler runs.
//
// Built-in and plugin commands are routed differently because built-in commands use
// urfave/cli's native help rendering, while plugin commands must delegate to the
// plugin's own help handler — the CLI registers plugin commands with SkipFlagParsing
// set to true, so "--help" would be passed as a raw argument rather than interpreted.
//
// See docs/plugin-contract.md for the full plugin execution model.
func cmdHelp(c *cli.Context) error {
	// Branch on whether the user specified a command name. If args are present,
	// route to command-specific help; otherwise show the app-level overview.
	if c.Args().Present() {
		cmdName := c.Args().First()
		cmd := c.App.Command(cmdName)
		if cmd == nil {
			return cli.ShowAppHelp(c)
		}

		// When a second argument exists or the command has subcommands, rewrite os.Args
		// to delegate to urfave/cli's nested help rendering via "--help" flag injection.
		if subCmd := c.Args().Get(1); subCmd != "" || len(cmd.Subcommands) > 0 {
			os.Args = append([]string{os.Args[0], cmdName}, c.Args().Tail()...)
			os.Args = append(os.Args, "--help")
			return c.App.Run(os.Args)
		}

		// Fork between built-in and plugin help rendering. Built-in commands get
		// direct urfave/cli help output; plugins are re-dispatched so their own
		// help handler runs (since plugins use SkipFlagParsing).
		if isBuiltinCommand(c, cmdName) {
			if shouldAddHelpFlag(cmd) {
				cmd.Flags = append(cmd.Flags, cli.HelpFlag)
			}
			return cli.ShowCommandHelp(c, cmdName)
		}

		// For plugin commands, inject "help" as a positional token rather than "--help"
		// because plugins may implement their own help subcommand handler and the CLI
		// uses SkipFlagParsing for plugins, so flags are not parsed by urfave/cli.
		os.Args = append([]string{os.Args[0], cmdName, "help"}, c.Args().Tail()...)
		return c.App.RunContext(c.Context, os.Args)
	}

	return cli.ShowAppHelp(c)
}

// hasHelpFlag checks whether a command's flag list already includes a flag named "help".
// This guard prevents duplicate help flag registration, which would cause a panic in
// urfave/cli when the flag set is parsed.
func hasHelpFlag(cmd *cli.Command) bool {
	for _, f := range cmd.Flags {
		if f.Names()[0] == "help" {
			return true
		}
	}
	return false
}

// shouldAddHelpFlag determines whether cli.HelpFlag should be appended to a built-in
// command's flags before rendering help. urfave/cli does not auto-attach the help flag
// when a custom help command is registered (see Setup in help.go), so this function
// ensures built-in commands still show "--help" in their help output.
//
// Returns true only when help is not hidden for the command, a global HelpFlag exists,
// and the command does not already have a help flag.
func shouldAddHelpFlag(cmd *cli.Command) bool {
	if !cmd.HideHelp && cli.HelpFlag != nil && !hasHelpFlag(cmd) {
		return true
	}
	return false
}

// isBuiltinCommand distinguishes built-in CLI commands (install, update, uninstall,
// list, search, config, etc.) from installed plugin commands. Built-in commands have
// an empty Category field, while plugin commands are registered with a non-empty
// category by createInstalledCommands in pkg/commands/command.go.
//
// This distinction drives help routing: built-in commands use urfave/cli's native help
// rendering, while plugin commands must be re-dispatched to invoke the plugin's own
// help handler. See docs/plugin-contract.md for the full plugin execution model.
func isBuiltinCommand(c *cli.Context, cmdName string) bool {
	for _, cmd := range c.App.Commands {
		if cmd.Category != "" {
			continue
		}
		if cmd.Name == cmdName {
			return true
		}
		if contains(cmd.Aliases, cmdName) {
			return true
		}
	}

	return false
}

// contains checks whether a string slice contains a given element. It is used by
// isBuiltinCommand to match command aliases when identifying built-in commands.
func contains(slc []string, e string) bool {
	for _, s := range slc {
		if s == e {
			return true
		}
	}
	return false
}
