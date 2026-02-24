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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"

	"github.com/akamai/cli/v2/pkg/log"
	"github.com/akamai/cli/v2/pkg/packages"
	"github.com/akamai/cli/v2/pkg/tools"
	"github.com/urfave/cli/v2"
)

// subcommands.go implements the cli.json package manifest parsing and package discovery
// for the Akamai CLI plugin system.
//
// The cli.json manifest schema defines how plugins declare their commands, language
// requirements, and binary download URLs. See docs/cli-json-schema.md for the formal
// schema specification.
//
// Key types:
//   - subcommands: top-level manifest containing commands[], requirements, and package name
//   - command: individual command entry with name, version, aliases, bin URL template, etc.
//     (defined in command.go)
//
// Key functions:
//   - readPackage: reads cli.json from a local package directory
//   - readPackageFromGithub: fetches cli.json from GitHub's raw content API
//   - getPackagePaths: discovers all installed package directories
//   - downloadBin: downloads a pre-built binary using URL templates
//   - findPackageDir: walks up from a binary to find its package root (containing cli.json)
//
// Version field handling: The cli.json schema supports an optional "version" field at the
// command level (see command.Version in command.go). Go's encoding/json silently ignores
// unknown fields and defaults missing fields to zero values (empty string for version).
// This ensures backward compatibility with existing plugins that do not include version
// information.

// subcommands represents the top-level structure of a cli.json package manifest. It
// contains the list of commands provided by the package, language requirements for
// building from source, the derived package name (Pkg), and optionally the raw JSON
// bytes for binary-only installations.
//
// The optional Version field is the package-level minimum CLI version requirement.
// When present (e.g., "version": "2.0.0"), the CLI uses version.IsCompatible to verify
// that the running CLI version meets the package's minimum requirement before executing
// plugin commands. When absent or empty, the package is treated as compatible with any
// CLI version (fail-open). This field is distinct from the per-command Version in
// the command struct, which tracks the command's own release version.
type subcommands struct {
	Commands     []command                     `json:"commands"`
	Requirements packages.LanguageRequirements `json:"requirements"`
	Version      string                        `json:"version,omitempty"`
	Action       cli.ActionFunc                `json:"-"`
	Pkg          string                        `json:"pkg"`
	raw          []byte
}

// readPackage reads and parses the cli.json manifest from the specified directory. If
// cli.json is not found in dir, it checks the parent directory (supporting binaries in
// subdirectories). Command names are normalized to lowercase after parsing.
//
// The Pkg field is derived from the directory name with the "cli-" prefix stripped,
// following the Akamai package naming convention (e.g., "cli-property" becomes "property").
//
// Version field handling: If a command's version field is absent from the JSON, it
// defaults to an empty string (Go's zero value). This is by design — missing versions
// are treated as compatible and do not cause errors.
//
// Returns the parsed subcommands or an error if the file cannot be read or parsed.
func readPackage(dir string) (subcommands, error) {
	// Try the specified directory first; if cli.json isn't found, check the parent.
	// This supports executables in bin/ subdirectories — the cli.json is one level up.
	if _, err := os.Stat(filepath.Join(dir, "cli.json")); err != nil {
		dir = filepath.Dir(dir)
		if _, err = os.Stat(filepath.Join(dir, "cli.json")); err != nil {
			return subcommands{}, fmt.Errorf("package does not contain a cli.json file: %v", err)
		}
	}

	var packageData subcommands
	cliJSON, err := os.ReadFile(filepath.Join(dir, "cli.json"))
	if err != nil {
		return subcommands{}, fmt.Errorf("unable to read package: %v", err)
	}

	// Go's json.Unmarshal silently ignores unknown JSON fields and defaults missing
	// fields to zero values. This is intentional for backward compatibility — new
	// optional fields (like version) do not break existing cli.json files.
	err = json.Unmarshal(cliJSON, &packageData)
	if err != nil {
		return subcommands{}, fmt.Errorf("unable to unmarshal package: %v", err)
	}

	// Normalize command names to lowercase for case-insensitive matching throughout
	// the CLI. This ensures "Property" and "property" resolve to the same command.
	for key := range packageData.Commands {
		packageData.Commands[key].Name = strings.ToLower(packageData.Commands[key].Name)
	}

	// Derive the package name from the directory name, stripping the "cli-" prefix
	// used by Akamai's package naming convention (e.g., directory "cli-property" becomes Pkg "property").
	packageData.Pkg = filepath.Base(strings.Replace(dir, "cli-", "", 1))

	return packageData, nil
}

// readPackageFromGithub fetches and parses a cli.json manifest from GitHub's raw content
// API at the given URL. Used during install/update to inspect package metadata before
// cloning the repository. The dir parameter is used to derive the Pkg field.
//
// The raw JSON bytes are preserved in subcommands.raw for later use when writing the
// manifest to disk during binary-only installations.
//
// Returns an error if the HTTP request fails, returns a non-200 status, or the JSON
// cannot be parsed.
func readPackageFromGithub(url, dir string) (subcommands, error) {
	response, err := http.Get(url)
	if err != nil {
		return subcommands{}, fmt.Errorf("unable to get package from github: %v", err)
	}
	if response.StatusCode == http.StatusOK {
		cliJSON, err := io.ReadAll(response.Body)
		if err != nil {
			return subcommands{}, fmt.Errorf("unable to read package: %v", err)
		}

		var packageData subcommands

		// Go's json.Unmarshal handles missing optional fields gracefully — unknown
		// fields are ignored and absent fields default to zero values.
		err = json.Unmarshal(cliJSON, &packageData)
		if err != nil {
			return subcommands{}, fmt.Errorf("unable to unmarshal package: %v", err)
		}

		// Preserve the raw JSON bytes for binary-only packages. When a package provides
		// pre-built binaries, the raw JSON is written to the local cli.json during install.
		packageData.raw = cliJSON

		// Normalize command names to lowercase for consistent case-insensitive matching.
		for key := range packageData.Commands {
			packageData.Commands[key].Name = strings.ToLower(packageData.Commands[key].Name)
		}

		// Derive the package name from the directory, stripping the "cli-" prefix.
		packageData.Pkg = filepath.Base(strings.Replace(dir, "cli-", "", 1))

		return packageData, nil

	}

	return subcommands{}, fmt.Errorf("invalid response status while fetching cli.json: %d", response.StatusCode)
}

// getPackagePaths returns the list of all installed package directories under
// ~/.akamai-cli/src/. Returns an empty slice if the source directory does not exist
// or contains no packages.
func getPackagePaths() []string {
	akamaiCliPath, err := tools.GetAkamaiCliSrcPath()
	if err == nil && akamaiCliPath != "" {
		paths, _ := filepath.Glob(filepath.Join(akamaiCliPath, "*"))
		if len(paths) > 0 {
			return paths
		}
	}

	return []string{}
}

// isBinary returns true if all commands in the package have a non-empty Bin field,
// indicating that pre-built binaries are available and no source compilation is needed.
func isBinary(cmdPackage subcommands) bool {
	for _, cmd := range cmdPackage.Commands {
		if len(cmd.Bin) == 0 {
			return false
		}
	}
	return true

}

// findPackageDir walks up the directory tree from dir to find the nearest directory
// containing a cli.json file. This is used to locate the package root from a binary's
// path, supporting both direct (bin in package root) and nested (bin/ subdirectory)
// layouts. Returns an empty string if no cli.json is found.
func findPackageDir(dir string) string {
	if stat, err := os.Stat(dir); err == nil && stat != nil && !stat.IsDir() {
		dir = filepath.Dir(dir)
	}

	if _, err := os.Stat(filepath.Join(dir, "cli.json")); err != nil {
		if os.IsNotExist(err) {
			if filepath.Dir(dir) == "" || filepath.Dir(dir) == "." || filepath.Dir(dir) == "/" {
				return ""
			}

			return findPackageDir(filepath.Dir(dir))
		}
	}

	// at this point, dir points to the package directory, with cli.json
	return dir
}

// downloadBin downloads a pre-built binary for the given command using its Bin URL
// template. The template supports {{.Version}}, {{.Name}}, {{.OS}}, {{.Arch}}, and
// {{.BinSuffix}} placeholders. The OS is mapped from Go's runtime.GOOS (darwin to mac).
// The downloaded binary is saved as akamai-<name><suffix> with 0775 permissions.
//
// Performance: single HTTP request per binary download; network-bound.
func downloadBin(ctx context.Context, dir string, cmd command) error {
	logger := log.FromContext(ctx)
	cmd.Arch = runtime.GOARCH

	// Map Go's runtime.GOOS to Akamai's OS naming convention: "darwin" becomes "mac".
	// This matches the binary naming used in GitHub releases.
	cmd.OS = runtime.GOOS
	if cmd.OS == "darwin" {
		cmd.OS = "mac"
	}

	if cmd.OS == "windows" {
		cmd.BinSuffix = ".exe"
	}

	t := template.Must(template.New("url").Parse(cmd.Bin))
	buf := &bytes.Buffer{}
	if err := t.Execute(buf, cmd); err != nil {
		logger.Error(fmt.Sprintf("Unable to create URL. Template: %s; Error: %v", cmd.Bin, err))
		return err
	}

	url := buf.String()
	logger.Debug(fmt.Sprintf("Fetching binary from %s", url))

	binName := filepath.Join(dir, "akamai-"+strings.ToLower(cmd.Name)+cmd.BinSuffix)
	bin, err := os.Create(binName)
	if err != nil {
		logger.Error(fmt.Sprintf("Unable to create %s file: %v", binName, err))
		return err
	}
	defer func() {
		if err := bin.Close(); err != nil {
			logger.Error(fmt.Sprintf("Error closing file: %v", err))
		}
	}()

	if err := os.Chmod(binName, 0775); err != nil {
		logger.Error(fmt.Sprintf("Unable to change the %s file mode: %v", binName, err))
		return err
	}

	res, err := http.Get(url)
	if err != nil {
		logger.Error(fmt.Sprintf("Unable to get command binary: %v", err))
		return err
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			logger.Error(fmt.Sprintf("Error closing request body: %v", err))
		}
	}()

	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("invalid response status while fetching command binary: %d", res.StatusCode)
	}

	n, err := io.Copy(bin, res.Body)
	if err != nil || n == 0 {
		logger.Error(fmt.Sprintf("Unable to copy from %s to %s: %v", res.Body, binName, err))
		return err
	}

	return nil
}
