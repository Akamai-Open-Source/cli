# Akamai CLI Plugin Contract

This document formalizes the contract between the Akamai CLI host (v2.0.3+) and
installed plugin packages. It covers executable naming conventions, flag handling,
exit codes, the execution model, environment variables, help behavior, directory
layout, and version compatibility.

This specification is intended for **plugin authors** building new Akamai CLI
packages and **CI/CD pipeline integrators** scripting against the CLI. Every
behavior documented here is traceable to a specific location in the CLI source
code, cited inline.

---

## Executable Naming

The CLI supports two naming patterns for plugin executables. Both are tried
during command discovery, implemented in `findExec`
(`pkg/commands/command.go`, line 274).

### Dashed-Lowercase Format

```
akamai-<command>
```

- A single-word command `property` maps to executable `akamai-property`.
- A hyphenated command `command-name` maps to executable `akamai-command-name`.

### CamelCase Format

```
akamai<Command>
```

- A single-word command `property` maps to executable `akamaiProperty`.
- A hyphenated command `command-name` maps to executable `akamaiCommandName`.
- Title-casing uses `cases.Title` from `golang.org/x/text`
  (`pkg/commands/command.go`, line 282).

### Discovery Order

1. The CLI first searches the system `PATH` for executables matching the
   dashed-lowercase name, then the CamelCase name
   (`pkg/commands/command.go`, lines 293–296).
2. If no match is found on `PATH`, the CLI scans package directories under
   `~/.akamai-cli/src/*/` and `~/.akamai-cli/src/*/bin/`
   (`getPackageBinPaths`, `pkg/commands/command.go`, lines 355–369).
3. For each scanned directory both naming patterns and glob variants are tried.
   On Windows the glob `akamai-<command>.*` and `akamai<Command>.*` catches
   extensions `.exe`, `.bat`, `.com`, `.cmd`, and `.jar`
   (`pkg/commands/command.go`, lines 318–321).
4. Binary files downloaded by the CLI are named `akamai-<command>` plus a
   platform suffix (e.g., `.exe` on Windows)
   (`downloadBin`, `pkg/commands/subcommands.go`, line 169).

**Code reference:** `findExec` in `pkg/commands/command.go` (lines 274–353),
`getPackageBinPaths` in `pkg/commands/command.go` (lines 355–369),
`downloadBin` in `pkg/commands/subcommands.go` (lines 146–208).

---

## Flag Handling

### Global Flags Passed to Plugins

The CLI defines three global flags that are automatically forwarded to plugin
executables. They are declared in `createAppTemplate`
(`pkg/app/cli.go`, lines 115–136) and injected by `prepareCommand`
(`pkg/commands/command_subcommand.go`, line 150).

| Flag | Alias | Environment Variable | Default | Description |
|------|-------|---------------------|---------|-------------|
| `--edgerc` | `-e` | `AKAMAI_EDGERC` | `~/.edgerc` | Location of the credentials file |
| `--section` | `-s` | `AKAMAI_EDGERC_SECTION` | `default` | Section of the credentials file |
| `--accountkey` | `--account-key` | `AKAMAI_EDGERC_ACCOUNT_KEY` | _(none)_ | Account switch key |

**Injection rules** (implemented in `findFlags`,
`pkg/commands/command_subcommand.go`, lines 176–184):

1. A flag is injected **only** if the user has set a non-empty value for it.
2. A flag is **not** injected if it is already present in the user's argument
   list (checked via `containsString`).
3. Injected flags are appended as `--flagname value` pairs.

### SkipFlagParsing Contract

Every installed plugin command is registered with `SkipFlagParsing: true`
(`subcommandToCliCommands`, `pkg/commands/command.go`, line 123). This means:

- The CLI does **not** parse any flags or arguments intended for the plugin.
- All user-supplied arguments after the command name are passed through to the
  plugin executable as-is.
- The CLI only appends the three global flags (`--edgerc`, `--section`,
  `--accountkey`) when they are set and not already present in the user's
  arguments.

### Flag Placement for Interpreted Commands

Flag placement depends on whether the command requires an interpreter
(`prepareCommand`, `pkg/commands/command_subcommand.go`, lines 164–171):

| Command Type | Argument Order |
|-------------|---------------|
| Interpreted (Python, Node.js, etc.) | `<interpreter> <script> <user-args...> <injected-flags...>` |
| Native binary | `<binary> <injected-flags...> <user-args...>` |

When the executable list has more than one element (i.e., an interpreter plus a
script path), user arguments come first, followed by injected flags. For a
single binary, injected flags come first, followed by user arguments.

**Code reference:** `subcommandToCliCommands` in `pkg/commands/command.go`
(lines 109–146), `prepareCommand` and `findFlags` in
`pkg/commands/command_subcommand.go` (lines 157–184).

---

## Exit Codes

The CLI uses the following standardized exit codes:

| Code | Meaning | When Used |
|------|---------|-----------|
| `0` | Success | The operation completed successfully |
| `1` | User error | A user-correctable problem occurred (e.g., invalid arguments, missing repository, executable not found) |
| `2` | System error | An internal or system-level error (e.g., filesystem failure, network error) |

### Plugin Exit Code Pass-Through

The CLI passes through the plugin's exit code unchanged. In `passthruCommand`
(`pkg/commands/command.go`, lines 396–407):

1. The subprocess runs with stdin/stdout/stderr connected to the parent process.
2. If the subprocess exits with an error, the CLI extracts the exit code from
   `syscall.WaitStatus.ExitStatus()`.
3. The CLI returns `cli.Exit("", exitCode)` to propagate the code to the caller.

Plugin executables **SHOULD** follow the 0/1/2 convention above for consistent
behavior when used in scripts and CI/CD pipelines.

### Built-in Command Error Formatting

Built-in commands (install, update, uninstall) format error messages using
`color.RedString` for styled terminal output and `tools.CapitalizeFirstWord`
to ensure the first character is uppercase
(`pkg/commands/command_install.go`, `pkg/tools/util.go`).

**Code reference:** `passthruCommand` in `pkg/commands/command.go`
(lines 371–408).

---

## Execution Model

### Command Discovery and Invocation

The full lifecycle of a plugin command invocation is implemented in
`cmdSubcommand` (`pkg/commands/command_subcommand.go`, lines 32–154):

1. **Discovery** — The CLI calls `findExec` to locate the plugin executable and
   determine whether an interpreter (e.g., Python) is needed.
2. **Package reading** — The CLI reads the `cli.json` manifest from the package
   directory via `readPackage` (`pkg/commands/subcommands.go`, line 44).
3. **Python handling** — If the package declares Python requirements:
   - The CLI finds the correct Python interpreter via `langManager.FindExec`.
   - The CLI checks for stale virtual-environment directories and may prompt
     for reinstall (see [Python Virtual Environment Prompt](#python-virtual-environment-prompt) below).
   - The CLI sets `PYTHONUSERBASE` to the package directory (line 115).
4. **Environment setup** — The CLI sets `AKAMAI_CLI_COMMAND` and
   `AKAMAI_CLI_COMMAND_VERSION` environment variables (lines 135–142).
5. **Flag preparation** — The CLI calls `prepareCommand` to inject global flags
   (line 150).
6. **Execution** — The CLI spawns the command via `passthruCommand` (line 153),
   which creates a subprocess with stdin/stdout/stderr connected to the parent.

### Python Virtual Environment Prompt

When a Python-based plugin is invoked, the CLI checks for legacy package
installation directories that indicate the package was installed before virtual
environment support was added
(`pkg/commands/command_subcommand.go`, lines 86–114):

- On **Linux**: the directory `.local` inside the package directory.
- On **macOS**: the directory `Library` inside the package directory.
- On **Windows**: the directory `Lib` inside the package directory.

If one of these directories exists, the CLI prompts:

> Would you like to reinstall it

This prompt (using `term.Confirm` with a default of `true`) indicates the
Python package needs to be migrated to a virtual environment. The behavior is:

- **User accepts (default):** The CLI uninstalls and reinstalls the package,
  then continues execution.
- **User declines:** The CLI exits with an error
  (`packages.ErrPackageNeedsReinstall`).

### passthruCommand Contract

The `passthruCommand` function (`pkg/commands/command.go`, lines 371–408)
implements the subprocess execution contract:

1. If the package requires Python ≥ 3.0.0 and no virtual environment exists,
   the CLI calls `langManager.PrepareExecution` to create one (lines 379–393).
2. A deferred call to `langManager.FinishExecution` runs on cleanup (line 394).
3. The subprocess is created via `createCommand`
   (`pkg/commands/command.go`, lines 431–438), connecting `os.Stdin`,
   `os.Stdout`, and `os.Stderr` to the parent process.
4. The subprocess exit code is extracted from
   `syscall.WaitStatus.ExitStatus()` (lines 399–403).
5. The exit code is returned via `cli.Exit("", exitCode)` to propagate it to
   the caller. A successful execution (exit code 0) returns `nil`.

**Code reference:** `cmdSubcommand` in `pkg/commands/command_subcommand.go`
(lines 32–154), `passthruCommand` in `pkg/commands/command.go`
(lines 371–408).

---

## Environment Variables

### Variables Set During Plugin Execution

The CLI sets the following environment variables before invoking a plugin:

| Variable | Set By | Value | Purpose |
|----------|--------|-------|---------|
| `AKAMAI_CLI` | `cli/app/run.go` | `"1"` | Indicates the command is running inside the Akamai CLI |
| `AKAMAI_CLI_VERSION` | `cli/app/run.go` | e.g., `"2.0.3"` | The current CLI version string |
| `AKAMAI_CLI_COMMAND` | `pkg/commands/command_subcommand.go` (line 135) | e.g., `"property"` | The currently executing command name |
| `AKAMAI_CLI_COMMAND_VERSION` | `pkg/commands/command_subcommand.go` (line 139) | e.g., `"1.0.0"` | The currently executing command's version from `cli.json` |
| `PYTHONUSERBASE` | `pkg/commands/command_subcommand.go` (line 115) | Package directory path | Python package installation directory (only for Python commands) |

### Configuration Environment Variables

The following environment variables affect CLI behavior and are relevant to
plugin authors and pipeline operators:

| Variable | Purpose |
|----------|---------|
| `AKAMAI_CLI_HOME` | Override the default `~/.akamai-cli` base directory (`pkg/tools/util.go`, `GetAkamaiCliPath`) |
| `AKAMAI_EDGERC` | Override the default `.edgerc` location (equivalent to the `--edgerc` flag) |
| `AKAMAI_EDGERC_SECTION` | Override the credentials section (equivalent to the `--section` flag) |
| `AKAMAI_EDGERC_ACCOUNT_KEY` | Account switch key (equivalent to the `--accountkey` flag) |
| `AKAMAI_LOG` | Log level: `fatal`, `error`, `warn`, `info`, `debug` (`pkg/log/log.go`, `SetupContext`) |
| `AKAMAI_CLI_LOG_PATH` | File path for log output (`pkg/log/log.go`, `SetupContext`) |
| `HTTP_PROXY` / `HTTPS_PROXY` | Proxy configuration; can also be set via the `--proxy` flag (`pkg/app/cli.go`, `app.Before`) |
| `AKAMAI_CLI_DAEMON` | Enable daemon mode — keep the CLI running in the background (`pkg/app/cli.go`, line 44) |
| `CLI_REPOSITORY` | Override the GitHub repository URL for CLI self-upgrade (`pkg/commands/upgrade.go`) |

**Code reference:** `cmdSubcommand` in `pkg/commands/command_subcommand.go`,
`createAppTemplate` in `pkg/app/cli.go` (lines 82–154),
`SetupContext` in `pkg/log/log.go`.

---

## Help Behavior

Plugins **SHOULD** respond to help requests in the following forms:

```
akamai <command> help
akamai <command> help <sub-command>
```

The CLI's help system routes between built-in and plugin commands via `cmdHelp`
in `pkg/apphelp/help_command.go`. Built-in commands use the CLI framework's
native help templates; plugin commands delegate to the plugin executable itself.

### Shell Completion

If a command declares `"auto-complete": true` in its `cli.json` manifest, the
CLI registers a `BashComplete` handler that invokes the plugin with
`--generate-bash-completion` appended to the argument list
(`subcommandToCliCommands`, `pkg/commands/command.go`, lines 124–141).

Plugins that support auto-completion should respond to
`--generate-bash-completion` by printing one completion candidate per line to
stdout.

---

## Directory Layout

Installed plugin packages follow this directory structure:

```
~/.akamai-cli/                          # Base directory (override with AKAMAI_CLI_HOME)
├── src/                                # Package source root
│   ├── cli-<package-name>/             # Package directory (git-cloned)
│   │   ├── cli.json                    # Package manifest (required)
│   │   ├── akamai-<command>            # Executable (dashed-lowercase)
│   │   └── bin/                        # Alternative binary location
│   │       └── akamai-<command>        # Executable in bin/ subdirectory
│   └── ...
└── venv/                               # Python virtual environments
    └── <package-name>/                 # Per-package venv directory
```

### Discovery Rules

- **Package root:** `~/.akamai-cli/src/` (configurable via `AKAMAI_CLI_HOME`).
  Resolved by `GetAkamaiCliSrcPath` (`pkg/tools/util.go`, lines 57–65).
- **Package scanning:** The CLI globs all directories under `src/` and reads
  `cli.json` from each (`getPackagePaths`,
  `pkg/commands/subcommands.go`, lines 105–115).
- **Executable scanning:** For each package, both the package root and a `bin/`
  subdirectory are searched (`getPackageBinPaths`,
  `pkg/commands/command.go`, lines 355–369).
- **Upward directory walk:** `findPackageDir` walks upward from a binary's
  location to find the nearest `cli.json`
  (`pkg/commands/subcommands.go`, lines 127–144).
- **Directory naming convention:** Packages installed from Git repositories
  use the `cli-` prefix for the directory name (e.g., `cli-property` for the
  `property` package). The `Githubize` function in `pkg/tools/util.go`
  normalizes repository names to this convention.

**Code reference:** `getPackagePaths` in `pkg/commands/subcommands.go`
(lines 105–115), `getPackageBinPaths` in `pkg/commands/command.go`
(lines 355–369), `findPackageDir` in `pkg/commands/subcommands.go`
(lines 127–144), `GetAkamaiCliPath` and `GetAkamaiCliSrcPath` in
`pkg/tools/util.go`.

---

## Version Compatibility

Plugin packages may optionally declare a `version` field in their `cli.json`
manifest (at the command level). The CLI uses semantic versioning for all version
comparisons.

### Compatibility Semantics

- If a package's `cli.json` does **not** include a version field, the package
  is treated as compatible with any CLI version.
- If a version field is present, the CLI may use `version.Compare`
  (`pkg/version/version.go`, line 27) to check compatibility with
  "requires CLI ≥ X" semantics.
- Version comparison uses the `github.com/Masterminds/semver` library.

### Compare Function Return Values

The `Compare(left, right string) int` function returns:

| Return Value | Constant | Meaning |
|-------------|----------|---------|
| `0` | `version.Equals` | `left == right` |
| `-1` | `version.Greater` | `left > right` |
| `1` | `version.Smaller` | `left < right` |
| `2` | `version.Error` | Unable to parse `left` or `right` |

### Design Principle

Version checks are **additive** and **backward-compatible**. A missing version
field never causes an error; it is equivalent to declaring compatibility with
all CLI versions. This ensures existing plugins that predate the version field
continue to work without modification.

**Code reference:** `version.Compare` in `pkg/version/version.go`
(lines 27–45), `version.Version` constant (`"2.0.3"`) at line 7.

---

## Complete Plugin Example

The following example illustrates how a Go-based plugin integrates with the CLI.

### 1. Package Manifest (`cli.json`)

```json
{
  "requirements": {
    "go": "1.21.0"
  },
  "commands": [
    {
      "name": "my-tool",
      "aliases": ["mt"],
      "description": "A sample plugin tool",
      "version": "1.2.0",
      "auto-complete": true
    }
  ]
}
```

### 2. Executable Name

The CLI searches for:

1. `akamai-my-tool` (dashed-lowercase)
2. `akamaiMyTool` (CamelCase)

### 3. Invocation

When a user runs:

```
akamai my-tool --edgerc ~/.edgerc --section production list --format json
```

The CLI:

1. Discovers the executable via `findExec`.
2. Sets `SkipFlagParsing: true`, so `--format json` is **not** parsed by the
   CLI.
3. Detects that `--edgerc` and `--section` are already in the user's arguments,
   so they are **not** injected a second time.
4. Spawns the executable with all arguments passed through as-is.

### 4. Environment Variables During Execution

```
AKAMAI_CLI=1
AKAMAI_CLI_VERSION=2.0.3
AKAMAI_CLI_COMMAND=my-tool
AKAMAI_CLI_COMMAND_VERSION=1.2.0
```

### 5. Exit Code

The plugin exits with code `0` on success, `1` for user errors, or `2` for
system errors. The CLI propagates the exit code unchanged.

---

## References

The following source files are the authoritative implementation of the
behaviors documented above:

| Source File | Key Functions |
|-------------|--------------|
| `pkg/commands/command.go` | `findExec` (line 274), `passthruCommand` (line 371), `subcommandToCliCommands` (line 109), `CommandLocator` (line 148), `createBuiltinCommands` (line 166), `createInstalledCommands` (line 262), `getPackageBinPaths` (line 355), `createCommand` (line 431) |
| `pkg/commands/command_subcommand.go` | `cmdSubcommand` (line 32), `prepareCommand` (line 157), `findFlags` (line 176) |
| `pkg/commands/subcommands.go` | `readPackage` (line 44), `readPackageFromGithub` (line 72), `getPackagePaths` (line 105), `findPackageDir` (line 127), `downloadBin` (line 146) |
| `pkg/app/cli.go` | `createAppTemplate` (line 82), global flag definitions (lines 115–136) |
| `pkg/version/version.go` | `Compare` (line 27), `Version` constant (line 7) |
| `pkg/tools/util.go` | `GetAkamaiCliPath` (line 38), `GetAkamaiCliSrcPath` (line 58), `GetPkgVenvPath` (line 78) |
