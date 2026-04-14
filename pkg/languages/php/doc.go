/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

/*
Package php implements omnibump support for PHP projects.

It provides automatic detection and management of PHP build tools
(currently Composer), enabling dependency updates through omnibump's
unified language interface.

The package auto-detects which build tool is in use by examining
manifest files in the project directory. Once detected, all operations
(update, validate, analyze) are delegated to the appropriate build
tool implementation.

# Build Tools

The following build tools are supported:

  - Composer: detected via composer.lock

# Usage

The PHP language is automatically registered via its init function.
Use the languages package to access it:

	lang, err := languages.Get("php")
	if err != nil {
		// handle error
	}
	detected, err := lang.Detect(ctx, "/path/to/project")
*/
package php
