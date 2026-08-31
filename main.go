package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/xml"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"

	entitiescustomv1grpc "buf.build/gen/go/getsynq/api/grpc/go/synq/entities/custom/v1/customv1grpc"
	entitiescustomv1 "buf.build/gen/go/getsynq/api/protocolbuffers/go/synq/entities/custom/v1"
	entitiesv1 "buf.build/gen/go/getsynq/api/protocolbuffers/go/synq/entities/v1"
	"cloud.google.com/go/storage"
	"github.com/getsynq/synq-google-cloud-storage/config"
	"github.com/joho/godotenv"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

//go:embed icons/gcs.svg
var defaultGCSIcon []byte

// Version information (set via ldflags during build)
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// ============================================================================
// Main Entry Point
// ============================================================================

var rootCmd = &cobra.Command{
	Use:   toolName,
	Short: "Sync Google Cloud Storage buckets to Coalesce Quality",
	Long: `A Google Cloud Storage integration that publishes buckets
as custom entities in Coalesce Quality.

The tool supports configuration through:
  - YAML config file (config.yaml by default)
  - Environment variables (QUALITY_CLIENT_ID, etc.)
  - Command-line flags (highest precedence)

Configuration precedence: defaults → config file → environment variables → flags

Authenticate with a browser login (` + toolName + ` auth login), client
credentials in QUALITY_CLIENT_ID and QUALITY_CLIENT_SECRET, or a pre-issued
QUALITY_TOKEN. A browser login is shared with the other Coalesce Quality tools.`,
	Version: version,
	RunE:    runSync,
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("%s %s\n", toolName, version)
		fmt.Printf("  commit: %s\n", commit)
		fmt.Printf("  built:  %s\n", date)
	},
}

func main() {
	// Load .env file if it exists
	_ = godotenv.Load()

	// Initialize configuration flags
	config.InitFlags()

	// Add subcommands
	rootCmd.AddCommand(versionCmd, authCmd)

	// Execute the root command
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// runSync orchestrates the sync process
func runSync(cmd *cobra.Command, args []string) error {
	// Get context (Cobra handles signal cancellation if set up)
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	// Setup context with cancellation and signal handling
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Setup logging and handle graceful shutdown
	setupLogging(ctx)
	handleShutdown(ctx, cancel)

	logger := slog.Default()
	logger.InfoContext(ctx, "Starting Google Cloud Storage integration")

	// Get config path from flags
	configPath, _ := cmd.Flags().GetString("config")

	// Load configuration
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		logger.ErrorContext(ctx, "Failed to load configuration", slog.String("error", err.Error()))
		return err
	}

	if len(cfg.DeprecatedKeys) > 0 {
		logger.WarnContext(ctx, "Configuration uses superseded keys; the quality section replaces them",
			slog.Any("keys", cfg.DeprecatedKeys),
		)
	}

	// Show dry-run mode warning
	if cfg.DryRun {
		logger.InfoContext(ctx, "DRY-RUN MODE: Will scan GCP resources but not call the Coalesce Quality API")
	}

	// Build filters from configuration. This is a pure read of the config, so it
	// runs before anything is dialled: a bad pattern should not cost a login.
	filters, err := buildFilters(cfg)
	if err != nil {
		logger.ErrorContext(ctx, "Invalid filter configuration", slog.String("error", err.Error()))
		return err
	}

	// Setup clients
	storageClient := mustCreateStorageClient(ctx, cfg)
	defer func() {
		if err := storageClient.Close(); err != nil {
			logger.ErrorContext(ctx, "Error closing Storage client", slog.String("error", err.Error()))
		}
	}()

	var qualityClients *qualityClients
	if !cfg.DryRun {
		qualityClients = mustCreateQualityClients(ctx, cfg)
		defer func() {
			if err := qualityClients.close(); err != nil {
				logger.ErrorContext(ctx, "Error closing Coalesce Quality clients", slog.String("error", err.Error()))
			}
		}()

		// Setup entity types in Coalesce Quality
		mustSetupEntityTypes(ctx, cfg, qualityClients.types)
	}

	// Sync GCS resources to Coalesce Quality
	stats := syncResources(ctx, cfg, storageClient, qualityClients, filters)

	logger.InfoContext(ctx, "Sync completed successfully",
		slog.Int("buckets", stats.Buckets),
		slog.Int("total_entities", stats.TotalEntities),
	)

	return nil
}

// ============================================================================
// Types
// ============================================================================

// qualityClients holds every Coalesce Quality API client a sync uses
type qualityClients struct {
	conn          *grpc.ClientConn
	types         entitiescustomv1grpc.TypesServiceClient
	entities      entitiescustomv1grpc.EntitiesServiceClient
	relationships entitiescustomv1grpc.RelationshipsServiceClient
	groups        entitiescustomv1grpc.GroupsServiceClient
}

func (c *qualityClients) close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// filters holds all configured filters
type filters struct {
	buckets       Filter
	relationships Filter
}

// syncStats tracks sync statistics
type syncStats struct {
	Buckets       int
	TotalEntities int
}

// ============================================================================
// Configuration and Setup
// ============================================================================

// handleShutdown sets up graceful shutdown on interrupt signal
func handleShutdown(ctx context.Context, cancel context.CancelFunc) {
	logger := slog.Default()
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt)
		<-sigChan
		logger.InfoContext(ctx, "Received interrupt signal, shutting down...")
		cancel()
	}()
}

// buildFilters creates all filters from configuration
func buildFilters(cfg *config.Config) (*filters, error) {
	buckets, err := buildIncludeExcludeFilter("filter.buckets", cfg.Filter.Buckets)
	if err != nil {
		return nil, err
	}
	relationships, err := buildIncludeExcludeFilter("relationships.filter", cfg.Relationships.Filter)
	if err != nil {
		return nil, err
	}
	return &filters{buckets: buckets, relationships: relationships}, nil
}

// buildIncludeExcludeFilter creates a filter from include/exclude patterns.
//
// The patterns are the user's, so a bad one is a configuration error naming the
// key and the pattern, not a panic out of the middle of a sync.
func buildIncludeExcludeFilter(section string, rules config.FilterRules) (Filter, error) {
	compile := func(list []string, key string) ([]Filter, error) {
		var compiled []Filter
		for _, pattern := range list {
			filter, err := NewRegexFilter(pattern)
			if err != nil {
				return nil, fmt.Errorf("%s.%s: invalid pattern %q: %w", section, key, pattern, err)
			}
			compiled = append(compiled, filter)
		}
		return compiled, nil
	}

	includeFilters, err := compile(rules.Include, "include")
	if err != nil {
		return nil, err
	}
	excludeFilters, err := compile(rules.Exclude, "exclude")
	if err != nil {
		return nil, err
	}

	return NewIncludeExcludeFilter(includeFilters, excludeFilters), nil
}

// ============================================================================
// Client Setup
// ============================================================================

// mustCreateStorageClient creates a Storage client or exits on error
func mustCreateStorageClient(ctx context.Context, cfg *config.Config) *storage.Client {
	logger := slog.Default()
	logger.InfoContext(ctx, "Connecting to Google Cloud Storage", slog.String("project", cfg.GCP.ProjectID))

	client, err := storage.NewClient(ctx, option.WithUserAgent(cfg.GCP.UserAgent))
	if err != nil {
		logger.ErrorContext(ctx, "Failed to create Storage client", slog.String("error", err.Error()))
		os.Exit(1)
	}

	logger.InfoContext(ctx, "Successfully connected to Google Cloud Storage")
	return client
}

// mustCreateQualityClients resolves credentials and opens the API clients, or exits
func mustCreateQualityClients(ctx context.Context, cfg *config.Config) *qualityClients {
	logger := slog.Default()

	target, err := resolveTarget(cfg)
	if err != nil {
		logger.ErrorContext(ctx, "Could not resolve the Coalesce Quality deployment", slog.String("error", err.Error()))
		os.Exit(1)
	}
	logger.InfoContext(ctx, "Authenticating with the Coalesce Quality API", slog.String("deployment", target.String()))

	conn, err := connect(ctx, cfg, target)
	if err != nil {
		logger.ErrorContext(ctx, "Failed to connect to the Coalesce Quality API", slog.String("error", err.Error()))
		os.Exit(1)
	}

	logger.InfoContext(ctx, "Successfully connected to the Coalesce Quality API")

	return &qualityClients{
		conn:          conn,
		types:         entitiescustomv1grpc.NewTypesServiceClient(conn),
		entities:      entitiescustomv1grpc.NewEntitiesServiceClient(conn),
		relationships: entitiescustomv1grpc.NewRelationshipsServiceClient(conn),
		groups:        entitiescustomv1grpc.NewGroupsServiceClient(conn),
	}
}

// ============================================================================
// Entity Type Management
// ============================================================================

// mustSetupEntityTypes creates or updates the custom entity types
func mustSetupEntityTypes(ctx context.Context, cfg *config.Config, typesClient entitiescustomv1grpc.TypesServiceClient) {
	logger := slog.Default()

	// Load icon
	logger.InfoContext(ctx, "Loading entity type icon")
	bucketIcon := mustLoadIcon(ctx, cfg.Types.BucketIcon, defaultGCSIcon)

	logger.DebugContext(ctx, "Icon loaded successfully",
		slog.Bool("bucket_custom", cfg.Types.BucketIcon != ""),
	)

	// Create/update entity type
	logger.InfoContext(ctx, "Creating/updating entity type in Coalesce Quality",
		slog.Int("bucket_type_id", int(cfg.Types.BucketTypeID)),
	)

	mustUpsertType(ctx, typesClient, cfg.Types.BucketTypeID, "Bucket", bucketIcon)

	logger.InfoContext(ctx, "Entity type created successfully")
}

// mustUpsertType creates or updates a single entity type
func mustUpsertType(ctx context.Context, client entitiescustomv1grpc.TypesServiceClient, typeID int32, name string, icon []byte) {
	logger := slog.Default()

	_, err := client.UpsertType(ctx, &entitiescustomv1.UpsertTypeRequest{
		Type: &entitiesv1.Type{
			TypeId:  typeID,
			Name:    name,
			SvgIcon: icon,
		},
	})
	if err != nil {
		logger.ErrorContext(ctx, "Failed to create entity type", slog.String("name", name), slog.String("error", err.Error()))
		os.Exit(1)
	}
}

// mustLoadIcon loads an icon or exits on error
func mustLoadIcon(ctx context.Context, customPath string, defaultIcon []byte) []byte {
	icon, err := loadIcon(customPath, defaultIcon)
	if err != nil {
		logger := slog.Default()
		logger.ErrorContext(ctx, "Failed to load icon", slog.String("error", err.Error()))
		os.Exit(1)
	}
	return icon
}

// loadIcon loads an icon from a custom path if provided, otherwise returns the default embedded icon
func loadIcon(customPath string, defaultIcon []byte) ([]byte, error) {
	var iconData []byte
	var err error

	if customPath == "" {
		iconData = defaultIcon
	} else {
		iconData, err = os.ReadFile(customPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read custom icon from %s: %w", customPath, err)
		}
	}

	// Validate that the icon is valid SVG
	if err := validateSVG(iconData); err != nil {
		return nil, fmt.Errorf("invalid SVG icon: %w", err)
	}

	return iconData, nil
}

// validateSVG checks if the provided data is valid SVG XML
func validateSVG(data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("icon data is empty")
	}

	trimmed := bytes.TrimSpace(data)
	lowerData := bytes.ToLower(trimmed)

	if !bytes.HasPrefix(lowerData, []byte("<svg")) && !bytes.HasPrefix(lowerData, []byte("<?xml")) {
		return fmt.Errorf("icon does not start with <svg> or <?xml> tag")
	}

	if !bytes.Contains(lowerData, []byte("<svg")) {
		return fmt.Errorf("icon does not contain <svg> tag")
	}

	// Validate as XML
	decoder := xml.NewDecoder(bytes.NewReader(data))
	decoder.Strict = false

	for {
		_, err := decoder.Token()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return fmt.Errorf("invalid XML structure: %w", err)
		}
	}

	return nil
}

// ============================================================================
// Resource Sync
// ============================================================================

// syncResources publishes every GCS resource
func syncResources(ctx context.Context, cfg *config.Config, storageClient *storage.Client, clients *qualityClients, filters *filters) syncStats {
	var entitiesClient entitiescustomv1grpc.EntitiesServiceClient
	if clients != nil {
		entitiesClient = clients.entities
	}

	// List existing custom entities for relationship validation (only if relationships are enabled)
	var customEntities map[string]bool
	if cfg.Relationships.Enabled && entitiesClient != nil {
		var err error
		customEntities, err = listCustomEntities(ctx, entitiesClient)
		if err != nil {
			slog.Default().WarnContext(ctx, "Failed to list custom entities", slog.String("error", err.Error()))
			customEntities = make(map[string]bool) // Use empty map on error
		}
	}

	// Sync buckets
	createdEntities, bucketCount, desired := syncBuckets(
		ctx,
		cfg,
		storageClient,
		entitiesClient,
		filters.buckets,
		filters.relationships,
		customEntities,
	)

	// Manage relationships and entity groups only if not in dry-run mode
	if clients != nil {
		if cfg.Relationships.Enabled {
			manageRelationships(ctx, clients.relationships, createdEntities, desired)
		}
		updateEntityGroup(ctx, cfg, clients.groups, createdEntities)
	}

	return syncStats{
		Buckets:       bucketCount,
		TotalEntities: len(createdEntities),
	}
}

// syncBuckets publishes every bucket
func syncBuckets(
	ctx context.Context,
	cfg *config.Config,
	storageClient *storage.Client,
	entitiesClient entitiescustomv1grpc.EntitiesServiceClient,
	bucketFilter Filter,
	relationshipFilter Filter,
	customEntities map[string]bool,
) ([]*entitiesv1.Identifier, int, desiredRelationships) {
	logger := slog.Default()
	logger.InfoContext(ctx, "Scanning GCS buckets")

	var createdEntities []*entitiesv1.Identifier
	desired := desiredRelationships{scope: newRelationshipScope()}
	bucketCount := 0

	bucketsIterator := storageClient.Buckets(ctx, cfg.GCP.ProjectID)
	for {
		// Check for cancellation
		if err := checkCancellation(ctx); err != nil {
			return createdEntities, bucketCount, desired
		}

		bucketAttrs, err := bucketsIterator.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			logger.ErrorContext(ctx, "Error iterating buckets", slog.String("error", err.Error()))
			os.Exit(1)
		}

		if !bucketFilter.Accept(bucketAttrs.Name) {
			logger.DebugContext(ctx, "Skipping filtered bucket", slog.String("bucket", bucketAttrs.Name))
			continue
		}

		bucketCount++
		logger.DebugContext(ctx, "Processing bucket", slog.String("bucket", bucketAttrs.Name))

		bucketID := &entitiesv1.Identifier{
			Id: &entitiesv1.Identifier_Custom{
				Custom: &entitiesv1.CustomIdentifier{
					Id: fmt.Sprintf("gcs::%s", bucketAttrs.Name),
				},
			},
		}

		createdEntities = append(createdEntities, bucketID)

		// Build description with bucket metadata
		description := buildBucketDescription(bucketAttrs)

		// Build annotations from bucket attributes and labels
		annotations := buildBucketAnnotations(bucketAttrs)

		mustUpsertEntity(ctx, entitiesClient, bucketID, cfg.Types.BucketTypeID, bucketAttrs.Name, description, annotations)

		// Scan bucket notifications to create relationships to Pub/Sub topics
		if cfg.Relationships.Enabled {
			logger.DebugContext(ctx, "Scanning bucket notifications", slog.String("bucket", bucketAttrs.Name))
			bucket := storageClient.Bucket(bucketAttrs.Name)
			notifications, err := bucket.Notifications(ctx)
			if err != nil {
				logger.WarnContext(ctx, "Failed to list bucket notifications",
					slog.String("bucket", bucketAttrs.Name),
					slog.String("error", err.Error()),
				)
			} else {
				logger.DebugContext(ctx, "Found bucket notifications",
					slog.String("bucket", bucketAttrs.Name),
					slog.Int("count", len(notifications)),
				)
				// The list was read in full, so a topic missing from it is one the
				// bucket no longer notifies rather than one this run never saw.
				desired.scope.scanned(bucketID.GetCustom().GetId())
				for _, notification := range notifications {
					// notification.TopicID contains just the topic name
					topicID := notification.TopicID
					logger.DebugContext(ctx, "Processing notification",
						slog.String("bucket", bucketAttrs.Name),
						slog.String("topic_id", topicID),
					)

					relationshipKey := fmt.Sprintf("%s->%s", bucketAttrs.Name, topicID)
					pubsubTopicCustomID := fmt.Sprintf("pubsub::%s", topicID)

					// Recorded before the reasons not to publish it, because none of
					// them mean the notification is gone.
					desired.scope.observed(bucketID.GetCustom().GetId(), pubsubTopicCustomID)

					if relationshipFilter.Accept(relationshipKey) {
						// Check if Pub/Sub topic entity exists (Pub/Sub topics are custom entities)
						if customEntities == nil || customEntities[pubsubTopicCustomID] {
							pubsubTopicID := &entitiesv1.Identifier{
								Id: &entitiesv1.Identifier_Custom{
									Custom: &entitiesv1.CustomIdentifier{
										Id: pubsubTopicCustomID,
									},
								},
							}

							desired.edges = append(desired.edges, &entitiescustomv1.Relationship{
								Upstream:   bucketID,
								Downstream: pubsubTopicID,
							})

							logger.DebugContext(ctx, "Queued bucket notification relationship",
								slog.String("bucket", bucketAttrs.Name),
								slog.String("topic", topicID),
							)
						} else {
							logger.DebugContext(ctx, "Skipping bucket notification relationship - Pub/Sub topic entity not found",
								slog.String("bucket", bucketAttrs.Name),
								slog.String("topic", topicID),
							)
						}
					}
				}
			}
		}
	}

	logger.InfoContext(ctx, "Buckets processed", slog.Int("count", bucketCount))
	return createdEntities, bucketCount, desired
}

// ============================================================================
// Relationship Management
// ============================================================================

// manageRelationships creates and deletes relationships as needed
func manageRelationships(
	ctx context.Context,
	client entitiescustomv1grpc.RelationshipsServiceClient,
	createdEntities []*entitiesv1.Identifier,
	desired desiredRelationships,
) {
	logger := slog.Default()
	logger.InfoContext(ctx, "Retrieving existing relationships")

	listResp, err := client.ListRelationships(ctx, &entitiescustomv1.ListRelationshipsRequest{
		Ids: createdEntities,
	})
	if err != nil {
		logger.ErrorContext(ctx, "Failed to list relationships", slog.String("error", err.Error()))
		os.Exit(1)
	}

	// Deduplicate relationships
	toCreate, toDelete := deduplicateRelationships(desired, listResp.Relationships)

	logger.InfoContext(ctx, "Managing relationships",
		slog.Int("to_create", len(toCreate)),
		slog.Int("to_delete", len(toDelete)),
	)

	if len(toCreate) > 0 {
		_, err = client.UpsertRelationships(ctx, &entitiescustomv1.UpsertRelationshipsRequest{
			Relationships: toCreate,
		})
		if err != nil {
			logger.ErrorContext(ctx, "Failed to create relationships", slog.String("error", err.Error()))
			os.Exit(1)
		}
		logger.InfoContext(ctx, "Relationships created", slog.Int("count", len(toCreate)))
	}

	if len(toDelete) > 0 {
		_, err = client.DeleteRelationships(ctx, &entitiescustomv1.DeleteRelationshipsRequest{
			Relationships: toDelete,
		})
		if err != nil {
			logger.ErrorContext(ctx, "Failed to delete relationships", slog.String("error", err.Error()))
			os.Exit(1)
		}
		logger.InfoContext(ctx, "Relationships deleted", slog.Int("count", len(toDelete)))
	}
}

// deduplicateRelationships compares desired relationships with existing ones
// Returns relationships to create and relationships to delete
func deduplicateRelationships(
	desired desiredRelationships,
	existing []*entitiescustomv1.Relationship,
) ([]*entitiescustomv1.Relationship, []*entitiescustomv1.Relationship) {
	// Build a map of existing relationships for quick lookup
	existingMap := make(map[string]struct{})
	for _, rel := range existing {
		existingMap[rel.String()] = struct{}{}
	}

	// Build a map of desired relationships
	desiredMap := make(map[string]*entitiescustomv1.Relationship)
	for _, rel := range desired.edges {
		desiredMap[rel.String()] = rel
	}

	// Find relationships to create (in desired but not in existing)
	var toCreate []*entitiescustomv1.Relationship
	for key, rel := range desiredMap {
		if _, exists := existingMap[key]; !exists {
			toCreate = append(toCreate, rel)
		}
	}

	// A run that computed nothing withdraws nothing. Relationships can be on while
	// no bucket has a notification configured, and treating that as "everything
	// stored is unwanted" is how a sync deletes lineage it never published.
	if len(desired.edges) == 0 {
		return toCreate, nil
	}

	// Find relationships to delete (in existing but not in desired)
	var toDelete []*entitiescustomv1.Relationship
	for _, rel := range existing {
		if !ownsRelationship(rel) {
			continue
		}
		if !desired.scope.withdraws(rel) {
			continue
		}
		if _, desired := desiredMap[rel.String()]; !desired {
			toDelete = append(toDelete, rel)
		}
	}

	return toCreate, toDelete
}

// desiredRelationships is what a run computed, carried together with the scope
// it computed it in. The edges alone cannot say why an edge is absent, and
// "absent" is the only evidence a withdrawal ever has.
type desiredRelationships struct {
	edges []*entitiescustomv1.Relationship
	scope relationshipScope
}

// relationshipScope records what a run established first-hand: the buckets whose
// notification list it read, and every notification it found there.
//
// The two are separate because a notification the run saw is not always one it
// publishes — the relationship filter may exclude it, or the Pub/Sub topic may
// not be an entity yet because that integration has not run. Neither is evidence
// the notification is gone.
type relationshipScope struct {
	scannedBuckets map[string]bool
	observedEdges  map[string]bool
}

func newRelationshipScope() relationshipScope {
	return relationshipScope{
		scannedBuckets: map[string]bool{},
		observedEdges:  map[string]bool{},
	}
}

// scanned records that this bucket's notification list was read in full, which
// is what makes an edge missing from it stale rather than merely unknown.
func (s relationshipScope) scanned(bucketID string) {
	s.scannedBuckets[bucketID] = true
}

// observed records a notification found on the bucket, published or not.
func (s relationshipScope) observed(bucketID, topicID string) {
	s.observedEdges[edgeKey(bucketID, topicID)] = true
}

// withdraws reports whether this run has the standing to delete rel: the bucket
// answered, and its notification list no longer names the topic.
func (s relationshipScope) withdraws(rel *entitiescustomv1.Relationship) bool {
	bucketID := rel.Upstream.GetCustom().GetId()
	if !s.scannedBuckets[bucketID] {
		return false
	}
	return !s.observedEdges[edgeKey(bucketID, rel.Downstream.GetCustom().GetId())]
}

func edgeKey(bucketID, topicID string) string {
	return bucketID + "->" + topicID
}

// ownsRelationship reports whether this integration is the producer of rel: an
// edge from a bucket to a Pub/Sub topic, which is the only shape it publishes.
//
// The stored edges are listed by bucket, so the answer also carries everything
// else touching that bucket — a service catalog's link to the service that reads
// it, a warehouse table loaded from it. Withdrawing those makes every run undo
// another producer's work.
func ownsRelationship(rel *entitiescustomv1.Relationship) bool {
	return strings.HasPrefix(rel.Upstream.GetCustom().GetId(), "gcs::") &&
		strings.HasPrefix(rel.Downstream.GetCustom().GetId(), "pubsub::")
}

// ============================================================================
// Entity Management
// ============================================================================

// mustUpsertEntity creates or updates an entity
func mustUpsertEntity(
	ctx context.Context,
	client entitiescustomv1grpc.EntitiesServiceClient,
	id *entitiesv1.Identifier,
	typeID int32,
	name string,
	description string,
	annotations []*entitiesv1.Annotation,
) {
	logger := slog.Default()

	// Skip the API call in dry-run mode
	if client == nil {
		logger.DebugContext(ctx, "[DRY-RUN] Would upsert entity",
			slog.String("name", name),
			slog.Int("type_id", int(typeID)),
			slog.String("id", id.GetCustom().GetId()),
			slog.String("description", truncate(description, 100)),
		)
		return
	}

	_, err := client.UpsertEntity(ctx, &entitiescustomv1.UpsertEntityRequest{
		Entity: &entitiesv1.Entity{
			Id:          id,
			TypeId:      typeID,
			Name:        name,
			Description: description,
			Annotations: annotations,
			CreatedAt:   timestamppb.Now(),
		},
	})
	if err != nil {
		logger.ErrorContext(ctx, "Failed to create entity", slog.String("name", name), slog.String("error", err.Error()))
		os.Exit(1)
	}
}

// buildBucketDescription creates a markdown description for a bucket
func buildBucketDescription(attrs *storage.BucketAttrs) string {
	var desc strings.Builder

	// Storage class and location
	_, _ = fmt.Fprintf(&desc, "**Storage Class:** %s\n\n", attrs.StorageClass)
	_, _ = fmt.Fprintf(&desc, "**Location:** %s\n\n", attrs.Location)

	// Creation date
	if !attrs.Created.IsZero() {
		_, _ = fmt.Fprintf(&desc, "**Created:** %s\n\n", attrs.Created.Format("2006-01-02 15:04:05 UTC"))
	}

	// Versioning
	if attrs.VersioningEnabled {
		desc.WriteString("**Versioning:** Enabled\n\n")
	}

	// Lifecycle rules details
	if len(attrs.Lifecycle.Rules) > 0 {
		_, _ = fmt.Fprintf(&desc, "**Lifecycle Rules:** %d configured\n\n", len(attrs.Lifecycle.Rules))
		for i, rule := range attrs.Lifecycle.Rules {
			_, _ = fmt.Fprintf(&desc, "**Rule %d:**\n", i+1)
			_, _ = fmt.Fprintf(&desc, "- Action: %s", rule.Action.Type)
			if rule.Action.StorageClass != "" {
				_, _ = fmt.Fprintf(&desc, " (to %s)", rule.Action.StorageClass)
			}
			desc.WriteString("\n")

			// Add conditions
			conditions := formatLifecycleConditions(rule.Condition)
			if len(conditions) > 0 {
				desc.WriteString("- Conditions:\n")
				for _, cond := range conditions {
					_, _ = fmt.Fprintf(&desc, "  - %s\n", cond)
				}
			}
			desc.WriteString("\n")
		}
	}

	return strings.TrimSpace(desc.String())
}

// formatLifecycleConditions formats lifecycle rule conditions as human-readable strings
func formatLifecycleConditions(cond storage.LifecycleCondition) []string {
	var conditions []string

	if cond.AllObjects {
		conditions = append(conditions, "All objects")
	}
	if cond.AgeInDays > 0 {
		conditions = append(conditions, fmt.Sprintf("Age >= %d days", cond.AgeInDays))
	}
	if !cond.CreatedBefore.IsZero() {
		conditions = append(conditions, fmt.Sprintf("Created before %s", cond.CreatedBefore.Format("2006-01-02")))
	}
	if cond.DaysSinceCustomTime > 0 {
		conditions = append(conditions, fmt.Sprintf("Days since custom time >= %d", cond.DaysSinceCustomTime))
	}
	if cond.DaysSinceNoncurrentTime > 0 {
		conditions = append(conditions, fmt.Sprintf("Days since noncurrent >= %d", cond.DaysSinceNoncurrentTime))
	}
	if cond.Liveness != 0 {
		livenessStr := "Unknown"
		switch cond.Liveness {
		case storage.Live:
			livenessStr = "Live"
		case storage.Archived:
			livenessStr = "Archived"
		}
		conditions = append(conditions, fmt.Sprintf("Liveness: %s", livenessStr))
	}
	if len(cond.MatchesPrefix) > 0 {
		conditions = append(conditions, fmt.Sprintf("Prefix matches: %v", cond.MatchesPrefix))
	}
	if len(cond.MatchesSuffix) > 0 {
		conditions = append(conditions, fmt.Sprintf("Suffix matches: %v", cond.MatchesSuffix))
	}
	if len(cond.MatchesStorageClasses) > 0 {
		conditions = append(conditions, fmt.Sprintf("Storage class in: %v", cond.MatchesStorageClasses))
	}
	if cond.NumNewerVersions > 0 {
		conditions = append(conditions, fmt.Sprintf("Number of newer versions >= %d", cond.NumNewerVersions))
	}
	if !cond.NoncurrentTimeBefore.IsZero() {
		conditions = append(conditions, fmt.Sprintf("Noncurrent before %s", cond.NoncurrentTimeBefore.Format("2006-01-02")))
	}
	if !cond.CustomTimeBefore.IsZero() {
		conditions = append(conditions, fmt.Sprintf("Custom time before %s", cond.CustomTimeBefore.Format("2006-01-02")))
	}

	return conditions
}

// buildBucketAnnotations creates annotations from bucket attributes
func buildBucketAnnotations(attrs *storage.BucketAttrs) []*entitiesv1.Annotation {
	annotations := []*entitiesv1.Annotation{
		{Name: "gcs.storage_class", Values: []string{attrs.StorageClass}},
		{Name: "gcs.location", Values: []string{attrs.Location}},
		{Name: "gcs.versioning_enabled", Values: []string{fmt.Sprintf("%t", attrs.VersioningEnabled)}},
	}

	// Add lifecycle rules annotation
	if len(attrs.Lifecycle.Rules) > 0 {
		annotations = append(annotations, &entitiesv1.Annotation{
			Name:   "gcs.has_lifecycle_rules",
			Values: []string{"true"},
		})
	}

	// Add uniform bucket-level access status
	if attrs.UniformBucketLevelAccess.Enabled {
		annotations = append(annotations, &entitiesv1.Annotation{
			Name:   "gcs.uniform_bucket_level_access",
			Values: []string{"true"},
		})
	}

	// Add user-defined labels from the bucket
	for key, value := range attrs.Labels {
		annotations = append(annotations, &entitiesv1.Annotation{
			Name:   fmt.Sprintf("gcs.label.%s", key),
			Values: []string{value},
		})
	}

	return annotations
}

// ============================================================================
// Entity Group Management
// ============================================================================

// updateEntityGroup updates the entity group for automatic cleanup
func updateEntityGroup(
	ctx context.Context,
	cfg *config.Config,
	client entitiescustomv1grpc.GroupsServiceClient,
	createdEntities []*entitiesv1.Identifier,
) {
	logger := slog.Default()

	logger.InfoContext(ctx, "Updating entity group", slog.String("group_id", cfg.GCP.EntityGroupID))

	_, err := client.UpsertEntitiesGroup(ctx, &entitiescustomv1.UpsertEntitiesGroupRequest{
		Group: &entitiescustomv1.Group{
			GroupId:   cfg.GCP.EntityGroupID,
			EntityIds: createdEntities,
			CreatedAt: timestamppb.Now(),
			UpdatedAt: timestamppb.Now(),
		},
	})
	if err != nil {
		logger.ErrorContext(ctx, "Failed to update entity group", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

// ============================================================================
// Logging Setup
// ============================================================================

// setupLogging configures slog based on environment variables
func setupLogging(ctx context.Context) {
	// Determine if source location should be added (defaults to false for cleaner output)
	addSource := false
	if logAddSource := os.Getenv("LOG_ADD_SOURCE"); logAddSource == "true" {
		addSource = true
	}

	// Determine log level from environment variable (defaults to INFO)
	logLevel := slog.LevelInfo
	if logLevelStr := os.Getenv("LOG_LEVEL"); logLevelStr != "" {
		switch logLevelStr {
		case "DEBUG":
			logLevel = slog.LevelDebug
		case "INFO":
			logLevel = slog.LevelInfo
		case "WARN":
			logLevel = slog.LevelWarn
		case "ERROR":
			logLevel = slog.LevelError
		default:
			slog.Default().WarnContext(ctx, "Invalid LOG_LEVEL value, using INFO", slog.String("value", logLevelStr))
		}
	}

	// Configure handler options
	handlerOpts := &slog.HandlerOptions{
		AddSource: addSource,
		Level:     logLevel,
	}

	// Determine log format from environment variable (defaults to text for human readability)
	logFormat := os.Getenv("LOG_FORMAT")
	var handler slog.Handler
	if logFormat == "json" {
		handler = slog.NewJSONHandler(os.Stdout, handlerOpts)
	} else {
		// Default to text format for better human readability
		handler = slog.NewTextHandler(os.Stdout, handlerOpts)
	}

	slog.SetDefault(slog.New(handler))
}

// ============================================================================
// Utility Functions
// ============================================================================

// checkCancellation checks if context is cancelled and returns early if so
func checkCancellation(ctx context.Context) error {
	select {
	case <-ctx.Done():
		logger := slog.Default()
		logger.InfoContext(ctx, "Context cancelled, stopping scan")
		return ctx.Err()
	default:
		return nil
	}
}

// truncate truncates a string to a maximum length
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) > maxLen {
		return string(runes[:maxLen])
	}
	return s
}

// listCustomEntities retrieves all custom entities and returns them as a map for efficient lookups
func listCustomEntities(ctx context.Context, client entitiescustomv1grpc.EntitiesServiceClient) (map[string]bool, error) {
	if client == nil {
		return nil, nil // In dry-run mode, return nil
	}

	resp, err := client.ListEntities(ctx, &entitiescustomv1.ListEntitiesRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to list entities: %w", err)
	}

	entityMap := make(map[string]bool)
	for _, entity := range resp.GetEntities() {
		if customID := entity.GetId().GetCustom(); customID != nil {
			entityMap[customID.GetId()] = true
		}
	}

	return entityMap, nil
}
