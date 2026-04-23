/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package python_test

import (
	"fmt"

	"github.com/chainguard-dev/omnibump/pkg/languages/python"
)

// Example_detectManifest demonstrates detecting a Python manifest file.
func Example_detectManifest() {
	// In a real scenario, you would have a project directory with pyproject.toml
	// For example purposes, we just show the API
	fmt.Println("Use DetectManifest to find Python manifest files in a project directory")
	// Output: Use DetectManifest to find Python manifest files in a project directory
}

// ExamplePython_Name demonstrates the Python language name.
func ExamplePython_Name() {
	p := &python.Python{}
	fmt.Println(p.Name())
	// Output: python
}

// ExampleNewVersionResolver demonstrates creating a version resolver.
func ExampleNewVersionResolver() {
	resolver := python.NewVersionResolver()
	fmt.Printf("VersionResolver created: %T\n", resolver)
	// Output: VersionResolver created: *python.VersionResolver
}
