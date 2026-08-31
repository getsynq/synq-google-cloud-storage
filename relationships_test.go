package main

import (
	"testing"

	entitiescustomv1 "buf.build/gen/go/getsynq/api/protocolbuffers/go/synq/entities/custom/v1"
	entitiesv1 "buf.build/gen/go/getsynq/api/protocolbuffers/go/synq/entities/v1"
	"github.com/stretchr/testify/assert"
)

func customID(id string) *entitiesv1.Identifier {
	return &entitiesv1.Identifier{
		Id: &entitiesv1.Identifier_Custom{Custom: &entitiesv1.CustomIdentifier{Id: id}},
	}
}

func edge(upstream, downstream string) *entitiescustomv1.Relationship {
	return &entitiescustomv1.Relationship{Upstream: customID(upstream), Downstream: customID(downstream)}
}

func edgeKeys(rels []*entitiescustomv1.Relationship) []string {
	keys := make([]string, 0, len(rels))
	for _, rel := range rels {
		keys = append(keys, rel.Upstream.GetCustom().GetId()+"->"+rel.Downstream.GetCustom().GetId())
	}
	return keys
}

// computed is what a run established: every edge given was found on its bucket's
// notification list and published, which also records that bucket as read.
func computed(published ...*entitiescustomv1.Relationship) desiredRelationships {
	run := desiredRelationships{scope: newRelationshipScope()}
	for _, rel := range published {
		run.edges = append(run.edges, rel)
		run = run.andSaw(rel)
	}
	return run
}

// andSaw adds a notification the run found on the bucket but did not publish.
func (d desiredRelationships) andSaw(rel *entitiescustomv1.Relationship) desiredRelationships {
	bucketID := rel.Upstream.GetCustom().GetId()
	d.scope.scanned(bucketID)
	d.scope.observed(bucketID, rel.Downstream.GetCustom().GetId())
	return d
}

// andRead adds a bucket whose notification list was read and named nothing.
func (d desiredRelationships) andRead(bucketID string) desiredRelationships {
	d.scope.scanned(bucketID)
	return d
}

// TestAnotherProducersEdgeSurvives is the reproducer for this sync withdrawing
// lineage it never published.
//
// The stored relationships are listed by bucket, so the answer carries every edge
// touching that bucket — including a service catalog's link from the bucket to
// the service that reads it. The only shape this integration publishes is a
// bucket to its notification topic, and anything else is another producer's.
func TestAnotherProducersEdgeSurvives(t *testing.T) {
	desired := computed(edge("gcs::artefacts", "pubsub::prod.gcs.artefacts"))
	existing := []*entitiescustomv1.Relationship{
		edge("gcs::artefacts", "pubsub::prod.gcs.artefacts"),
		edge("gcs::artefacts", "service::consumer"),
		edge("service::producer", "gcs::artefacts"),
	}

	toCreate, toDelete := deduplicateRelationships(desired, existing)

	assert.Empty(t, toCreate, "an edge that already exists is not created again")
	assert.Empty(t, edgeKeys(toDelete), "only a bucket-to-topic edge is this tool's to withdraw")
}

// TestNothingIsDeletedWhenTheRunEstablishedNothing is the incident rule: a run
// that read no bucket's notification list knows nothing, so it withdraws nothing.
// Relationships being on is not evidence; a listing that failed leaves the same
// empty edge set as a workspace with no notifications at all.
func TestNothingIsDeletedWhenTheRunEstablishedNothing(t *testing.T) {
	existing := []*entitiescustomv1.Relationship{
		edge("gcs::artefacts", "pubsub::prod.gcs.artefacts"),
	}

	_, toDelete := deduplicateRelationships(computed(), existing)

	assert.Empty(t, edgeKeys(toDelete))
}

// TestTheLastNotificationOfABucketLosesItsEdge is the other half of that pair,
// and the case the old edge-count sentinel leaked forever: this run computed no
// edges either, but it read the bucket and the bucket named nothing, so the edge
// is stale rather than unknown.
func TestTheLastNotificationOfABucketLosesItsEdge(t *testing.T) {
	desired := computed().andRead("gcs::artefacts")
	existing := []*entitiescustomv1.Relationship{
		edge("gcs::artefacts", "pubsub::prod.gcs.artefacts"),
	}

	_, toDelete := deduplicateRelationships(desired, existing)

	assert.Equal(t, []string{"gcs::artefacts->pubsub::prod.gcs.artefacts"}, edgeKeys(toDelete))
}

// TestAStaleNotificationEdgeIsDeleted keeps the reconciliation this tool is for:
// a notification removed from the bucket leaves an edge behind, and that edge is
// this tool's own shape, so it goes.
func TestAStaleNotificationEdgeIsDeleted(t *testing.T) {
	desired := computed(edge("gcs::artefacts", "pubsub::prod.gcs.artefacts"))
	existing := []*entitiescustomv1.Relationship{
		edge("gcs::artefacts", "pubsub::prod.gcs.artefacts"),
		edge("gcs::artefacts", "pubsub::prod.gcs.artefacts.retired"),
	}

	_, toDelete := deduplicateRelationships(desired, existing)

	assert.Equal(t, []string{"gcs::artefacts->pubsub::prod.gcs.artefacts.retired"}, edgeKeys(toDelete))
}

// TestOnlyBucketToTopicIsOwned pins the shape test itself.
func TestOnlyBucketToTopicIsOwned(t *testing.T) {
	assert.True(t, ownsRelationship(edge("gcs::artefacts", "pubsub::prod.gcs.artefacts")))
	assert.False(t, ownsRelationship(edge("gcs::artefacts", "service::consumer")))
	assert.False(t, ownsRelationship(edge("service::producer", "gcs::artefacts")))
	assert.False(t, ownsRelationship(edge("gcs::artefacts", "gcs::mirror")))
}

// TestABucketThisRunDidNotComputeKeepsItsEdges is the reproducer for the
// per-bucket form of the withdrawal bug.
//
// The empty-desired guard only covers a run that computed nothing at all. One
// bucket answering while another's notification lookup fails is the ordinary
// case, and the bucket that did not answer contributes no desired edge — so its
// stored edge is indistinguishable from a stale one and goes.
func TestABucketThisRunDidNotComputeKeepsItsEdges(t *testing.T) {
	desired := computed(edge("gcs::logs", "pubsub::prod.gcs.logs"))
	existing := []*entitiescustomv1.Relationship{
		edge("gcs::logs", "pubsub::prod.gcs.logs"),
		// The bucket whose notification list this run never read.
		edge("gcs::artefacts", "pubsub::prod.gcs.artefacts"),
	}

	_, toDelete := deduplicateRelationships(desired, existing)

	assert.Empty(t, edgeKeys(toDelete), "a bucket this run did not compute is not this run's to withdraw")
}

// TestAnEdgeTheRunSawButDidNotPublishSurvives covers the two reasons a bucket's
// own notification does not become a desired edge: the relationship filter
// excludes it, or the Pub/Sub topic is not an entity yet because that
// integration has not run. Neither means the notification is gone.
func TestAnEdgeTheRunSawButDidNotPublishSurvives(t *testing.T) {
	desired := computed(edge("gcs::artefacts", "pubsub::prod.gcs.artefacts")).
		andSaw(edge("gcs::artefacts", "pubsub::prod.gcs.audit"))
	existing := []*entitiescustomv1.Relationship{
		edge("gcs::artefacts", "pubsub::prod.gcs.artefacts"),
		// Configured on the bucket, seen by this run, deliberately not published.
		edge("gcs::artefacts", "pubsub::prod.gcs.audit"),
	}

	_, toDelete := deduplicateRelationships(desired, existing)

	assert.Empty(t, edgeKeys(toDelete), "an edge the run saw but chose not to publish is still configured")
}

// TestABucketReadWithNoNotificationsLeftWithdrawsItsEdge is the other side of the
// scope rule, and the reason it is not simply "never withdraw for a bucket that
// published nothing": the bucket answered and named no topic, so the edge left
// behind is genuinely stale.
func TestABucketReadWithNoNotificationsLeftWithdrawsItsEdge(t *testing.T) {
	desired := computed(edge("gcs::logs", "pubsub::prod.gcs.logs")).andRead("gcs::artefacts")
	existing := []*entitiescustomv1.Relationship{
		edge("gcs::logs", "pubsub::prod.gcs.logs"),
		edge("gcs::artefacts", "pubsub::prod.gcs.artefacts"),
	}

	_, toDelete := deduplicateRelationships(desired, existing)

	assert.Equal(t, []string{"gcs::artefacts->pubsub::prod.gcs.artefacts"}, edgeKeys(toDelete))
}
