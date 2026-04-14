/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package php_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chainguard-dev/omnibump/pkg/languages"
	"github.com/chainguard-dev/omnibump/pkg/languages/php"
)

func Example() {
	// Get the PHP language from the registry.
	lang, err := languages.Get("php")
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("Language:", lang.Name())
	// Output:
	// Language: php
}

func ExamplePHP_Detect() {
	// Create a temporary directory with a composer.lock to simulate a PHP project.
	dir, err := os.MkdirTemp("", "php-example-*")
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	defer func() { _ = os.RemoveAll(dir) }()

	if err := os.WriteFile(filepath.Join(dir, "composer.lock"), []byte(`{"packages":[]}`), 0o644); err != nil {
		fmt.Println("error:", err)
		return
	}

	p := &php.PHP{}
	detected, err := p.Detect(context.Background(), dir)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("Detected:", detected)
	// Output:
	// Detected: true
}

func ExamplePHP_GetManifestFiles() {
	p := &php.PHP{}
	files := p.GetManifestFiles()
	for _, f := range files {
		fmt.Println(f)
	}
	// Output:
	// composer.json
	// composer.lock
}
