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
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/akamai/cli/v2/pkg/apphelp"
	"github.com/akamai/cli/v2/pkg/autocomplete"
	"github.com/akamai/cli/v2/pkg/color"
	"github.com/akamai/cli/v2/pkg/git"
	"github.com/akamai/cli/v2/pkg/packages"
	"github.com/akamai/cli/v2/pkg/tools"
	"github.com/akamai/cli/v2/pkg/version"
	"github.com/urfave/cli/v2"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

type (
	// command represents a single command entry from a cli.json package manifest.
	// JSON-tagged fields are deserialized from the manifest file. Fields with
	// `json:"-"` tags are internal runtime state populated during command
	// discovery and execution, and are never present in the manifest file.
	// See docs/cli-json-schema.md for the full schema specification.
	command struct {
		Name         string   `json:"name"`
		Aliases      []string `json:"aliases"`
		Version      string   `json:"version"`
		Description  string   `json:"description"`
		Usage        string   `json:"usage"`
		Arguments    string   `json:"arguments"`
		Bin          string   `json:"bin"`
		AutoComplete bool     `json:"auto-complete"`
		LdFlags      string   `json:"ldflags"`

		Flags       []cli.Flag     `json:"-"`
		Docs        string         `json:"-"`
		BinSuffix   string         `json:"-"`
		OS          string         `json:"-"`
		Arch        string         `json:"-"`
		Subcommands []*cli.Command `json:"-"`
	}

	// Command represents an external command being prepared for execution. It wraps
	// an exec.Cmd with stdin/stdout/stderr connected to the parent process for
	// transparent pass-through of I/O.
	Command struct {
		cmd *exec.Cmd
	}

	// Cmd is a wrapper interface for exec.Cmd.Run, enabling testability of
	// passthruCommand by allowing mock command implementations.
	Cmd interface {
		Run() error
	}
)

// getBuiltinCommands returns only the built-in commands (those without a Category set)
// from the application's command list, converted to subcommands structs.
func getBuiltinCommands(c *cli.Context) []subcommands {
	commands := make([]subcommands, 0)
	for _, cmd := range c.App.Commands {
		// builtin commands do not have Category set
		if cmd.Category != "" {
			continue
		}
		commands = append(commands, cliCommandToSubcommand(cmd))
	}
	return commands
}

// getCommands returns all registered commands (built-in and installed) from the
// application's command list, converted to subcommands structs.
func getCommands(c *cli.Context) []subcommands {
	commands := make([]subcommands, 0)
	for _, cmd := range c.App.Commands {
		commands = append(commands, cliCommandToSubcommand(cmd))
	}
	return commands
}

// cliCommandToSubcommand converts a urfave/cli Command to an internal subcommands struct,
// preserving name, aliases, description, usage, arguments, flags, docs, and subcommands.
func cliCommandToSubcommand(from *cli.Command) subcommands {
	return subcommands{
		Commands: []command{
			{
				Name:        from.Name,
				Aliases:     from.Aliases,
				Description: from.Description,
				Usage:       from.Usage,
				Arguments:   from.ArgsUsage,
				Flags:       from.Flags,
				Docs:        from.UsageText,
				Subcommands: from.Subcommands,
			},
		},
		Action: from.Action,
	}
}

// subcommandToCliCommands converts a subcommands manifest into urfave/cli Command structs
// suitable for registration with the CLI application. Each command is configured with:
//   - SkipFlagParsing: true — all flags/args are passed through to the plugin as-is
//   - Category set to "Installed Commands:" (yellow) for visual grouping in help
//   - An automatic alias of "<pkg>/<name>" for namespaced invocation
//   - BashComplete handler that invokes the plugin with --generate-bash-completion
//     when auto-complete is enabled in the cli.json manifest
//
// This is a core part of the plugin contract: plugins receive raw arguments because
// SkipFlagParsing prevents the CLI from consuming any flags intended for the plugin.
func subcommandToCliCommands(from subcommands, gitRepo git.Repository, langManager packages.LangManager) []*cli.Command {
	commands := make([]*cli.Command, 0)
	for key, command := range from.Commands {
		commandPkg := from
		commandPkg.Commands = commandPkg.Commands[key : key+1]
		// Automatically add a namespaced alias "<pkg>/<name>" so plugins can be invoked
		// as e.g. "akamai property/list" in addition to "akamai list".
		aliases := append(command.Aliases, fmt.Sprintf("%s/%s", from.Pkg, command.Name))

		commands = append(commands, &cli.Command{
			Name:        strings.ToLower(command.Name),
			Aliases:     aliases,
			Description: command.Description,

			Action:   cmdSubcommand(gitRepo, langManager),
			Category: color.YellowString("Installed Commands:"),
			// SkipFlagParsing is essential to the plugin contract: it prevents urfave/cli from
			// parsing any flags intended for the plugin. All arguments after the command name
			// are passed through as-is to the plugin executable.
			SkipFlagParsing: true,
			BashComplete: func(c *cli.Context) {
				if command.AutoComplete {
					executable, packageReqs, err := findExec(c.Context, langManager, c.Command.Name)
					if err != nil {
						return
					}

					packageDir, err := findBinPackageDir(executable)
					if err != nil {
						return
					}

					executable = append(executable, os.Args[2:]...)
					subCmd := createCommand(executable[0], executable[1:])
					if err = passthruCommand(c.Context, subCmd, langManager, *packageReqs, packageDir); err != nil {
						return
					}
				}
			},
		})
	}
	return commands
}

// CommandLocator builds a sorted slice of all CLI commands — both built-in commands
// (config, install, list, search, uninstall, update, upgrade) and installed plugin
// commands discovered from ~/.akamai-cli/src/*/cli.json manifests. Called once during
// CLI initialization to populate the application's command registry.
//
// Performance: O(n) where n = number of installed packages, due to readPackage calls
// in createInstalledCommands. Each package requires a filesystem read of cli.json.
func CommandLocator(ctx context.Context) []*cli.Command {
	gitRepo := git.NewRepository()
	langManager := packages.NewLangManager()
	commands := createBuiltinCommands()
	commands = append(commands, createInstalledCommands(ctx, gitRepo, langManager)...)

	sortCommands(commands)
	return commands
}

// sortCommands sorts commands alphabetically by name for consistent display ordering.
func sortCommands(commands []*cli.Command) {
	sort.Slice(commands, func(i, j int) bool {
		cmp := strings.Compare(commands[i].Name, commands[j].Name)
		return cmp < 0
	})
}

// createBuiltinCommands returns the fixed set of built-in CLI commands: config, install,
// list, search, uninstall, update, and upgrade. These commands are part of the CLI binary
// and are NOT loaded from plugins. Built-in commands do not have a Category set, which
// distinguishes them from installed plugin commands in getBuiltinCommands.
//
// The command list and registration here is a stable contract — built-in commands are not
// modified by this refactor. See docs/plugin-contract.md for the full command taxonomy.
func createBuiltinCommands() []*cli.Command {
	gitRepo := git.NewRepository()
	langManager := packages.NewLangManager()
	return []*cli.Command{
		{
			Name:        "config",
			ArgsUsage:   "<action> <setting> [value]",
			Description: "Manages configuration.",
			Subcommands: []*cli.Command{
				{
					Name:      "get",
					ArgsUsage: "<setting>",
					Action:    cmdConfigGet,
				},
				{
					Name:      "set",
					ArgsUsage: "<setting> <value>",
					Action:    cmdConfigSet,
				},
				{
					Name:      "list",
					ArgsUsage: "[section]",
					Action:    cmdConfigList,
				},
				{
					Name:      "unset",
					Aliases:   []string{"rm"},
					ArgsUsage: "<setting>",
					Action:    cmdConfigUnset,
				},
			},
			HideHelp:     true,
			BashComplete: autocomplete.Default,
		},
		{
			Name:        "install",
			Aliases:     []string{"get"},
			ArgsUsage:   "<package name or repository URL>...",
			Description: "Fetches and installs packages from a Git repository.",
			Action:      cmdInstall(gitRepo, langManager),
			UsageText: fmt.Sprintf("Examples:\n\n   %v\n,  %v\n   %v\n   %v",
				"akamai install property purge",
				"akamai install akamai/cli-property",
				"akamai install git@github.com:akamai/cli-property.git",
				"akamai install https://github.com/akamai/cli-property.git"),
			HideHelp:     true,
			BashComplete: autocomplete.Default,
		},
		{
			Name:        "list",
			Description: "By default, displays installed commands. Optionally, can display package commands from Git repositories.",
			Action:      cmdList,
			Flags: []cli.Flag{
				&cli.BoolFlag{
					Name:  "remote",
					Usage: "Displays all available packages.",
				},
			},
			HideHelp:           true,
			BashComplete:       autocomplete.Default,
			CustomHelpTemplate: apphelp.SimplifiedHelpTemplate,
		},
		{
			Name:         "search",
			ArgsUsage:    "<keyword>...",
			Description:  "Searches for packages in the official Akamai CLI package repository.",
			Action:       cmdSearch,
			UsageText:    "Examples:\n\n   akamai search property",
			HideHelp:     true,
			BashComplete: autocomplete.Default,
		},
		{
			Name:         "uninstall",
			ArgsUsage:    "<command>...",
			Description:  "Uninstalls a package containing a given <command>.",
			Action:       cmdUninstall(langManager),
			HideHelp:     true,
			BashComplete: autocomplete.Default,
		},
		{
			Name:         "update",
			ArgsUsage:    "[<command>...]",
			Description:  "Updates one or more commands. If no command is specified, all commands are updated.",
			Action:       cmdUpdate(gitRepo, langManager),
			HideHelp:     true,
			BashComplete: autocomplete.Default,
		},
		{
			Name:         "upgrade",
			Description:  "Upgrades the Akamai CLI to the latest version.",
			Action:       cmdUpgrade,
			BashComplete: autocomplete.Default,
		},
	}
}

// createInstalledCommands discovers all installed plugin packages from the filesystem
// and converts them to urfave/cli Command structs. It scans ~/.akamai-cli/src/*/
// directories via getPackagePaths, reads each cli.json manifest via readPackage, and
// converts them via subcommandToCliCommands.
//
// Performance: O(n) where n = number of installed packages. Each package requires one
// filesystem read and JSON parse. Errors are silently skipped (packages with invalid
// cli.json are ignored rather than causing CLI startup failure).
func createInstalledCommands(_ context.Context, gitRepo git.Repository, langManager packages.LangManager) []*cli.Command {
	commands := make([]*cli.Command, 0)
	packagePaths := getPackagePaths()
	for _, dir := range packagePaths {
		pkg, err := readPackage(dir)
		if err == nil {
			commands = append(commands, subcommandToCliCommands(pkg, gitRepo, langManager)...)
		}
	}
	return commands
}

// findExec resolves the executable path(s) for a given command name. It searches for
// both naming conventions: dashed-lowercase (akamai-<command>) and CamelCase
// (akamai<Command>). The discovery algorithm:
//
//  1. Construct both name variants from the command name.
//  2. Temporarily set PATH to package bin paths (from getPackageBinPaths).
//  3. Quick check: use exec.LookPath for both variants on the modified PATH.
//  4. If found on PATH, return immediately.
//  5. Fallback: iterate all package directories, glob for matching executables
//     (including platform extensions: .exe, .bat, .com, .cmd, .jar on Windows).
//  6. For each match, read the package's cli.json to determine language requirements
//     and call langManager.FindExec to resolve the full command (may prepend interpreter).
//
// Returns: (executable paths, language requirements, error). The executable paths slice
// has length 1 for native binaries and length 2+ for interpreted commands (interpreter + script).
//
// Performance: O(n × m) where n = number of package directories and m = name variants.
// Called on every plugin command invocation — performance-critical path.
func findExec(ctx context.Context, langManager packages.LangManager, cmd string) ([]string, *packages.LanguageRequirements, error) {
	// "command" becomes: akamai-command, and akamaiCommand
	// "command-name" becomes: akamai-command-name, and akamaiCommandName
	cmdName := "akamai"
	cmdNameTitle := "akamai"
	for _, cmdPart := range strings.Split(cmd, "-") {
		cmdName += "-" + strings.ToLower(cmdPart)
		cmdNameTitle += cases.Title(language.Und, cases.NoLower).String(strings.ToLower(cmdPart))
	}

	// Temporarily replace the system PATH with package bin paths so exec.LookPath
	// searches plugin directories. The original PATH is restored afterward.
	systemPath := os.Getenv("PATH")
	packagePaths := getPackageBinPaths()
	if err := os.Setenv("PATH", packagePaths); err != nil {
		return nil, nil, err
	}

	// Quick look for executables on the path
	var path string
	path, err := exec.LookPath(cmdName)
	if err != nil {
		path, _ = exec.LookPath(cmdNameTitle)
	}

	if path != "" {
		if err := os.Setenv("PATH", systemPath); err != nil {
			return nil, nil, err
		}
		return []string{path}, &packages.LanguageRequirements{}, nil
	}

	if err := os.Setenv("PATH", systemPath); err != nil {
		return nil, nil, err
	}
	if packagePaths == "" {
		return nil, nil, packages.ErrNoExeFound
	}

	for _, path := range filepath.SplitList(packagePaths) {
		filePaths := []string{
			// Search for <path>/akamai-command, <path>/akamaiCommand
			filepath.Join(path, cmdName),
			filepath.Join(path, cmdNameTitle),

			// Search for <path>/akamai-command.*, <path>/akamaiCommand.*
			// This should catch .exe, .bat, .com, .cmd, and .jar
			filepath.Join(path, cmdName+".*"),
			filepath.Join(path, cmdNameTitle+".*"),
		}

		var files []string
		for _, filePath := range filePaths {
			files, _ = filepath.Glob(filePath)
			if len(files) > 0 {
				break
			}
		}

		if len(files) == 0 {
			continue
		}

		cmdBinary := files[0]

		packageDir := findPackageDir(filepath.Dir(cmdBinary))
		cmdPackage, err := readPackage(packageDir)
		if err != nil {
			return nil, nil, err
		}

		comm, err := langManager.FindExec(ctx, cmdPackage.Requirements, cmdBinary)
		if err != nil {
			return nil, nil, err
		}

		return comm, &cmdPackage.Requirements, nil
	}

	return nil, nil, packages.ErrNoExeFound
}

// getPackageBinPaths constructs a PATH-style string of all directories where installed
// plugin executables may reside. Scans ~/.akamai-cli/src/*/ and ~/.akamai-cli/src/*/bin/
// directories and joins them with the platform path separator.
//
// Performance: O(n) where n = number of installed packages (directory glob). Called by
// findExec on every plugin command invocation — performance-critical supporting path.
func getPackageBinPaths() string {
	path := ""
	if akamaiCliPath, err := tools.GetAkamaiCliSrcPath(); err == nil {
		paths, _ := filepath.Glob(filepath.Join(akamaiCliPath, "*"))
		if len(paths) > 0 {
			path += strings.Join(paths, string(os.PathListSeparator))
		}
		paths, _ = filepath.Glob(filepath.Join(akamaiCliPath, "*", "bin"))
		if len(paths) > 0 {
			path += string(os.PathListSeparator) + strings.Join(paths, string(os.PathListSeparator))
		}
	}

	return path
}

// passthruCommand performs the external command invocation for plugin execution. This is
// the core execution contract for the plugin system:
//
//  1. For Python packages requiring >= 3.0.0: sets up a virtual environment via
//     langManager.PrepareExecution if one doesn't already exist.
//  2. Defers langManager.FinishExecution for cleanup.
//  3. Runs the subprocess with stdin/stdout/stderr connected to the parent (transparent I/O).
//  4. Captures the subprocess exit code from WaitStatus.ExitStatus().
//  5. Returns cli.Exit("", exitCode) to propagate the plugin's exit code to the CLI caller.
//
// The plugin's exit code is passed through unchanged — the CLI does not interpret or
// modify it. This ensures plugins can use standard exit codes (0, 1, 2) for scripting.
//
// IMPORTANT: This function MUST NOT parse or modify the command's arguments. All flags
// and arguments have already been assembled by prepareCommand with SkipFlagParsing: true.
func passthruCommand(ctx context.Context, subCmd Cmd, langManager packages.LangManager, languageRequirements packages.LanguageRequirements, dirName string) error {
	// For Python packages with version >= 3.0.0, ensure a virtual environment exists
	// before execution. This isolates Python dependencies per-package and avoids
	// conflicts with the system Python installation.
	if v3Comparison := version.Compare(languageRequirements.Python, "3.0.0"); languageRequirements.Python != "" && (v3Comparison == version.Greater || v3Comparison == version.Equals) {
		vePath, err := tools.GetPkgVenvPath(filepath.Base(dirName))
		if err != nil {
			return err
		}
		veExists, err := langManager.FileExists(vePath)
		if err != nil {
			return err
		}
		if !veExists {
			if err := langManager.PrepareExecution(ctx, languageRequirements, dirName); err != nil {
				return err
			}
		}
	}
	defer langManager.FinishExecution(ctx, languageRequirements, dirName)

	err := subCmd.Run()

	exitCode := 1
	if exitError, ok := err.(*exec.ExitError); ok {
		if waitStatus, ok := exitError.Sys().(syscall.WaitStatus); ok {
			exitCode = waitStatus.ExitStatus()
		}
	}
	if err != nil {
		return cli.Exit("", exitCode)
	}
	return nil
}

// findBinPackageDir returns the package root directory given a list of binary paths.
// It takes the last element of binPath, resolves its absolute path, and returns the
// grandparent directory (two levels up from the binary file).
func findBinPackageDir(binPath []string) (string, error) {
	if len(binPath) == 0 {
		return "", packages.ErrPackageExecutableNotFound
	}

	absPath, err := filepath.Abs(binPath[len(binPath)-1])
	if err != nil {
		return "", err
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", errors.New("the specified binary is a directory")
	}

	return filepath.Dir(filepath.Dir(absPath)), nil
}

// createCommand creates a Command wrapping an exec.Cmd with the given name and arguments.
// Stdin, stdout, and stderr are connected to the parent process for transparent I/O
// pass-through during plugin command execution.
func createCommand(name string, args []string) *Command {
	comm := &Command{cmd: exec.Command(name, args...)}
	comm.cmd.Stdin = os.Stdin
	comm.cmd.Stderr = os.Stderr
	comm.cmd.Stdout = os.Stdout

	return comm
}

// Run starts the command and waits for it to complete
func (c *Command) Run() error {
	return c.cmd.Run()
}
