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

1. `runSync()` - Cobra command handler, sets up context and logging
2. `syncResources()` - Main orchestration
3. `syncBuckets()` - Iterates buckets, filters, creates entities
4. `updateEntityGroup()` - Updates group for automatic cleanup

Referred to by name, not by line: the numbers were stale within one PR of being
written.

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
- That merge takes credentials and the deployment whole, not field by field.
  Both are pairs of alternatives — `connect` prefers a client pair over a token,
  `resolveTarget` prefers an endpoint over a region — so a field-wise merge lets
  the section that is meant to lose decide. A half-filled `quality:` section is
  still completed from `synq:`.

### Relationship reconciliation — a run only withdraws what it computed

The stored edges are listed by bucket, so the answer carries every edge touching
it, including other producers'. Three rules, all breakable in silence:

- `ownsRelationship` is the shape test: `gcs::` upstream, `pubsub::` downstream.
  Anything else — a service linked to the bucket, a warehouse table loaded from
  it — belongs to another producer.
- A run that established nothing withdraws nothing. The sentinel is
  `len(scope.scannedBuckets)`, not the edge count: relationships being on is not
  evidence, and a listing that failed leaves the same empty edge set as a
  workspace with no notifications at all. Asking the edge count instead leaked
  every stale edge forever once a workspace's last notification was removed.
- Within a run, `relationshipScope` decides per bucket. An edge is withdrawn only
  when that bucket's notification list was read in full (`scanned`) and no longer
  names the topic. A bucket whose `Notifications` call failed is never scanned,
  and a notification the run saw is `observed` before the reasons not to publish
  it — the relationship filter excluded it, or the Pub/Sub topic is not an entity
  yet because that integration has not run. None of those mean the notification
  is gone.

Absence is the only evidence a withdrawal ever has, which is why `syncBuckets`
carries the scope out alongside the edges in `desiredRelationships` rather than
letting `deduplicateRelationships` infer intent from an empty set.

`relationships_test.go` covers all three. The sibling Pub/Sub integration shipped
the first form of this bug in a version that fired by default and deleted seven
edges from a live workspace.

## Key Implementation Details

- Buckets use simple identifiers: `gcs::<bucket_name>`
- Entity groups enable automatic cleanup via API's automatic deletion of entities not in new group
- Icons validated as valid SVG XML before use
- Version info injected via ldflags: `version`, `commit`, `date`
- Context cancellation propagates to all operations for graceful shutdown
- Default bucket type ID is 40
