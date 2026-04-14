/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package composer_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chainguard-dev/omnibump/pkg/languages/php/composer"
)

func ExampleParseLock() {
	lockContent := `{
		"packages": [
			{"name": "monolog/monolog", "version": "3.8.0"},
			{"name": "psr/log", "version": "3.0.2"}
		],
		"packages-dev": [
			{"name": "phpunit/phpunit", "version": "11.5.3"}
		]
	}`

	packages, err := composer.ParseLock(strings.NewReader(lockContent))
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	for _, pkg := range packages {
		fmt.Printf("%s: %s\n", pkg.Name, pkg.Version)
	}
	// Output:
	// monolog/monolog: 3.8.0
	// phpunit/phpunit: 11.5.3
	// psr/log: 3.0.2
}

func ExampleComposer_Detect() {
	dir, err := os.MkdirTemp("", "composer-example-*")
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	defer func() { _ = os.RemoveAll(dir) }()

	if err := os.WriteFile(filepath.Join(dir, "composer.lock"), []byte(`{"packages":[]}`), 0o644); err != nil {
		fmt.Println("error:", err)
		return
	}

	c := &composer.Composer{}
	detected, err := c.Detect(context.Background(), dir)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("Detected:", detected)
	// Output:
	// Detected: true
}

func ExampleComposer_GetManifestFiles() {
	c := &composer.Composer{}
	files := c.GetManifestFiles()
	for _, f := range files {
		fmt.Println(f)
	}
	// Output:
	// composer.json
	// composer.lock
}
