package commands

import (
	"testing"

	"github.com/akamai/cli/v2/pkg/version"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadPackage(t *testing.T) {
	tests := map[string]struct {
		directory string
		pkg       string
		withError string
	}{
		"return subcommands with directory name": {
			directory: "./testdata/repo",
			pkg:       "repo",
		},
		"return subcommands with directory name - strip prefix": {
			directory: "./testdata/.akamai-cli/src/cli-echo-python",
			pkg:       "echo-python",
		},
		"no error if no cli.json": {
			directory: "./testdata/cli-search",
			withError: `does not contain a cli.json`,
		},
		"return error if cli.json is not valid": {
			directory: "./testdata/.akamai-cli/src/cli-echo-invalid-json",
			withError: `invalid`,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			subcommands, err := readPackage(test.directory)
			if test.withError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), test.withError)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.pkg, subcommands.Pkg, "the package name was not resolved properly")
		})
	}
}

// TestReadPackageVersionField validates backward-compatible handling of the
// optional "version" field in cli.json command entries. When the version field
// is present it must be parsed into command.Version; when absent, the Go zero
// value (empty string) must be used and readPackage must not return an error.
func TestReadPackageVersionField(t *testing.T) {
	tests := map[string]struct {
		directory     string
		expectVersion string
		withError     string
	}{
		"cli.json with version field present is parsed correctly": {
			directory:     "./testdata/repo",
			expectVersion: "1.0.0",
		},
		"cli.json without explicit version field is compatible": {
			directory:     "./testdata/repo_no_binary",
			expectVersion: "",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			subcommands, err := readPackage(test.directory)
			if test.withError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), test.withError)
				return
			}
			require.NoError(t, err)
			// Backward compatibility: missing version field must not cause errors
			require.NotEmpty(t, subcommands.Commands, "commands should not be empty")
			if test.expectVersion != "" {
				assert.Equal(t, test.expectVersion, subcommands.Commands[0].Version,
					"version field should be parsed from cli.json")
			} else {
				assert.Empty(t, subcommands.Commands[0].Version,
					"missing version field should default to empty string")
			}
		})
	}
}

// TestReadPackageTopLevelVersion validates that the optional top-level "version" field
// in cli.json is parsed into subcommands.Version. This field represents the package's
// minimum required CLI version for compatibility enforcement via version.IsCompatible.
func TestReadPackageTopLevelVersion(t *testing.T) {
	tests := map[string]struct {
		directory            string
		expectPackageVersion string
		withError            string
	}{
		"top-level version present is parsed correctly": {
			directory:            "./testdata/repo_version_compatible",
			expectPackageVersion: "1.0.0",
		},
		"top-level version absent defaults to empty string": {
			directory:            "./testdata/repo_version_empty",
			expectPackageVersion: "",
		},
		"high top-level version is parsed correctly": {
			directory:            "./testdata/repo_version_incompatible",
			expectPackageVersion: "99.0.0",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			pkg, err := readPackage(test.directory)
			if test.withError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), test.withError)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.expectPackageVersion, pkg.Version,
				"top-level version field should match expected value")
		})
	}
}

// TestVersionEnforcementWithIsCompatible verifies the integration between the
// parsed top-level package version and version.IsCompatible. This tests the
// enforcement logic used in cmdSubcommand before plugin execution.
func TestVersionEnforcementWithIsCompatible(t *testing.T) {
	tests := map[string]struct {
		directory        string
		expectCompatible bool
	}{
		"compatible package version passes enforcement": {
			directory:        "./testdata/repo_version_compatible",
			expectCompatible: true,
		},
		"incompatible package version fails enforcement": {
			directory:        "./testdata/repo_version_incompatible",
			expectCompatible: false,
		},
		"missing package version is always compatible": {
			directory:        "./testdata/repo_version_empty",
			expectCompatible: true,
		},
		"existing repo without top-level version is compatible": {
			directory:        "./testdata/repo",
			expectCompatible: true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			pkg, err := readPackage(test.directory)
			require.NoError(t, err)
			// Simulate the enforcement check from cmdSubcommand: use the parsed
			// package version as "required" and the current CLI version as "current".
			compatible := version.IsCompatible(pkg.Version, version.Version)
			assert.Equal(t, test.expectCompatible, compatible,
				"version.IsCompatible(pkg.Version=%q, version.Version=%q) should be %v",
				pkg.Version, version.Version, test.expectCompatible)
		})
	}
}
