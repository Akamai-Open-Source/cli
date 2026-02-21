package commands

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

// Embed the package catalog at compile time. This file contains the list of all
// officially supported Akamai CLI packages with their metadata and download URLs.
//
//go:embed package_list/package-list.json
var embeddedPackages string

type (
	// packageReader is an interface for reading the embedded package catalog.
	// It abstracts the package list data source for testability, allowing
	// cmdSearch and cmdList to be tested with custom package data.
	packageReader interface {
		readPackage() (*packageList, error)
	}

	// pkgReader implements packageReader by deserializing JSON from its content string.
	// The content is typically the embedded package-list.json catalog.
	pkgReader struct {
		content string
	}

	// packageList represents the top-level structure of the embedded package catalog
	// (package-list.json). It contains a schema version and a list of available packages.
	packageList struct {
		Version  float64           `json:"version"`
		Packages []packageListItem `json:"packages"`
	}

	// packageListItem represents a single package entry in the package catalog. Each item
	// describes a package available for installation, including its metadata, download URL,
	// contained commands, and language requirements. The Version field tracks the catalog
	// version of the package and is used for version display in search results.
	packageListItem struct {
		Title        string       `json:"title"`
		Name         string       `json:"name"`
		Version      string       `json:"version"` // Package version from catalog; optional, defaults to "" if absent
		URL          string       `json:"url"`
		Issues       string       `json:"issues"`
		Commands     []command    `json:"commands"`
		Requirements requirements `json:"requirements"`
	}

	// requirements represents the language runtime requirements for a package.
	// Each field specifies a minimum version for the corresponding language runtime.
	// Empty fields indicate the language is not required.
	requirements struct {
		Go     string `json:"go"`
		Php    string `json:"php"`
		Node   string `json:"node"`
		Ruby   string `json:"ruby"`
		Python string `json:"python"`
	}
)

// newPackageReader returns a new pkgReader initialized with the given JSON string.
// Typically called with embeddedPackages to create a reader for the built-in catalog.
func newPackageReader(str string) *pkgReader {
	return &pkgReader{content: str}
}

// readPackage deserializes the JSON content into a packageList structure.
// Returns the parsed package list or an error if the JSON is malformed.
func (pr *pkgReader) readPackage() (*packageList, error) {
	packagesList := &packageList{}
	if err := json.Unmarshal([]byte(pr.content), packagesList); err != nil {
		return nil, fmt.Errorf("readPackage: %s", err)
	}

	return packagesList, nil
}
