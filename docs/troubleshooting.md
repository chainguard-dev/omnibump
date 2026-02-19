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
