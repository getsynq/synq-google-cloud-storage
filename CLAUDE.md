# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

**For general information about the project, configuration, and usage, see [README.md](README.md).**

## Development Commands

For basic commands (build, run, test), see [README.md](README.md#development).

**Type checking with gopls MCP (preferred when available):**
```bash
mcp__gopls__go_diagnostics
```

**Type checking fallback:**
```bash
go test -run=XXX_SHOULD_NEVER_MATCH_XXX ./...
```

**Format code:**
```bash
golines -w -m 150 .
```

## Code Architecture for Development

### Function Call Hierarchy

Understanding the sync flow when modifying code:

1. `runSync()` (main.go:90) - Cobra command handler, sets up context and logging
2. `syncResources()` (main.go:401) - Main orchestration
3. `syncBuckets()` (main.go:422) - Iterates buckets, filters, creates entities
4. `updateEntityGroup()` (main.go:508) - Updates group for automatic cleanup

**Important patterns:**
- All `must*` functions exit with `os.Exit(1)` on fatal errors (don't return errors)
- Context cancellation checked in iterator loops via `checkCancellation()`
- GCP iterators use `iterator.Done` pattern for completion

### SYNQ Custom Entities API Patterns

**Custom Identifiers:**
```go
&entitiesv1.Identifier{
    Id: &entitiesv1.Identifier_Custom{
        Custom: &entitiesv1.CustomIdentifier{
            Id: fmt.Sprintf("gcs::%s", bucketAttrs.Name),
        },
    },
}
```

### Resource Filtering Implementation

**Filter Interface:**
```go
type Filter interface {
    Accept(id string) bool
}
```

**IncludeExcludeFilter Logic:**
- If include list is empty, all items accepted by default
- Items must match at least one include filter (if any)
- Items matching any exclude filter are rejected (exclude takes precedence)

### Testing Patterns

Uses testify/suite:
```go
type FilterSuite struct {
    suite.Suite
}

func TestFilterSuite(t *testing.T) {
    suite.Run(t, new(FilterSuite))
}

func (s *FilterSuite) TestFilter() {
    s.Require().NoError(err)
    s.True(condition)
}
```

## Key Implementation Details

- Buckets use simple identifiers: `gcs::<bucket_name>`
- Entity groups enable automatic cleanup via API's automatic deletion of entities not in new group
- Icons validated as valid SVG XML before use
- Version info injected via ldflags: `version`, `commit`, `date`
- Context cancellation propagates to all operations for graceful shutdown
- Default bucket type ID is 40
