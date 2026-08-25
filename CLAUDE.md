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

### Custom Entities API Patterns

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

### Authentication

`auth.go` owns it. Credentials and the deployment come from
`github.com/getsynq/quality-oauth-go`, the same library every Coalesce Quality
CLI uses, so the credential store is shared and `--region` resolves identically
everywhere.

- Read environment variables through `qualityoauth.Getenv`, never `os.Getenv`, or
  that one setting stops honouring its `SYNQ_`-prefixed alias.
- Precedence is the library's, not this repo's: `Sources.Resolve` decides, and
  `auth_test.go` pins the tiers so a local change cannot quietly diverge.
- `App.FirstPartyClientID` stays empty. The authorization server seeds a client
  row per released first-party CLI and this is not one; an id it does not know is
  rejected at the authorize endpoint and the login then waits for a callback that
  never arrives.
- The `synq:` config section and the `--synq.*` flags are permanent aliases,
  merged in `config.QualityConfig.merge` as the lower-precedence source.

### Relationship reconciliation — a run only withdraws what it computed

The stored edges are listed by bucket, so the answer carries every edge touching
it, including other producers'. Two rules, both breakable in silence:

- A run that computed no relationships withdraws none. Relationships can be on
  while no bucket has a notification configured.
- `ownsRelationship` is the shape test: `gcs::` upstream, `pubsub::` downstream.
  Anything else — a service linked to the bucket, a warehouse table loaded from
  it — belongs to another producer.

`relationships_test.go` covers both; the first two tests in it are the reproducer.
The sibling Pub/Sub integration shipped the same bug in a form that fired by
default and deleted seven edges from a live workspace.

## Key Implementation Details

- Buckets use simple identifiers: `gcs::<bucket_name>`
- Entity groups enable automatic cleanup via API's automatic deletion of entities not in new group
- Icons validated as valid SVG XML before use
- Version info injected via ldflags: `version`, `commit`, `date`
- Context cancellation propagates to all operations for graceful shutdown
- Default bucket type ID is 40
