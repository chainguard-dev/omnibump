/*
Copyright 2024 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

// Package main provides the omnibump CLI entry point.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"

	"github.com/chainguard-dev/omnibump/cmd/omnibump"
)

func main() {
	ctx, done := signal.NotifyContext(context.Background(), os.Interrupt)

	if err := omnibump.New().ExecuteContext(ctx); err != nil {
		done()
		log.Fatalf("error during command execution: %v", err)
	}
	done()
}
