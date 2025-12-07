plugins {
    java
}

repositories {
    mavenCentral()
}

dependencies {
    // Spring Boot style library() function (as seen in spring-boot.yaml)
    implementation(library("commons-lang3", "3.17.0"))
    implementation(library("netty-handler", "4.1.100.Final"))
}

// Mock library function (Spring Boot injects this)
fun library(name: String, version: String): String {
    return "org.example:artifact:$version"
}
