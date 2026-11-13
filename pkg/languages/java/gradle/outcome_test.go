/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package gradle

import (
	"errors"
	"testing"

	"github.com/chainguard-dev/omnibump/pkg/gradlefile"
	"github.com/chainguard-dev/omnibump/pkg/languages"
)

// The tests in this file pin down which Gradle conditions return an error
// and which do not. Builds that run omnibump as a step fail on an error, so
// moving a condition from one side to the other is a behaviour change, not a
// refactor.

// TestGradle_Update_Errors lists the conditions Update rejects.
func TestGradle_Update_Errors(t *testing.T) {
	tests := []struct {
		name       string
		fixture    string
		deps       []languages.Dependency
		properties map[string]string
		wantErr    error
	}{{
		name:    "dependency version with script characters",
		fixture: "opensearch-style",
		deps:    []languages.Dependency{{Name: "org.apache.logging.log4j:log4j-core", Version: `2.25.4"); evil("`}},
		wantErr: gradlefile.ErrInvalidVersion,
	}, {
		name:       "property value with script characters",
		fixture:    "sonarqube-style",
		properties: map[string]string{"nettyVersion": "4.1.126.Final\nevil=1"},
		wantErr:    gradlefile.ErrInvalidVersion,
	}, {
		name:       "explicit property not defined anywhere",
		fixture:    "sonarqube-style",
		properties: map[string]string{"doesNotExistVersion": "1.0.0"},
		wantErr:    ErrPropertyNotFound,
	}, {
		name:    "two libraries sharing a catalog key request different versions",
		fixture: "opensearch-style",
		deps: []languages.Dependency{
			{Name: "org.apache.logging.log4j:log4j-core", Version: "2.25.4"},
			{Name: "org.apache.logging.log4j:log4j-api", Version: "2.25.5"},
		},
		wantErr: ErrVersionConflict,
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := copyFixture(t, tt.fixture)
			cfg := &languages.UpdateConfig{RootDir: dir, Dependencies: tt.deps, Properties: tt.properties}
			if err := (&Gradle{}).Update(t.Context(), cfg); !errors.Is(err, tt.wantErr) {
				t.Fatalf("Update() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// TestGradle_UpdateAndValidate_NoError lists requests that Update and
// Validate both accept.
func TestGradle_UpdateAndValidate_NoError(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		deps    []languages.Dependency
	}{{
		name:    "dependency the build does not declare",
		fixture: "kayenta-style",
		deps:    []languages.Dependency{{Name: "com.fasterxml.jackson.core:jackson-core", Version: "2.22.4"}},
	}, {
		name:    "version older than the build declares",
		fixture: "kayenta-style",
		deps:    []languages.Dependency{{Name: "com.netflix.kayenta:kayenta-core", Version: "0.9.0"}},
	}, {
		name:    "catalog version older than the build declares",
		fixture: "opensearch-style",
		deps:    []languages.Dependency{{Name: "org.apache.logging.log4j:log4j-core", Version: "2.25.1"}},
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			updateAndValidate(t, &languages.UpdateConfig{RootDir: copyFixture(t, tt.fixture), Dependencies: tt.deps})
		})
	}
}

// TestGradle_Validate_UnappliedUpgradeFails checks that Validate reports an
// upgrade that is not in the build files as an error.
func TestGradle_Validate_UnappliedUpgradeFails(t *testing.T) {
	cfg := &languages.UpdateConfig{
		RootDir:      copyFixture(t, "kayenta-style"),
		Dependencies: []languages.Dependency{{Name: "com.netflix.kayenta:kayenta-core", Version: "1.2.0"}},
	}
	if err := (&Gradle{}).Validate(t.Context(), cfg); !errors.Is(err, ErrValidationFailed) {
		t.Fatalf("Validate() error = %v, want %v", err, ErrValidationFailed)
	}
}
