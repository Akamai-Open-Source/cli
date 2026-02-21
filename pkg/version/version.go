package version

import "github.com/Masterminds/semver"

const (
	// Version is the current Akamai CLI application version string.
	Version = "2.0.3"
	// Equals indicates the two versions compared by Compare are equal (left == right).
	Equals = 0
	// Error indicates a failure parsing one of the version parameters in Compare.
	Error = 2
	// Greater indicates left is newer than right (left > right) in Compare.
	Greater = -1
	// Smaller indicates left is older than right (left < right) in Compare.
	Smaller = 1
)

// Compare compares two semantic version strings and returns a sentinel integer
// indicating their relative ordering.
//
// Return values:
//   - Equals (0): left and right represent the same version
//   - Greater (-1): left is a newer version than right
//   - Smaller (1): left is an older version than right
//   - Error (2): one or both version strings could not be parsed as valid semver
//
// Compare is safe for concurrent use and has no side effects. It delegates
// parsing to github.com/Masterminds/semver, supporting full semver syntax
// including prerelease identifiers and build metadata.
func Compare(left, right string) int {
	leftVersion, err := semver.NewVersion(left)
	if err != nil {
		return Error
	}

	rightVersion, err := semver.NewVersion(right)
	if err != nil {
		return Error
	}

	if leftVersion.LessThan(rightVersion) {
		return Smaller
	} else if leftVersion.GreaterThan(rightVersion) {
		return Greater
	}

	return Equals
}

// IsCompatible checks whether the current version satisfies a required minimum
// version constraint. It is used to validate CLI-plugin compatibility when a
// plugin's cli.json specifies a minimum required CLI version.
//
// Rules:
//   - If required is an empty string, the check passes (compatible). This ensures
//     backward compatibility with plugins that do not specify a minimum version.
//   - If current >= required (Compare returns Greater or Equals), compatible (true).
//   - If current < required (Compare returns Smaller), incompatible (false).
//   - If either version string cannot be parsed (Compare returns Error), the check
//     passes (true). This is a fail-open policy to avoid blocking plugin execution
//     due to version parsing issues.
func IsCompatible(required, current string) bool {
	// Empty required version means no constraint — always compatible.
	if required == "" {
		return true
	}
	// Compare current (left) against required (right); only Smaller means incompatible.
	result := Compare(current, required)
	return result != Smaller
}
