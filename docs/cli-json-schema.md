# cli.json Schema Reference

`cli.json` is the package manifest file used by the Akamai CLI to discover and manage installed plugin packages. Each package installed under `~/.akamai-cli/src/<package-name>/` must contain a `cli.json` file at its root directory.

The file is read by `readPackage` (`pkg/commands/subcommands.go`, line 44) during local package discovery and by `readPackageFromGithub` (`pkg/commands/subcommands.go`, line 72) during package installation from GitHub. The CLI uses Go's standard `encoding/json` unmarshaling, so **unknown fields are silently ignored** — this ensures backward compatibility when new fields are added to the schema.

This schema reference applies to **Akamai CLI v2.0.3 and later**.

---

## Top-Level Fields

The top-level object corresponds to the `subcommands` struct defined in `pkg/commands/subcommands.go` (lines 36–42).

| Field | JSON Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Commands | `commands` | Array of [Command objects](#command-object) | **Yes** | List of commands provided by this package. |
| Requirements | `requirements` | [Requirements object](#requirements-object) | No | Language runtime requirements for building or running the package. |
| Version | `version` | String (semver) | No (optional) | Package-level version for CLI compatibility checking. If absent, the package is treated as compatible with any CLI version. See [Version Field](#version-field). |

### Notes

- The **`pkg` field** seen in the Go struct (`json:"pkg"`) is computed at runtime by `readPackage` from the directory name — specifically, `filepath.Base(strings.Replace(dir, "cli-", "", 1))` (subcommands.go, line 67). It is **not read from** the `cli.json` file. Even if present in the JSON, it is overwritten at runtime.
- The **`Action` field** has the `json:"-"` tag and is internal to the CLI runtime. It never appears in `cli.json`.
- The **`raw` field** is an unexported byte slice used internally to cache the raw JSON content. It is not part of the file schema.
- All command names are **automatically lowercased** after parsing (subcommands.go, line 64: `strings.ToLower`).

---

## Command Object

Each entry in the `commands` array corresponds to the `command` struct defined in `pkg/commands/command.go` (lines 41–58).

| Field | JSON Key | Type | Required | Default | Description |
|-------|----------|------|----------|---------|-------------|
| Name | `name` | String | **Yes** | — | The command name. Used to construct the executable name (`akamai-<name>`). Automatically lowercased on read. |
| Aliases | `aliases` | Array of strings | No | `[]` | Alternative names for the command. The CLI also automatically adds `<pkg>/<name>` as an alias at runtime (command.go, line 114). |
| Version | `version` | String (semver) | No | `""` | The command version string. Exposed to the plugin at runtime via the `AKAMAI_CLI_COMMAND_VERSION` environment variable (command_subcommand.go, line 139). |
| Description | `description` | String | No | `""` | Human-readable description shown in `akamai list` and help output. |
| Usage | `usage` | String | No | `""` | Short usage line displayed in help output. |
| Arguments | `arguments` | String | No | `""` | Arguments usage text displayed in help output. |
| Bin | `bin` | String (URL template) | No | `""` | URL template for downloading pre-built binaries. Uses Go `text/template` syntax. See [Binary URL Templates](#binary-url-templates). |
| AutoComplete | `auto-complete` | Boolean | No | `false` | When `true`, enables shell auto-completion. The CLI invokes the plugin with `--generate-bash-completion` to obtain completions. |
| LdFlags | `ldflags` | String | No | `""` | Go linker flags passed during `go build -ldflags`. Used for compile-time variable injection (e.g., embedding a version string). |

### Internal-Only Fields (Not in cli.json)

The following fields exist on the `command` struct but have `json:"-"` tags. They are populated at runtime and **never appear** in a `cli.json` file:

| Field | Purpose |
|-------|---------|
| `Flags` | CLI flags parsed by the `urfave/cli` framework at runtime. |
| `Docs` | Extended documentation text populated from `UsageText`. |
| `BinSuffix` | Binary filename suffix (`.exe` on Windows, empty otherwise). Set by `downloadBin`. |
| `OS` | Operating system identifier for binary downloads. Set by `downloadBin`. |
| `Arch` | CPU architecture identifier for binary downloads. Set by `downloadBin`. |
| `Subcommands` | Nested CLI subcommands, populated at runtime. |

---

## Requirements Object

The `requirements` field maps to the `LanguageRequirements` struct defined in `pkg/packages/package.go` (lines 36–43).

| Field | JSON Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Go | `go` | String (semver) | No | Minimum Go version required (e.g., `"1.21.0"`). |
| Node | `node` | String (semver) | No | Minimum Node.js version required (e.g., `"18.0.0"`). |
| Python | `python` | String (semver) | No | Minimum Python version required (e.g., `"3.0.0"`). |
| Php | `php` | String (semver) | No | Minimum PHP version required. |
| Ruby | `ruby` | String (semver) | No | Minimum Ruby version required. |

### Notes

- **All fields are optional.** If no requirements are specified, the package is assumed to be self-contained (pre-built binary or already compiled).
- The **Python requirement** triggers special handling in the CLI:
  - If Python >= 3.0.0 is specified, the CLI creates and manages a virtual environment under `~/.akamai-cli/venv/<package-name>/` via `passthruCommand` (command.go, lines 379–393).
  - The `PYTHONUSERBASE` environment variable is set to the package directory during execution (command_subcommand.go, line 115).
- Requirements are used by `langManager.FindExec` to locate the correct language interpreter for the package.
- Only **one language requirement** should be specified per package. If multiple are present, the CLI uses the first match in its internal priority order.

---

## Binary URL Templates

The `bin` field on a [Command object](#command-object) uses Go's `text/template` syntax to construct the download URL for a pre-built binary. The template is parsed and executed by `downloadBin` in `pkg/commands/subcommands.go` (lines 146–208).

### Available Placeholders

| Placeholder | Value | Source |
|-------------|-------|--------|
| `{{.Version}}` | Command version from `cli.json` | `command.Version` field |
| `{{.Name}}` | Command name (lowercased) | `command.Name` field |
| `{{.OS}}` | Operating system: `linux`, `mac`, or `windows` | `runtime.GOOS` with `darwin` mapped to `mac` (subcommands.go, lines 150–153) |
| `{{.Arch}}` | CPU architecture: `amd64`, `arm64`, `386`, etc. | `runtime.GOARCH` (subcommands.go, line 148) |
| `{{.BinSuffix}}` | Binary file suffix: `".exe"` on Windows, `""` otherwise | Set based on OS (subcommands.go, lines 155–157) |

> **Important:** The OS placeholder maps Go's `darwin` identifier to `mac`. If your release assets use `darwin`, you must account for this in your URL template or rename your release assets.

### Example Template

```
https://github.com/akamai/cli-property/releases/download/{{.Version}}/akamai-{{.Name}}-{{.OS}}-{{.Arch}}{{.BinSuffix}}
```

On a 64-bit Linux system with command version `1.5.0` and name `property`, this resolves to:

```
https://github.com/akamai/cli-property/releases/download/1.5.0/akamai-property-linux-amd64
```

### Download Behavior

1. The URL template is parsed using Go's `text/template` package (subcommands.go, line 159).
2. The binary is downloaded via `http.Get` (subcommands.go, line 186).
3. The downloaded file is saved as `akamai-<command-name-lowercased><BinSuffix>` in the package directory (subcommands.go, line 169).
4. File permissions are set to `0775` (subcommands.go, line 181).
5. A non-200 HTTP status code results in an error (subcommands.go, lines 197–198).

---

## Version Field

The optional top-level `version` field enables CLI-to-package compatibility checking.

### Semantics

- The `version` field at the top level of `cli.json` is **optional**.
- If **present**, the CLI **enforces** compatibility via `version.IsCompatible` (in `pkg/version/version.go`) before executing plugin commands. The top-level version is used as the "required" minimum CLI version, and the running CLI version (`version.Version`) is the "current" version. If the current CLI version is older than the required version, the CLI returns a clear error message and exits with code 1 (user error). This enforcement is performed in `cmdSubcommand` (`pkg/commands/command_subcommand.go`) after reading the package manifest.
- If **absent**, the package is treated as **compatible with any CLI version**. This is the default behavior and preserves backward compatibility with all existing packages.
- If the version string cannot be parsed as valid semver, the check **fails open** (compatible) to avoid blocking plugin execution due to version parsing issues.
- Version strings should follow [semantic versioning](https://semver.org/) (e.g., `"1.0.0"`, `"2.1.3"`).
- The CLI uses `version.Compare` from `pkg/version/version.go` for version comparisons, which relies on the `github.com/Masterminds/semver` library.
- Go's `encoding/json.Unmarshal` handles the absent field by leaving it as the zero value (`""`), so existing packages without this field continue to work without modification.

### Distinction from Command-Level Version

The top-level `version` field is distinct from the per-command `version` field inside the `commands` array:

| Field | Location | Purpose |
|-------|----------|---------|
| Top-level `version` | Root of `cli.json` | Package-level compatibility; used for CLI version requirement checks. |
| Command `version` | Inside each command object | Command release version; exposed via `AKAMAI_CLI_COMMAND_VERSION` env var at runtime. |

---

## Examples

### Example 1: Minimal Manifest (Source-Built Go Package)

A package built from source with Go, containing a single command. No binary download, no version, no linker flags.

```json
{
  "requirements": {
    "go": "1.14.0"
  },
  "commands": [
    {
      "name": "app-1-cmd-1",
      "aliases": ["ac1", "apcmd1"],
      "description": "First command from app 1"
    }
  ]
}
```

Based on `pkg/commands/testdata/repo_no_binary/cli.json`.

### Example 2: Binary Download with Version and LdFlags

A package that downloads a pre-built binary and uses Go linker flags for compile-time version injection.

```json
{
  "requirements": {
    "go": "1.14.0"
  },
  "commands": [
    {
      "name": "app-1-cmd-1",
      "aliases": ["ac1", "apcmd1"],
      "description": "First command from app 1",
      "version": "1.0.0",
      "bin": "https://github.com/akamai/cli-test-command/releases/download/{{.Version}}/akamai-{{.Name}}",
      "ldflags": "-X 'github.com/akamai/cli-test-command/cli.Version=%s'"
    }
  ]
}
```

Based on `pkg/commands/testdata/repo_ldflags/cli.json`.

### Example 3: Python Package

A Python-based package. The Python >= 3.0.0 requirement triggers virtual environment management by the CLI.

```json
{
  "requirements": {
    "python": "3.0.0"
  },
  "commands": [
    {
      "name": "echo-python",
      "aliases": ["e"],
      "description": "echo command",
      "version": "1.0.0"
    }
  ]
}
```

Based on `pkg/commands/testdata/.akamai-cli/src/cli-echo-python/cli.json`.

### Example 4: With Optional Top-Level Version

A package that declares a top-level version for CLI compatibility checking, alongside per-command versioning and auto-completion.

```json
{
  "version": "1.0.0",
  "requirements": {
    "go": "1.21.0"
  },
  "commands": [
    {
      "name": "my-tool",
      "aliases": ["mt"],
      "description": "A sample tool with version compatibility",
      "version": "2.0.0",
      "auto-complete": true
    }
  ]
}
```

The top-level `version` field (`"1.0.0"`) is used for CLI compatibility checking. The per-command `version` field (`"2.0.0"`) is the command's own release version. If the top-level `version` is absent, the package is always treated as compatible.

---

## Parsing Behavior

The following describes how the Akamai CLI reads and processes `cli.json` files.

### File Location

The CLI looks for `cli.json` in the package directory. If not found, it checks the parent directory (subcommands.go, lines 44–49). This allows flexibility in package directory layouts.

### Parsing Steps

1. **Read**: The file is read using `os.ReadFile` (subcommands.go, line 53).
2. **Unmarshal**: The JSON content is parsed using `encoding/json.Unmarshal` into the `subcommands` struct (subcommands.go, line 58).
3. **Name normalization**: All command names are lowercased after parsing (subcommands.go, lines 63–65: `strings.ToLower`).
4. **Package name derivation**: The `Pkg` field is derived from the directory name with the `cli-` prefix stripped (subcommands.go, line 67: `filepath.Base(strings.Replace(dir, "cli-", "", 1))`). This value is **not** read from the JSON file.
5. **Unknown fields**: Silently ignored by Go's JSON unmarshaler. This ensures backward compatibility when new fields are added to the schema.

### Error Handling

| Error Condition | Error Message | Behavior |
|-----------------|---------------|----------|
| `cli.json` not found in package directory or parent | `"package does not contain a cli.json file: ..."` | Package is skipped during discovery. |
| File cannot be read | `"unable to read package: ..."` | Package is skipped during discovery. |
| Invalid JSON content | `"unable to unmarshal package: ..."` | Package is skipped during discovery. |

### Remote Parsing (GitHub)

When installing from GitHub, `readPackageFromGithub` (subcommands.go, line 72) fetches the `cli.json` content via HTTP GET. The parsing behavior is identical to local parsing, with the addition that:

- A non-200 HTTP response results in an error: `"invalid response status while fetching cli.json: <status>"`.
- The raw JSON content is cached in the `raw` field for later use during installation.

---

## Schema Validation Rules

The following rules describe what constitutes a valid `cli.json` file:

1. **Valid JSON**: The file must be valid JSON. Invalid JSON causes the package to be skipped with an `"unable to unmarshal package"` error.
2. **Commands required**: The `commands` array must contain at least one command entry for the package to be functional.
3. **Command name required**: Each command object must have a `name` field. The name is used to construct the executable filename (`akamai-<name>`) and to register the command with the CLI.
4. **Valid binary URL template**: If the `bin` field is specified, it must be a valid Go `text/template` string. An invalid template causes a runtime error during binary download.
5. **Semantic versioning**: All version strings (top-level `version`, command-level `version`, and requirement versions) should follow semantic versioning (`X.Y.Z`) for compatibility with the CLI's `version.Compare` function, which uses `github.com/Masterminds/semver`.
6. **Python virtual environment**: If `requirements` specifies `python` with a version >= `3.0.0`, the CLI automatically manages a virtual environment for the package under `~/.akamai-cli/venv/<package-name>/`.
7. **Backward compatibility**: Unknown fields are silently ignored. Existing packages without new optional fields (such as the top-level `version`) continue to work without modification.

---

## Code References

| Topic | Source File | Lines |
|-------|------------|-------|
| `subcommands` struct (top-level schema) | `pkg/commands/subcommands.go` | 36–42 |
| `command` struct (command object schema) | `pkg/commands/command.go` | 41–58 |
| `LanguageRequirements` struct | `pkg/packages/package.go` | 36–43 |
| `readPackage` (local parsing) | `pkg/commands/subcommands.go` | 44–70 |
| `readPackageFromGithub` (remote parsing) | `pkg/commands/subcommands.go` | 72–103 |
| `downloadBin` (binary download) | `pkg/commands/subcommands.go` | 146–208 |
| `subcommandToCliCommands` (alias injection) | `pkg/commands/command.go` | 109–146 |
| `findExec` (executable discovery) | `pkg/commands/command.go` | 274–353 |
| `passthruCommand` (Python venv handling) | `pkg/commands/command.go` | 371–408 |
| `version.Compare` (version comparison) | `pkg/version/version.go` | — |
| `AKAMAI_CLI_COMMAND_VERSION` env var | `pkg/commands/command_subcommand.go` | 139 |
| `PYTHONUSERBASE` env var | `pkg/commands/command_subcommand.go` | 115 |
