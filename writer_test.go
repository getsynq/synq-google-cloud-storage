package main

import (
	"context"
	"testing"

	entitiescustomv1grpc "buf.build/gen/go/getsynq/api/grpc/go/synq/entities/custom/v1/customv1grpc"
	entitiescustomv1 "buf.build/gen/go/getsynq/api/protocolbuffers/go/synq/entities/custom/v1"
	entitiesv1 "buf.build/gen/go/getsynq/api/protocolbuffers/go/synq/entities/v1"
	actorv1 "buf.build/gen/go/getsynq/api/protocolbuffers/go/synq/issues/actor/v1"
	"github.com/getsynq/synq-google-cloud-storage/config"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

// recordingTypes records every UpsertType request. Any other method panics, as
// the embedded interface is nil.
type recordingTypes struct {
	entitiescustomv1grpc.TypesServiceClient
	upserts []*entitiescustomv1.UpsertTypeRequest
}

func (r *recordingTypes) UpsertType(
	_ context.Context,
	req *entitiescustomv1.UpsertTypeRequest,
	_ ...grpc.CallOption,
) (*entitiescustomv1.UpsertTypeResponse, error) {
	r.upserts = append(r.upserts, req)
	return &entitiescustomv1.UpsertTypeResponse{}, nil
}

// recordingEntities records every UpsertEntity request. Any other method
// panics, as the embedded interface is nil.
type recordingEntities struct {
	entitiescustomv1grpc.EntitiesServiceClient
	upserts []*entitiescustomv1.UpsertEntityRequest
}

func (r *recordingEntities) UpsertEntity(
	_ context.Context,
	req *entitiescustomv1.UpsertEntityRequest,
	_ ...grpc.CallOption,
) (*entitiescustomv1.UpsertEntityResponse, error) {
	r.upserts = append(r.upserts, req)
	return &entitiescustomv1.UpsertEntityResponse{}, nil
}

// TestWriterActorNamesTheToolAndTheProject pins what the workspace shows as the
// writer of a bucket and of the Bucket type.
func TestWriterActorNamesTheToolAndTheProject(t *testing.T) {
	actor := writerActor("my-project")
	require.Equal(t, "synq-google-cloud-storage (my-project)", actor.GetName())
	require.Equal(t, actorv1.Actor_VIA_API, actor.GetVia())
	require.Nil(t, actor.GetUser(), "no person runs the sync, so the actor names no user")
}

// TestEveryUpsertSendsTheWriter checks that the type and the entity upserts
// carry the actor. An upsert that sends no actor gets the writer from the
// token, so the app shows a client id instead of the tool.
func TestEveryUpsertSendsTheWriter(t *testing.T) {
	ctx := context.Background()
	writer := writerActor("my-project")

	types := &recordingTypes{}
	cfg := &config.Config{Types: config.TypesConfig{BucketTypeID: 40}}
	mustSetupEntityTypes(ctx, cfg, types, writer)

	require.Len(t, types.upserts, 1)
	require.True(t, proto.Equal(writer, types.upserts[0].GetActor()), "type upsert actor: %v", types.upserts[0].GetActor())

	entities := &recordingEntities{}
	for _, bucket := range []string{"bucket-a", "bucket-b"} {
		mustUpsertEntity(ctx, entities, writer, customID("gcs::"+bucket), 40, bucket, "", []*entitiesv1.Annotation{})
	}

	require.Len(t, entities.upserts, 2)
	for _, req := range entities.upserts {
		require.True(t, proto.Equal(writer, req.GetActor()), "entity %s actor: %v", req.GetEntity().GetId(), req.GetActor())
	}
}
