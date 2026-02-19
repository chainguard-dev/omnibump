# Common Workflows

## Workflow 1: CVE Response

When a CVE is announced, quickly patch affected dependencies:

```bash
# Step 1: Analyze current state
omnibump analyze --output json > before.json

# Step 2: Create patch configuration
cat > deps.yaml <<EOF
language: auto
packages:
  - name: vulnerable-package
    version: 1.2.3-patched
EOF

# Step 3: Test update (dry run)
omnibump --deps deps.yaml --dry-run

# Step 4: Apply update
omnibump --deps deps.yaml --tidy

# Step 5: Verify
omnibump analyze --output json > after.json
```

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
