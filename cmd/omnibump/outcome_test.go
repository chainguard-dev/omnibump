/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package omnibump

import (
	"context"
	"errors"
	"testing"

	"github.com/chainguard-dev/omnibump/pkg/languages"
	"github.com/spf13/cobra"
)

// errStubValidate is what stubLanguage.Validate returns.
var errStubValidate = errors.New("pin did not land")

// errStubUpdate is what stubLanguage.Update returns when failUpdate is set.
var errStubUpdate = errors.New("update failed")

// stubLanguage is a Language whose Update and Validate outcomes the test
// controls, so runUpdate's handling of them can be checked for every
// ecosystem at once.
type stubLanguage struct {
	failUpdate     bool
	validateCalled bool
}

func (s *stubLanguage) Name() string                                 { return "outcome-stub" }
func (s *stubLanguage) Detect(context.Context, string) (bool, error) { return false, nil }
func (s *stubLanguage) GetManifestFiles() []string                   { return nil }
func (s *stubLanguage) SupportsAnalysis() bool                       { return false }

func (s *stubLanguage) Update(context.Context, *languages.UpdateConfig) error {
	if s.failUpdate {
		return errStubUpdate
	}
	return nil
}

func (s *stubLanguage) Validate(context.Context, *languages.UpdateConfig) error {
	s.validateCalled = true
	return errStubValidate
}

// TestRunUpdate_Outcomes checks how runUpdate turns a language's Update and
// Validate results into the command's result. A Validate error is only a
// warning unless --fail-on-unapplied-pins is set; an Update error always
// fails the command, which fails any build that runs omnibump as a step.
func TestRunUpdate_Outcomes(t *testing.T) {
	tests := []struct {
		name                string
		failUpdate          bool
		dryRun              bool
		failOnUnappliedPins bool
		wantErr             error
		wantValidate        bool
	}{{
		name:         "validation error is a warning by default",
		wantValidate: true,
	}, {
		name:                "validation error fails with --fail-on-unapplied-pins",
		failOnUnappliedPins: true,
		wantErr:             errStubValidate,
		wantValidate:        true,
	}, {
		name:                "dry run skips validation",
		dryRun:              true,
		failOnUnappliedPins: true,
	}, {
		name:       "update error fails the command",
		failUpdate: true,
		wantErr:    errStubUpdate,
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := &stubLanguage{failUpdate: tt.failUpdate}
			languages.Register(stub)

			original := flags
			t.Cleanup(func() { flags = original })
			flags = rootFlags{
				language:            stub.Name(),
				packages:            "example.com/module@v1.2.3",
				rootDir:             t.TempDir(),
				dryRun:              tt.dryRun,
				failOnUnappliedPins: tt.failOnUnappliedPins,
			}

			cmd := &cobra.Command{}
			cmd.SetContext(t.Context())
			if err := runUpdate(cmd, nil); !errors.Is(err, tt.wantErr) {
				t.Fatalf("runUpdate() error = %v, want %v", err, tt.wantErr)
			}
			if stub.validateCalled != tt.wantValidate {
				t.Errorf("Validate called: got = %v, want = %v", stub.validateCalled, tt.wantValidate)
			}
		})
	}
}
