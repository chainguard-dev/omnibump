// The `lib` module declares its own `val nettyVersion` and interpolates it into
// a real dependency coordinate.
val nettyVersion = "4.1.100.Final"

dependencies {
    implementation("io.netty:netty-codec:$nettyVersion")
}
