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
	"fmt"
	"os"
	"time"

	"github.com/akamai/cli/v2/pkg/color"
	"github.com/akamai/cli/v2/pkg/log"
	"github.com/akamai/cli/v2/pkg/terminal"
	"github.com/akamai/cli/v2/pkg/version"
	"github.com/urfave/cli/v2"
)

// cmdUpgrade handles the "akamai upgrade" command. It checks for a newer CLI version
// by calling CheckUpgradeVersion with force=true (bypassing the 24-hour throttle), then
// compares the latest version against the current version.Version. If an upgrade is
// available, it delegates to UpgradeCli to download and apply the update. If already
// up-to-date, it displays the current version.
//
// The upgrade flow: spinner start → CheckUpgradeVersion → version comparison →
// UpgradeCli (if newer) or display current version (if same).
//
// This function is only compiled when the "noautoupgrade" build tag is NOT set.
// When the tag IS set, command_upgrade_noop.go provides a no-op implementation.
func cmdUpgrade(c *cli.Context) error {
	c.Context = log.WithCommandContext(c.Context, c.Command.Name)
	logger := log.FromContext(c.Context)
	start := time.Now()
	logger.Debug("UPGRADE START")
	defer func() {
		logger.Debug(fmt.Sprintf("UPGRADE FINISH: %v", time.Since(start)))
	}()
	term := terminal.Get(c.Context)

	term.Spinner().Start("Checking for upgrades...")

	// Force an immediate upgrade check, bypassing the normal 24-hour throttle.
	latestVersion := CheckUpgradeVersion(c.Context, true)
	if latestVersion != "" && latestVersion != version.Version {
		term.Spinner().Stop(terminal.SpinnerStatusOK)
		// Set os.Args to re-execute with --version so the user sees the new version after upgrade.
		os.Args = []string{os.Args[0], "--version"}
		return UpgradeCli(c.Context, latestVersion)
	}
	term.Spinner().Stop(terminal.SpinnerStatusWarnOK)
	// If the latest version equals the current version, inform the user they're up-to-date.
	if latestVersion == version.Version {
		term.Printf("Akamai CLI (%s) is already up-to-date", color.CyanString("v"+version.Version))
		return nil
	}
	if latestVersion != "" {
		term.Printf("Akamai CLI version: %s", color.CyanString("v"+version.Version))
	}
	return nil
}
