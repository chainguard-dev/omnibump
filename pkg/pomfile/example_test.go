/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package pomfile_test

import (
	"fmt"

	"github.com/chainguard-dev/omnibump/pkg/pomfile"
)

func ExampleParse() {
	content := []byte(`<project xmlns="http://maven.apache.org/POM/4.0.0">
  <properties>
    <logback.version>1.5.32</logback.version>
    <netty.version>4.1.135.Final</netty.version>
  </properties>
</project>`)

	pom, err := pomfile.Parse("pom.xml", content)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("path:", pom.Path())
	for _, p := range pom.Properties() {
		fmt.Printf("%s=%s\n", p.Name, p.Value)
	}
	// Output:
	// path: pom.xml
	// logback.version=1.5.32
	// netty.version=4.1.135.Final
}

func ExampleFile_Get() {
	pom, _ := pomfile.Parse("pom.xml", []byte(`<project xmlns="http://maven.apache.org/POM/4.0.0">
  <properties>
    <logback.version>1.5.32</logback.version>
  </properties>
</project>`))

	value, declared := pom.Get("logback.version")
	fmt.Println(value, declared)
	_, declared = pom.Get("jackson.version")
	fmt.Println(declared)
	// Output:
	// 1.5.32 true
	// false
}

func ExampleFile_Has() {
	pom, _ := pomfile.Parse("pom.xml", []byte(`<project xmlns="http://maven.apache.org/POM/4.0.0">
  <properties>
    <logback.version>1.5.32</logback.version>
  </properties>
  <profiles>
    <profile>
      <id>fips</id>
      <properties>
        <bouncycastle.version>1.80</bouncycastle.version>
      </properties>
    </profile>
  </profiles>
</project>`))

	// Profile-scoped properties are not editable, so they are not reported.
	fmt.Println("logback.version:", pom.Has("logback.version"))
	fmt.Println("bouncycastle.version:", pom.Has("bouncycastle.version"))
	// Output:
	// logback.version: true
	// bouncycastle.version: false
}

// A property rewrite touches only the value, so the license header and the
// surrounding formatting survive intact.
func ExampleFile_SetProperty() {
	pom, _ := pomfile.Parse("pom.xml", []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!--
    Copyright Example, Inc. Licensed under Apache-2.0.
-->
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <properties>
    <!-- keep in sync with the agent module -->
    <logback.version>1.5.32</logback.version>
  </properties>
</project>`))

	if err := pom.SetProperty("logback.version", "1.5.35"); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(string(pom.Content()))
	// Output:
	// <?xml version="1.0" encoding="UTF-8"?>
	// <!--
	//     Copyright Example, Inc. Licensed under Apache-2.0.
	// -->
	// <project xmlns="http://maven.apache.org/POM/4.0.0">
	//   <properties>
	//     <!-- keep in sync with the agent module -->
	//     <logback.version>1.5.35</logback.version>
	//   </properties>
	// </project>
}

// Setting a property the POM does not declare is an error rather than an
// insertion: the caller resolved this POM as the property's owner, so a miss
// means that resolution was wrong.
func ExampleFile_SetProperty_undeclared() {
	pom, _ := pomfile.Parse("pom.xml", []byte(`<project xmlns="http://maven.apache.org/POM/4.0.0">
  <properties>
    <logback.version>1.5.32</logback.version>
  </properties>
</project>`))

	fmt.Println(pom.SetProperty("jackson.version", "2.21.5"))
	// Output: property not declared in pom properties: jackson.version in pom.xml
}

func ExampleFile_Changed() {
	content := []byte(`<project xmlns="http://maven.apache.org/POM/4.0.0">
  <properties>
    <logback.version>1.5.32</logback.version>
  </properties>
</project>`)

	pom, _ := pomfile.Parse("pom.xml", content)
	_ = pom.SetProperty("logback.version", "1.5.32")
	fmt.Println("same value:", pom.Changed())

	pom, _ = pomfile.Parse("pom.xml", content)
	_ = pom.SetProperty("logback.version", "1.5.35")
	fmt.Println("new value:", pom.Changed())
	// Output:
	// same value: false
	// new value: true
}
