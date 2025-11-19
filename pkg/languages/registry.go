/*
Copyright 2025 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package languages

import (
	"context"
	"fmt"
	"sync"
)

var (
	// registry holds all registered language implementations
	registry = make(map[string]Language)
	mu       sync.RWMutex
)

// Register adds a language implementation to the registry.
// This is typically called from init() functions in each language package.
func Register(lang Language) {
	mu.Lock()
	defer mu.Unlock()
	registry[lang.Name()] = lang
}

// Get retrieves a language implementation by name.
func Get(name string) (Language, error) {
	mu.RLock()
	defer mu.RUnlock()
	lang, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("language %q not registered", name)
	}
	return lang, nil
}

// List returns all registered language names.
func List() []string {
	mu.RLock()
	defer mu.RUnlock()
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	return names
}

// DetectLanguage attempts to detect which language is present in the given directory.
// Returns the first language that reports a positive detection.
func DetectLanguage(ctx context.Context, dir string) (string, error) {
	mu.RLock()
	defer mu.RUnlock()

	for name, lang := range registry {
		detected, err := lang.Detect(ctx, dir)
		if err != nil {
			continue // Skip languages that error during detection
		}
		if detected {
			return name, nil
		}
	}

	return "", fmt.Errorf("no supported language detected in directory: %s", dir)
}

// DetectLanguages returns all languages detected in the given directory.
// Useful for multi-language projects.
func DetectLanguages(ctx context.Context, dir string) ([]string, error) {
	mu.RLock()
	defer mu.RUnlock()

	var detected []string
	for name, lang := range registry {
		found, err := lang.Detect(ctx, dir)
		if err != nil {
			continue
		}
		if found {
			detected = append(detected, name)
		}
	}

	if len(detected) == 0 {
		return nil, fmt.Errorf("no supported language detected in directory: %s", dir)
	}

	return detected, nil
}
