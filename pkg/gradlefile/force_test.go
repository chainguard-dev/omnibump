/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package gradlefile

import "testing"

// TestForcedCoordinates verifies that a legacy force block written into a
// build script by an older omnibump is still recognized.
func TestForcedCoordinates(t *testing.T) {
	content := ForceBlockBegin + `
allprojects {
    afterEvaluate {
        configurations.matching { it.name ==~ /.*([Cc]ompileClasspath|[Rr]untimeClasspath)/ }.all {
            resolutionStrategy {
                force 'a.b:c:1.0'
                force 'io.netty:netty-codec:4.1.133.Final'
            }
        }
    }
}
` + ForceBlockEnd + "\n"

	f := mustParseBuild(t, "build.gradle", content)
	coords := f.ForcedCoordinates()
	if coords["a.b:c"] != "1.0" {
		t.Errorf("ForcedCoordinates()[a.b:c] = %q, want 1.0", coords["a.b:c"])
	}
	if coords["io.netty:netty-codec"] != "4.1.133.Final" {
		t.Errorf("ForcedCoordinates()[io.netty:netty-codec] = %q, want 4.1.133.Final", coords["io.netty:netty-codec"])
	}

	none := mustParseBuild(t, "build.gradle", "apply plugin: 'java'\n")
	if got := none.ForcedCoordinates(); len(got) != 0 {
		t.Errorf("ForcedCoordinates() with no block = %v, want empty", got)
	}
}
