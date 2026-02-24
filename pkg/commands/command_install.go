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
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/akamai/cli/v2/pkg/color"
	"github.com/akamai/cli/v2/pkg/git"
	"github.com/akamai/cli/v2/pkg/log"
	"github.com/akamai/cli/v2/pkg/packages"
	"github.com/akamai/cli/v2/pkg/terminal"
	"github.com/akamai/cli/v2/pkg/tools"
	"github.com/urfave/cli/v2"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

var (
	thirdPartyDisclaimer = color.CyanString("Disclaimer: You are installing a third-party package, subject to its own terms and conditions. Akamai makes no warranty or representation with respect to the third-party package.")
	githubRawURLTemplate = "https://raw.githubusercontent.com/akamai/%s/master/cli.json"
)

// cmdInstall creates a cli.ActionFunc that handles the "akamai install" command. It installs
// one or more packages from Git repositories or the official Akamai package catalog. For each
// repository argument, it resolves the URL via tools.Githubize, fetches the cli.json manifest,
// downloads binaries or clones the repository, installs language-specific dependencies, and
// registers the new commands with the running application.
//
// Exit codes: 0 success, 1 user error (missing args, repo not found), 2 system error
// (filesystem failures). Uses the named-return-error pattern with deferred timing/logging.
func cmdInstall(git git.Repository, langManager packages.LangManager) cli.ActionFunc {
	return func(c *cli.Context) (e error) {
		start := time.Now()
		c.Context = log.WithCommandContext(c.Context, c.Command.Name)
		logger := log.FromContext(c.Context)
		logger.Debug("INSTALL START")
		defer func() {
			if e == nil {
				logger.Debug(fmt.Sprintf("INSTALL FINISH: %v", time.Since(start)))
			} else {
				var exitErr cli.ExitCoder
				if errors.As(e, &exitErr) && exitErr.ExitCode() == 0 {
					logger.Warn(fmt.Sprintf("INSTALL WARN: %v", e))
				} else {
					logger.Error(fmt.Sprintf("INSTALL ERROR: %v", e))
				}
			}
		}()
		if !c.Args().Present() {
			return cli.Exit(color.RedString("You must specify a repository URL"), 1)
		}

		oldCmds := getCommands(c)

		// Install each package sequentially. On success, register the new commands with
		// the running application so they appear in help and list output immediately.
		for _, repo := range c.Args().Slice() {
			repo = tools.Githubize(repo)
			subCmd, err := installPackage(c.Context, git, langManager, repo)
			if err != nil {
				logger.Error(fmt.Sprintf("Error installing package: %v", err))
				return err
			}
			c.App.Commands = append(c.App.Commands, subcommandToCliCommands(*subCmd, git, langManager)...)
			sortCommands(c.App.Commands)
		}

		packageListDiff(c, oldCmds)

		return nil
	}
}

// packageListDiff computes and displays the difference between the command list before and
// after an install operation. Commands added are highlighted in green, removed commands in red,
// and unchanged commands in bold. It delegates rendering to listInstalledCommands.
func packageListDiff(c *cli.Context, oldcmds []subcommands) {
	cmds := getCommands(c)

	var old []command
	for _, oldcmd := range oldcmds {
		old = append(old, oldcmd.Commands...)
	}

	var newCmds []command
	for _, newcmd := range cmds {
		newCmds = append(newCmds, newcmd.Commands...)
	}

	var added = make(map[string]bool)
	var removed = make(map[string]bool)

	for _, newCmd := range newCmds {
		found := false
		for _, oldCmd := range old {
			if newCmd.Name == oldCmd.Name {
				found = true
				break
			}
		}

		if !found {
			added[newCmd.Name] = true
		}
	}

	for _, oldCmd := range old {
		found := false
		for _, newCmd := range newCmds {
			if newCmd.Name == oldCmd.Name {
				found = true
				break
			}
		}

		if !found {
			removed[oldCmd.Name] = true
		}
	}

	listInstalledCommands(c, added, removed)
}

// installPackage installs a single package from the given repository URL. It first attempts
// to fetch the cli.json manifest from GitHub's raw content API to determine if the package
// provides pre-built binaries. If binary installation fails or the package requires source
// compilation, it falls back to cloning the repository and building from source.
//
// Returns the parsed subcommands manifest on success, or an error wrapped in cli.Exit with
// appropriate exit codes: 0 (package already exists, warning), 1 (user error such as missing
// repo), 2 (system error such as filesystem failure).
func installPackage(ctx context.Context, gitRepo git.Repository, langManager packages.LangManager, repo string) (*subcommands, error) {
	logger := log.FromContext(ctx)
	logger.Debug(fmt.Sprintf("Installing package from repository: %s", repo))

	srcPath, err := tools.GetAkamaiCliSrcPath()
	if err != nil {
		// System-level error (exit code 2 category): unable to resolve the CLI source directory.
		// Propagated as a raw error to preserve backward compatibility with existing callers.
		logger.Error(fmt.Sprintf("Unable to get akamai cli source path: %v", err))
		return nil, err
	}

	term := terminal.Get(ctx)
	spin := term.Spinner()

	dirName := strings.TrimSuffix(filepath.Base(repo), ".git")
	packageDir := filepath.Join(srcPath, dirName)

	if _, err = os.Stat(packageDir); err == nil {
		warningMsg := fmt.Sprintf("Package directory already exists (%s). To reinstall this package, first run 'akamai uninstall' command.", packageDir)
		logger.Warn(warningMsg)
		return nil, cli.Exit(color.YellowString("%s", warningMsg), 0)
	}

	// Fetch the cli.json manifest from GitHub's raw content API before cloning. This allows
	// the CLI to determine whether pre-built binaries are available, avoiding unnecessary
	// repository clones for binary-only packages.
	spin.Start("Attempting to fetch package configuration from %s...", repo)

	base := filepath.Base(dirName)
	url := fmt.Sprintf(githubRawURLTemplate, base)
	cmdPackage, err := readPackageFromGithub(url, dirName)
	if err != nil {
		spin.Stop(terminal.SpinnerStatusFail)
		logger.Error(fmt.Sprintf("Failed to read package from github: %v", err))
		term.WriteError(err.Error())

		if strings.Contains(err.Error(), "404") {
			return nil, cli.Exit(color.RedString("%s", tools.CapitalizeFirstWord(git.ErrPackageNotAvailable.Error())), 1)
		}
		return nil, cli.Exit(color.RedString("%s", "Unable to install selected package"), 1)
	}
	spin.OK()

	// If the package provides pre-built binaries, attempt to download them directly.
	// Falls back to source cloning if binary download fails (e.g., binary not available
	// for the current OS/architecture).
	if isBinary(cmdPackage) {
		logger.Debug(fmt.Sprintf("Installing binaries for package in directory: %s", packageDir))
		ok, subCmd := installPackageBinaries(ctx, packageDir, cmdPackage, logger)
		if ok {
			return subCmd, nil
		}
		// delete package directory
		if err := os.RemoveAll(packageDir); err != nil {
			logger.Error(fmt.Sprintf("Failed to remove package directory: %v", err))
			return nil, err
		}
		logger.Debug(fmt.Sprintf("Unable to install binaries for package in directory: %s, cloning repository: %s", packageDir, repo))
	}

	spin.Start("Attempting to fetch command from %s...", repo)

	if !strings.HasPrefix(repo, "https://github.com/akamai/cli-") && !strings.HasPrefix(repo, "git@github.com:akamai/cli-") {
		term.Printf(color.CyanString("%s", thirdPartyDisclaimer))
	}

	err = gitRepo.Clone(ctx, packageDir, repo, false, spin)
	if err != nil {
		spin.Stop(terminal.SpinnerStatusFail)
		if err := os.RemoveAll(packageDir); err != nil {
			logger.Error(fmt.Sprintf("Failed to remove package directory: %v", err))
			return nil, err
		}

		logger.Error(cases.Title(language.Und, cases.NoLower).String(err.Error()))
		return nil, cli.Exit(color.RedString("%s", tools.CapitalizeFirstWord(err.Error())), 1)
	}
	spin.OK()

	logger.Debug(fmt.Sprintf("Installing dependencies for package in directory: %s", packageDir))

	ok, subCmd := installPackageDependencies(ctx, langManager, packageDir, logger)
	if !ok {
		logger.Error(fmt.Sprintf("Dependency installation failed, removing package directory: %s", packageDir))
		if err := os.RemoveAll(packageDir); err != nil {
			logger.Error(fmt.Sprintf("Failed to remove package directory: %v", err))
			return nil, err
		}
		return nil, cli.Exit(color.RedString("Unable to install selected package"), 1)
	}
	logger.Debug(fmt.Sprintf("Dependencies installed successfully for package in directory: %s", packageDir))

	return subCmd, nil
}

// installPackageDependencies reads the cli.json manifest from dir, extracts language
// requirements and ldflags, and invokes langManager.Install to build/install the package.
// Uses a spinner for user feedback. Returns (true, subcommands) on success, (false, nil)
// on failure.
func installPackageDependencies(ctx context.Context, langManager packages.LangManager, dir string, logger *slog.Logger) (bool, *subcommands) {
	term := terminal.Get(ctx)
	term.Spinner().Start("Installing Dependencies...")

	cmdPackage, err := readPackage(dir)
	if err != nil {
		term.Spinner().Stop(terminal.SpinnerStatusFail)
		logger.Error(fmt.Sprintf("Failed to read package: %v", err))
		term.WriteError(err.Error())
		return false, nil
	}

	var commands, ldFlags []string
	for _, cmd := range cmdPackage.Commands {
		commands = append(commands, cmd.Name)
		ldFlag := cmd.LdFlags
		if ldFlag != "" {
			ldFlag = fmt.Sprintf(ldFlag, cmd.Version)
		}
		ldFlags = append(ldFlags, ldFlag)
	}

	err = langManager.Install(ctx, dir, cmdPackage.Requirements, commands, ldFlags)
	if errors.Is(err, packages.ErrUnknownLang) {
		term.Spinner().WarnOK()
		warnMsg := "Package installed successfully, however package type is unknown, and may or may not function correctly."
		if _, err := term.Writeln(color.CyanString("%s", warnMsg)); err != nil {
			term.WriteError(err.Error())
			return false, nil
		}
		logger.Warn(warnMsg)

		return true, &cmdPackage
	}

	if err != nil {
		term.Spinner().Stop(terminal.SpinnerStatusFail)
		logger.Error(fmt.Sprintf("Failed to install dependecies: %v", err))
		term.WriteError(err.Error())
		return false, nil
	}

	term.Spinner().OK()

	return true, &cmdPackage
}

// installPackageBinaries downloads pre-built binaries for all commands in the package
// manifest. Creates a bin/ subdirectory under dir, downloads each binary via downloadBin,
// and writes the cli.json manifest to the package directory. Uses a spinner for user feedback.
// Returns (true, subcommands) on success, (false, nil) on failure.
func installPackageBinaries(ctx context.Context, dir string, cmdPackage subcommands, logger *slog.Logger) (bool, *subcommands) {
	term := terminal.Get(ctx)
	spin := term.Spinner()
	spin.Start("Installing Binaries...")

	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0700); err != nil {
		spin.Stop(terminal.SpinnerStatusWarn)
		logger.Error(fmt.Sprintf("Unable to create directory %s: %v", filepath.Join(dir, "bin"), err))
		term.WriteError(err.Error())
		return false, nil
	}

	for _, cmd := range cmdPackage.Commands {
		err := downloadBin(ctx, filepath.Join(dir, "bin"), cmd)
		if err != nil {
			warnMsg := fmt.Sprintf("Unable to download binary: %v", err.Error())
			spin.Stop(terminal.SpinnerStatusWarn)
			if _, err := term.Writeln(color.YellowString("%s", warnMsg)); err != nil {
				term.WriteError(err.Error())
				return false, nil
			}
			logger.Warn(warnMsg)

			return false, nil
		}
	}

	err := os.WriteFile(filepath.Join(dir, "cli.json"), cmdPackage.raw, 0644)
	if err != nil {
		spin.Stop(terminal.SpinnerStatusWarn)
		warnMsg := "Unable to save configuration file " + err.Error()
		logger.Warn(warnMsg)
		if _, err := term.Writeln(color.YellowString("%s", warnMsg)); err != nil {
			term.WriteError(err.Error())
			return false, nil
		}
		return false, nil
	}

	spin.OK()
	logger.Debug(fmt.Sprintf("Binaries installed successfully in directory: %s", dir))

	return true, &cmdPackage
}
