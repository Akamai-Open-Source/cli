package version

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCompareVersion(t *testing.T) {
	tests := map[string]struct {
		left, right string
		expected    int
	}{
		"left is greater than right":                 {"1.0.1", "1.0.0", Greater},
		"left is less than right":                    {"0.9.0", "1.0.0", Smaller},
		"versions are equal":                         {"0.9.0", "0.9.0", Equals},
		"left version does not match semver syntax":  {"abc", "0.9.0", Error},
		"right version does not match semver syntax": {"1.0.0", "abc", Error},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			res := Compare(test.left, test.right)
			assert.Equal(t, test.expected, res)
		})
	}
}

func TestIsCompatible(t *testing.T) {
	tests := map[string]struct {
		required string
		current  string
		expected bool
	}{
		"empty required is always compatible":          {"", "2.0.3", true},
		"current greater than required is compatible":  {"1.0.0", "2.0.3", true},
		"current equals required is compatible":        {"2.0.3", "2.0.3", true},
		"current less than required is incompatible":   {"3.0.0", "2.0.3", false},
		"invalid required fails open to compatible":    {"abc", "2.0.3", true},
		"invalid current fails open to compatible":     {"1.0.0", "xyz", true},
		"both invalid fails open to compatible":        {"abc", "xyz", true},
		"empty required with empty current compatible": {"", "", true},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			res := IsCompatible(test.required, test.current)
			assert.Equal(t, test.expected, res)
		})
	}
}
