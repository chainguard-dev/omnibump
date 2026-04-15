# Troubleshooting

## Common Issues and Solutions

### Issue: Language Detection Fails

```
ERROR: failed to detect language
```

**Solution:** Specify language explicitly
```bash
omnibump --language go --deps deps.yaml
```

### Issue: Dependency Not Found

```
ERROR: dependency not found: package-name
```

**Solutions:**
1. Verify the package name is correct
2. For Maven: Check groupId and artifactId
3. For Go: Ensure module path is complete
4. Run analyze first to see existing dependencies

```bash
omnibump analyze
```

### Issue: Version Already Current

```
INFO: Package xyz is already at v1.2.3, skipping
```

This is normal behavior. omnibump skips unnecessary updates to avoid spurious changes.

### Issue: Property Not Found (Maven)

```
ERROR: property not defined: property-name
```

**Solution:** Add property definition to your pom.xml first, or use direct dependency update instead.

### Issue: Permission Denied

```
ERROR: failed to write file: permission denied
```

**Solution:** Ensure you have write permissions to the project directory:
```bash
ls -la go.mod  # Check file permissions
sudo chown $USER:$USER go.mod  # Fix if needed
```

### Issue: go.mod vs go.work Conflicts

```
ERROR: version mismatch between go.mod and go.work
```

**Solution:** omnibump automatically handles workspaces. If you see this error, ensure go.work and go.mod are synchronized:
```bash
go work sync
omnibump --deps deps.yaml
```

### Issue: Downgrade Prevention

```
WARN: Refusing to downgrade package from v2.0.0 to v1.9.0
```

This is a safety feature. To force a downgrade, update manually or adjust your deps.yaml.

### Issue: Build Failures After Update

**Solution:** Run tests and verify compatibility:
```bash
# For Go
go mod tidy
go test ./...

# For Rust
cargo update
cargo test

# For Maven
mvn clean verify

# For Gradle
./gradlew clean build
```

### Issue: Invalid Version Error (Go +incompatible)

```
ERROR: go.mod:18:2: require github.com/docker/docker: version "v28.0.0" invalid: should be v0 or v1, not v28
```

**Cause:** Package requires `+incompatible` suffix but it's missing from go.mod

**Solution:** Omnibump now handles this automatically:
```bash
# Specify version without +incompatible
omnibump --packages "github.com/docker/docker@v28.0.0"

# Omnibump automatically resolves to v28.0.0+incompatible
```

**Why this happens:**
- Go packages with major version >= 2 need `/vN` in path OR `+incompatible` suffix
- `github.com/docker/docker` doesn't have `/v28` in path
- So it needs `+incompatible`: `v28.0.0+incompatible`
- Omnibump queries the Go proxy to get the canonical version

### Issue: Type Mismatch After Dependency Update (Go)

```
ERROR: cannot convert cred (variable of struct type "github.com/docker/docker/api/types/registry".AuthConfig) to type types.AuthConfig
```

**Cause:** Updated a package that requires newer versions of other dependencies

**Solution:** Use omnibump's transitive dependency detection:
```bash
# Omnibump will detect all required co-updates
omnibump --packages "oras.land/oras-go@v1.2.7"

# Error output shows exactly what else needs updating:
# Error: the following dependencies need to be co-updated:
#   - github.com/docker/docker: current v28.0.0, required >= v28.5.1
#   - github.com/docker/cli: current v25.0.1, required >= v28.5.1
#
# To proceed, add these packages to your update:
#   omnibump --packages "oras.land/oras-go@v1.2.7 github.com/docker/docker@v28.5.1 ..."

# Run the suggested command - all packages updated together
```

**Why this happens:**
- Package A v2.0 uses new API from Package B
- Your project has old Package B with old API
- Types/functions don't match between versions
- Need to update both together

**Prevention:**
- Always run the command omnibump suggests
- It automatically detects all required co-updates
- Validates the entire update set together

### Issue: Missing go.sum Entries with Vendor

```
ERROR: failed to run 'go vendor': missing go.sum entry for module providing package xyz
```

**Cause:** go.mod was updated but go.sum wasn't refreshed before vendoring

**Solution:** Omnibump now handles this automatically:
- When vendor directory exists, omnibump runs `go mod tidy` before `go vendor`
- This ensures go.sum is up-to-date

**If you still see this:**
```bash
# Run manually
go mod tidy
go mod vendor
```

### Issue: Merge Conflicts in Lock Files

**Solution:** Let the build tool regenerate the lock file:
```bash
# For Go
go mod tidy

# For Rust
cargo update

# For Maven
# No lock file - just rebuild
```

### Getting More Help

If you encounter an issue not covered here:

1. Run with debug logging:
   ```bash
   omnibump --deps deps.yaml --log-level debug
   ```

2. Check the project issues:
   https://github.com/chainguard-dev/omnibump/issues

3. Create a new issue with:
   - Your omnibump version (`omnibump version`)
   - The command you ran
   - Debug output
   - Minimal reproduction case
