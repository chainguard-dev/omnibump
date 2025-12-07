plugins {
    kotlin("jvm") version "1.9.0"
}

group = "com.example"
version = "1.0-SNAPSHOT"

repositories {
    mavenCentral()
}

dependencies {
    // String notation - most common
    implementation("org.apache.commons:commons-lang3:3.14.0")
    implementation("io.netty:netty-all:4.1.101.Final")

    // Test dependencies
    testImplementation("junit:junit:4.13.3")
    testImplementation("org.mockito:mockito-core:5.0.0")
}

tasks.test {
    useJUnitPlatform()
}
