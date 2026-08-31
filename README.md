# Coalesce Quality Google Cloud Storage Integration

A Google Cloud Storage integration that automatically publishes your GCS buckets as custom entities in the Coalesce Quality data catalog.

## What It Does

This integration:
- **Discovers** all GCS buckets in your Google Cloud project
- **Creates** a custom entity for each bucket with rich metadata
- **Syncs** bucket information including:
  - Storage class and location
  - Versioning status
  - Lifecycle rules (with detailed conditions)
  - Uniform bucket-level access settings
  - User-defined labels
- **Creates lineage** relationships to Pub/Sub topics for bucket notification configurations
- **Filters** buckets based on include/exclude patterns
- **Auto-cleanup** removed buckets using entity groups

Each bucket entity includes a detailed description with storage class, location, creation date, versioning status, and complete lifecycle rule configurations.

## Installation

### Download Pre-built Binaries

Download the latest release for your platform from the [releases page](https://github.com/getsynq/synq-google-cloud-storage/releases).

**macOS (Intel):**
```bash
curl -LO https://github.com/getsynq/synq-google-cloud-storage/releases/latest/download/synq-google-cloud-storage_darwin_amd64.tar.gz
tar -xzf synq-google-cloud-storage_darwin_amd64.tar.gz
sudo mv synq-google-cloud-storage /usr/local/bin/
```

**macOS (Apple Silicon):**
```bash
curl -LO https://github.com/getsynq/synq-google-cloud-storage/releases/latest/download/synq-google-cloud-storage_darwin_arm64.tar.gz
tar -xzf synq-google-cloud-storage_darwin_arm64.tar.gz
sudo mv synq-google-cloud-storage /usr/local/bin/
```

**Linux (AMD64):**
```bash
curl -LO https://github.com/getsynq/synq-google-cloud-storage/releases/latest/download/synq-google-cloud-storage_linux_amd64.tar.gz
tar -xzf synq-google-cloud-storage_linux_amd64.tar.gz
sudo mv synq-google-cloud-storage /usr/local/bin/
```

**Linux (ARM64):**
```bash
curl -LO https://github.com/getsynq/synq-google-cloud-storage/releases/latest/download/synq-google-cloud-storage_linux_arm64.tar.gz
tar -xzf synq-google-cloud-storage_linux_arm64.tar.gz
sudo mv synq-google-cloud-storage /usr/local/bin/
```

**Windows:**
Download the `.zip` file from the releases page and extract it.

### Build from Source

Requires Go 1.24 or later:
```bash
git clone https://github.com/getsynq/synq-google-cloud-storage.git
cd synq-google-cloud-storage
go build
```

## Configuration

The application works with sensible defaults and minimal configuration. All it needs is a credential and a GCP project.

### Authentication

There are three ways to authenticate, and they resolve in this order — the
unattended ones win, so a scheduled sync never picks up a developer's browser
session:

**1. Client credentials.** For CI and scheduled runs. In the environment or in a
`.env` file in the project root:

```bash
QUALITY_CLIENT_ID=your_client_id_here
QUALITY_CLIENT_SECRET=your_client_secret_here
```

**2. A pre-issued access token**, as `QUALITY_TOKEN`.

**3. A browser login.** For running it by hand:

```bash
synq-google-cloud-storage auth login          # opens a browser
synq-google-cloud-storage auth status         # what is stored, and for which deployment
synq-google-cloud-storage auth logout
```

The credential is cached under `~/.synq/oauth/`, partitioned by deployment, and
**shared with the other Coalesce Quality tools** — so a login done by `synqctl`
or `synq-recon` already serves this integration, and the other way round.

Publishing entities needs `SCOPE_ENTITY_EDIT`, `SCOPE_ENTITY_TYPE_EDIT` and
`SCOPE_LINEAGE_EDIT`, which reach a personal token through the Admin role and
above. An account without them can still run `--dry-run`; `auth status` says
which of your stored credentials can publish.

The `SYNQ_`-prefixed spelling of every variable above is still read, so existing
`.env` files and CI configuration keep working.

See `.env.example` for a template.

### Choosing a deployment

```bash
synq-google-cloud-storage --region us          # eu (default), us or au
synq-google-cloud-storage --endpoint host:443  # a self-hosted deployment
```

Resolved highest first: `--endpoint`, `--region`, the config file, then
`QUALITY_API_ENDPOINT` / `QUALITY_REGION`, then the deployment you last logged
into, then the EU region. So logging into a single region once is enough; you
never type `--region` again.

**Note:** The GCP project ID can be auto-detected from (in order of precedence):
1. `GCP_PROJECT_ID` environment variable
2. `GOOGLE_CLOUD_PROJECT` environment variable
3. `GCLOUD_PROJECT` environment variable (legacy)
4. `gcloud` CLI configuration (`gcloud config get-value project`)
5. GCP metadata server (when running on GCP)
6. `gcp.project_id` in config.yaml

If your gcloud CLI is configured with a project (`gcloud config set project YOUR_PROJECT`), the application will automatically use it.

### Optional: Configuration File (config.yaml)

The application works with defaults out of the box. For customization, create a `config.yaml` file (see `config.yaml.example` for reference):

**Configuration precedence:** defaults → config.yaml → environment variables

```yaml
# Coalesce Quality configuration (all optional)
quality:
  # region: "us"                      # eu (default), us or au
  # endpoint: "api.us.synq.io:443"    # overrides region
  # client_id: ""                     # prefer QUALITY_CLIENT_ID
  # client_secret: ""                 # prefer QUALITY_CLIENT_SECRET
  # token: ""                         # a pre-issued access token
# The `synq:` section this replaced is still read, and fills in anything
# `quality:` leaves unset.

# GCP Configuration (optional, defaults shown)
gcp:
  user_agent: "synq-gcs-client-v1.0.0"
  # project_id can also be set here instead of GCP_PROJECT_ID env var
  # entity_group_id: "gcs::custom-group-id"  # defaults to gcs::<project_id>

# Custom Entity Type IDs (optional, defaults shown)
types:
  bucket_type_id: 40
  # Optional: custom icon
  # bucket_icon: "path/to/custom-bucket-icon.svg"

# Resource Filters (optional)
filter:
  buckets:
    include: []  # Empty means include all
    exclude: []  # Regex patterns to exclude
    # Examples:
    # include: ["prod-.*"]      # Only include buckets starting with prod-
    # exclude: ["test-.*"]      # Skip buckets starting with test-

# Relationship Management (optional)
relationships:
  enabled: false  # Set to true to enable bucket->topic relationships for Pub/Sub notifications
  filter:
    include: []  # Empty means include all (format: "bucket_name->topic_id")
    exclude: []  # Regex patterns to exclude relationship pairs
    # Examples:
    # include: ["important-bucket->.*"]  # Only create relationships for important-bucket
    # exclude: ["test-.*->.*"]           # Skip relationships for test buckets
```

**Defaults:**
- Deployment: the EU region, or the one you last logged into. `--region us` / `--region au` select the others, and every OAuth endpoint is discovered from the deployment itself.
- Type ID: Bucket=40
- User agent: `synq-gcs-client-v1.0.0`
- Entity group ID: `gcs::<project_id>` (for automatic cleanup of removed resources)
- Icons: Embedded SVG from `icons/gcs.svg`
- Relationships: Disabled by default

### Cross-Platform Lineage

The integration can automatically create lineage relationships when buckets have notification configurations that send events to Pub/Sub topics:

**GCS Bucket → Pub/Sub Topic** (bucket sends event notifications to topic)

**Requirements:**
- **Pub/Sub integration**: [synq-google-cloud-pubsub](https://github.com/getsynq/synq-google-cloud-pubsub) should be set up first
- Links to non-existent `pubsub::<topic_id>` entities are skipped with debug logging

**Cycles:** each integration only withdraws the relationship shape it publishes,
so the two never undo each other. With both relationship features enabled, a
bucket that notifies a topic whose subscription writes back to that same bucket
forms a three-hop cycle in the graph. That is a faithful picture of the delivery
path rather than a fault, but it is worth knowing before you read it as one.

**Configuration:**

```yaml
relationships:
  enabled: true  # Enable bucket notification lineage
  filter:
    exclude: []  # Optional: exclude specific relationships
```

**Behavior:**
- The integration automatically detects bucket notification configurations using the GCS Notifications API
- Only creates relationships to Pub/Sub topics that exist as custom entities
- If a Pub/Sub topic entity doesn't exist, the relationship is skipped with a debug log message
- No sync failures - relationships are created opportunistically
- Run the Pub/Sub integration first to ensure topic entities exist

A run only ever withdraws relationships **it computed itself**: a run that
computed none withdraws none, and only a bucket-to-topic edge is this
integration's to withdraw. Everything else the catalog holds around a bucket — a
service linked to it, a warehouse table loaded from it — belongs to another
producer and is left alone.

**Optional - Excluding Pub/Sub relationships:**

While not required (missing entities are safely skipped), you can explicitly exclude Pub/Sub relationships or disable relationship scanning entirely:

```yaml
relationships:
  enabled: false  # Disable relationship management completely

  # Or exclude specific patterns:
  # filter:
  #   exclude: ["test-.*->.*"]  # Skip test bucket relationships
```

### Logging Configuration

The application uses structured logging (slog) configured via environment variables:

```bash
# Log level (default: INFO)
LOG_LEVEL=DEBUG    # Options: DEBUG, INFO, WARN, ERROR

# Log format (default: text)
LOG_FORMAT=text    # Options: text, json

# Add source code location to logs (default: false)
LOG_ADD_SOURCE=true
```

Example with different log levels:
```bash
# Debug mode - shows all details including filtered resources
LOG_LEVEL=DEBUG go run main.go

# Production mode - JSON format for log aggregation
LOG_FORMAT=json LOG_LEVEL=INFO go run main.go

# Minimal output
LOG_LEVEL=WARN go run main.go
```

## Network Requirements

If your GCP project has firewall rules that restrict inbound connections, you may need to whitelist the Coalesce Quality egress IP addresses to allow the integration to access your Cloud Storage buckets.

### Egress IP addresses

Whitelist the following IP addresses based on your deployment region:

**EU Region (Default)**
- App: https://app.synq.io
- API: https://developer.synq.io
- **Egress IP: `34.105.135.39`**

**US Region**
- App: https://app.us.synq.io
- API: https://api.us.synq.io
- **Egress IP: `35.238.250.82`**

For the latest IP addresses, see the [security documentation](https://docs.synq.io/security/ip#ip-addresses-by-region).

## Running

The application requires only a `.env` file with credentials. No `config.yaml` needed for basic usage:

```bash
# Minimal setup: just create .env with credentials
cp .env.example .env
# Edit .env and add your credentials

# Run with defaults
go run main.go

# Run with debug logging
LOG_LEVEL=DEBUG go run main.go

# Run with JSON logging for production
LOG_FORMAT=json go run main.go

# View all available flags
go run main.go --help
```

The application supports graceful shutdown with Ctrl-C.

### Command-Line Flags

All configuration options are available as command-line flags. Flags have the highest precedence (override config file and environment variables).

**Common flags:**
- `-c, --config` - Path to config file (default: `config.yaml`)
- `-h, --help` - Show help message
- `--dry-run` - Dry-run mode: scan GCP resources but don't call the Coalesce Quality API
- `--gcp.project-id` - GCP project ID (auto-detected if not set)
- `--client-id` - client credential id (or `QUALITY_CLIENT_ID`)
- `--client-secret` - client credential secret (or `QUALITY_CLIENT_SECRET`)
- `--region` - deployment: `eu`, `us` or `au`
- `--endpoint` - API endpoint, overriding `--region`

**Filter flags:**
- `--filter.buckets.include` - Bucket name patterns to include
- `--filter.buckets.exclude` - Bucket name patterns to exclude

**Relationship flags:**
- `--relationships.enabled` - Enable bucket->topic relationships for notifications (default: false)
- `--relationships.filter.include` - Relationship patterns to include
- `--relationships.filter.exclude` - Relationship patterns to exclude

**Type configuration flags:**
- `--types.bucket-type-id` - custom entity type ID for buckets (default: 40)
- `--types.bucket-icon` - Path to custom bucket icon SVG

**Advanced flags:**
- `--gcp.entity-group-id` - Entity group ID (defaults to `gcs::<project_id>`)
- `--gcp.user-agent` - User agent for GCP API calls
- `--synq.endpoint`, `--synq.client-id`, `--synq.client-secret`, `--synq.oauth-url` - the names these replaced. Still accepted, hidden from `--help`.

Run `go run main.go --help` to see all available flags.

### Dry-Run Mode

Use `--dry-run` to scan GCS buckets without publishing anything:

```bash
# Dry-run mode (no API calls)
go run main.go --dry-run

# Dry-run with debug logging to see what would be created
LOG_LEVEL=DEBUG go run main.go --dry-run
```

In dry-run mode:
- ✅ Scans GCS buckets
- ✅ Applies filters
- ✅ Shows what entities would be created
- ❌ Does not call the Coalesce Quality API
- ❌ Does not create/update entities
- ❌ Does not require credentials

### Examples

```bash
# Run with custom project ID
go run main.go --gcp.project-id=my-project

# Run against the US deployment
go run main.go --region=us

# Run with custom filters
go run main.go --filter.buckets.exclude="test-.*"

# Run with custom config file
go run main.go --config=config.production.yaml

# Combine config file with flag overrides
go run main.go --config=config.yaml --filter.buckets.include="prod-.*"

# Run with custom entity type ID
go run main.go --types.bucket-type-id=50
```

## How It Works

1. Authenticates against the Coalesce Quality API (browser login, client credentials or a pre-issued token)
2. Creates/updates custom entity type (Bucket)
3. Iterates through GCS buckets in the project
4. Creates an entity for each bucket
5. Uses entity groups to track resources by project (enables automatic cleanup)

### Architecture Overview

The integration consists of three main components:

**main.go** - Main application entry point:
- Configuration management using viper (supports config file, env vars, and CLI flags)
- Client setup (Coalesce Quality gRPC, GCP Cloud Storage)
- Resource synchronization orchestration
- Graceful shutdown handling

**config/config.go** - Configuration management:
- Multi-source configuration loading with proper precedence
- Auto-detection of GCP project ID from multiple sources
- Validation of required fields

**filter.go** - Resource filtering system:
- `IncludeExcludeFilter` - Combines multiple filters with include/exclude logic
- `RegexFilter` - Matches strings against regex patterns
- Default filter excludes auto-generated per-pod subscriptions

### Key Features

**Entity Groups:** The integration uses entity groups to track all entities created in each run. When the group is updated, Coalesce Quality automatically removes entities that were in the previous group but not in the current one, enabling automatic cleanup of deleted buckets.

**Custom Identifiers:** All entities use custom identifiers with `gcs::` prefix for namespace isolation. Buckets use identifiers in the format: `gcs::<bucket_name>`.

## Development

### Running Tests

```bash
# Run all tests
go test ./...

# Run a specific test
go test -v -run TestFilterSuite

# Run tests with coverage
go test -v -cover ./...
```

### Dependencies

Key dependencies used by this project:

- `buf.build/gen/go/getsynq/api` - Coalesce Quality API protocol buffers (gRPC and protobuf)
- `cloud.google.com/go/storage` - Google Cloud Storage client
- `github.com/getsynq/quality-oauth-go` - the shared browser login and credential store
- `github.com/spf13/cobra` - CLI framework
- `github.com/spf13/viper` - Configuration management
- `github.com/stretchr/testify` - Testing framework with suite support

See `go.mod` for the complete list of dependencies.
