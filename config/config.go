package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"cloud.google.com/go/compute/metadata"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

type Config struct {
	DryRun bool        `mapstructure:"dry_run"`
	SYNQ   SYNQConfig  `mapstructure:"synq"`
	GCP    GCPConfig   `mapstructure:"gcp"`
	Types  TypesConfig `mapstructure:"types"`
	Filter FilterConfig `mapstructure:"filter"`
}

type SYNQConfig struct {
	ClientID     string `mapstructure:"client_id"`
	ClientSecret string `mapstructure:"client_secret"`
	Endpoint     string `mapstructure:"endpoint"`
	OAuthURL     string `mapstructure:"oauth_url"`
}

type GCPConfig struct {
	ProjectID     string `mapstructure:"project_id"`
	UserAgent     string `mapstructure:"user_agent"`
	EntityGroupID string `mapstructure:"entity_group_id"` // Entity group ID for tracking resources (defaults to gcs::<project_id>)
}

type TypesConfig struct {
	BucketTypeID int32  `mapstructure:"bucket_type_id"`
	BucketIcon   string `mapstructure:"bucket_icon"` // Optional path to custom bucket icon SVG
}

type FilterConfig struct {
	Buckets FilterRules `mapstructure:"buckets"`
}

type FilterRules struct {
	Include []string `mapstructure:"include"`
	Exclude []string `mapstructure:"exclude"`
}


// detectProjectID attempts to auto-detect the GCP project ID from the environment
func detectProjectID(ctx context.Context) string {
	// Try GCP_PROJECT_ID environment variable first
	if projectID := os.Getenv("GCP_PROJECT_ID"); projectID != "" {
		return projectID
	}

	// Try GOOGLE_CLOUD_PROJECT (standard GCP env var)
	if projectID := os.Getenv("GOOGLE_CLOUD_PROJECT"); projectID != "" {
		return projectID
	}

	// Try GCLOUD_PROJECT (legacy)
	if projectID := os.Getenv("GCLOUD_PROJECT"); projectID != "" {
		return projectID
	}

	// Try gcloud CLI configuration
	if projectID := getGcloudProjectID(ctx); projectID != "" {
		return projectID
	}

	// Try GCP metadata server (when running on GCP)
	if metadata.OnGCE() {
		if projectID, err := metadata.ProjectIDWithContext(ctx); err == nil && projectID != "" {
			return projectID
		}
	}

	return ""
}

// getGcloudProjectID attempts to read the project ID from gcloud CLI configuration
func getGcloudProjectID(ctx context.Context) string {
	cmd := exec.CommandContext(ctx, "gcloud", "config", "get-value", "project")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	projectID := strings.TrimSpace(string(output))
	// gcloud returns "(unset)" if no project is configured
	if projectID == "" || projectID == "(unset)" {
		return ""
	}
	return projectID
}

// InitFlags initializes all configuration flags
func InitFlags() {
	// Config file flag
	pflag.StringP("config", "c", "config.yaml", "Path to config file")

	// General flags
	pflag.Bool("dry-run", false, "Dry-run mode: scan GCP resources but don't call SYNQ API")

	// SYNQ configuration
	pflag.String("synq.client-id", "", "SYNQ API client ID (env: SYNQ_CLIENT_ID)")
	pflag.String("synq.client-secret", "", "SYNQ API client secret (env: SYNQ_CLIENT_SECRET)")
	pflag.String("synq.endpoint", "developer.synq.io:443", "SYNQ API endpoint")
	pflag.String("synq.oauth-url", "https://developer.synq.io/oauth2/token", "SYNQ OAuth2 token URL")

	// GCP configuration
	pflag.String("gcp.project-id", "", "GCP project ID (auto-detected if not set)")
	pflag.String("gcp.user-agent", "synq-gcs-client-v1.0.0", "User agent for GCP API calls")
	pflag.String("gcp.entity-group-id", "", "Entity group ID (defaults to gcs::<project_id>)")

	// Entity type configuration
	pflag.Int32("types.bucket-type-id", 40, "SYNQ entity type ID for buckets")
	pflag.String("types.bucket-icon", "", "Path to custom bucket icon SVG")

	// Filter configuration
	pflag.StringSlice("filter.buckets.include", []string{}, "Bucket name patterns to include (empty = all)")
	pflag.StringSlice("filter.buckets.exclude", []string{}, "Bucket name patterns to exclude")
}

// LoadConfig loads configuration from file, environment variables, and flags
// Configuration precedence: defaults → config file → environment variables → flags
func LoadConfig(configPath string) (*Config, error) {
	v := viper.New()

	// Set config file
	if configPath != "" {
		v.SetConfigFile(configPath)
	} else {
		v.SetConfigName("config")
		v.SetConfigType("yaml")
		v.AddConfigPath(".")
	}

	// Set defaults
	setDefaults(v)

	// Read config file (optional - don't error if it doesn't exist)
	if err := v.ReadInConfig(); err != nil {
		var configFileNotFoundError viper.ConfigFileNotFoundError
		if !errors.As(err, &configFileNotFoundError) && !os.IsNotExist(err) {
			// Config file was found but another error was produced (not a "not found" error)
			return nil, fmt.Errorf("error reading config file: %w", err)
		}
		// Config file not found; ignore and continue
	}

	// Bind environment variables
	v.SetEnvPrefix("") // No prefix, use exact names
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	v.AutomaticEnv()

	// Manually bind specific env vars for backward compatibility
	_ = v.BindEnv("synq.client_id", "SYNQ_CLIENT_ID")
	_ = v.BindEnv("synq.client_secret", "SYNQ_CLIENT_SECRET")
	_ = v.BindEnv("gcp.project_id", "GCP_PROJECT_ID", "GOOGLE_CLOUD_PROJECT", "GCLOUD_PROJECT")

	// Bind flags
	if err := v.BindPFlags(pflag.CommandLine); err != nil {
		return nil, fmt.Errorf("error binding flags: %w", err)
	}

	// Manually bind dry-run flag (hyphen to underscore mapping)
	if flag := pflag.CommandLine.Lookup("dry-run"); flag != nil {
		_ = v.BindPFlag("dry_run", flag)
	}

	// Unmarshal into config struct
	cfg := &Config{}
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("error unmarshaling config: %w", err)
	}

	// Auto-detect project ID if not set
	if cfg.GCP.ProjectID == "" {
		cfg.GCP.ProjectID = detectProjectID(context.Background())
	}

	// Set default entity group ID if not configured
	if cfg.GCP.EntityGroupID == "" && cfg.GCP.ProjectID != "" {
		cfg.GCP.EntityGroupID = fmt.Sprintf("gcs::%s", cfg.GCP.ProjectID)
	}

	// Validate required fields
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// setDefaults sets default values in viper
func setDefaults(v *viper.Viper) {
	// General defaults
	v.SetDefault("dry_run", false)

	// SYNQ defaults
	v.SetDefault("synq.endpoint", "developer.synq.io:443")
	v.SetDefault("synq.oauth_url", "https://developer.synq.io/oauth2/token")

	// GCP defaults
	v.SetDefault("gcp.user_agent", "synq-gcs-client-v1.0.0")

	// Entity type defaults
	v.SetDefault("types.bucket_type_id", 40)
}

// validateConfig validates required configuration fields
func validateConfig(cfg *Config) error {
	// Skip SYNQ credentials validation in dry-run mode
	if !cfg.DryRun {
		if cfg.SYNQ.ClientID == "" {
			return fmt.Errorf("SYNQ_CLIENT_ID is required (set via env var or --synq.client-id flag)")
		}
		if cfg.SYNQ.ClientSecret == "" {
			return fmt.Errorf("SYNQ_CLIENT_SECRET is required (set via env var or --synq.client-secret flag)")
		}
		if cfg.SYNQ.Endpoint == "" {
			return fmt.Errorf("synq.endpoint is required")
		}
	}
	if cfg.GCP.ProjectID == "" {
		return fmt.Errorf(
			"GCP project ID is required. Set via:\n" +
				"  - GCP_PROJECT_ID environment variable\n" +
				"  - GOOGLE_CLOUD_PROJECT environment variable\n" +
				"  - --gcp.project-id flag\n" +
				"  - gcp.project_id in config.yaml\n" +
				"  - or run on GCP (auto-detected from metadata server)",
		)
	}
	if cfg.Types.BucketTypeID == 0 {
		return fmt.Errorf("types.bucket_type_id is required")
	}

	return nil
}
