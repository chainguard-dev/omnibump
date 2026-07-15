// The `app` module has an UNRELATED `val nettyVersion` that happens to share a
// name with the dependency version declared in the `lib` module. It is the
// application's own artifact version, not a dependency coordinate.
val nettyVersion = "9.9.9"

group = "com.example"
version = nettyVersion
