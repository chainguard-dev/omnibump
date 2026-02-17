/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package omnibump

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/chainguard-dev/omnibump/pkg/languages"
)

func supportedCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "supported",
		Short: "List supported languages and build systems",
		Long: `Display all supported language ecosystems and their build tools.

This command shows which languages omnibump can handle and what build
systems are supported for each language. Useful for understanding what
projects can be bumped with omnibump.`,
		RunE: runSupported,
	}

	return cmd
}

func runSupported(cmd *cobra.Command, args []string) error {
	fmt.Println()
	fmt.Println("Supported Languages and Build Systems")
	fmt.Println("=====================================")
	fmt.Println()

	// Get all registered languages
	registeredLanguages := languages.List()

	for _, langName := range registeredLanguages {
		lang, err := languages.Get(langName)
		if err != nil {
			continue
		}

		fmt.Printf("Language: %s\n", langName)

		// Get manifest files to show what the language detects
		manifestFiles := lang.GetManifestFiles()
		if len(manifestFiles) > 0 {
			fmt.Printf("  Detects: %v\n", manifestFiles)
		}

		// For Java, show supported build tools
		if langName == "java" {
			fmt.Println("  Build Tools:")
			fmt.Println("    - Maven (pom.xml)")
			fmt.Println("    - Gradle (build.gradle, build.gradle.kts)")
		}

		fmt.Println()
	}

	fmt.Println("Usage:")
	fmt.Println("------")
	fmt.Println("  omnibump --language <lang> --packages \"package@version\"")
	fmt.Println("  omnibump --language auto --deps deps.yaml")
	fmt.Println()
	fmt.Println("For more information, run: omnibump --help")
	fmt.Println()

	return nil
}
