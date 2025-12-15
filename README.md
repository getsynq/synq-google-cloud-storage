# synq-google-cloud-storage

SYNQ integration for Google Cloud Storage that automatically syncs your GCS buckets as custom entities in the SYNQ platform.

## What It Does

This integration:
- **Discovers** all GCS buckets in your Google Cloud project
- **Creates** custom entities in SYNQ for each bucket with rich metadata
- **Syncs** bucket information including:
  - Storage class and location
  - Versioning status
  - Lifecycle rules (with detailed conditions)
  - Uniform bucket-level access settings
  - User-defined labels
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

The application works with sensible defaults and minimal configuration. Only SYNQ API credentials are required.

### Required: Environment Variables (.env)

Create a `.env` file in the project root with your SYNQ API credentials:

```bash
SYNQ_CLIENT_ID=your_client_id_here
SYNQ_CLIENT_SECRET=your_client_secret_here

# GCP Project ID (optional if running on GCP or using gcloud)
GCP_PROJECT_ID=your-gcp-project-id
```

See `.env.example` for a template.

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
# SYNQ API Configuration (optional, defaults shown for EU region)
synq:
  endpoint: "developer.synq.io:443"  # EU region (default)
  # For US region, use: "api.us.synq.io:443"
  oauth_url: "https://developer.synq.io/oauth2/token"  # EU region (default)
  # For US region, use: "https://api.us.synq.io/oauth2/token"

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
```

**Defaults:**
- SYNQ endpoint: `developer.synq.io:443` (EU region - for US region use `api.us.synq.io:443`)
- OAuth URL: `https://developer.synq.io/oauth2/token` (EU region - for US region use `https://api.us.synq.io/oauth2/token`)
- Type ID: Bucket=40
- User agent: `synq-gcs-client-v1.0.0`
- Entity group ID: `gcs::<project_id>` (for automatic cleanup of removed resources)
- Icons: Embedded SVG from `icons/gcs.svg`

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

If your GCP project has firewall rules that restrict inbound connections, you may need to whitelist SYNQ's egress IP addresses to allow the integration to access your Cloud Storage buckets.

### SYNQ Egress IP Addresses

Whitelist the following IP addresses based on your SYNQ deployment region:

**EU Region (Default)**
- App: https://app.synq.io
- API: https://developer.synq.io
- **Egress IP: `34.105.135.39`**

**US Region**
- App: https://app.us.synq.io
- API: https://api.us.synq.io
- **Egress IP: `35.238.250.82`**

For the latest IP addresses, see the [SYNQ Security Documentation](https://docs.synq.io/security/ip#ip-addresses-by-region).

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
- `--dry-run` - Dry-run mode: scan GCP resources but don't call SYNQ API
- `--gcp.project-id` - GCP project ID (auto-detected if not set)
- `--synq.client-id` - SYNQ API client ID (or use SYNQ_CLIENT_ID env var)
- `--synq.client-secret` - SYNQ API client secret (or use SYNQ_CLIENT_SECRET env var)

**Filter flags:**
- `--filter.buckets.include` - Bucket name patterns to include
- `--filter.buckets.exclude` - Bucket name patterns to exclude

**Type configuration flags:**
- `--types.bucket-type-id` - SYNQ entity type ID for buckets (default: 40)
- `--types.bucket-icon` - Path to custom bucket icon SVG

**Advanced flags:**
- `--gcp.entity-group-id` - Entity group ID (defaults to `gcs::<project_id>`)
- `--gcp.user-agent` - User agent for GCP API calls
- `--synq.endpoint` - SYNQ API endpoint (EU: `developer.synq.io:443`, US: `api.us.synq.io:443`)
- `--synq.oauth-url` - SYNQ OAuth2 token URL (EU: `https://developer.synq.io/oauth2/token`, US: `https://api.us.synq.io/oauth2/token`)

Run `go run main.go --help` to see all available flags.

### Dry-Run Mode

Use `--dry-run` to scan GCS buckets without making any changes to SYNQ:

```bash
# Dry-run mode (no SYNQ API calls)
go run main.go --dry-run

# Dry-run with debug logging to see what would be created
LOG_LEVEL=DEBUG go run main.go --dry-run
```

In dry-run mode:
- ✅ Scans GCS buckets
- ✅ Applies filters
- ✅ Shows what entities would be created
- ❌ Does not call SYNQ API
- ❌ Does not create/update entities
- ❌ Does not require SYNQ credentials

### Examples

```bash
# Run with custom project ID
go run main.go --gcp.project-id=my-project

# Run with US region endpoints
go run main.go --synq.endpoint=api.us.synq.io:443 --synq.oauth-url=https://api.us.synq.io/oauth2/token

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

1. Authenticates with SYNQ API using OAuth2 client credentials
2. Creates/updates custom entity type (Bucket)
3. Iterates through GCS buckets in the project
4. Creates entities in SYNQ for each bucket
5. Uses entity groups to track resources by project (enables automatic cleanup)

### Architecture Overview

The integration consists of three main components:

**main.go** - Main application entry point:
- Configuration management using viper (supports config file, env vars, and CLI flags)
- Client setup (SYNQ gRPC with OAuth2, GCP Pub/Sub)
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

**Entity Groups:** The integration uses entity groups to track all entities created in each run. When the group is updated, SYNQ automatically removes entities that were in the previous group but not in the current one, enabling automatic cleanup of deleted buckets.

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

- `buf.build/gen/go/getsynq/api` - SYNQ API protocol buffers (gRPC and protobuf)
- `cloud.google.com/go/storage` - Google Cloud Storage client
- `golang.org/x/oauth2/clientcredentials` - OAuth2 client credentials flow
- `github.com/spf13/cobra` - CLI framework
- `github.com/spf13/viper` - Configuration management
- `github.com/stretchr/testify` - Testing framework with suite support

See `go.mod` for the complete list of dependencies.
