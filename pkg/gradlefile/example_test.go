/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package gradlefile_test

import (
	"fmt"
	"strings"

	"github.com/chainguard-dev/omnibump/pkg/gradlefile"
)

func ExampleParseBuild() {
	content := []byte(`dependencies {
    implementation "io.netty:netty-codec:4.1.100.Final"
    implementation "ch.qos.logback:logback-classic:${logbackVersion}"
}`)

	build, err := gradlefile.ParseBuild("build.gradle", content)
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, dep := range build.Dependencies() {
		fmt.Printf("%s:%s version=%q varRef=%q\n", dep.Group, dep.Artifact, dep.Version, dep.VarRef)
	}
	// Output:
	// io.netty:netty-codec version="4.1.100.Final" varRef=""
	// ch.qos.logback:logback-classic version="" varRef="logbackVersion"
}

func ExampleBuildFile_SetDependencyVersion() {
	build, _ := gradlefile.ParseBuild("build.gradle", []byte(`implementation "io.netty:netty-codec:4.1.100.Final"`))

	for _, dep := range build.Dependencies() {
		if err := build.SetDependencyVersion(dep, "4.1.133.Final"); err != nil {
			fmt.Println(err)
			return
		}
	}
	fmt.Println(string(build.Content()))
	fmt.Println("changed:", build.Changed(), "edits:", build.ChangeCount())
	// Output:
	// implementation "io.netty:netty-codec:4.1.133.Final"
	// changed: true edits: 1
}

func ExampleBuildFile_SetVariable() {
	build, _ := gradlefile.ParseBuild("dependencies.gradle", []byte(`versions += [
  log4j2: "2.25.1",
]`))

	for _, variable := range build.Variables() {
		if variable.Path() == "versions.log4j2" {
			if err := build.SetVariable(variable, "2.25.4"); err != nil {
				fmt.Println(err)
				return
			}
		}
	}
	fmt.Println(string(build.Content()))
	// Output:
	// versions += [
	//   log4j2: "2.25.4",
	// ]
}

func ExampleSettingsFile_EnsureManagedBlock() {
	settings, _ := gradlefile.ParseSettings("settings.gradle", []byte("rootProject.name = 'demo'\n"))

	err := settings.EnsureManagedBlock(
		map[string]string{"io.netty:netty-handler": "4.1.135.Final"},
		[]gradlefile.Substitution{{OldModule: "org.lz4:lz4-java", NewModule: "at.yawk.lz4:lz4-java", Version: "1.10.1"}},
	)
	if err != nil {
		fmt.Println(err)
		return
	}

	// Re-parsing the result exposes the managed block's effective pins: each
	// constraint, and each substitution's new module at its target version.
	pinned, _ := gradlefile.ParseSettings("settings.gradle", settings.Content())
	fmt.Println(pinned.ManagedCoordinates()["io.netty:netty-handler"])
	fmt.Println(pinned.ManagedCoordinates()["at.yawk.lz4:lz4-java"])
	// Output:
	// 4.1.135.Final
	// 1.10.1
}

func ExampleNewSettingsFileContent() {
	content, err := gradlefile.NewSettingsFileContent(gradlefile.Kotlin, map[string]string{"a.b:c": "1.0"}, nil)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(content[:len(gradlefile.ForceBlockBegin)])
	// Output:
	// // omnibump:resolutionStrategy:begin
}

func ExampleParseProperties() {
	properties, err := gradlefile.ParseProperties("gradle.properties", []byte("nettyVersion=4.1.125.Final\njackson = 2.15.0\n"))
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println(properties.Keys())
	if err := properties.Set("nettyVersion", "4.1.133.Final"); err != nil {
		fmt.Println(err)
		return
	}
	value, ok := properties.Get("nettyVersion")
	fmt.Println(value, ok, len(properties.Entries()))
	fmt.Print(string(properties.Content()))
	// Output:
	// [nettyVersion jackson]
	// 4.1.125.Final true 2
	// nettyVersion=4.1.133.Final
	// jackson = 2.15.0
}

func ExampleParseCatalog() {
	content := []byte(`[versions]
netty = "4.2.12.Final"

[libraries]
netty-codec = { module = "io.netty:netty-codec", version.ref = "netty" }
okio = { module = "com.squareup.okio:okio", version = "3.4.0" }
`)
	catalog, err := gradlefile.ParseCatalog("gradle/libs.versions.toml", content)
	if err != nil {
		fmt.Println(err)
		return
	}

	for _, library := range catalog.Libraries() {
		fmt.Printf("%s -> ref=%q inline=%q\n", library.Module(), library.VersionRef, library.Version)
	}
	if err := catalog.SetVersion("netty", "4.2.13.Final"); err != nil {
		fmt.Println(err)
		return
	}
	for _, version := range catalog.Versions() {
		fmt.Println(version.Key)
	}
	fmt.Println("changed:", catalog.Changed(), "edits:", catalog.ChangeCount(), catalog.Path())
	// Output:
	// io.netty:netty-codec -> ref="netty" inline=""
	// com.squareup.okio:okio -> ref="" inline="3.4.0"
	// netty
	// changed: true edits: 1 gradle/libs.versions.toml
}

func ExampleCatalogFile_SetLibraryVersion() {
	catalog, _ := gradlefile.ParseCatalog("libs.versions.toml", []byte(`[libraries]
okio = { module = "com.squareup.okio:okio", version = "3.4.0" }
`))

	for _, library := range catalog.Libraries() {
		if err := catalog.SetLibraryVersion(library, "3.6.0"); err != nil {
			fmt.Println(err)
			return
		}
	}
	fmt.Print(string(catalog.Content()))
	// Output:
	// [libraries]
	// okio = { module = "com.squareup.okio:okio", version = "3.6.0" }
}

func ExampleParseSettings() {
	content := []byte(`dependencyResolutionManagement {
    versionCatalogs {
        create("libs") {
            version("netty", "4.1.100.Final")
            library("netty-codec", "io.netty", "netty-codec").versionRef("netty")
        }
    }
}`)
	settings, err := gradlefile.ParseSettings("settings.gradle.kts", content)
	if err != nil {
		fmt.Println(err)
		return
	}

	for _, version := range settings.CatalogVersions() {
		if err := settings.SetCatalogVersion(version, "4.1.133.Final"); err != nil {
			fmt.Println(err)
			return
		}
	}
	fmt.Println(len(settings.CatalogLibraries()), settings.Changed(), settings.ChangeCount(), settings.Path())
	// Output:
	// 1 true 1 settings.gradle.kts
}

func ExampleDSLFromPath() {
	fmt.Println(gradlefile.DSLFromPath("build.gradle") == gradlefile.Groovy)
	fmt.Println(gradlefile.DSLFromPath("build.gradle.kts") == gradlefile.Kotlin)
	// Output:
	// true
	// true
}

func ExampleNormalizeAlias() {
	fmt.Println(gradlefile.NormalizeAlias("netty.codec.http"))
	// Output:
	// netty-codec-http
}

func ExampleValidateVersion() {
	fmt.Println(gradlefile.ValidateVersion("4.1.133.Final"))
	fmt.Println(gradlefile.ValidateVersion(`1.0"; exec("rm")`) != nil)
	// Output:
	// <nil>
	// true
}

func ExampleValidateCoordinate() {
	fmt.Println(gradlefile.ValidateCoordinate("io.netty"))
	fmt.Println(gradlefile.ValidateCoordinate("io.netty'") != nil)
	// Output:
	// <nil>
	// true
}

func ExampleBuildFile_ResolutionRules() {
	content := []byte(`eachDependency { DependencyResolveDetails details ->
    if (details.requested.group == "io.netty" && !details.requested.name.startsWith("netty-tcnative-")) {
        details.useVersion(libs.versions.netty.get())
    }
    if (details.requested.group == 'com.signalfx.public') {
        if (details.requested.name == 'signalfx-java') {
            details.useVersion '1.0.49'
        }
    }
}`)
	build, _ := gradlefile.ParseBuild("build.gradle", content)

	for _, rule := range build.ResolutionRules() {
		fmt.Printf("group=%s artifact=%q catalogKey=%q version=%q\n", rule.Group, rule.Artifact, rule.CatalogKey, rule.Version)
	}

	// Literal rules are editable in place; catalog-backed rules are bumped
	// through their [versions] key instead.
	for _, rule := range build.ResolutionRules() {
		if rule.Version != "" {
			if err := build.SetResolutionRuleVersion(rule, "1.0.50"); err != nil {
				fmt.Println(err)
			}
		}
	}
	fmt.Println("changed:", build.Changed())
	// Output:
	// group=io.netty artifact="" catalogKey="netty" version=""
	// group=com.signalfx.public artifact="signalfx-java" catalogKey="" version="1.0.49"
	// changed: true
}

func ExampleBuildFile_ShipConfigs() {
	// A shadowJar bundling a custom configuration alongside runtimeClasspath:
	// both end up inside the fat jar, so both must be force-pinned.
	content := []byte("shadowJar {\n    configurations = [project.configurations.runtimeClasspath, project.configurations.lineageImplementation]\n}\n")
	build, _ := gradlefile.ParseBuild("build.gradle", content)

	for _, ref := range build.ShipConfigs() {
		fmt.Println(ref.Name)
	}
	// Output:
	// runtimeClasspath
	// lineageImplementation
}

func ExampleIsManagedClasspathName() {
	fmt.Println(gradlefile.IsManagedClasspathName("testRuntimeClasspath"))
	fmt.Println(gradlefile.IsManagedClasspathName("lineageImplementation"))
	// Output:
	// true
	// false
}

func ExampleIsNonShippingConfigName() {
	fmt.Println(gradlefile.IsNonShippingConfigName("groovyDoc"))
	fmt.Println(gradlefile.IsNonShippingConfigName("lineageImplementation"))
	// Output:
	// true
	// false
}

func ExampleSettingsFile_EnsureManagedBlockWithConfigs() {
	settings, _ := gradlefile.ParseSettings("settings.gradle", []byte("rootProject.name = 'demo'\n"))

	// lineageImplementation is bundled into the artifact but is not a classpath,
	// so it is added to the managed block's guard alongside the default match.
	err := settings.EnsureManagedBlockWithConfigs(
		map[string]string{"a.b:c": "1.0"},
		nil,
		[]string{"lineageImplementation"},
	)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(strings.Contains(string(settings.Content()), "configuration.name in ['lineageImplementation']"))
	// Output:
	// true
}

func ExampleNewSettingsFileContentWithConfigs() {
	content, err := gradlefile.NewSettingsFileContentWithConfigs(
		gradlefile.Groovy,
		map[string]string{"a.b:c": "1.0"},
		nil,
		[]string{"lineageImplementation"},
	)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(strings.Contains(content, "configuration.name in ['lineageImplementation']"))
	// Output:
	// true
}
