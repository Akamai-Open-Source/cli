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

package commands

import (
	"fmt"
	"time"

	"github.com/akamai/cli/v2/pkg/color"
	"github.com/akamai/cli/v2/pkg/log"
	"github.com/akamai/cli/v2/pkg/terminal"
	"github.com/akamai/cli/v2/pkg/tools"
	"github.com/urfave/cli/v2"
)

// cmdList handles the "akamai list" command. It delegates to cmdListWithPackageReader
// using the embedded package catalog as the default package reader.
func cmdList(c *cli.Context) (e error) {
	pr := newPackageReader(embeddedPackages)
	return cmdListWithPackageReader(c, pr)
}

// cmdListWithPackageReader implements the list command logic. It displays installed commands
// and, when the --remote flag is set, also shows available commands from the package catalog
// that are not yet installed, along with install hints. Uses the named-return-error pattern
// with deferred timing/logging.
func cmdListWithPackageReader(c *cli.Context, pr packageReader) (e error) {
	c.Context = log.WithCommandContext(c.Context, c.Command.Name)
	start := time.Now()
	logger := log.FromContext(c.Context)
	logger.Debug("LIST START")
	defer func() {
		if e == nil {
			logger.Debug(fmt.Sprintf("LIST FINISH: %v", time.Since(start)))
		} else {
			logger.Error(fmt.Sprintf("LIST ERROR: %v", e))
		}
	}()
	term := terminal.Get(c.Context)

	// Retrieve all registered commands (built-in + installed) to render the installed section.
	commands := listInstalledCommands(c, nil, nil)

	// When --remote is set, fetch the package catalog and show commands that are available
	// but not yet installed. Skip packages whose commands are all already installed.
	if c.IsSet("remote") {
		packages, err := pr.readPackage()
		if err != nil {
			logger.Error(fmt.Sprintf("Failed to read package: %v", err))
			return cli.Exit(fmt.Sprintf("list: %v", err), 1)
		}

		foundCommands := true
		for _, cmd := range packages.Packages {
			for _, command := range cmd.Commands {
				if _, ok := commands[command.Name]; !ok {
					foundCommands = false
					continue
				}
			}
		}

		if foundCommands {
			return nil
		}
		headerMsg := "\nAvailable Commands:\n\n"
		if _, err := term.Writeln(color.YellowString("%s", headerMsg)); err != nil {
			return err
		}
		logger.Debug(headerMsg)

		for _, remotePackage := range packages.Packages {
			for _, command := range remotePackage.Commands {
				if _, ok := commands[command.Name]; ok {
					continue
				}
				commandName := color.BoldString("  %s", command.Name)
				term.Printf(commandName)
				packageName := fmt.Sprintf(" [package: %s]", color.BlueString("%s", remotePackage.Name))
				if _, err := term.Writeln(packageName); err != nil {
					return err
				}
				commandDescription := fmt.Sprintf("    %s\n", command.Description)
				term.Printf(commandDescription)
				logger.Debug(commandName)
				logger.Debug(packageName)
				logger.Debug(commandDescription)
			}
		}

		term.Printf("\nInstall using \"%s\".\n", color.BlueString("%s install [package]", tools.Self()))
	}

	return nil
}

// listInstalledCommands renders the list of installed commands to the terminal. Commands in
// the added map are highlighted in green (newly installed), those in the removed map in red,
// and all others in bold. Each command's aliases and description are displayed below its name.
// Returns a map of installed command names for use in deduplication by the caller.
func listInstalledCommands(c *cli.Context, added map[string]bool, removed map[string]bool) map[string]bool {
	term := terminal.Get(c.Context)

	commands := make(map[string]bool)
	installedCmds := color.YellowString("\nInstalled Commands:\n")
	if _, err := term.Writeln(installedCmds); err != nil {
		term.WriteError(err.Error())
		return nil
	}

	cmds := getCommands(c)
	for _, cmd := range cmds {
		for _, command := range cmd.Commands {
			commands[command.Name] = true
			if _, ok := added[command.Name]; ok {
				term.Printf(color.GreenString("  %s", command.Name))
			} else if _, ok := removed[command.Name]; ok {
				term.Printf(color.RedString("  %s", command.Name))
			} else {
				term.Printf(color.BoldString("  %s", command.Name))
			}

			if len(command.Aliases) > 0 {
				var aliases string

				if len(command.Aliases) == 1 {
					aliases = "alias"
				} else {
					aliases = "aliases"
				}

				term.Printf(" (%s: ", aliases)
				for i, alias := range command.Aliases {
					term.Printf(color.BoldString("%s", alias))
					if i < len(command.Aliases)-1 {
						term.Printf(", ")
					}
				}
				term.Printf(")")
			}

			if _, err := term.Writeln(); err != nil {
				term.WriteError(err.Error())
				return nil
			}

			if len(command.Description) > 0 {
				cmdDescription := fmt.Sprintf("    %s\n", command.Description)
				term.Printf(cmdDescription)
			}
		}
	}
	term.Printf("\nSee \"%s\" for details.\n", color.BlueString("%s help [command]", tools.Self()))
	return commands
}
