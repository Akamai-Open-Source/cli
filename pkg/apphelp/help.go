// Package apphelp customizes the Akamai CLI's help experience by overriding
// urfave/cli's default templates, help printer, and help command. It provides
// branded help templates (app-level, command-level, and subcommand-level),
// color-aware output, global flag visibility in help pages, and unified help
// command routing for both built-in and plugin commands.
//
// Templates are loaded from the embedded templates/ directory via go:embed at
// package init time, ensuring consistent help output across all distributions
// without requiring external file access at runtime.
//
// Setup is the main entry point, called during CLI bootstrap from
// pkg/app/cli.go. The plugin contract including help behavior is documented
// in docs/plugin-contract.md.
package apphelp

import (
	"embed"
	"errors"
	"io"
	"text/template"

	"github.com/akamai/cli/v2/pkg/autocomplete"
	"github.com/akamai/cli/v2/pkg/tools"

	"github.com/akamai/cli/v2/pkg/color"
	"github.com/urfave/cli/v2"
)

var (
	// Templates are embedded at compile time to ensure consistent help output
	// across all distributions without requiring external file access at runtime.
	//go:embed templates/*
	files embed.FS

	// SimplifiedHelpTemplate is the help template with simplified usage and
	// excluded global flags. It is used for the built-in "help" command itself
	// and for commands that opt into simplified display. This prevents recursive
	// global flag display when viewing help for the help command.
	SimplifiedHelpTemplate string

	// ErrReadingTemplateFile is returned when reading a help template fails.
	// This error causes a panic during init or Setup because missing templates
	// indicate a broken build — the CLI cannot render any help without them.
	ErrReadingTemplateFile = errors.New("could not read help template file")
)

// init preloads the simplified command help template from the embedded
// filesystem at package initialization time. It panics on failure because the
// simplified template is required for the help command itself; a missing
// template indicates a corrupted build and the CLI cannot function without it.
// This runs before Setup and makes SimplifiedHelpTemplate available for
// immediate use.
func init() {
	tmpl, err := files.ReadFile("templates/simplified_command_help.tmpl")
	if err != nil {
		panic(ErrReadingTemplateFile)
	}
	SimplifiedHelpTemplate = string(tmpl)
}

// Setup is the main entry point for help system initialization, called from
// pkg/app/cli.go during CLI bootstrap. It sets up custom help outputs and
// uniforms behavior of the help flag and command.
//
// Default help command and flag from urfave/cli have some known discrepancies
// (https://github.com/urfave/cli/issues/557) which are removed by the custom
// help command that is added by this function. Help flag needs to be added
// manually as it is not appended by the library in case of a custom help
// command being specified.
//
// Setup performs the following steps:
//  1. Calls SetTemplates to load all branded help templates and install the
//     custom printer.
//  2. Appends cli.HelpFlag manually because urfave/cli does not auto-attach
//     it when a custom help command is present.
//  3. Replaces all app commands with a single "help" command (argsUsage:
//     "[command] [sub-command]") that routes between built-in and plugin help
//     via cmdHelp. The help command uses SimplifiedHelpTemplate for its own
//     help display.
//  4. Sets autocomplete.Default for bash completion support on the help
//     command.
//
// After configuring your .edgerc credentials file, use "akamai help <command>"
// for command-specific usage information. See docs/plugin-contract.md for the
// full plugin contract.
//
// The function MUST be called after the app is created but before command
// registration. The app parameter is the CLI application to configure.
func Setup(app *cli.App) {
	SetTemplates(app.Flags)
	app.Flags = append(app.Flags, cli.HelpFlag)
	app.Commands = []*cli.Command{
		{
			Name:               "help",
			ArgsUsage:          "[command] [sub-command]",
			Description:        "Displays help information for commands. For more details, see docs/plugin-contract.md",
			Action:             cmdHelp,
			CustomHelpTemplate: SimplifiedHelpTemplate,
			BashComplete:       autocomplete.Default,
		},
	}
}

// SetTemplates loads branded help templates for app-level, command-level, and
// subcommand-level help output from the embedded filesystem, overriding
// urfave/cli's defaults. It configures the following:
//
//  1. cli.AppHelpTemplate — from templates/app_help.tmpl (used by "akamai help")
//  2. cli.CommandHelpTemplate — from templates/command_help.tmpl (used by
//     "akamai help <command>")
//  3. cli.SubcommandHelpTemplate — from templates/subcommand_help.tmpl (used by
//     "akamai help <command> <subcommand>")
//  4. cli.HelpPrinter — custom printer via makePrintHelp with color functions
//     and global flag injection
//
// It panics on read failure because templates are embedded at compile time and
// should always be available; failure indicates a corrupted build.
//
// The globalFlags parameter specifies the app-level flags to display in help
// output (e.g., --edgerc, --section, --accountkey).
func SetTemplates(globalFlags []cli.Flag) {
	tmpl, err := files.ReadFile("templates/app_help.tmpl")
	if err != nil {
		panic(ErrReadingTemplateFile)
	}
	cli.AppHelpTemplate = string(tmpl)

	tmpl, err = files.ReadFile("templates/command_help.tmpl")
	if err != nil {
		panic(ErrReadingTemplateFile)
	}
	cli.CommandHelpTemplate = string(tmpl)

	tmpl, err = files.ReadFile("templates/subcommand_help.tmpl")
	if err != nil {
		panic(ErrReadingTemplateFile)
	}
	cli.SubcommandHelpTemplate = string(tmpl)

	cli.HelpPrinter = makePrintHelp(globalFlags)
}

// makePrintHelp creates a closure that serves as the custom help printer for
// all Akamai CLI help output, matching urfave/cli's HelpPrinter signature
// func(io.Writer, string, interface{}).
//
// The closure provides the following capabilities:
//   - Color functions mapped to template names: "blue" (BlueString), "green"
//     (GreenString), "hiBlack" (HiBlackString), "yellow" (YellowString) — used
//     in templates/*.tmpl for branded output.
//   - String utility: "insertString" (InsertAfterNthWord) — used by templates
//     to insert "[global flags]" into usage lines.
//   - Global flags injection: Wraps the data in a helpData struct that bundles
//     GlobalFlags ([]cli.Flag) and Command (interface{}) so templates can
//     access both the command data and the app-level flags.
//
// This custom printer exists because urfave/cli's default printer lacks color
// support and global flag visibility in command help; this printer adds both.
//
// The globalFlags parameter specifies the app-level flags to include in every
// help page. The color functions rely on pkg/color which respects TTY
// detection — colors are automatically disabled for non-TTY output.
func makePrintHelp(globalFlags []cli.Flag) func(io.Writer, string, interface{}) {
	type helpData struct {
		GlobalFlags []cli.Flag
		Command     interface{}
	}
	return func(out io.Writer, templ string, data interface{}) {
		funcMap := template.FuncMap{
			"blue":         color.BlueString,
			"green":        color.GreenString,
			"hiBlack":      color.HiBlackString,
			"yellow":       color.YellowString,
			"insertString": tools.InsertAfterNthWord,
		}

		hData := helpData{
			GlobalFlags: globalFlags,
			Command:     data,
		}

		cli.HelpPrinterCustom(out, templ, hData, funcMap)
	}
}
