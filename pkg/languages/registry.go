/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package languages

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var (
	// registry holds all registered language implementations.
	registry = make(map[string]Language)
	mu       sync.RWMutex

	// ErrLanguageNotRegistered is returned when a language is not found in the registry.
	ErrLanguageNotRegistered = errors.New("language not registered")

	// ErrNoLanguageDetected is returned when no supported language is detected.
	ErrNoLanguageDetected = errors.New("no supported language detected")
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
		return nil, fmt.Errorf("%w: %q", ErrLanguageNotRegistered, name)
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
// For polyglot projects, checks languages in priority order: go, java, rust.
func DetectLanguage(ctx context.Context, dir string) (string, error) {
	mu.RLock()
	defer mu.RUnlock()

	// Define priority order for polyglot projects
	priorityOrder := []string{"go", "java", "rust"}

	// Check priority languages first
	for _, name := range priorityOrder {
		lang, exists := registry[name]
		if !exists {
			continue
		}
		detected, err := lang.Detect(ctx, dir)
		if err != nil {
			continue
		}
		if detected {
			return name, nil
		}
	}

	// Check remaining languages
	for name, lang := range registry {
		// Skip if already checked in priority order
		isPriority := false
		for _, pName := range priorityOrder {
			if name == pName {
				isPriority = true
				break
			}
		}
		if isPriority {
			continue
		}

		detected, err := lang.Detect(ctx, dir)
		if err != nil {
			continue
		}
		if detected {
			return name, nil
		}
	}

	return "", fmt.Errorf("%w in directory: %s", ErrNoLanguageDetected, dir)
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
		return nil, fmt.Errorf("%w in directory: %s", ErrNoLanguageDetected, dir)
	}

	return detected, nil
}
