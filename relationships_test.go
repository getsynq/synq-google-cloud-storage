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

// TestAnotherProducersEdgeSurvives is the reproducer for this sync withdrawing
// lineage it never published.
//
// The stored relationships are listed by bucket, so the answer carries every edge
// touching that bucket — including a service catalog's link from the bucket to
// the service that reads it. The only shape this integration publishes is a
// bucket to its notification topic, and anything else is another producer's.
func TestAnotherProducersEdgeSurvives(t *testing.T) {
	desired := []*entitiescustomv1.Relationship{
		edge("gcs::artefacts", "pubsub::prod.gcs.artefacts"),
	}
	existing := []*entitiescustomv1.Relationship{
		edge("gcs::artefacts", "pubsub::prod.gcs.artefacts"),
		edge("gcs::artefacts", "service::consumer"),
		edge("service::producer", "gcs::artefacts"),
	}

	toCreate, toDelete := deduplicateRelationships(desired, existing)

	assert.Empty(t, toCreate, "an edge that already exists is not created again")
	assert.Empty(t, edgeKeys(toDelete), "only a bucket-to-topic edge is this tool's to withdraw")
}

// TestNothingIsDeletedWhenTheRunComputedNothing covers a run with relationships
// on where no bucket has a notification configured: the desired set is empty, and
// an empty desired set must not mean "everything stored is unwanted".
func TestNothingIsDeletedWhenTheRunComputedNothing(t *testing.T) {
	existing := []*entitiescustomv1.Relationship{
		edge("gcs::artefacts", "pubsub::prod.gcs.artefacts"),
	}

	_, toDelete := deduplicateRelationships(nil, existing)

	assert.Empty(t, edgeKeys(toDelete))
}

// TestAStaleNotificationEdgeIsDeleted keeps the reconciliation this tool is for:
// a notification removed from the bucket leaves an edge behind, and that edge is
// this tool's own shape, so it goes.
func TestAStaleNotificationEdgeIsDeleted(t *testing.T) {
	desired := []*entitiescustomv1.Relationship{
		edge("gcs::artefacts", "pubsub::prod.gcs.artefacts"),
	}
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
