//go:build !noautoupgrade

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
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/akamai/cli/v2/pkg/color"
	"github.com/akamai/cli/v2/pkg/config"
	"github.com/akamai/cli/v2/pkg/log"
	"github.com/akamai/cli/v2/pkg/terminal"
	"github.com/akamai/cli/v2/pkg/version"
)

// versionProvider abstracts the source of CLI version information for testing.
// It provides methods to get the latest released version from the remote repository
// and the currently running version. The default implementation (defaultVersionProvider)
// queries GitHub releases and reads from version.Version respectively.
type versionProvider interface {
	getLatestReleaseVersion(ctx context.Context) string
	getCurrentVersion() string
}

// CheckUpgradeVersion checks whether a newer version of the Akamai CLI is available.
// It delegates to checkUpgradeVersion with the default version provider that queries
// GitHub releases. The force parameter bypasses the 24-hour throttle stored in config.
//
// Returns the latest version string if an upgrade is available or the current version
// if already up-to-date. Returns an empty string if the check is skipped (non-TTY,
// throttled, or upgrade checks disabled via config "ignore" setting).
func CheckUpgradeVersion(ctx context.Context, force bool) string {
	return checkUpgradeVersion(ctx, force, defaultVersionProvider{})
}

// checkUpgradeVersion implements the version check logic with an injected versionProvider
// for testability. The check flow:
//
//  1. Skip if not running in a TTY (non-interactive environments).
//  2. Read "cli.last-upgrade-check" from config. Skip if "ignore" (unless forced).
//  3. If forced, "never", or last check was >24 hours ago: proceed with check.
//  4. Save the current timestamp to config for throttling future checks.
//  5. Compare current version against latest using version.Compare:
//     - Smaller: newer version available → prompt user and return latest version
//     - Equals: up-to-date → return current version (caller detects no-op)
//     - Greater/Error: no action → return empty string
func checkUpgradeVersion(ctx context.Context, force bool, provider versionProvider) string {
	term := terminal.Get(ctx)
	cfg := config.Get(ctx)
	logger := log.FromContext(ctx)

	// Skip upgrade checks in non-interactive environments (pipes, CI) since the user
	// cannot respond to the upgrade prompt.
	if !term.IsTTY() {
		return ""
	}

	logger.Debug("Checking for upgrades")

	// "ignore" disables upgrade checks entirely unless --force is used.
	// "never" means no check has been performed yet → always check.
	data, _ := cfg.GetValue("cli", "last-upgrade-check")
	data = strings.TrimSpace(data)
	if data == "ignore" && !force {
		logger.Error("Upgrade checks are disabled")
		return ""
	}

	var checkForUpgrade bool
	if data == "never" || force {
		checkForUpgrade = true
	}

	// Parse the last upgrade check timestamp. If the check was performed less than
	// 24 hours ago, skip to avoid excessive GitHub API calls.
	if !checkForUpgrade {
		configValue := strings.TrimPrefix(strings.TrimSuffix(data, "\""), "\"")
		lastUpgrade, err := time.Parse(time.RFC3339, configValue)
		if err != nil {
			logger.Error(fmt.Sprintf("Error parsing last upgrade check time: %v", err))
			return ""
		}

		currentTime := time.Now()
		if lastUpgrade.Add(sleep24HDuration).Before(currentTime) {
			checkForUpgrade = true
		}
	}

	if checkForUpgrade {
		cfg.SetValue("cli", "last-upgrade-check", time.Now().Format(time.RFC3339))
		err := cfg.Save(ctx)
		if err != nil {
			logger.Error(fmt.Sprintf("Error saving config: %v", err))
			return ""
		}

		// Compare versions using semver. If the current version is older (Smaller),
		// prompt the user to upgrade. If equal, return the version so the caller can
		// determine it's a no-op without re-checking.
		latestVersion := provider.getLatestReleaseVersion(ctx)
		currentVersion := provider.getCurrentVersion()
		comp := version.Compare(currentVersion, latestVersion)
		if comp == version.Smaller {
			term.Spinner().Stop(terminal.SpinnerStatusOK)
			_, _ = term.Writeln("You can find more details about the new version here: https://github.com/akamai/cli/releases")
			if answer, err := term.Confirm(fmt.Sprintf(
				"New update found: %s. You are running: %s. Upgrade now?",
				color.BlueString("%s", latestVersion),
				color.BlueString("%s", currentVersion),
			), true); err != nil || !answer {
				logger.Error(fmt.Sprintf("Upgrade declined: %v", err))
				return ""
			}
			return latestVersion
		}
		if comp == version.Equals {
			// A non-empty version is returned but the caller checks whether latest == current
			// and does not perform an upgrade in such case.
			return currentVersion
		}
	}

	return ""
}

// defaultVersionProvider implements versionProvider by querying the GitHub releases API
// for the latest version and reading the compiled-in version constant.
type defaultVersionProvider struct{}

// getLatestReleaseVersion queries the GitHub releases API for the latest release tag.
// It uses a HEAD request to /releases/latest and extracts the version from the redirect
// Location header. Returns "0" on any error (network failure, non-302 response).
// The CLI_REPOSITORY environment variable can override the default GitHub URL.
func (p defaultVersionProvider) getLatestReleaseVersion(ctx context.Context) string {
	logger := log.FromContext(ctx)
	client := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	repo := "https://github.com/akamai/cli"
	if r := os.Getenv("CLI_REPOSITORY"); r != "" {
		repo = r
	}
	resp, err := client.Head(fmt.Sprintf("%s/releases/latest", repo))
	if err != nil {
		logger.Error(fmt.Sprintf("Error checking for latest version: %v", err))
		return "0"
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logger.Error(fmt.Sprintf("Error closing response body: %v", err))
		}
	}()

	if resp.StatusCode != http.StatusFound {
		logger.Error(fmt.Sprintf("Error checking for latest version: %s", resp.Status))
		return "0"
	}

	location := resp.Header.Get("Location")
	latestVersion := filepath.Base(location)

	return latestVersion
}

// getCurrentVersion returns the compiled-in CLI version string from version.Version.
func (p defaultVersionProvider) getCurrentVersion() string {
	return version.Version
}
