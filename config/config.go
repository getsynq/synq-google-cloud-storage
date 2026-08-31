package config

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"cloud.google.com/go/compute/metadata"
	qualityoauth "github.com/getsynq/quality-oauth-go"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

type Config struct {
	DryRun bool `mapstructure:"dry_run"`
	// Quality is the Coalesce Quality deployment and credentials. The `synq`
	// section is its predecessor and is still read into SYNQ, then merged in as
	// the lower-precedence source, because it is in existing config files.
	Quality       QualityConfig       `mapstructure:"quality"`
	SYNQ          QualityConfig       `mapstructure:"synq"`
	GCP           GCPConfig           `mapstructure:"gcp"`
	Types         TypesConfig         `mapstructure:"types"`
	Filter        FilterConfig        `mapstructure:"filter"`
	Relationships RelationshipsConfig `mapstructure:"relationships"`
	// DeprecatedKeys are the superseded config keys this load actually used, so
	// the caller can name them once instead of warning per key or not at all.
	DeprecatedKeys []string `mapstructure:"-"`
}

// flagValue reads a flag the user typed, or "" when it was not set. It is only
// ever the typed value: a flag's default belongs to the defaults tier, not to
// the flag tier, and reading it here would make a default outrank the
// environment.
func flagValue(name string) string {
	flag := pflag.CommandLine.Lookup(name)
	if flag == nil || !flag.Changed {
		return ""
	}
	return flag.Value.String()
}

type QualityConfig struct {
	ClientID     string `mapstructure:"client_id"`
	ClientSecret string `mapstructure:"client_secret"`
	// Token is a pre-issued access token, used as-is.
	Token string `mapstructure:"token"`
	// Endpoint is host:port of the API, overriding Region.
	Endpoint string `mapstructure:"endpoint"`
	// Region names a deployment: eu, us or au.
	Region string `mapstructure:"region"`
	// OAuthURL overrides the token endpoint the client-credentials grant uses.
	// It is derived from the deployment otherwise, and only a self-hosted
	// deployment that serves the authorization server from another host needs it.
	OAuthURL string `mapstructure:"oauth_url"`
	// RegionFlag and EndpointFlag are what the user typed. They are held apart
	// from the file values because they outrank the environment, while the file
	// values do not.
	RegionFlag   string `mapstructure:"-"`
	EndpointFlag string `mapstructure:"-"`
}

// hasCredential reports whether this section authenticates on its own. The two
// kinds are alternatives, not fields to combine: `connect` prefers a client pair
// over a token, so a section that supplies either has settled the question.
func (q QualityConfig) hasCredential() bool {
	return q.Token != "" || (q.ClientID != "" && q.ClientSecret != "")
}

// namesDeployment reports whether this section points at a deployment. Endpoint
// and region are likewise alternatives: the endpoint wins where both are given.
func (q QualityConfig) namesDeployment() bool {
	return q.Endpoint != "" || q.Region != ""
}

// merge fills empty fields from a lower-precedence source, and reports which
// deprecated keys were used so the caller can say so once.
//
// Credentials and the deployment are taken as a whole rather than field by
// field. Merging those field-wise hands the outcome to the section that is meant
// to lose: a legacy client pair beside a promoted token is the pair that
// authenticates, and a legacy endpoint beside a promoted region is the endpoint
// that is dialled. A half-filled promoted section is still completed from the
// older one, which is what makes a split across the two sections keep working.
func (q *QualityConfig) merge(older QualityConfig) []string {
	var used []string
	take := func(dst *string, src, name string) {
		if *dst == "" && src != "" {
			*dst = src
			used = append(used, "synq."+name)
		}
	}
	if !q.hasCredential() {
		take(&q.ClientID, older.ClientID, "client_id")
		take(&q.ClientSecret, older.ClientSecret, "client_secret")
		take(&q.Token, older.Token, "token")
	}
	if !q.namesDeployment() {
		take(&q.Endpoint, older.Endpoint, "endpoint")
		take(&q.Region, older.Region, "region")
	}
	take(&q.OAuthURL, older.OAuthURL, "oauth_url")
	return used
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

type RelationshipsConfig struct {
	Enabled bool        `mapstructure:"enabled"`
	Filter  FilterRules `mapstructure:"filter"`
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
	pflag.Bool("dry-run", false, "Dry-run mode: scan GCP resources but don't call the Coalesce Quality API")

	// Coalesce Quality configuration
	pflag.String("region", "", "Coalesce Quality deployment: "+strings.Join(qualityoauth.RegionNames(), ", ")+" (env: QUALITY_REGION)")
	pflag.String("endpoint", "", "Coalesce Quality API endpoint, host:port, overriding --region (env: QUALITY_API_ENDPOINT)")
	pflag.String("client-id", "", "Client credential id (env: QUALITY_CLIENT_ID)")
	pflag.String("client-secret", "", "Client credential secret (env: QUALITY_CLIENT_SECRET)")

	// The names these replaced. They still work, and are hidden rather than
	// removed because they are in existing scripts and CI configuration.
	pflag.String("synq.client-id", "", "Deprecated alias for --client-id")
	pflag.String("synq.client-secret", "", "Deprecated alias for --client-secret")
	pflag.String("synq.endpoint", "", "Deprecated alias for --endpoint")
	pflag.String("synq.oauth-url", "", "Deprecated: the token endpoint is derived from the deployment")
	for _, name := range []string{"synq.client-id", "synq.client-secret", "synq.endpoint", "synq.oauth-url"} {
		if flag := pflag.CommandLine.Lookup(name); flag != nil {
			flag.Hidden = true
		}
	}

	// GCP configuration
	pflag.String("gcp.project-id", "", "GCP project ID (auto-detected if not set)")
	pflag.String("gcp.user-agent", "synq-gcs-client-v1.0.0", "User agent for GCP API calls")
	pflag.String("gcp.entity-group-id", "", "Entity group ID (defaults to gcs::<project_id>)")

	// Entity type configuration
	pflag.Int32("types.bucket-type-id", 40, "Custom entity type ID for buckets")
	pflag.String("types.bucket-icon", "", "Path to custom bucket icon SVG")

	// Filter configuration
	pflag.StringSlice("filter.buckets.include", []string{}, "Bucket name patterns to include (empty = all)")
	pflag.StringSlice("filter.buckets.exclude", []string{}, "Bucket name patterns to exclude")

	// Relationship configuration
	pflag.Bool("relationships.enabled", false, "Enable bucket->topic relationships for notifications")
	pflag.StringSlice("relationships.filter.include", []string{}, "Relationship patterns to include")
	pflag.StringSlice("relationships.filter.exclude", []string{}, "Relationship patterns to exclude")
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

	// Credentials and deployment from the environment are read by the auth
	// library, which prefers the QUALITY_ names and falls back to the SYNQ_ ones.
	// Binding them here as well would give one of the two settings a different
	// precedence from the rest.
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

	// The flags outrank the environment; the file values do not, so they are
	// carried separately and resolved by the auth library.
	//
	// Every deprecated alias is read by name here. Viper cannot unmarshal a
	// dashed sub-key such as `synq.client-id` onto the `client_id` field it
	// names, so an alias that is not in this block resolves to nothing.
	cfg.Quality.RegionFlag = flagValue("region")
	cfg.Quality.EndpointFlag = cmp.Or(flagValue("endpoint"), flagValue("synq.endpoint"))
	if id := cmp.Or(flagValue("client-id"), flagValue("synq.client-id")); id != "" {
		cfg.Quality.ClientID = id
	}
	if secret := cmp.Or(flagValue("client-secret"), flagValue("synq.client-secret")); secret != "" {
		cfg.Quality.ClientSecret = secret
	}
	if url := flagValue("synq.oauth-url"); url != "" {
		cfg.Quality.OAuthURL = url
	}
	if deprecated := cfg.Quality.merge(cfg.SYNQ); len(deprecated) > 0 {
		cfg.DeprecatedKeys = deprecated
	}

	// Auto-detect project ID if not set
	if cfg.GCP.ProjectID == "" {
		cfg.GCP.ProjectID = detectProjectID(context.Background())
	}

	// Set default entity group ID if not configured
	if cfg.GCP.EntityGroupID == "" && cfg.GCP.ProjectID != "" {
		cfg.GCP.EntityGroupID = fmt.Sprintf("gcs::%s", cfg.GCP.ProjectID)
	}

	// Validate required fields. The parsed config is returned alongside the
	// error: what fails here is what a sync needs, and an auth subcommand needs
	// only the deployment, which parsed fine. Callers that need a valid config
	// must still check the error.
	if err := validateConfig(cfg); err != nil {
		return cfg, err
	}

	return cfg, nil
}

// setDefaults sets default values in viper
func setDefaults(v *viper.Viper) {
	// General defaults
	v.SetDefault("dry_run", false)

	// GCP defaults
	v.SetDefault("gcp.user_agent", "synq-gcs-client-v1.0.0")

	// Entity type defaults
	v.SetDefault("types.bucket_type_id", 40)

	// Relationship defaults
	v.SetDefault("relationships.enabled", false)
}

// validateConfig validates required configuration fields
func validateConfig(cfg *Config) error {
	// Credentials are not validated here. There are four ways to hold one — the
	// environment, the config file, a pre-issued token, a browser login in the
	// shared credential store — and only the code that resolves them knows
	// whether any produced a usable credential.
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
