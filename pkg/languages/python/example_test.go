/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package python_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chainguard-dev/omnibump/pkg/languages"
	"github.com/chainguard-dev/omnibump/pkg/languages/python"
)

func Example() {
	// Get the Python language from the registry.
	lang, err := languages.Get("python")
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("Language:", lang.Name())
	// Output:
	// Language: python
}

func ExamplePython_Detect() {
	// Create a temporary directory with a pyproject.toml to simulate a Python project.
	dir, err := os.MkdirTemp("", "python-example-*")
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	defer func() { _ = os.RemoveAll(dir) }()

	pyproject := filepath.Join(dir, "pyproject.toml")
	if err := os.WriteFile(pyproject, []byte("[project]\nname = \"example\"\ndependencies = [\"requests>=2.28.0\"]\n"), 0o644); err != nil {
		fmt.Println("error:", err)
		return
	}

	p := &python.Python{}
	detected, err := p.Detect(context.Background(), dir)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("Detected:", detected)
	// Output:
	// Detected: true
}

func ExamplePython_GetManifestFiles() {
	p := &python.Python{}
	files := p.GetManifestFiles()
	for _, f := range files {
		fmt.Println(f)
	}
	// Output:
	// pyproject.toml
	// requirements.txt
	// setup.cfg
	// setup.py
	// Pipfile
}
