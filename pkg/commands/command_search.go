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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/akamai/cli/v2/pkg/color"
	"github.com/akamai/cli/v2/pkg/log"
	"github.com/akamai/cli/v2/pkg/terminal"
	"github.com/akamai/cli/v2/pkg/tools"
	"github.com/urfave/cli/v2"
)

// githubURLTemplate is the URL pattern used to fetch the latest cli.json manifest
// from a package's GitHub repository (raw content). The %s placeholder is replaced
// with the repository name (last segment of the package URL).
var (
	githubURLTemplate = "https://raw.githubusercontent.com/akamai/%s/master/cli.json"
)

// cmdSearch handles the "akamai search" command. It delegates to cmdSearchWithPackageReader
// using the embedded package catalog as the default package reader.
func cmdSearch(c *cli.Context) (e error) {
	pr := newPackageReader(embeddedPackages)
	return cmdSearchWithPackageReader(c, pr)
}

// cmdSearchWithPackageReader implements the search command logic. It searches the package
// catalog for commands matching the specified keywords, displays matching results with
// version information and install/update hints, and optionally fetches the latest version
// from GitHub for each matching package. Uses the named-return-error pattern with deferred
// timing/logging.
func cmdSearchWithPackageReader(c *cli.Context, pr packageReader) (e error) {
	c.Context = log.WithCommandContext(c.Context, c.Command.Name)
	start := time.Now()
	logger := log.FromContext(c.Context)
	logger.Debug("SEARCH START")
	defer func() {
		if e == nil {
			logger.Debug(fmt.Sprintf("SEARCH FINISHED: %v", time.Since(start)))
		} else {
			logger.Error(fmt.Sprintf("SEARCH ERROR: %v", e))
		}
	}()
	if !c.Args().Present() {
		logger.Error("No keywords specified")
		return cli.Exit(color.RedString("You must specify one or more keywords"), 1)
	}

	packages, err := pr.readPackage()
	if err != nil {
		logger.Error(fmt.Sprintf("Failed to read package: %v", err))
		return cli.Exit(color.RedString("%s", err.Error()), 1)
	}

	err = searchPackages(c.Context, c.Args().Slice(), packages)
	if err != nil {
		logger.Error(fmt.Sprintf("Failed to search packages: %v", err))
		return cli.Exit(color.RedString("%s", err.Error()), 1)
	}

	return nil
}

// searchPackages scores and filters packages from the catalog by matching each keyword
// against package and command metadata. Results are ranked by a weighted relevance score
// and displayed in descending order of relevance, with ties broken alphabetically by
// package name. The scoring weights are:
//
//   - Package name match:  +100 (strongest signal — direct package name hit)
//   - Package title match: +50  (moderate signal — human-readable title hit)
//   - Command name match:  +30  (moderate signal — specific subcommand hit)
//   - Alias match:         +20  (weaker signal — alternate command name hit)
//   - Description match:   +1   (weakest signal — keyword in free-text description)
//
// Only commands that individually matched at least one keyword are included in the
// output for each package. Packages with zero total hits are omitted entirely.
func searchPackages(ctx context.Context, keywords []string, packageList *packageList) error {
	// results maps relevance score → package name → package item, allowing grouping
	// by score for ranked output.
	results := make(map[int]map[string]packageListItem)

	term := terminal.Get(ctx)

	var hits int
	for key, pkg := range packageList.Packages {
		hits = 0
		validCmds := make([]command, 0)
		for _, keyword := range keywords {
			keyword = strings.ToLower(keyword)
			// Package name carries the highest weight because it is the primary
			// identifier users type in install/update commands.
			if strings.Contains(strings.ToLower(pkg.Name), keyword) {
				hits += 100
			}

			// Package title is the human-readable label shown in search results.
			if strings.Contains(strings.ToLower(pkg.Title), keyword) {
				hits += 50
			}
		}

		for _, cmd := range pkg.Commands {
			cmdMatches := false
			for _, keyword := range keywords {
				keyword = strings.ToLower(keyword)

				if strings.Contains(strings.ToLower(cmd.Name), keyword) {
					hits += 30
					cmdMatches = true
				}

				// Aliases are alternate names for a command (e.g., short forms).
				// They are scored lower than the canonical command name but still
				// indicate a meaningful match.
				for _, alias := range cmd.Aliases {
					if strings.Contains(strings.ToLower(alias), keyword) {
						hits += 20
						cmdMatches = true
					}
				}

				if strings.Contains(strings.ToLower(cmd.Description), keyword) {
					hits++
					cmdMatches = true
				}
			}

			// Only include commands that individually matched at least one keyword,
			// so the output shows only relevant subcommands for each package.
			if cmdMatches {
				validCmds = append(validCmds, cmd)
			}
		}
		packageList.Packages[key].Commands = validCmds

		if hits > 0 {
			if _, ok := results[hits]; !ok {
				results[hits] = make(map[string]packageListItem)
			}
			results[hits][pkg.Name] = packageList.Packages[key]
		}
	}

	resultHits := make([]int, 0)
	resultPkgs := make([]string, 0)
	for hits := range results {
		resultHits = append(resultHits, hits)
		for _, pkg := range results[hits] {
			resultPkgs = append(resultPkgs, pkg.Name)
		}
	}

	// Sort hits descending so highest-relevance packages appear first;
	// sort package names alphabetically for stable, predictable output.
	sort.Sort(sort.Reverse(sort.IntSlice(resultHits)))
	sort.Strings(resultPkgs)

	term.Printf(color.YellowString("Results Found:")+" %d\n\n", len(resultPkgs))

	return printResult(resultHits, resultPkgs, results, term)
}

// printResult iterates through search results ordered by relevance score (resultHits,
// descending) and package name (resultPkgs, alphabetical). For each matching package
// and command it displays the package title, command name with aliases, the latest
// available version fetched from GitHub, the locally installed version (if present),
// and the command description. After all results, it prints an actionable hint:
// "Install using …" if not installed, "Update using …" if outdated, or an
// up-to-date confirmation otherwise.
func printResult(resultHits []int, resultPkgs []string, results map[int]map[string]packageListItem, term terminal.Terminal) error {
	var installedVersion, availableVersion string
	for _, hits := range resultHits {
		for _, pkgName := range resultPkgs {
			if _, ok := results[hits][pkgName]; ok {
				pkg := results[hits][pkgName]
				term.Printf(color.GreenString("Package: ")+"%s [%s]\n", pkg.Title, color.BlueString("%s", pkg.Name))
				for _, cmd := range pkg.Commands {
					var aliases string
					if len(cmd.Aliases) == 1 {
						aliases = fmt.Sprintf("(alias: %s)", cmd.Aliases[0])
					} else if len(cmd.Aliases) > 1 {
						aliases = fmt.Sprintf("(aliases: %s)", strings.Join(cmd.Aliases, ", "))
					}
					term.Printf(color.BoldString("  Command:")+" %s %s\n", cmd.Name, aliases)

					url := pkg.URL
					var err error
					availableVersion, err = getLatestVersion(url)
					if err != nil {
						return cli.Exit(color.RedString("%s", err.Error()), 1)
					}
					term.Printf(color.BoldString("  Available Version:")+" %s\n", availableVersion)
					installedVersion, err = getVersionFromSystem(pkg.Name)
					if err != nil {
						return cli.Exit(color.RedString("%s", err.Error()), 1)
					}
					if installedVersion != "" {
						term.Printf(color.BoldString("  Installed Version:")+" %s\n", installedVersion)
					}
					term.Printf(color.BoldString("  Description:")+" %s\n\n", cmd.Description)
				}
			}
		}
	}

	if len(resultHits) > 0 {
		if installedVersion == "" {
			term.Printf("\nInstall using \"%s\".\n", color.BlueString("%s install [package]", tools.Self()))
		} else if installedVersion != availableVersion {
			term.Printf("\nUpdate using \"%s\".\n", color.BlueString("%s update [package]", tools.Self()))
		} else {
			term.Printf(color.BlueString("Package is already up-to-date on your system"))
		}
	}
	return nil
}

// getLatestVersion fetches the latest version string for a package by downloading
// its cli.json manifest from GitHub raw content. It extracts the repository name
// from the package URL (last path segment), constructs a raw.githubusercontent.com
// URL using githubURLTemplate, and returns the version of the first command entry.
// Returns an error if the URL is malformed, the fetch fails, the response is non-200,
// or the manifest contains no commands.
func getLatestVersion(s string) (string, error) {
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("error parsing URL: %v", err)
	}

	// Extract the repository name from the package URL (e.g., "cli-terraform"
	// from "https://github.com/akamai/cli-terraform") to construct the raw
	// GitHub content URL for the package's cli.json manifest.
	lastSegment := path.Base(u.Path)

	repoURL := fmt.Sprintf(githubURLTemplate, lastSegment)
	resp, err := http.Get(repoURL)
	if err != nil {
		return "", fmt.Errorf("error fetching the URL: %v", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			fmt.Println("error closing the response body:", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("error: status code %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("error reading the response body: %w", err)
	}

	var cli CLI
	if err := json.Unmarshal(body, &cli); err != nil {
		return "", fmt.Errorf("error parsing the JSON: %w", err)
	}

	if len(cli.CommandList) > 0 {
		return cli.CommandList[0].Version, nil
	}
	return "", fmt.Errorf("no latest version found")
}

// CLI represents the top-level structure of a cli.json manifest as served by
// GitHub raw content. It contains a list of command entries, each with a name,
// version, and description. This struct is used exclusively by the search command
// to retrieve the latest available version for display.
type CLI struct {
	CommandList []CommandObject `json:"commands"`
}

// CommandObject contains the name, version, and description for a single command
// entry within a cli.json manifest. Used by getLatestVersion and getVersionFromSystem
// to extract version information for search result display.
type CommandObject struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
}

// getVersionFromSystem looks up the locally installed version of a package by scanning
// the installed package bin paths (returned by getPackageBinPaths) for a directory whose
// name ends with "cli-<command>". If found, it reads the cli.json manifest from that
// directory and returns the version of the first command entry. Returns an empty string
// (not an error) if the package is not installed locally.
func getVersionFromSystem(command string) (string, error) {
	paths := filepath.SplitList(getPackageBinPaths())
	// Convention: installed packages reside in directories named "cli-<command>"
	// under the Akamai CLI source tree (e.g., ~/.akamai-cli/src/cli-terraform).
	suffix := "cli-" + command
	finalPath := ""
	for _, path := range paths {
		if strings.HasSuffix(path, suffix) {
			finalPath = path
			break
		}
	}

	if finalPath == "" {
		return "", nil
	}
	body, err := os.ReadFile(filepath.Join(finalPath, "cli.json"))
	if err != nil {
		return "", fmt.Errorf("error reading the file: %v", err)
	}

	var cli CLI
	if err := json.Unmarshal(body, &cli); err != nil {
		return "", fmt.Errorf("error parsing the JSON: %v", err)
	}

	// Guard against an empty command list to avoid an index-out-of-range panic,
	// consistent with the same check in getLatestVersion.
	if len(cli.CommandList) == 0 {
		return "", nil
	}

	return cli.CommandList[0].Version, nil
}
