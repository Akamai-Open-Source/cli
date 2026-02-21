# Technical Specification

# 0. Agent Action Plan

## 0.1 Intent Clarification

### 0.1.1 Core Refactoring Objective

Based on the prompt, the Blitzy platform understands that the refactoring objective is to transform the Akamai CLI (`github.com/akamai/cli`, currently at v2.0.3) from its current implicit-contract implementation into a system with a **stable, formally documented plugin contract**, **clear versioning and compatibility enforcement**, **consistent user experience**, and **predictable, scriptable behavior** — all while maintaining full backward compatibility with existing plugins, scripts, and public API surfaces.

- **Refactoring type:** Code structure, documentation, UX consistency, and contract formalization
- **Target repository:** Same repository (in-place refactoring)
- **Refactoring goals:**
  - **Plugin contract formalization:** Define and document the `cli.json` schema (parsed in `pkg/commands/subcommands.go` via `readPackage` / `readPackageFromGithub`, structured by the `subcommands` and `command` types in `pkg/commands/command.go` and `subcommands.go`). Document required plugin behavior including global flags (`--edgerc`, `--section`, `--accountkey`), exit code conventions, executable naming (`akamai-<command>` / `akamai<Command>`), and help behavior
  - **Versioning and lifecycle:** Introduce optional version/compatibility handling using `pkg/version/version.go` (`Version`, `Compare`) so "requires CLI ≥ X" or "package format vN" can be enforced; treat missing version as compatible
  - **Command extensibility contract:** Document the CLI protocol: plugin commands run via `passthruCommand` in `pkg/commands/command.go` with `SkipFlagParsing: true`; flags/args are passed through as-is; document which flags the CLI always passes
  - **UX clarity, feedback, consistency:** Standardize error messages and exit codes across `command_install.go`, `command_update.go`, `command_uninstall.go`, `command_subcommand.go`; use spinners consistently; reduce surprising prompts (e.g., Python venv reinstall in `command_subcommand.go` line 96); improve help text
  - **Reliability and discoverability:** Standardize exit codes (0 success, 1 user error, 2 system error), improve list/search/help behavior, and ensure consistent use of `cli.Exit(..., code)`
- **Implicit requirements:**
  - Preserve all public API contracts (`findExec`, `passthruCommand`, `readPackage`, `subcommands`/`command` structs)
  - Maintain full test suite pass rate (baseline: `go test ./...` passes with zero failures)
  - Maintain or exceed baseline test coverage (82.3% for `pkg/commands`)
  - No changes to `SkipFlagParsing: true` behavior for plugin commands
  - No removal or renaming of global flags (`--edgerc`, `--section`, `--accountkey`)

### 0.1.2 Technical Interpretation

This refactoring translates to the following technical transformation strategy:

**Current Architecture → Target Architecture Mapping:**

| Aspect | Current State | Target State |
|--------|--------------|--------------|
| Plugin contract | Implicit; `cli.json` parsed without formal schema documentation | Formally documented in `docs/cli-json-schema.md` with optional version field |
| Plugin documentation | None; behavior inferred from code | `docs/plugin-contract.md` covering flags, naming, exit codes, help |
| Version compatibility | No compatibility checks between CLI and plugins | Optional `version` field in `cli.json`; `pkg/version.Compare` used for "requires CLI ≥ X" checks; missing = compatible |
| Exit codes | Inconsistent across commands (mix of 1, -1, 0) | Standardized: 0 success, 1 user error, 2 system error across install/update/uninstall |
| Error messages | Varied styles across command files | Consistent style aligned with existing patterns (e.g., `color.RedString`, `tools.CapitalizeFirstWord`) |
| Spinner usage | Mostly consistent but gaps exist | Spinners for all long operations in install/update/uninstall; success/failure summary for multi-package operations |
| Python venv prompt | Appears unexpectedly in `command_subcommand.go` (line 96) | Clearer messaging or conditional triggering without changing core logic |
| Help text | Basic descriptions | Enhanced with workflow guidance (e.g., "run after configuring .edgerc") |
| Color/quiet behavior | Partially documented; relies on `AKAMAI_LOG`, `AKAMAI_CLI_LOG_PATH` | Documented when colors/quiet apply (non-TTY, env vars) |
| Discovery rules | Implicit in code (`getPackageBinPaths`, `findExec`) | Documented directory layout, `cli.json` location, executable naming conventions |

**Transformation rules:**
- All new or modified code follows existing Go conventions and the code style catalog (MUST/SHOULD rules)
- Schema validation for `cli.json` must be backward-compatible (optional fields only)
- Version checks must be additive and must not break existing packages lacking version fields
- Exit code standardization must preserve existing test behavior while adding consistency
- Documentation must be traceable to code references

## 0.2 Source Analysis

### 0.2.1 Comprehensive Source File Discovery

The following exhaustive inventory lists every source file requiring refactoring. Files were identified through systematic deep-search of the repository hierarchy, cross-referenced with the user's explicit module references and the refactoring objectives.

**Current Structure Mapping:**

```
Current:
github.com/akamai/cli/v2 (Go 1.24.11, version 2.0.3)
├── go.mod                                    (module definition, Go 1.24.11)
├── go.sum                                    (dependency checksums)
├── Makefile                                  (build/test/lint/coverage targets)
├── .golangci.yaml                            (linter configuration)
├── README.md                                 (user-facing documentation)
├── CHANGELOG.md                              (release history)
├── build.sh                                  (cross-platform binary build)
├── LICENSE                                   (Apache 2.0)
├── .github/
│   └── workflows/
│       └── checks.yml                        (CI: build, test, lint)
├── cli/
│   ├── main.go                               (binary entry point)
│   └── app/
│       ├── run.go                            (launcher, guards, collision detection)
│       ├── run_test.go                       (collision/duplicate tests)
│       ├── firstrun.go                       (interactive onboarding, build tag !nofirstrun)
│       ├── firstrun_noinstall.go             (no-op onboarding, build tag nofirstrun)
│       ├── access_nix.go                     (Unix write-access check)
│       └── access_windows.go                 (Windows stub)
├── pkg/
│   ├── app/
│   │   ├── cli.go                            (CreateApp, global flags, proxy, daemon)
│   │   └── cli_test.go                       (flag/builder tests)
│   ├── apphelp/
│   │   ├── help.go                           (Setup, SetTemplates, makePrintHelp)
│   │   ├── help_command.go                   (cmdHelp routing)
│   │   ├── help_command_test.go              (help routing tests)
│   │   ├── help_test.go                      (setup regression test)
│   │   └── templates/                        (embedded help templates)
│   ├── autocomplete/                         (shell completion)
│   ├── color/
│   │   └── color.go                          (styled output: RedString, GreenString, etc.)
│   ├── commands/
│   │   ├── command.go                        (CommandLocator, findExec, passthruCommand, createBuiltinCommands, createInstalledCommands, getPackageBinPaths)
│   │   ├── command_config.go                 (config get/set/list/unset handlers)
│   │   ├── command_config_test.go            (config command tests)
│   │   ├── command_install.go                (cmdInstall, installPackage, packageListDiff, listInstalledCommands)
│   │   ├── command_install_test.go           (install command tests)
│   │   ├── command_list.go                   (cmdList, cmdListWithPackageReader, listInstalledCommands)
│   │   ├── command_list_test.go              (list command tests)
│   │   ├── command_search.go                 (cmdSearch, searchPackages, getLatestVersion, getVersionFromSystem)
│   │   ├── command_search_test.go            (search command tests)
│   │   ├── command_subcommand.go             (cmdSubcommand, prepareCommand, findFlags)
│   │   ├── command_subcommand_test.go        (subcommand execution tests)
│   │   ├── command_test.go                   (command discovery/sorting/passthru tests)
│   │   ├── command_uninstall.go              (cmdUninstall, uninstallPackage)
│   │   ├── command_uninstall_test.go         (uninstall command tests)
│   │   ├── command_update.go                 (cmdUpdate, updatePackage, updateRepo)
│   │   ├── command_update_test.go            (update command tests)
│   │   ├── command_upgrade.go                (cmdUpgrade, build tag !noautoupgrade)
│   │   ├── command_upgrade_noop.go           (no-op upgrade, build tag noautoupgrade)
│   │   ├── command_upgrade_test.go           (upgrade command tests)
│   │   ├── constants.go                      (sleep24HDuration)
│   │   ├── helpers_test.go                   (shared test helpers)
│   │   ├── mocks.go                          (MockCmd, mockPackageReader)
│   │   ├── package_reader.go                 (packageReader, packageList, packageListItem)
│   │   ├── package_reader_test.go            (package reader tests)
│   │   ├── subcommands.go                    (subcommands/command structs, readPackage, readPackageFromGithub, downloadBin)
│   │   ├── subcommands_test.go               (subcommands parsing tests)
│   │   ├── upgrade.go                        (CheckUpgradeVersion, versionProvider, build tag !noautoupgrade)
│   │   ├── upgrade_common.go                 (UpgradeCli)
│   │   ├── package_list/
│   │   │   └── package-list.json             (embedded package catalog, 25 packages)
│   │   └── testdata/                         (test fixtures: cli.json manifests, mock repos)
│   ├── config/
│   │   ├── config.go                         (IniConfig, Save, Values, ExportEnv, migration)
│   │   ├── config_test.go                    (config tests)
│   │   ├── mock.go                           (config mock)
│   │   └── testdata/                         (config fixtures)
│   ├── git/
│   │   ├── repository.go                     (Repository interface, Clone, Pull, Open, Head, Reset)
│   │   └── mock.go                           (MockRepo)
│   ├── log/
│   │   ├── context.go                        (NewContext, FromContext)
│   │   ├── handler.go                        (custom slog.Handler)
│   │   ├── handler_test.go                   (handler tests)
│   │   ├── log.go                            (SetupContext, WithCommand, WithCommandContext)
│   │   └── log_test.go                       (log tests)
│   ├── packages/
│   │   ├── package.go                        (LangManager interface, LanguageRequirements, errors)
│   │   ├── command_executor.go               (executor interface)
│   │   ├── golang.go / javascript.go / php.go / python.go / ruby.go
│   │   ├── mock.go                           (LangManager mock)
│   │   └── *_test.go                         (language-specific tests)
│   ├── terminal/
│   │   ├── terminal.go                       (Terminal, DefaultTerminal, IsTTY, Confirm, Prompt)
│   │   ├── spinner.go                        (Spinner interface, DefaultSpinner, StandardSpinner)
│   │   ├── mock.go                           (terminal mock)
│   │   ├── terminal_test.go                  (terminal tests)
│   │   └── spinner_test.go                   (spinner tests)
│   ├── tools/
│   │   ├── util.go                           (GetAkamaiCliPath, Githubize, Self, CapitalizeFirstWord)
│   │   ├── files.go                          (MoveFile)
│   │   └── util_test.go                      (tools tests)
│   └── version/
│       ├── version.go                        (Version="2.0.3", Compare, Equals/Error/Greater/Smaller)
│       └── version_test.go                   (version comparison tests)
```

### 0.2.2 Primary Source Files Requiring Modification

The following files are directly impacted by the refactoring objectives:

| File | Lines | Refactoring Impact | Reason |
|------|-------|-------------------|--------|
| `pkg/commands/subcommands.go` | 209 | HIGH — Schema documentation, optional version field handling | Contains `subcommands`/`command` structs and `readPackage`/`readPackageFromGithub` — the single place that parses `cli.json` |
| `pkg/commands/command.go` | 444 | MEDIUM — Contract documentation, no signature changes | Contains `findExec`, `passthruCommand`, `createBuiltinCommands`, `createInstalledCommands`, `CommandLocator` |
| `pkg/commands/command_subcommand.go` | 194 | MEDIUM — UX improvement for Python venv prompt, documentation | Contains `prepareCommand`, Python venv reinstall prompt (line 96) |
| `pkg/commands/command_install.go` | 310 | MEDIUM — Exit code standardization, error message consistency, spinner usage | Contains `cmdInstall`, `installPackage`, `packageListDiff` |
| `pkg/commands/command_update.go` | 265 | MEDIUM — Exit code standardization, error message consistency | Contains `cmdUpdate`, `updatePackage`, `updateRepo` |
| `pkg/commands/command_uninstall.go` | 137 | MEDIUM — Exit code standardization, error message consistency | Contains `cmdUninstall`, `uninstallPackage` |
| `pkg/commands/command_list.go` | 155 | LOW — Minor UX improvements | Contains `cmdList`, `listInstalledCommands` |
| `pkg/commands/command_search.go` | 281 | LOW — Minor UX improvements | Contains `cmdSearch`, `searchPackages`, version display |
| `pkg/commands/package_reader.go` | 64 | LOW — Optional version field support | Contains `packageList`, `packageListItem` structs |
| `pkg/app/cli.go` | 207 | LOW — Documentation only, no code changes | Contains global flag definitions (`--edgerc`, `--section`, `--accountkey`) |
| `pkg/apphelp/help.go` | 100 | LOW — Help text improvements | Contains `Setup`, `SetTemplates` |
| `pkg/apphelp/help_command.go` | ~80 | LOW — Help text for workflows | Contains `cmdHelp`, built-in vs plugin routing |
| `pkg/version/version.go` | 46 | LOW — Possible minor extension for compatibility checking | Contains `Version`, `Compare` |
| `pkg/commands/upgrade.go` | 154 | LOW — Documentation | Contains `CheckUpgradeVersion`, version provider |
| `pkg/color/color.go` | ~60 | NONE — Reference only for output consistency | Color helper definitions |
| `pkg/log/log.go` | ~80 | NONE — Reference only for logging consistency | Logging setup, `AKAMAI_LOG` handling |
| `pkg/terminal/spinner.go` | ~120 | NONE — Reference only for spinner patterns | Spinner interface and status constants |
| `pkg/terminal/terminal.go` | ~200 | NONE — Reference only for terminal patterns | Terminal interface, `IsTTY`, `Confirm` |
| `pkg/config/config.go` | ~200 | NONE — Reference only, stable interface | Config persistence, env var export |
| `pkg/tools/util.go` | 127 | NONE — Reference only | `GetAkamaiCliPath`, `Githubize`, utility helpers |

### 0.2.3 New Files to Create

| File | Purpose |
|------|---------|
| `docs/plugin-contract.md` | Plugin contract documentation: required flags, executable naming, exit codes, help behavior |
| `docs/cli-json-schema.md` | Formal `cli.json` schema documentation with field descriptions and examples |

### 0.2.4 Test Files Requiring Updates

| Test File | Lines | Change Type |
|-----------|-------|-------------|
| `pkg/commands/subcommands_test.go` | Existing | Add tests for `cli.json` with optional version field present and absent |
| `pkg/commands/command_install_test.go` | Existing | Add exit code verification test for failure path |
| `pkg/commands/command_update_test.go` | Existing | Add exit code verification test for failure path |
| `pkg/commands/command_uninstall_test.go` | Existing | Add exit code verification test for failure path |
| `pkg/version/version_test.go` | Existing | Add tests for version compatibility logic if new helpers are introduced |
| `pkg/commands/command_subcommand_test.go` | Existing | Add tests if Python venv prompt behavior changes |

## 0.3 Scope Boundaries

### 0.3.1 Exhaustively In Scope

**Source transformations:**
- `pkg/commands/subcommands.go` — Document `cli.json` schema inline; add optional version/compatibility field handling in `readPackage` / `readPackageFromGithub`; backward-compatible validation
- `pkg/commands/command.go` — Add contract documentation (Go doc comments) for `findExec`, `passthruCommand`, `createBuiltinCommands`, `createInstalledCommands`, `subcommandToCliCommands`, `getPackageBinPaths`; no signature changes
- `pkg/commands/command_subcommand.go` — Document `prepareCommand` contract; improve Python venv reinstall prompt messaging (line 96, `term.Confirm`) without changing core logic; document flags passed to subcommands
- `pkg/commands/command_install.go` — Standardize exit codes (0 success, 1 user error, 2 system error); consistent error message style; spinner usage for all long operations; success/failure summary via `packageListDiff`
- `pkg/commands/command_update.go` — Standardize exit codes; align error message style with install; spinner consistency
- `pkg/commands/command_uninstall.go` — Standardize exit codes; align error message style with install/update; spinner consistency
- `pkg/commands/command_list.go` — Minor UX improvements to output formatting
- `pkg/commands/command_search.go` — Minor UX improvements, version display consistency
- `pkg/commands/package_reader.go` — Support optional version field in `packageListItem` if needed for backward-compatible schema
- `pkg/app/cli.go` — Document global flags as part of plugin contract (Go doc comments only; no code changes)
- `pkg/apphelp/help.go` — Adjust help text for common workflows
- `pkg/apphelp/help_command.go` — Add or adjust help text for workflows (e.g., "configure .edgerc then run")
- `pkg/version/version.go` — Use existing `Version` and `Compare` for CLI/package version checks; potential minor helper addition (backward-compatible)
- `pkg/commands/upgrade.go` — Documentation; potential minor version check integration
- `pkg/commands/upgrade_common.go` — Documentation only
- `pkg/commands/command_upgrade.go` — Documentation only
- `pkg/commands/constants.go` — Reference only

**Test updates:**
- `pkg/commands/subcommands_test.go` — Add tests for optional version field (present and absent)
- `pkg/commands/command_install_test.go` — Add exit code verification for failure paths
- `pkg/commands/command_update_test.go` — Add exit code verification for failure paths
- `pkg/commands/command_uninstall_test.go` — Add exit code verification for failure paths
- `pkg/commands/command_subcommand_test.go` — Add tests if Python venv prompt behavior changes
- `pkg/version/version_test.go` — Add tests for any new version compatibility helpers

**Configuration updates:**
- No changes to `.golangci.yaml`, `Makefile`, `go.mod`, `go.sum`, `.github/workflows/checks.yml`, or `build.sh`

**Documentation creation (new files):**
- `docs/plugin-contract.md` — Plugin contract documentation
- `docs/cli-json-schema.md` — Formal `cli.json` schema documentation

**Documentation updates:**
- `README.md` — Update to reference new `docs/` documentation; clarify exit code semantics; reference plugin contract
- `CHANGELOG.md` — Add entry for contract/versioning/UX refactor

**Import corrections:**
- No import path changes required; all refactoring is in-place within existing module structure

### 0.3.2 Explicitly Out of Scope

The following items are explicitly excluded from this refactor, per user directives:

| Exclusion | Rationale |
|-----------|-----------|
| New registries or distribution sources | Scope is contract, versioning, and UX only; distribution is a separate initiative |
| New hooks or extension APIs | Plugin contract documentation only; no new extension points |
| Breaking changes to `findExec` / `passthruCommand` / `readPackage` signatures | Public API surface preservation is non-negotiable |
| Removal or renaming of global flags (`--edgerc`, `--section`, `--accountkey`) | Plugins and scripts depend on these flags |
| Changes to `SkipFlagParsing: true` behavior for plugin commands | All installed plugins expect raw args |
| Changes to `createBuiltinCommands` list or registration | Built-ins are part of the documented CLI surface |
| Modification of third-party package behavior (urfave/cli, semver) | Only normal usage allowed |
| Plugin signing or verification | Out of scope for this refactor |
| First-run wizard or new onboarding flows | Out of scope; `firstrun.go` preserved as-is |
| SDK or plugin templates | Not part of this effort |
| New integration tests | Unit tests only; no new integration tests required |
| Test file deletions or coverage reduction | Non-negotiable preservation |
| Changes to the core install mechanism (Git clone, Githubize) | Only versioning/contract documentation changes allowed |
| Changes to `pkg/packages/` language managers | These are stable internal dependencies |
| Changes to `pkg/config/config.go` interface | Stable; reference only |
| Changes to `pkg/terminal/` or `pkg/color/` implementations | Reference only for consistency patterns |
| Changes to `pkg/git/repository.go` interface | Stable; no modifications |
| Changes to `cli/app/firstrun.go` or `cli/app/access_*.go` | Preserve behavior; minor messaging tweaks only if needed |
| Performance optimization beyond documentation | Only document/review; no intentional changes to startup or scanning performance |

## 0.4 Target Design

### 0.4.1 Refactored Structure Planning

The target architecture preserves the existing project layout and adds two new documentation files under a new `docs/` directory. All modifications are in-place within existing files; no files are moved or deleted.

```
Target:
github.com/akamai/cli/v2 (Go 1.24.11, version 2.0.3)
├── go.mod                                    (UNCHANGED)
├── go.sum                                    (UNCHANGED)
├── Makefile                                  (UNCHANGED)
├── .golangci.yaml                            (UNCHANGED)
├── README.md                                 (UPDATE — reference docs/, exit codes, plugin contract)
├── CHANGELOG.md                              (UPDATE — add refactor entry)
├── build.sh                                  (UNCHANGED)
├── LICENSE                                   (UNCHANGED)
├── docs/                                     (NEW directory)
│   ├── plugin-contract.md                    (NEW — plugin contract documentation)
│   └── cli-json-schema.md                    (NEW — cli.json schema documentation)
├── .github/
│   └── workflows/
│       └── checks.yml                        (UNCHANGED)
├── cli/
│   ├── main.go                               (UNCHANGED)
│   └── app/
│       ├── run.go                            (UNCHANGED)
│       ├── run_test.go                       (UNCHANGED)
│       ├── firstrun.go                       (UNCHANGED)
│       ├── firstrun_noinstall.go             (UNCHANGED)
│       ├── access_nix.go                     (UNCHANGED)
│       └── access_windows.go                 (UNCHANGED)
├── pkg/
│   ├── app/
│   │   ├── cli.go                            (UPDATE — Go doc comments for plugin contract)
│   │   └── cli_test.go                       (UNCHANGED)
│   ├── apphelp/
│   │   ├── help.go                           (UPDATE — minor help text improvements)
│   │   ├── help_command.go                   (UPDATE — workflow help text)
│   │   ├── help_command_test.go              (UNCHANGED or minor update)
│   │   └── help_test.go                      (UNCHANGED)
│   ├── commands/
│   │   ├── command.go                        (UPDATE — contract documentation, inline comments)
│   │   ├── command_install.go                (UPDATE — exit codes, error messages, spinner)
│   │   ├── command_install_test.go           (UPDATE — exit code verification tests)
│   │   ├── command_list.go                   (UPDATE — minor UX)
│   │   ├── command_list_test.go              (UNCHANGED or minor update)
│   │   ├── command_search.go                 (UPDATE — minor UX)
│   │   ├── command_search_test.go            (UNCHANGED or minor update)
│   │   ├── command_subcommand.go             (UPDATE — venv prompt UX, documentation)
│   │   ├── command_subcommand_test.go        (UPDATE — new test for prompt behavior)
│   │   ├── command_uninstall.go              (UPDATE — exit codes, error messages)
│   │   ├── command_uninstall_test.go         (UPDATE — exit code verification tests)
│   │   ├── command_update.go                 (UPDATE — exit codes, error messages)
│   │   ├── command_update_test.go            (UPDATE — exit code verification tests)
│   │   ├── command_upgrade.go                (UPDATE — documentation only)
│   │   ├── package_reader.go                 (UPDATE — optional version field)
│   │   ├── package_reader_test.go            (UNCHANGED or minor update)
│   │   ├── subcommands.go                    (UPDATE — schema documentation, optional version handling)
│   │   ├── subcommands_test.go               (UPDATE — version field present/absent tests)
│   │   ├── upgrade.go                        (UPDATE — documentation)
│   │   ├── upgrade_common.go                 (UPDATE — documentation)
│   │   └── (all other files UNCHANGED)
│   ├── version/
│   │   ├── version.go                        (UPDATE — potential compatibility check helper)
│   │   └── version_test.go                   (UPDATE — tests for compatibility logic)
│   └── (all other pkg/ subdirectories UNCHANGED)
```

### 0.4.2 Design Pattern Applications

The refactoring applies the following design patterns, consistent with the existing codebase:

- **Contract Documentation Pattern:** Formalize existing implicit contracts through Go doc comments and external Markdown documentation, tracing every documented behavior to specific code locations
- **Backward-Compatible Extension:** Add optional fields (version in `cli.json`) using the zero-value-as-default pattern — missing fields default to empty strings, which are treated as "compatible"
- **Consistent Error Handling:** Align all command handlers to the named-return-error pattern already used in `command_install.go` (e.g., `(e error)` with deferred logger) and standardized exit codes via `cli.Exit`
- **Sentinel Value Pattern:** Use existing `version.Equals`, `version.Greater`, `version.Smaller`, `version.Error` sentinels for compatibility checking rather than introducing new error types
- **Interface Preservation:** All public interfaces (`Cmd`, `Repository`, `LangManager`, `Terminal`, `Config`) remain unchanged; refactoring operates within existing contracts

### 0.4.3 Key Design Decisions

| Decision | Approach | Rationale |
|----------|----------|-----------|
| `cli.json` schema documentation location | `docs/cli-json-schema.md` (new file) | User explicitly requests `docs/cli-json-schema.md`; `docs/` directory does not exist and will be created |
| Plugin contract documentation location | `docs/plugin-contract.md` (new file) | User explicitly requests `docs/plugin-contract.md` |
| Version field in `cli.json` | Optional; handled in `readPackage`/`readPackageFromGithub` | Missing version must be treated as compatible; no breaking change |
| Exit code standardization | 0 = success, 1 = user error, 2 = system error | Aligns with Unix conventions and user specification; some existing codes (e.g., `-1` in `command_subcommand.go:104`) will be adjusted |
| Python venv prompt improvement | Clearer copy/timing without changing core logic | User requests reducing surprise without removing the prompt |
| Inline documentation scope | All new or modified production code in `pkg/commands/`, `pkg/version/`, `pkg/app/` | User explicitly scopes inline comment requirements |

### 0.4.4 User Interface Design

This refactoring improves the CLI's textual user interface in the following ways:

- **Error message consistency:** All error messages across install/update/uninstall will follow the pattern: `color.RedString("<capitalized message>")` with appropriate exit codes
- **Spinner feedback:** All long operations (install, update, uninstall) will use spinners with consistent start/stop/OK/Fail lifecycle
- **Success/failure summary:** Multi-package install/update operations will display clear summary via `packageListDiff` and `listInstalledCommands`
- **Help text improvements:** Workflow guidance added to help output (e.g., "configure .edgerc then run commands")
- **Python venv prompt:** Clearer messaging when the reinstall prompt appears (e.g., explaining why and what will happen)
- **Color/quiet behavior:** Documented when colors apply (TTY detection via `terminal.IsTTY()`) and when quiet output is used (non-TTY, `AKAMAI_LOG` env var)

## 0.5 Transformation Mapping

### 0.5.1 File-by-File Transformation Plan

The entire refactor is executed in **one phase**. Every target file is mapped to its source file with explicit transformation mode and key changes.

| Target File | Transformation | Source File | Key Changes |
|-------------|---------------|-------------|-------------|
| `docs/plugin-contract.md` | CREATE | `pkg/commands/command_subcommand.go`, `pkg/commands/command.go`, `pkg/app/cli.go` | New documentation: required flags, executable naming (`akamai-<cmd>` / `akamai<Cmd>`), exit code conventions (0/1/2), help behavior, `SkipFlagParsing` contract; traceable to `prepareCommand`, `findExec`, `passthruCommand`, and app flags |
| `docs/cli-json-schema.md` | CREATE | `pkg/commands/subcommands.go`, `pkg/commands/command.go`, `pkg/commands/package_reader.go` | New documentation: formal `cli.json` schema with fields `name`, `version` (optional), `commands[]`, `bin`, `requirements`, `aliases`, `description`, `auto-complete`, `ldflags`; validated against testdata manifests |
| `pkg/commands/subcommands.go` | UPDATE | `pkg/commands/subcommands.go` | Add module-level Go doc comment documenting `cli.json` schema; ensure `readPackage`/`readPackageFromGithub` handle optional version field gracefully (already tolerant due to Go's zero-value JSON unmarshaling); add inline comments explaining what/why for non-obvious logic; optional backward-compatible validation |
| `pkg/commands/command.go` | UPDATE | `pkg/commands/command.go` | Add Go doc comments for `findExec` (discovery algorithm, naming conventions), `passthruCommand` (execution contract, `SkipFlagParsing`, Python venv setup), `CommandLocator`, `createBuiltinCommands`, `createInstalledCommands`, `getPackageBinPaths`; performance annotations for `getPackageBinPaths` (O(n) directory scan); no signature changes |
| `pkg/commands/command_subcommand.go` | UPDATE | `pkg/commands/command_subcommand.go` | Improve Python venv reinstall prompt copy at line 96 (clearer messaging explaining why reinstall is needed); document `prepareCommand` contract (flags passed: `edgerc`, `section`, `accountkey`); add Go doc comments for `cmdSubcommand`, `prepareCommand`, `findFlags` |
| `pkg/commands/command_install.go` | UPDATE | `pkg/commands/command_install.go` | Standardize exit codes: ensure `installPackage` failures use exit code 1 for user errors (e.g., missing repo) and 2 for system errors (e.g., filesystem failures); align error message style using `color.RedString` + `tools.CapitalizeFirstWord`; ensure spinners used for all long operations; add Go doc comments |
| `pkg/commands/command_update.go` | UPDATE | `pkg/commands/command_update.go` | Standardize exit codes (1 for user error, 2 for system error); align error message format with install patterns; add Go doc comments for `cmdUpdate`, `updatePackage`, `updateRepo` |
| `pkg/commands/command_uninstall.go` | UPDATE | `pkg/commands/command_uninstall.go` | Standardize exit codes (1 for user error, 2 for system error); align error message format; add Go doc comments for `cmdUninstall`, `uninstallPackage` |
| `pkg/commands/command_list.go` | UPDATE | `pkg/commands/command_list.go` | Minor output formatting improvements; add Go doc comments |
| `pkg/commands/command_search.go` | UPDATE | `pkg/commands/command_search.go` | Minor UX improvements; add Go doc comments; consistent version display |
| `pkg/commands/package_reader.go` | UPDATE | `pkg/commands/package_reader.go` | Ensure `packageListItem` supports optional version field; add Go doc comments |
| `pkg/app/cli.go` | UPDATE | `pkg/app/cli.go` | Add Go doc comments documenting global flags (`--edgerc`, `--section`, `--accountkey`) as part of plugin contract; document proxy and daemon flags; no code changes |
| `pkg/apphelp/help.go` | UPDATE | `pkg/apphelp/help.go` | Add or adjust help text for common workflows; add Go doc comments |
| `pkg/apphelp/help_command.go` | UPDATE | `pkg/apphelp/help_command.go` | Add workflow guidance in help output (e.g., "configure .edgerc then run"); add Go doc comments |
| `pkg/version/version.go` | UPDATE | `pkg/version/version.go` | Add compatibility-check helper (e.g., `IsCompatible(required, current string) bool`) using existing `Compare`; add Go doc comments; no breaking changes to `Compare` |
| `pkg/commands/upgrade.go` | UPDATE | `pkg/commands/upgrade.go` | Add Go doc comments for `CheckUpgradeVersion`, `checkUpgradeVersion`, `versionProvider`; document version check flow |
| `pkg/commands/upgrade_common.go` | UPDATE | `pkg/commands/upgrade_common.go` | Add Go doc comments for `UpgradeCli`; document upgrade flow and checksum verification |
| `pkg/commands/command_upgrade.go` | UPDATE | `pkg/commands/command_upgrade.go` | Add Go doc comments for `cmdUpgrade` |
| `README.md` | UPDATE | `README.md` | Add references to `docs/plugin-contract.md` and `docs/cli-json-schema.md`; clarify exit code semantics; document env var usage for predictable behavior |
| `CHANGELOG.md` | UPDATE | `CHANGELOG.md` | Add entry documenting plugin contract formalization, versioning, exit code standardization, UX improvements |
| `pkg/commands/subcommands_test.go` | UPDATE | `pkg/commands/subcommands_test.go` | Add test cases: `cli.json` with version field present → parsed correctly; `cli.json` with version field absent → no error, treated as compatible |
| `pkg/commands/command_install_test.go` | UPDATE | `pkg/commands/command_install_test.go` | Add at least one test verifying non-zero exit code on a failure path (e.g., missing repository) |
| `pkg/commands/command_update_test.go` | UPDATE | `pkg/commands/command_update_test.go` | Add at least one test verifying non-zero exit code on a failure path |
| `pkg/commands/command_uninstall_test.go` | UPDATE | `pkg/commands/command_uninstall_test.go` | Add at least one test verifying non-zero exit code on a failure path |
| `pkg/commands/command_subcommand_test.go` | UPDATE | `pkg/commands/command_subcommand_test.go` | Add tests for updated Python venv prompt behavior if messaging changes |
| `pkg/version/version_test.go` | UPDATE | `pkg/version/version_test.go` | Add tests for compatibility-check helper (missing version → compatible, incompatible version) |

### 0.5.2 Cross-File Dependencies

**Import statement updates:**

No import path changes are required. All refactoring is in-place within existing packages. However, the following cross-file dependency relationships must be maintained:

- `pkg/commands/command.go` imports `pkg/version` — used in `passthruCommand` for Python version comparison; any new version check helpers will use the same import
- `pkg/commands/subcommands.go` — if version validation is added, will need to import `pkg/version`
- `pkg/commands/upgrade.go` imports `pkg/version` — `CheckUpgradeVersion` uses `version.Compare` and `version.Version`
- `pkg/commands/command_install.go` imports `pkg/git`, `pkg/packages`, `pkg/terminal`, `pkg/tools`, `pkg/color`, `pkg/log` — all remain unchanged
- All `*_test.go` files use `github.com/stretchr/testify/assert` and `github.com/stretchr/testify/mock` — patterns remain consistent

**Import transformation rules (if version import is added to subcommands.go):**
- FROM: (no `pkg/version` import in `subcommands.go`)
- TO: `"github.com/akamai/cli/v2/pkg/version"` added to `subcommands.go` imports
- Apply to: `pkg/commands/subcommands.go` only

**Configuration updates for new structure:**
- No configuration changes required; `docs/` is a new directory with no build system integration needed
- `go:embed` directives in `pkg/commands/package_reader.go` and `pkg/apphelp/help.go` remain unchanged

### 0.5.3 Wildcard Patterns

Wildcard patterns are used conservatively for file groups:

| Pattern | Transformation | Description |
|---------|---------------|-------------|
| `pkg/commands/command_*.go` | UPDATE | All command handler files — exit code standardization, error message consistency, Go doc comments |
| `pkg/commands/*_test.go` | UPDATE | All test files in pkg/commands — add exit code verification tests, version field tests |
| `docs/*.md` | CREATE | New documentation files for plugin contract and schema |

### 0.5.4 One-Phase Execution

The entire refactor is executed by Blitzy in **one phase**. All files listed in the transformation plan above are included in a single execution. There is no phasing, no week-by-week schedule, and no deferred work.

## 0.6 Dependency Inventory

### 0.6.1 Key Public Packages

All dependencies are already declared in `go.mod` with exact versions. No new dependencies are introduced by this refactor.

| Registry | Package | Version | Purpose |
|----------|---------|---------|---------|
| go modules | `github.com/urfave/cli/v2` | v2.19.3 | CLI framework: app/command/flag parsing, `cli.Exit`, `cli.ActionFunc`, `SkipFlagParsing` |
| go modules | `github.com/Masterminds/semver` | v1.5.0 | Semantic version parsing and comparison (used by `pkg/version/version.go`) |
| go modules | `github.com/fatih/color` | v1.18.0 | Terminal color output (used by `pkg/color/color.go`) |
| go modules | `github.com/briandowns/spinner` | v1.23.2 | Terminal spinner animations (used by `pkg/terminal/spinner.go`) |
| go modules | `github.com/go-git/go-git/v5` | v5.16.4 | Git operations: clone, pull, reset, head (used by `pkg/git/repository.go`) |
| go modules | `github.com/go-ini/ini` | v1.67.0 | INI config file parsing (used by `pkg/config/config.go`) |
| go modules | `github.com/mitchellh/go-homedir` | v1.1.0 | Home directory resolution (used by `pkg/tools/util.go`, `pkg/commands/command_uninstall.go`) |
| go modules | `github.com/AlecAivazis/survey/v2` | v2.3.7 | Interactive prompts and confirmations (used by `pkg/terminal/terminal.go`) |
| go modules | `github.com/mattn/go-colorable` | v0.1.14 | Cross-platform color terminal writer (used by `pkg/terminal/terminal.go`) |
| go modules | `github.com/mattn/go-isatty` | v0.0.20 | TTY detection (used by `pkg/terminal/terminal.go`) |
| go modules | `github.com/inconshreveable/go-update` | v0.0.0-20160112193335-8152e7eb6ccf | Binary self-update (used by `pkg/commands/upgrade_common.go`) |
| go modules | `github.com/kardianos/osext` | v0.0.0-20190222173326-2bc1f35cddc0 | Executable path resolution (used by `pkg/app/cli.go`) |
| go modules | `github.com/stretchr/testify` | v1.11.1 | Testing assertions and mocks (used across all `*_test.go` files) |
| go modules | `golang.org/x/text` | v0.32.0 | Text case transformations (used by `pkg/commands/command.go`, `command_install.go`) |
| go modules | `golang.org/x/sys` | v0.39.0 | System calls for Unix write-access checks (used by `cli/app/access_nix.go`) |

### 0.6.2 Key Private Packages

No private or internal packages are used. The project uses only public Go modules as declared in `go.mod`.

### 0.6.3 Development Toolchain Dependencies

| Tool | Version | Purpose | Source |
|------|---------|---------|--------|
| Go | 1.24.11 | Compiler and runtime | `go.mod` line 3 |
| goimports | v0.24.0 | Code formatting and import organization | `Makefile` line 1 |
| go-junit-report | v2.1.0 | JUnit XML test report generation | `Makefile` line 2 |
| gocov | v1.1.0 | Coverage analysis | `Makefile` line 3 |
| gocov-xml | v1.1.0 | Coverage XML report generation | `Makefile` line 4 |
| golangci-lint | v2.6.1 | Linter aggregation (errcheck, gocyclo, govet, ineffassign, misspell, revive, staticcheck, unused) | `Makefile` line 5, `.golangci.yaml` |

### 0.6.4 Dependency Updates

**No dependency version changes are required for this refactor.** All existing dependencies remain at their current pinned versions.

**Import Refactoring:**

The only potential import change is adding `pkg/version` to `pkg/commands/subcommands.go` if version compatibility checks are added to the `readPackage` function:

- `pkg/commands/subcommands.go` — Potential addition: `"github.com/akamai/cli/v2/pkg/version"`

**External Reference Updates:**

| File | Update Type |
|------|-------------|
| `README.md` | Add references to `docs/plugin-contract.md`, `docs/cli-json-schema.md`; clarify exit codes |
| `CHANGELOG.md` | Add refactor entry with release metadata |

**Build files:** No changes to `go.mod`, `go.sum`, `Makefile`, `build.sh`, `.golangci.yaml`, or `.github/workflows/checks.yml`.

### 0.6.5 Environment Variables

The following environment variables are relevant to this refactor and must be documented:

| Variable | Used In | Purpose |
|----------|---------|---------|
| `AKAMAI_CLI_HOME` | `pkg/tools/util.go` (`GetAkamaiCliPath`) | Override default `~/.akamai-cli` directory |
| `AKAMAI_EDGERC` | `pkg/app/cli.go` (flag `--edgerc`) | Override default `.edgerc` location |
| `AKAMAI_EDGERC_SECTION` | `pkg/app/cli.go` (flag `--section`) | Override default credentials section |
| `AKAMAI_EDGERC_ACCOUNT_KEY` | `pkg/app/cli.go` (flag `--accountkey`) | Account switch key |
| `AKAMAI_CLI` | `cli/app/run.go` | Sentinel: set to "1" when running inside CLI |
| `AKAMAI_CLI_VERSION` | `cli/app/run.go` | Current CLI version string |
| `AKAMAI_CLI_COMMAND` | `pkg/commands/command_subcommand.go` | Current command name |
| `AKAMAI_CLI_COMMAND_VERSION` | `pkg/commands/command_subcommand.go` | Current command version |
| `AKAMAI_LOG` | `pkg/log/log.go` (`SetupContext`) | Log level: error, warn, info, debug |
| `AKAMAI_CLI_LOG_PATH` | `pkg/log/log.go` (`SetupContext`) | File path for log output |
| `AKAMAI_CLI_DAEMON` | `pkg/app/cli.go` (flag `--daemon`) | Enable daemon mode |
| `HTTP_PROXY` / `HTTPS_PROXY` | `pkg/app/cli.go` (`app.Before`) | Proxy configuration |
| `PYTHONUSERBASE` | `pkg/commands/command_subcommand.go` | Python package directory for plugin execution |
| `CLI_REPOSITORY` | `pkg/commands/upgrade.go`, `upgrade_common.go` | Override GitHub repository URL for upgrades |

## 0.7 Refactoring Rules

### 0.7.1 User-Specified Refactoring Rules

The following rules are explicitly emphasized by the user and are **non-negotiable**:

- **Maintain all public API contracts:** The public function signatures of `findExec`, `passthruCommand`, `readPackage`, and the `subcommands`/`command` structs must remain callable by existing callers without modification
- **Preserve all existing functionality:** Discovery, execution, install/update/uninstall, config, first-run, and upgrade flows must behave identically
- **Ensure all tests continue passing:** `go test ./...` must pass with zero new failures before delivery
- **Maintain or exceed baseline test coverage:** Coverage for `pkg/commands` must be ≥ 82.3% (baseline); `pkg/version` must remain at 100%
- **No test files deleted or disabled:** No tests skipped or rewritten in a way that reduces coverage of production code
- **Do NOT change `SkipFlagParsing: true`** for plugin commands or the way args are passed through in `passthruCommand`
- **Do NOT remove or rename global flags** `--edgerc`, `--section`, `--accountkey` or change how they are passed to subcommands via `prepareCommand`
- **Do NOT change built-in command registration** (`createBuiltinCommands` in `command.go`) or the list of built-in commands
- **Do NOT modify third-party package behavior** (urfave/cli, semver) beyond normal usage
- **Do NOT add new external registries or distribution mechanisms**

### 0.7.2 Minimal Change Clause

- Make only the changes necessary to achieve the IN SCOPE tasks
- Do not refactor unrelated code, add features beyond contract/versioning/UX, or optimize performance beyond documentation
- Preserve existing functionality and behavior; do not modify code that is not directly impacted by the refactor
- Isolate new implementations in dedicated files/modules when possible (e.g., schema doc, contract doc, small version-check helper)
- Document all contract- or versioning-related changes with clear comments

### 0.7.3 Backward Compatibility (Booster 1)

Before modifying any public interface, function signature, or data contract:
- Identify all existing callers and consumers
- Preserve all existing behavior that external or internal code depends on
- When a change to a public contract is necessary: add the new version alongside the old, mark the old as deprecated (Go doc deprecation notice), include a migration note, and add an adapter if the old signature must keep working
- Document in code: what is preserved, what is deprecated and what replaces it, how to migrate

**Forbidden:**
- Removing or renaming a public function/type without a deprecated alias
- Breaking signature or return type changes without preserving the old signature
- Changing data formats consumed by other modules without a compatibility layer
- Silently changing behavior

**Validation:** Any change to a public interface must preserve the old contract or provide a deprecated alias with a migration note.

### 0.7.4 Code Style Catalog (Booster 2)

The following code style patterns were cataloged from scanning the existing codebase:

**MUST-Priority Rules (zero tolerance):**

| Pattern | Example Source | Snippet | Rationale |
|---------|--------------|---------|-----------|
| Exported functions have Go doc comments starting with the function name | `pkg/commands/command.go:149` | `// CommandLocator builds a sorted slice...` | Go convention; enforced by `golangci-lint` revive rules |
| Error propagation uses `fmt.Errorf` with `%w` when wrapping | `pkg/commands/subcommands.go:48` | `fmt.Errorf("package does not contain a cli.json file: %v", err)` | Enables `errors.Is`/`errors.As` chains; existing code uses `%v` which should migrate to `%w` for new code |
| Named return errors with deferred logger in command handlers | `pkg/commands/command_install.go:44` | `func(c *cli.Context) (e error) { defer func() { if e == nil ... }() }` | Consistent error logging pattern across all command handlers |
| `cli.Exit` with `color.RedString` for user-facing errors | `pkg/commands/command_install.go:63` | `cli.Exit(color.RedString("You must specify..."), 1)` | Consistent error presentation to users |
| `tools.CapitalizeFirstWord` for error message capitalization | `pkg/commands/command_install.go:199` | `tools.CapitalizeFirstWord(err.Error())` | Ensures first character is uppercase in user-facing errors |

**SHOULD-Priority Rules (document reason if deviating):**

| Pattern | Example Source | Snippet | Rationale |
|---------|--------------|---------|-----------|
| Error handling in commands: named return + deferred logger + `cli.Exit` | `pkg/commands/command_install.go:44-60` | Deferred function checks `e` and logs appropriately | Consistent lifecycle logging across all command actions |
| Test function naming: `Test<CmdOrFunctionName>` or `Test<CmdOrFunctionName>_<Scenario>` | `pkg/commands/command_install_test.go` | `TestCmdInstall`, `TestCmdListWithRemote` | Aligns new tests with existing naming conventions |
| Table-driven subtests with descriptive case names | `pkg/version/version_test.go` | `for name, tc := range tests { t.Run(name, ...) }` | Consistent test structure throughout the codebase |
| Spinner lifecycle: `Start` → operation → `OK()`/`Fail()`/`WarnOK()` | `pkg/commands/command_install.go:153-168` | `spin.Start(...)` → operation → `spin.OK()` | Consistent UX feedback for all long operations |
| Context-aware logging via `log.FromContext` | `pkg/commands/command_install.go:48` | `logger := log.FromContext(c.Context)` | All command handlers use context-based logger |
| Time tracking with deferred debug log | `pkg/commands/command_install.go:45-59` | `start := time.Now()` → `defer { logger.Debug(fmt.Sprintf("... %v", time.Since(start))) }` | Performance visibility in debug logs |

**Forbidden:**
- Violating a MUST rule
- Introducing a new architectural pattern where the catalog already covers the case
- Deviating from SHOULD without a documented code comment reason

### 0.7.5 Code Documentation (Booster 3)

**Scope:** All new or modified production code in `pkg/commands/`, `pkg/version/`, `pkg/app/` files. Exclude test files from heavy comment changes except where documenting contract.

**Requirements:**
- Every new or modified exported function/symbol must have a Go doc comment (purpose, parameters, returns, errors where applicable)
- Inline comments explain **why**, not just what; avoid restating the code
- Go convention: comment starts with symbol name
- Line length and style consistent with the project

**Forbidden:**
- Comments that only restate the code
- Docstrings that omit purpose, parameters, or return values
- Leaving non-obvious decisions undocumented

### 0.7.6 Risk Assessment (Booster 4)

| Risk | Severity | Category | What Could Go Wrong | Affected Systems | Mitigation | Rollback |
|------|----------|----------|--------------------|--------------------|------------|----------|
| Exit code changes break existing scripts | HIGH | Breaking Change | Scripts relying on current exit codes (e.g., `-1` from `command_subcommand.go:104`) may behave differently | External scripts, CI pipelines using Akamai CLI | Document exit code changes in CHANGELOG; standardize to 0/1/2 which is more predictable; existing code uses inconsistent codes already | Revert exit code changes; restore original values |
| `cli.json` version field parsing breaks existing plugins | HIGH | Breaking Change | Existing `cli.json` files with unexpected fields could cause parse errors | All installed plugins | Go's `json.Unmarshal` already ignores unknown fields; version field is optional; test with existing testdata manifests | Remove version field handling; revert `readPackage` changes |
| Version compatibility check rejects valid plugins | HIGH | Breaking Change | Overly strict version check could reject plugins that should work | Plugin users | Missing version field → treat as compatible (default); version check only warns, does not block | Disable version checks; revert to current behavior |
| Python venv prompt change confuses users | MEDIUM | UX | Different prompt wording may confuse users who memorized the old prompt | Python plugin users | Only change copy/timing, not core logic; test manually | Revert to original prompt text |
| Documentation references become stale | LOW | Operational | Code changes without doc updates create inconsistency | Developers, plugin authors | Docs are created in same refactor phase; CI does not validate docs vs code | Update docs separately |
| Test coverage drops below baseline | MEDIUM | Testing | New code not adequately tested could reduce coverage | Code quality, CI pipeline | Run `go test -cover` after every change; baseline coverage: 82.3% for `pkg/commands` | Revert changes that reduce coverage |

**Summary:** 2 HIGH, 2 MEDIUM, 1 LOW severity risks identified. All HIGH risks have clear mitigation (backward-compatible defaults, Go's JSON flexibility) and rollback paths. Affected systems: external plugin ecosystem, CI pipelines, Python plugin users.

### 0.7.7 Performance Impact Analysis (Booster 5)

| Component | Operation | Current Complexity | Impact of Refactor | Notes |
|-----------|-----------|-------------------|-------------------|-------|
| `getPackageBinPaths()` | Scan `~/.akamai-cli/src/*` and `*/bin` | O(n) where n = number of installed packages | No change | Performance-critical path; called by `findExec` for every plugin command invocation |
| `createInstalledCommands()` | Iterate package paths, call `readPackage` for each | O(n) where n = number of installed packages | No change; if version check added, adds O(1) per package | Called once at startup via `CommandLocator` |
| `findExec()` | Scan package bin paths, glob for matching executables | O(n × m) where n = packages, m = bin patterns per package | No change | Called for every plugin command invocation; performance-critical |
| `readPackage()` | Read and unmarshal single `cli.json` file | O(1) — single file read + JSON parse | Minimal: optional version field check adds O(1) comparison | Called per package during discovery |
| `readPackageFromGithub()` | HTTP GET + JSON unmarshal | O(1) — network-bound | No change | Called during install/update only |
| `passthruCommand()` | Single subprocess invocation | O(1) — delegates to `exec.Cmd.Run()` | No change | Core execution path; no refactoring impact |
| `searchPackages()` | Nested loop: packages × commands × keywords | O(p × c × k) where p = packages, c = commands per package, k = keywords | No change | Bounded by embedded `package-list.json` (25 packages) |
| Version compatibility check (new) | `version.Compare` for two semver strings | O(1) — string parse + comparison | New O(1) operation per package during `readPackage` if version present | Only when version field is present; missing version skips check |

**Performance-critical paths requiring inline annotations:**
- `getPackageBinPaths()` — O(n) directory scan; called on every plugin invocation
- `findExec()` — O(n × m) glob-based discovery; called on every plugin invocation
- `createInstalledCommands()` — O(n) package scanning; called once at startup

**Trade-off decisions:**
- Adding version checks in `readPackage` adds negligible O(1) overhead per package but provides valuable compatibility safety; acceptable trade-off
- No new scans or loops introduced; all refactoring operates within existing algorithmic bounds

### 0.7.8 Error Handling and Edge Cases

- **Test failure after a refactoring step:** Roll back that step, document why it failed, and try an alternative that preserves exact behavior
- **Coverage drop:** If coverage for `pkg/commands` (or modified packages) decreases, roll back changes that reduced coverage and investigate
- **Schema/version edge cases:** If a `cli.json` has no version field, treat as compatible; do not fail or change behavior. When adding version checks, ensure existing packages without the field continue to work
- **Rollback discipline:** Proceed in small steps (one module or logical change at a time). Each step must pass the full test suite before the next. If a step introduces failures or behavioral changes, roll back, document, and try an alternative or mark as out of scope

### 0.7.9 Validation Gates

The following validation gates must all pass for the refactor to be considered complete:

| Gate | Verification Method | Baseline |
|------|-------------------|----------|
| Exit code standardization | All install/update/uninstall commands exit 0 on success, non-zero on failure | Current: mixed exit codes |
| Test suite pass | `go test ./...` passes with zero new failures | Baseline: all packages pass |
| Backward compatibility | All public function signatures unchanged; existing test suite passes unmodified | Baseline: current signatures |
| `cli.json` schema documentation | `docs/cli-json-schema.md` exists with fields: `name`, `version`, `commands[]`, `bin`, `requirements` | New file |
| Plugin contract documentation | `docs/plugin-contract.md` exists, traceable to code | New file |
| Test coverage | `pkg/commands` coverage ≥ 82.3%; `pkg/version` coverage = 100% | Baseline: 82.3%, 100% |
| No test deletions | No test files deleted or disabled | Baseline: all test files present |

## 0.8 References

### 0.8.1 Codebase Files and Folders Searched

The following exhaustive inventory lists every file and folder inspected during analysis to derive the conclusions in this Agent Action Plan:

**Root-level files:**
- `go.mod` — Module definition, Go version (1.24.11), all direct and indirect dependencies with exact versions
- `go.sum` — Dependency checksums for reproducible builds
- `Makefile` — Build/test/lint/coverage/fmt targets with pinned tool versions
- `.golangci.yaml` — Linter configuration (errcheck, gocyclo, govet, revive, staticcheck, unused)
- `README.md` — User-facing documentation, installation, usage, commands, `cli.json` schema, exit codes
- `CHANGELOG.md` — Release history from 2.0.3 back to 1.3.0
- `build.sh` — Cross-platform binary build script
- `LICENSE` — Apache 2.0 license

**CI/CD:**
- `.github/workflows/checks.yml` — GitHub Actions CI: build, test, lint on Go version from `go.mod`

**CLI entry point and runtime:**
- `cli/main.go` — Binary entry point delegating to `cli/app`
- `cli/app/run.go` — Launcher: context setup, config, collision detection, upgrade checks
- `cli/app/run_test.go` — Collision/duplicate detection tests
- `cli/app/firstrun.go` — Interactive onboarding (build tag `!nofirstrun`)
- `cli/app/firstrun_noinstall.go` — No-op onboarding (build tag `nofirstrun`)
- `cli/app/access_nix.go` — Unix write-access check
- `cli/app/access_windows.go` — Windows stub

**Core packages (full contents inspected):**
- `pkg/app/cli.go` — `CreateApp`, global flags, proxy, daemon mode
- `pkg/app/cli_test.go` — Flag/builder tests
- `pkg/apphelp/help.go` — Help template setup, `SetTemplates`, `makePrintHelp`
- `pkg/apphelp/help_command.go` — Help command routing
- `pkg/apphelp/help_command_test.go` — Help routing tests
- `pkg/apphelp/help_test.go` — Setup regression test
- `pkg/color/color.go` — Styled output helpers (RedString, GreenString, etc.)
- `pkg/commands/command.go` — `CommandLocator`, `findExec`, `passthruCommand`, `createBuiltinCommands`, `createInstalledCommands`, `getPackageBinPaths`, `command`/`Command`/`Cmd` types
- `pkg/commands/command_config.go` — Config subcommand handlers (summary inspected)
- `pkg/commands/command_install.go` — `cmdInstall`, `installPackage`, `packageListDiff`, `installPackageDependencies`, `installPackageBinaries`
- `pkg/commands/command_list.go` — `cmdList`, `cmdListWithPackageReader`, `listInstalledCommands`
- `pkg/commands/command_search.go` — `cmdSearch`, `searchPackages`, `getLatestVersion`, `getVersionFromSystem`, `CLI`/`CommandObject` types
- `pkg/commands/command_subcommand.go` — `cmdSubcommand`, `prepareCommand`, `findFlags`, `containsString`
- `pkg/commands/command_uninstall.go` — `cmdUninstall`, `uninstallPackage`
- `pkg/commands/command_update.go` — `cmdUpdate`, `updatePackage`, `updateRepo`
- `pkg/commands/command_upgrade.go` — `cmdUpgrade` (build tag `!noautoupgrade`)
- `pkg/commands/command_upgrade_noop.go` — No-op upgrade (build tag `noautoupgrade`)
- `pkg/commands/constants.go` — `sleep24HDuration`
- `pkg/commands/mocks.go` — `MockCmd`, `mockPackageReader`
- `pkg/commands/package_reader.go` — `packageReader`, `pkgReader`, `packageList`, `packageListItem`, `requirements`
- `pkg/commands/subcommands.go` — `subcommands`/`command` structs, `readPackage`, `readPackageFromGithub`, `getPackagePaths`, `isBinary`, `findPackageDir`, `downloadBin`
- `pkg/commands/upgrade.go` — `CheckUpgradeVersion`, `checkUpgradeVersion`, `versionProvider`, `defaultVersionProvider` (build tag `!noautoupgrade`)
- `pkg/commands/upgrade_common.go` — `UpgradeCli`
- `pkg/commands/package_list/package-list.json` — Embedded package catalog (25 packages, summary inspected)
- `pkg/commands/testdata/` — Test fixtures: `repo/cli.json`, `.akamai-cli/src/cli-echo/cli.json`, and other fixture manifests (summaries and contents inspected)
- `pkg/config/config.go` — `Config` interface, `IniConfig`, `Save`, `Values`, `ExportEnv`, migration (summary inspected)
- `pkg/config/mock.go` — Config mock (summary inspected)
- `pkg/git/repository.go` — `Repository` interface, `Clone`, `Pull`, `Open`, `Head`, `Reset` (summary inspected)
- `pkg/git/mock.go` — `MockRepo` (summary inspected)
- `pkg/log/context.go` — `NewContext`, `FromContext` (summary inspected)
- `pkg/log/handler.go` — Custom `slog.Handler` (summary inspected)
- `pkg/log/log.go` — `SetupContext`, `WithCommand`, `WithCommandContext` (summary inspected)
- `pkg/packages/package.go` — `LangManager` interface, `LanguageRequirements`, sentinel errors (summary inspected)
- `pkg/packages/mock.go` — LangManager mock (summary inspected)
- `pkg/terminal/terminal.go` — `Terminal`, `DefaultTerminal`, `IsTTY`, `Confirm`, `Prompt` (summary inspected)
- `pkg/terminal/spinner.go` — `Spinner` interface, `DefaultSpinner`, `StandardSpinner`, status constants (summary inspected)
- `pkg/terminal/mock.go` — Terminal mock (summary inspected)
- `pkg/tools/util.go` — `GetAkamaiCliPath`, `GetAkamaiCliSrcPath`, `GetAkamaiCliVenvPath`, `GetPkgVenvPath`, `Githubize`, `Self`, `CapitalizeFirstWord`, `InsertAfterNthWord`
- `pkg/tools/files.go` — `MoveFile` (summary inspected)
- `pkg/version/version.go` — `Version` (2.0.3), `Compare`, sentinel constants (`Equals`, `Error`, `Greater`, `Smaller`)
- `pkg/version/version_test.go` — Table-driven version comparison tests (summary inspected)

### 0.8.2 Technical Specification Sections Referenced

- **1.1 Executive Summary** — Project overview, CLI version 2.0.3, Go 1.24.11, Apache 2.0, plugin architecture
- **3.1 Programming Languages** — Go toolchain version, build tags, standard library usage, plugin language support

### 0.8.3 Environment Setup Verification

| Check | Result |
|-------|--------|
| Go version installed | 1.24.11 (matches `go.mod`) |
| `go build ./...` | PASS |
| `go test ./...` | PASS — all 12 test packages pass |
| `pkg/commands` coverage | 82.3% (baseline captured) |
| `pkg/version` coverage | 100.0% (baseline captured) |
| `pkg/app` coverage | 62.3% (baseline captured) |
| `pkg/apphelp` coverage | 81.7% (baseline captured) |
| `docs/` directory | Does not exist yet (will be created) |

### 0.8.4 Attachments

No external attachments, Figma URLs, or external design files were provided for this project.

### 0.8.5 External References

- Repository: `github.com/akamai/cli` (source codebase)
- External service integrations (unchanged): GitHub (install/update), `developer.akamai.com` (search)
- Framework documentation: `github.com/urfave/cli/v2` (CLI framework)
- Semantic versioning: `github.com/Masterminds/semver` (version comparison)

