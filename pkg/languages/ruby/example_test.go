/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package ruby_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chainguard-dev/omnibump/pkg/languages"
	"github.com/chainguard-dev/omnibump/pkg/languages/ruby"
)

func Example() {
	// Get the Ruby language from the registry.
	lang, err := languages.Get("ruby")
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("Language:", lang.Name())
	// Output:
	// Language: ruby
}

func ExampleRuby_Detect() {
	// Create a temporary directory with a Gemfile.lock to simulate a Ruby project.
	dir, err := os.MkdirTemp("", "ruby-example-*")
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	defer func() { _ = os.RemoveAll(dir) }()

	lockfile := filepath.Join(dir, "Gemfile.lock")
	content := "GEM\n  remote: https://rubygems.org/\n  specs:\n    rack (3.1.8)\n"
	if err := os.WriteFile(lockfile, []byte(content), 0o644); err != nil {
		fmt.Println("error:", err)
		return
	}

	r := &ruby.Ruby{}
	detected, err := r.Detect(context.Background(), dir)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("Detected:", detected)
	// Output:
	// Detected: true
}

func ExampleRuby_GetManifestFiles() {
	r := &ruby.Ruby{}
	files := r.GetManifestFiles()
	for _, f := range files {
		fmt.Println(f)
	}
	// Output:
	// Gemfile
	// Gemfile.lock
}
