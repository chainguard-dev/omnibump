/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package mavenversion

import "testing"

// Ordered lists from Apache Maven's ComparableVersionTest
// (maven-artifact, org.apache.maven.artifact.versioning). Each entry is
// strictly greater than the one before it.
var (
	versionsQualifier = []string{
		"1-alpha2snapshot", "1-alpha2", "1-alpha-123", "1-beta-2", "1-beta123", "1-m2", "1-m11", "1-rc",
		"1-cr2", "1-rc123", "1-SNAPSHOT", "1", "1-sp", "1-sp2", "1-sp123", "1-abc", "1-def", "1-pom-1",
		"1-1-snapshot", "1-1", "1-2", "1-123",
	}
	versionsNumber = []string{
		"2.0", "2.0.a", "2-1", "2.0.2", "2.0.123", "2.1.0", "2.1-a", "2.1b", "2.1-c", "2.1-1", "2.1.0.1",
		"2.2", "2.123", "11.a2", "11.a11", "11.b2", "11.b11", "11.m2", "11.m11", "11", "11.a", "11b",
		"11c", "11m",
	}
)

// assertOrdered checks that lower sorts strictly before higher, in both
// argument orders.
func assertOrdered(t *testing.T, lower, higher string) {
	t.Helper()
	if got := compareVersions(lower, higher); got >= 0 {
		t.Errorf("compareVersions(%q, %q): got = %d, want < 0", lower, higher, got)
	}
	if got := compareVersions(higher, lower); got <= 0 {
		t.Errorf("compareVersions(%q, %q): got = %d, want > 0", higher, lower, got)
	}
}

func TestCompareVersions_MavenOrdering(t *testing.T) {
	for name, list := range map[string][]string{"qualifiers": versionsQualifier, "numbers": versionsNumber} {
		t.Run(name, func(t *testing.T) {
			for i := range len(list) - 1 {
				assertOrdered(t, list[i], list[i+1])
			}
		})
	}
}

// TestCompareVersions_RealVersions orders versions taken from real
// dependency pins and the upstream builds they target.
func TestCompareVersions_RealVersions(t *testing.T) {
	tests := []struct {
		name          string
		lower, higher string
	}{
		{"activemq", "6.2.7", "6.3.3"},
		{"tomcat", "10.1.59", "10.1.61"},
		{"jackson", "2.21.7", "2.22.4"},
		{"kafka major", "3.9.2", "4.2.0"},
		{"netty Final", "4.1.118.Final", "4.1.125.Final"},
		{"netty numeric not lexical", "4.2.9.Final", "4.2.17.Final"},
		{"netty-tcnative", "2.0.61.Final", "2.0.70.Final"},
		{"guava jre", "33.4.8-jre", "33.7.2-jre"},
		{"milestone before release candidate", "6.1.0-M1", "6.1.0-RC1"},
		{"release candidate before release", "6.1.0-RC1", "6.1.0"},
		{"RELEASE naming", "2.3.12.RELEASE", "2.4.0"},
		{"bouncycastle", "1.85", "1.85.2"},
		{"four segments", "13.12.0.5977", "13.15.0.6376"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertOrdered(t, tt.lower, tt.higher)
		})
	}
}

func TestCompareVersions_Equal(t *testing.T) {
	// Equal pairs from ComparableVersionTest.testVersionsEqual, then release
	// spellings used by real artifacts.
	for _, pair := range [][2]string{
		{"1", "1"},
		{"1", "1.0"},
		{"1", "1.0.0"},
		{"1.0", "1.0.0"},
		{"1", "1-0"},
		{"1", "1.0-0"},
		{"1.0", "1.0-0"},
		{"1a", "1-a"},
		{"1a", "1.0-a"},
		{"1a", "1.0.0-a"},
		{"1.0a", "1-a"},
		{"1.0.0a", "1-a"},
		{"1x", "1-x"},
		{"1x", "1.0-x"},
		{"1x", "1.0.0-x"},
		{"1.0x", "1-x"},
		{"1.0.0x", "1-x"},
		{"1ga", "1"},
		{"1release", "1"},
		{"1final", "1"},
		{"1cr", "1rc"},
		{"1a1", "1-alpha-1"},
		{"1b2", "1-beta-2"},
		{"1m3", "1-milestone-3"},
		{"1X", "1x"},
		{"1A", "1a"},
		{"1B", "1b"},
		{"1M", "1m"},
		{"1Ga", "1"},
		{"1GA", "1"},
		{"1RELEASE", "1"},
		{"1Final", "1"},
		{"1FINAL", "1"},
		{"1Cr", "1Rc"},
		{"1cR", "1rC"},
		{"1m3", "1Milestone3"},
		{"1m3", "1MileStone3"},
		{"1m3", "1MILESTONE3"},
		{"2.3.12.RELEASE", "2.3.12"},
		{"2.0.61.Final", "2.0.61"},
	} {
		t.Run(pair[0]+"="+pair[1], func(t *testing.T) {
			if got := compareVersions(pair[0], pair[1]); got != 0 {
				t.Errorf("compareVersions(%q, %q): got = %d, want = 0", pair[0], pair[1], got)
			}
		})
	}
}

func TestHighest(t *testing.T) {
	tests := []struct {
		name     string
		versions []string
		want     string
	}{
		{"numeric order", []string{"4.1.118.Final", "4.1.125.Final", "4.1.9.Final"}, "4.1.125.Final"},
		{"property reference ignored", []string{"${netty.version}", "4.1.118.Final"}, "4.1.118.Final"},
		{"nothing concrete", []string{"", "[1.0,2.0)"}, ""},
		{"empty", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Highest(tt.versions); got != tt.want {
				t.Errorf("Highest(%q): got = %q, want = %q", tt.versions, got, tt.want)
			}
		})
	}
}

func TestAtLeast(t *testing.T) {
	tests := []struct {
		name         string
		actual, want string
		ok           bool
	}{
		{"newer", "6.3.3", "6.2.7", true},
		{"same", "6.3.3", "6.3.3", true},
		{"older", "6.3.3", "6.3.4", false},
		{"both empty", "", "", true},
		{"same property reference", "${version.activemq}", "${version.activemq}", true},
		{"property reference against version", "${version.activemq}", "6.3.3", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := AtLeast(tt.actual, tt.want); got != tt.ok {
				t.Errorf("AtLeast(%q, %q): got = %v, want = %v", tt.actual, tt.want, got, tt.ok)
			}
		})
	}
}

func TestIsDowngrade(t *testing.T) {
	tests := []struct {
		name               string
		current, requested string
		want               bool
	}{
		{"older minor", "6.3.3", "6.2.7", true},
		{"older patch", "3.2.4", "3.2.2", true},
		{"same", "6.3.3", "6.3.3", false},
		{"newer", "6.3.3", "6.3.4", false},
		{"older with qualifier", "33.7.2-jre", "33.4.8-jre", true},
		// Without a concrete current version there is nothing to compare.
		{"no current version", "", "1.0.0", false},
		{"unresolved property", "${version.netty}", "4.1.118.Final", false},
		// Version ranges are not single versions.
		{"current is a range", "[1.0,2.0)", "1.0.0", false},
		{"request is a range", "2.0.0", "[1.0,2.0)", false},
		// A versionless request (scope-only entry) is never a downgrade.
		{"versionless request", "6.3.3", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsDowngrade(tt.current, tt.requested); got != tt.want {
				t.Errorf("IsDowngrade(%q, %q): got = %v, want = %v", tt.current, tt.requested, got, tt.want)
			}
		})
	}
}
