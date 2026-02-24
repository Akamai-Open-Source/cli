package commands

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"text/template"
	"time"

	"github.com/akamai/cli/v2/pkg/color"
	"github.com/akamai/cli/v2/pkg/log"
	"github.com/akamai/cli/v2/pkg/packages"
	"github.com/akamai/cli/v2/pkg/terminal"
	"github.com/inconshreveable/go-update"
	"github.com/urfave/cli/v2"
)

// UpgradeCli pulls the latest released CLI binary from GitHub and performs an in-place
// upgrade of the current executable. The upgrade process:
//
//  1. Constructs a download URL using the command template with version, OS, architecture,
//     and binary suffix placeholders (e.g., akamai-2.0.3-linuxamd64).
//  2. Downloads the binary from the constructed URL.
//  3. Fetches and verifies the SHA-256 checksum from a corresponding .sig file.
//  4. Applies the update atomically using go-update, which replaces the current executable
//     with the downloaded binary after checksum verification.
//  5. On success, re-launches the CLI with the original arguments via passthruCommand
//     to allow the upgraded binary to take over execution.
//
// The CLI_REPOSITORY environment variable can override the default GitHub repository URL
// (https://github.com/akamai/cli) for testing or private deployments.
//
// Exit codes on failure: 1 for all error conditions (template error, download failure,
// checksum mismatch, rollback failure). Uses color.RedString for user-facing error messages.
//
// Parameters:
//   - ctx: context carrying terminal and logging configuration
//   - latestVersion: the version string to download (e.g., "2.1.0")
func UpgradeCli(ctx context.Context, latestVersion string) (e error) {
	term := terminal.Get(ctx)
	logger := log.FromContext(ctx)
	start := time.Now()

	term.Spinner().Start("Upgrading Akamai CLI")
	defer func() {
		if e == nil {
			term.Spinner().OK()
			logger.Debug(fmt.Sprintf("UPGRADE FINISH: %v", time.Since(start)))
		} else {
			term.Spinner().Fail()
			logger.Error(fmt.Sprintf("UPGRADE ERROR: %v", e))
		}
	}()

	// Allow overriding the default GitHub repository URL via CLI_REPOSITORY env var.
	// This enables testing against staging releases or internal mirrors.
	repo := "https://github.com/akamai/cli"
	if r := os.Getenv("CLI_REPOSITORY"); r != "" {
		repo = r
	}

	// Build a command struct with version, architecture, and OS info for URL templating.
	// The URL template follows the pattern: <repo>/releases/download/<version>/akamai-<version>-<os><arch><suffix>
	cmd := command{
		Version: latestVersion,
		Bin:     fmt.Sprintf("%s/releases/download/{{.Version}}/akamai-{{.Version}}-{{.OS}}{{.Arch}}{{.BinSuffix}}", repo),
		Arch:    runtime.GOARCH,
		OS:      runtime.GOOS,
	}

	if runtime.GOOS == "darwin" {
		cmd.OS = "mac"
	}

	if runtime.GOOS == "windows" {
		cmd.BinSuffix = ".exe"
	}

	// Execute the URL template to produce the final download URL. The template supports
	// {{.Version}}, {{.OS}}, {{.Arch}}, and {{.BinSuffix}} placeholders.
	t := template.Must(template.New("url").Parse(cmd.Bin))
	buf := &bytes.Buffer{}
	if err := t.Execute(buf, cmd); err != nil {
		return cli.Exit(color.RedString("Templating error: %s", err), 1)
	}

	resp, err := http.Get(buf.String())
	if err != nil || resp.StatusCode != http.StatusOK {
		logger.Error(fmt.Sprintf("Unable to get release: %s", err))
		var reason string
		if err == nil {
			reason = fmt.Sprintf("%s: %s", buf.String(), resp.Status)
		} else {
			reason = err.Error()
		}
		return cli.Exit(color.RedString("Unable to download release: %s. Please try again.", reason), 1)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logger.Error(err.Error())
		}
	}()

	// Fetch the .sig file containing the expected SHA-256 checksum for the downloaded binary.
	// The checksum is stored as a hex-encoded string and decoded for verification by go-update.
	shaURL := fmt.Sprintf("%v%v", buf.String(), ".sig")
	shaResp, err := http.Get(shaURL)
	if err != nil || shaResp.StatusCode != http.StatusOK {
		var reason string
		if err == nil {
			reason = fmt.Sprintf("%s: %s", shaURL, shaResp.Status)
		} else {
			reason = err.Error()
		}
		return cli.Exit(color.RedString("Unable to retrieve signature for verification: %s. Please try again.", reason), 1)
	}
	defer func() {
		if err := shaResp.Body.Close(); err != nil {
			logger.Error(err.Error())
		}
	}()

	shaBody, err := io.ReadAll(shaResp.Body)
	if err != nil {
		return cli.Exit(color.RedString("Unable to retrieve signature for verification: %s. Please try again.", err.Error()), 1)
	}

	shaSum, err := hex.DecodeString(strings.TrimSpace(string(shaBody)))
	if err != nil {
		return cli.Exit(color.RedString("Unable to retrieve signature for verification: %s. Please try again.", err.Error()), 1)
	}

	selfPath := os.Args[0]

	// Atomically replace the current executable with the downloaded binary. go-update handles
	// the platform-specific mechanics (rename, permission preservation). On failure, it attempts
	// to roll back to the previous binary.
	err = update.Apply(resp.Body, update.Options{TargetPath: selfPath, Checksum: shaSum})
	if err != nil {
		if rerr := update.RollbackError(err); rerr != nil {
			return cli.Exit(color.RedString("Unable to install or rollback: %s. Please re-install.", rerr.Error()), 1)
		}
		if strings.HasPrefix(err.Error(), "Updated file has wrong checksum.") {
			return cli.Exit(color.RedString("Checksums do not match: %s. Please try again.", err.Error()), 1)
		}
		return cli.Exit(color.RedString("Unable to upgrade: %s", err), 1)
	}

	// Re-execute the CLI with the original arguments using passthruCommand. This allows the
	// newly installed binary to handle the --version flag and display the updated version.
	os.Args[0] = selfPath
	subCmd := createCommand(os.Args[0], os.Args[1:])
	return passthruCommand(ctx, subCmd, packages.NewLangManager(), packages.LanguageRequirements{}, selfPath)
}
