/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package languages

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

	// ErrMultipleLanguagesDetected is returned when more than one language matches.
	ErrMultipleLanguagesDetected = errors.New("multiple languages detected")
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
// When multiple languages match, languages with a manifest file in the root directory
// are preferred over those detected only via recursive scanning. Among equals,
// selection is alphabetical for determinism. Callers should use --language to override.
func DetectLanguage(ctx context.Context, dir string) (string, error) {
	mu.RLock()
	defer mu.RUnlock()

	// Collect all matching languages.
	var matches []string
	for name, lang := range registry {
		detected, err := lang.Detect(ctx, dir)
		if err != nil {
			continue // Skip languages that error during detection
		}
		if detected {
			matches = append(matches, name)
		}
	}

	if len(matches) == 0 {
		return "", fmt.Errorf("%w in directory: %s", ErrNoLanguageDetected, dir)
	}

	if len(matches) == 1 {
		return matches[0], nil
	}

	// Multiple matches: prefer languages with a manifest in the root directory.
	var rootMatches []string
	for _, name := range matches {
		lang := registry[name]
		if hasRootManifest(dir, lang) {
			rootMatches = append(rootMatches, name)
		}
	}

	// If root-level filtering narrowed it to one, use that directly.
	if len(rootMatches) == 1 {
		return rootMatches[0], nil
	}

	// If root-level filtering found multiple, prefer those over deeper matches.
	// If none had root manifests, fall back to the full match set.
	candidates := matches
	if len(rootMatches) > 0 {
		candidates = rootMatches
	}

	sort.Strings(candidates)
	return candidates[0], fmt.Errorf(
		"%w %v in directory: %s; using %q (specify --language to override)",
		ErrMultipleLanguagesDetected, matches, dir, candidates[0])
}

// hasRootManifest checks if a language has any of its manifest files directly in dir.
func hasRootManifest(dir string, lang Language) bool {
	for _, f := range lang.GetManifestFiles() {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			return true
		}
	}
	return false
}

// DetectLanguages returns all languages detected in the given directory, sorted
// alphabetically for deterministic output. Useful for multi-language projects.
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

	sort.Strings(detected)
	return detected, nil
}
