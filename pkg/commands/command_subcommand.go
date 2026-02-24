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
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/akamai/cli/v2/pkg/color"
	"github.com/akamai/cli/v2/pkg/git"
	"github.com/akamai/cli/v2/pkg/log"
	"github.com/akamai/cli/v2/pkg/packages"
	"github.com/akamai/cli/v2/pkg/terminal"
	"github.com/akamai/cli/v2/pkg/version"
	"github.com/urfave/cli/v2"
)

// cmdSubcommand creates a cli.ActionFunc that handles the execution of installed plugin
// commands. It resolves the executable via findExec, reads the package manifest, handles
// Python virtual environment setup when required, exports AKAMAI_CLI_COMMAND and
// AKAMAI_CLI_COMMAND_VERSION environment variables, prepares the command with injected
// global flags (edgerc, section, accountkey), and delegates execution to passthruCommand.
//
// The git and langManager parameters provide repository operations and language runtime
// management respectively. The returned ActionFunc follows the named-return-error pattern
// with deferred logging for consistent command lifecycle tracking.
func cmdSubcommand(git git.Repository, langManager packages.LangManager) cli.ActionFunc {
	return func(c *cli.Context) (e error) {
		c.Context = log.WithCommandContext(c.Context, c.Command.Name)
		logger := log.FromContext(c.Context)
		term := terminal.Get(c.Context)

		defer func() {
			if e != nil {
				logger.Error(fmt.Sprintf("Command execution failed: %v", e))
			} else {
				logger.Info("Command execution completed")
			}
		}()

		logger.Info(fmt.Sprintf("Executing subcommand: %s", c.Command.Name))

		commandName := strings.ToLower(c.Command.Name)

		executable, _, err := findExec(c.Context, langManager, commandName)
		if err != nil {
			errMsg := color.RedString("Executable \"%s\" not found.", commandName)
			logger.Error(errMsg)
			return cli.Exit(errMsg, 1)
		}

		var packageDir string
		if len(executable) == 1 {
			packageDir = findPackageDir(executable[0])
		} else if len(executable) > 1 {
			packageDir = findPackageDir(executable[1])
		}

		cmdPackage, err := readPackage(packageDir)
		if err != nil {
			logger.Error(fmt.Sprintf("Error reading package: %v", err))
			return err
		}

		// Enforce CLI version compatibility: if the package declares a minimum required
		// CLI version via the top-level "version" field in cli.json, verify the running
		// CLI meets that requirement. Missing or empty version is treated as compatible
		// (fail-open). Incompatible version returns exit code 1 (user error).
		if cmdPackage.Version != "" && !version.IsCompatible(cmdPackage.Version, version.Version) {
			errMsg := color.RedString(
				"Package \"%s\" requires CLI version >= %s, but current version is %s. Please upgrade the Akamai CLI.",
				commandName, cmdPackage.Version, version.Version,
			)
			logger.Error(errMsg)
			return cli.Exit(errMsg, 1)
		}

		if cmdPackage.Requirements.Python != "" {
			exec, err := langManager.FindExec(c.Context, cmdPackage.Requirements, packageDir)
			if err != nil {
				logger.Error(fmt.Sprintf("Error finding executable: %v", err))
				return err
			}

			if len(executable) == 1 {
				executable = append([]string{exec[0]}, executable...)
			} else {
				if strings.Contains(strings.ToLower(executable[0]), "python") ||
					strings.Contains(strings.ToLower(executable[0]), "py.exe") {
					executable[0] = exec[0]
				}
			}

			// Check for stale per-user package directories that indicate the package was
			// installed before the CLI adopted virtual environment isolation. The presence
			// of platform-specific directories (.local on Linux, Library on macOS, Lib on
			// Windows) signals that reinstallation is needed to migrate to venv-based layout.
			switch runtime.GOOS {
			case "linux":
				_, err = os.Stat(filepath.Join(packageDir, ".local"))
			case "darwin":
				_, err = os.Stat(filepath.Join(packageDir, "Library"))
			case "windows":
				_, err = os.Stat(filepath.Join(packageDir, "Lib"))
			}

			if err == nil {
				answer, err := term.Confirm("Package requires reinstallation to enable virtual environment support. Reinstall now", true)
				logger.Debug(fmt.Sprintf("Would you like to reinstall it? %v", answer))
				if err != nil {
					logger.Error(fmt.Sprintf("Error confirming reinstall: %v", err))
					return err
				}
				if !answer {
					logger.Error(packages.ErrPackageNeedsReinstall.Error())
					return cli.Exit(color.RedString("%s", packages.ErrPackageNeedsReinstall.Error()), 1)
				}

				if err = uninstallPackage(c.Context, langManager, commandName, logger); err != nil {
					return err
				}

				if _, err = installPackage(c.Context, git, langManager, commandName); err != nil {
					return err
				}
			}
			// Set PYTHONUSERBASE so that pip and the Python runtime install/find packages
			// in the per-package directory rather than the user's global site-packages.
			if err := os.Setenv("PYTHONUSERBASE", packageDir); err != nil {
				logger.Error(fmt.Sprintf("Error setting PYTHONUSERBASE: %v", err))
				return err
			}
		}

		var currentCmd command
		for _, cmd := range cmdPackage.Commands {
			if strings.EqualFold(cmd.Name, commandName) {
				currentCmd = cmd
				break
			}

			for _, alias := range cmd.Aliases {
				if strings.EqualFold(alias, commandName) {
					currentCmd = cmd
				}
			}
		}

		// Export the current command name and version as environment variables so the
		// plugin can identify itself and report version information without re-reading
		// its own cli.json manifest.
		if err := os.Setenv("AKAMAI_CLI_COMMAND", commandName); err != nil {
			logger.Error(fmt.Sprintf("Error setting AKAMAI_CLI_COMMAND: %v", err))
			return err
		}
		if err := os.Setenv("AKAMAI_CLI_COMMAND_VERSION", currentCmd.Version); err != nil {
			logger.Error(fmt.Sprintf("Error setting AKAMAI_CLI_COMMAND_VERSION: %v", err))
			return err
		}

		cmdPackage, err = readPackage(packageDir)
		if err != nil {
			logger.Error(fmt.Sprintf("Error reading package: %v", err))
			return err
		}

		// Assemble the final command line by injecting the three global credential flags
		// (edgerc, section, accountkey) if they were set by the user and are not already
		// present in the user's arguments. This is the plugin contract's flag propagation.
		executable = prepareCommand(c, executable, c.Args().Slice(), "edgerc", "section", "accountkey")

		subCmd := createCommand(executable[0], executable[1:])
		return passthruCommand(c.Context, subCmd, langManager, cmdPackage.Requirements, fmt.Sprintf("cli-%s", cmdPackage.Commands[0].Name))
	}
}

// prepareCommand assembles the final command line for a plugin invocation by combining
// the resolved executable path, user-supplied arguments, and automatically injected global
// flags (edgerc, section, accountkey).
//
// Flag injection behavior:
//   - For interpreted commands (len(command) > 1, e.g., Python/Node.js): user args are
//     appended first, then injected flags, preserving interpreter compatibility.
//   - For single-binary commands (len(command) == 1): injected flags come first, then
//     user args.
//   - If the user already specified a flag (e.g., --edgerc), it is NOT duplicated.
//   - If no args are provided, the command is returned unmodified.
//
// This is a core part of the plugin contract: plugins can rely on receiving --edgerc,
// --section, and --accountkey flags when set by the user, without needing to parse
// them from environment variables.
func prepareCommand(c *cli.Context, command, args []string, flags ...string) []string {
	// dont search for flags is there are no args
	if len(args) == 0 {
		return command
	}
	additionalFlags := findFlags(c, args, flags...)

	if len(command) > 1 {
		// for python or js append flags to the end
		command = append(command, args...)
		command = append(command, additionalFlags...)
	} else {
		command = append(command, additionalFlags...)
		command = append(command, args...)
	}

	return command
}

// findFlags scans the CLI context for the specified flag names and returns them as
// command-line arguments (--flagname value pairs) if they have non-empty values and
// are not already present in the target argument list. This prevents duplicating flags
// that the user explicitly passed.
func findFlags(c *cli.Context, target []string, flags ...string) []string {
	var ret []string
	for _, flagName := range flags {
		if flagVal := c.String(flagName); flagVal != "" && !containsString(target, fmt.Sprintf("--%s", flagName)) {
			ret = append(ret, fmt.Sprintf("--%s", flagName), flagVal)
		}
	}
	return ret
}

// containsString returns true if the slice s contains the given item string.
// Used by findFlags to check whether a flag is already present in the user's arguments.
func containsString(s []string, item string) bool {
	for _, v := range s {
		if v == item {
			return true
		}
	}
	return false
}
