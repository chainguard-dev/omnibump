/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package maven

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/chainguard-dev/omnibump/pkg/languages"
)

// The tests in this file pin down which Maven conditions return an error and
// which only log a warning. Builds that run omnibump as a step fail on an
// error, so moving a condition from one side to the other is a behaviour
// change, not a refactor.

// TestMaven_Update_Errors lists the conditions Update rejects. Each one fails
// before any file is written.
func TestMaven_Update_Errors(t *testing.T) {
	tests := []struct {
		name         string
		deps         []languages.Dependency
		properties   map[string]string
		manifestFile string
		wantErr      error
	}{{
		name:    "dependency version with XML characters",
		deps:    []languages.Dependency{mavenDep("org.apache.neethi", "neethi", `3.2.5"><evil/>`, "")},
		wantErr: ErrInvalidVersion,
	}, {
		name:       "property value with XML characters",
		properties: map[string]string{"version.activemq": "6.3.4</version.activemq>"},
		wantErr:    ErrInvalidVersion,
	}, {
		name:         "manifest file does not exist",
		deps:         []languages.Dependency{mavenDep("org.apache.neethi", "neethi", "3.2.5", "")},
		manifestFile: "missing/pom.xml",
		wantErr:      ErrPomNotFound,
	}, {
		name:       "explicit property not defined anywhere",
		properties: map[string]string{"version.does-not-exist": "1.0.0"},
		wantErr:    ErrPropertyNotFound,
	}, {
		name: "two artifacts sharing a property request different versions",
		deps: []languages.Dependency{
			mavenDep("org.apache.activemq", "activemq-broker", "6.3.4", ""),
			mavenDep("org.apache.activemq", "activemq-client", "6.3.5", ""),
		},
		wantErr: ErrVersionConflict,
	}, {
		name:       "dependency disagrees with the explicit property it reads",
		deps:       []languages.Dependency{mavenDep("org.apache.activemq", "activemq-broker", "6.3.4", "")},
		properties: map[string]string{"version.activemq": "6.3.5"},
		wantErr:    ErrVersionConflict,
	}, {
		name:    "dependency without groupId and artifactId",
		deps:    []languages.Dependency{{Name: "neethi", Version: "3.2.5"}},
		wantErr: ErrMissingRequiredFields,
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := copyTomeePom(t)
			cfg := &languages.UpdateConfig{RootDir: dir, Dependencies: tt.deps, Properties: tt.properties}
			if tt.manifestFile != "" {
				cfg.ManifestFile = filepath.Join(dir, tt.manifestFile)
			}
			err := (&Maven{}).Update(t.Context(), cfg)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Update() error = %v, want %v", err, tt.wantErr)
			}
			if got := readPomProperty(t, dir, "version.activemq"); got != "6.3.3" {
				t.Errorf("pom.xml changed by a failed update: version.activemq = %q", got)
			}
		})
	}
}

// TestMaven_Update_NoError lists conditions Update handles with a log line
// and no error.
func TestMaven_Update_NoError(t *testing.T) {
	tests := []struct {
		name string
		deps []languages.Dependency
	}{{
		name: "dependency the POM does not declare",
		deps: []languages.Dependency{mavenDep("com.fasterxml.jackson.core", "jackson-core", "2.22.4", "")},
	}, {
		name: "versionless entry for a dependency the POM does not declare",
		deps: []languages.Dependency{mavenDep("dom4j", "dom4j", "", "provided")},
	}, {
		name: "version older than the POM declares",
		deps: []languages.Dependency{mavenDep("org.apache.neethi", "neethi", "3.2.2", "")},
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := copyTomeePom(t)
			updateAndValidate(t, &languages.UpdateConfig{RootDir: dir, Dependencies: tt.deps})
		})
	}
}

// TestMaven_Validate_Outcomes checks Validate against a POM that was not
// updated: a dependency at a different version only logs a warning, while a
// property that does not match or does not exist is an error.
func TestMaven_Validate_Outcomes(t *testing.T) {
	tests := []struct {
		name       string
		deps       []languages.Dependency
		properties map[string]string
		wantErr    error
	}{{
		name: "dependency at a different version",
		deps: []languages.Dependency{mavenDep("org.apache.neethi", "neethi", "3.2.5", "")},
	}, {
		name: "dependency the POM does not declare",
		deps: []languages.Dependency{mavenDep("com.fasterxml.jackson.core", "jackson-core", "2.22.4", "")},
	}, {
		name:       "property at a different value",
		properties: map[string]string{"version.activemq": "6.3.4"},
		wantErr:    ErrPropertyValidationFailed,
	}, {
		name:       "property not defined",
		properties: map[string]string{"version.does-not-exist": "1.0.0"},
		wantErr:    ErrPropertyNotFound,
	}, {
		name:       "property at the expected value",
		properties: map[string]string{"version.activemq": "6.3.3"},
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := copyTomeePom(t)
			cfg := &languages.UpdateConfig{RootDir: dir, Dependencies: tt.deps, Properties: tt.properties}
			if err := (&Maven{}).Validate(t.Context(), cfg); !errors.Is(err, tt.wantErr) {
				t.Fatalf("Validate() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// TestMaven_Update_Upgrades runs upgrades through each place the real TomEE
// POM sets a version, and checks where each one lands.
func TestMaven_Update_Upgrades(t *testing.T) {
	dir := copyTomeePom(t)
	cfg := &languages.UpdateConfig{
		RootDir: dir,
		Dependencies: []languages.Dependency{
			// Shared property: both artifacts read ${version.activemq}.
			mavenDep("org.apache.activemq", "activemq-broker", "6.3.4", ""),
			mavenDep("org.apache.activemq", "activemq-client", "6.3.4", ""),
			// Literal managed version.
			mavenDep("org.apache.neethi", "neethi", "3.2.5", ""),
			// Not declared: added to dependencyManagement.
			mavenDep("com.fasterxml.jackson.core", "jackson-core", "2.22.5", ""),
		},
		Properties: map[string]string{"tomcat.version": "10.1.62"},
	}
	updateAndValidate(t, cfg)

	project := parsePomIn(t, dir)
	checkProps(t, project, map[string]string{"version.activemq": "6.3.4", "tomcat.version": "10.1.62"})
	for coordinate, want := range map[string]string{
		"org.apache.activemq:activemq-broker":     "${version.activemq}",
		"org.apache.neethi:neethi":                "3.2.5",
		"com.fasterxml.jackson.core:jackson-core": "2.22.5",
	} {
		if got, ok := managedVersion(project, coordinate); !ok || got != want {
			t.Errorf("managed %s version: got = %q (present %v), want = %q", coordinate, got, ok, want)
		}
	}
}
