# Common Workflows

## Workflow 1: CVE Response

When a CVE is announced, quickly patch affected dependencies:

```bash
# Step 1: Analyze current state
omnibump analyze --output json > before.json

# Step 2: Try to update vulnerable package
omnibump --packages "vulnerable-package@1.2.3-patched"

# Step 3: If omnibump detects co-updates needed, it provides exact command
# Error: the following dependencies need to be co-updated:
#   - dep-a: current v1.0.0, required >= v1.2.0
#   - dep-b: current v2.5.0, required >= v2.8.0
#
# To proceed, add these packages to your update:
#   omnibump --packages "vulnerable-package@1.2.3-patched dep-a@v1.2.0 dep-b@v2.8.0"

# Step 4: Run suggested command
omnibump --packages "vulnerable-package@1.2.3-patched dep-a@v1.2.0 dep-b@v2.8.0" --tidy

# Step 5: Verify build works
make test

# Step 6: Document the update
omnibump analyze --output json > after.json
```

**For Go projects with transitive detection:**
- Omnibump automatically detects all required co-updates
- Prevents partial updates that break builds
- Ensures compatible version set before applying changes

## Workflow 2: Batch Updates

Update multiple projects with the same dependencies:

```bash
# Create shared configuration
cat > shared-deps.yaml <<EOF
language: auto
packages:
  - name: common-lib
    version: 2.0.0
EOF

# Update all projects
for project in project-a project-b project-c; do
  cd $project
  omnibump --deps ../shared-deps.yaml --dry-run
  omnibump --deps ../shared-deps.yaml
  cd ..
done
```

## Workflow 3: Migration from Legacy Tools

Migrating from gobump, cargobump, or pombump:

```bash
# Your existing files work automatically
omnibump --deps gobump-deps.yaml

# Or rename them (optional)
mv gobump-deps.yaml deps.yaml
omnibump --deps deps.yaml

# The tool warns about legacy names and suggests migration
```

## Workflow 4: CI/CD Integration

Example GitHub Actions workflow:

```yaml
name: Update Dependencies
on:
  workflow_dispatch:
    inputs:
      package:
        description: 'Package to update (name@version)'
        required: true

jobs:
  update:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Install omnibump
        run: |
          wget https://github.com/chainguard-dev/omnibump/releases/latest/download/omnibump-linux-amd64
          chmod +x omnibump-linux-amd64
          sudo mv omnibump-linux-amd64 /usr/local/bin/omnibump

      - name: Update dependency
        run: |
          omnibump --packages "${{ github.event.inputs.package }}" --tidy

      - name: Create PR
        uses: peter-evans/create-pull-request@v5
        with:
          commit-message: "Update ${{ github.event.inputs.package }}"
          title: "Dependency Update: ${{ github.event.inputs.package }}"
```

## Workflow 5: Property Management (Maven)

Understand and manage Maven properties:

```bash
# Step 1: Analyze property usage
omnibump analyze > analysis.txt

# Step 2: Identify which dependencies use properties
grep "used by" analysis.txt

# Step 3: Update properties (affects multiple dependencies)
cat > properties.yaml <<EOF
properties:
  - property: netty.version
    value: 4.1.94.Final
EOF

omnibump --properties properties.yaml

# This single property update affects all netty dependencies
```
