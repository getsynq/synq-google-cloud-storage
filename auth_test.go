package main

import (
	"os"
	"path/filepath"
	"testing"

	qualityoauth "github.com/getsynq/quality-oauth-go"
	"github.com/getsynq/synq-google-cloud-storage/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// clearDeploymentEnv keeps a developer's own shell out of the precedence tests.
func clearDeploymentEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"QUALITY_API_ENDPOINT", "SYNQ_API_ENDPOINT",
		"QUALITY_REGION", "SYNQ_REGION",
	} {
		t.Setenv(name, "")
	}
	// The remembered deployment of the last login is read from the store, which
	// is the tier directly below the environment.
	t.Setenv("QUALITY_HOME", t.TempDir())
}

func TestRegionFromTheConfigFileResolves(t *testing.T) {
	clearDeploymentEnv(t)

	target, err := resolveTarget(&config.Config{Quality: config.QualityConfig{Region: "us"}})
	require.NoError(t, err)
	assert.Equal(t, "us", target.Region)
}

// TestEndpointFlagOutranksTheConfigFile pins the tier the flags sit in. The file
// is offered as the configured endpoint, which deliberately loses to what the
// user typed.
func TestEndpointFlagOutranksTheConfigFile(t *testing.T) {
	clearDeploymentEnv(t)

	target, err := resolveTarget(&config.Config{Quality: config.QualityConfig{
		Region:       "eu",
		EndpointFlag: "api.au.synq.io:443",
	}})
	require.NoError(t, err)
	assert.Equal(t, "api.au.synq.io:443", target.Endpoint)
}

// TestTheConfigFileOutranksTheEnvironment records the ordering rather than
// choosing it: the library places explicit configuration above the ambient
// environment and both below the flags, and every Coalesce Quality tool resolves
// a deployment through it. A config file that names a deployment therefore wins
// over an exported QUALITY_REGION, and only what the user typed wins over the
// file.
func TestTheConfigFileOutranksTheEnvironment(t *testing.T) {
	clearDeploymentEnv(t)
	t.Setenv("QUALITY_REGION", "au")

	target, err := resolveTarget(&config.Config{Quality: config.QualityConfig{Region: "eu"}})
	require.NoError(t, err)
	assert.Equal(t, "eu", target.Region)

	// Nothing in the file, so the environment decides.
	target, err = resolveTarget(&config.Config{})
	require.NoError(t, err)
	assert.Equal(t, "au", target.Region)

	// A flag beats both.
	target, err = resolveTarget(&config.Config{Quality: config.QualityConfig{
		Region:     "eu",
		RegionFlag: "us",
	}})
	require.NoError(t, err)
	assert.Equal(t, "us", target.Region)
}

// TestConfigFileEndpointBeatsItsOwnRegion keeps the two file settings ordered the
// same way the flags are: the more specific one wins.
func TestConfigFileEndpointBeatsItsOwnRegion(t *testing.T) {
	clearDeploymentEnv(t)

	target, err := resolveTarget(&config.Config{Quality: config.QualityConfig{
		Region:   "eu",
		Endpoint: "api.us.synq.io:443",
	}})
	require.NoError(t, err)
	assert.Equal(t, "api.us.synq.io:443", target.Endpoint)
}

func TestAnUnknownRegionInTheConfigFileIsAnError(t *testing.T) {
	clearDeploymentEnv(t)

	_, err := resolveTarget(&config.Config{Quality: config.QualityConfig{Region: "atlantis"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "quality.region in the config file")
}

// TestDeclaredScopesCoverWhatASyncWrites keeps the consent screen and the sync in
// step: a scope the tool needs but never declares is one a login cannot grant.
func TestDeclaredScopesCoverWhatASyncWrites(t *testing.T) {
	assert.Empty(t, qualityoauth.MissingScopes(declaredScopes, writeScopes))
}

// TestTheAppRegistersDynamically pins the reason FirstPartyClientID is empty: the
// authorization server seeds a row per released first-party CLI and this is not
// one, so an id it does not know would hang the login waiting for a callback.
func TestTheAppRegistersDynamically(t *testing.T) {
	assert.Empty(t, authApp().FirstPartyClientID)
	assert.Equal(t, toolName, authApp().SoftwareID)
}

// TestAnOAuthURLMustBeHTTPS is the reproducer for a config file reading exported
// client credentials off the wire.
//
// The override is applied to whichever credentials the run resolved, including a
// pair exported into the environment by CI, and the client-credentials grant
// posts them to the token endpoint as form fields. A plain-HTTP override sends
// them in the clear to a host the config file names.
func TestAnOAuthURLMustBeHTTPS(t *testing.T) {
	cfg := &config.Config{Quality: config.QualityConfig{OAuthURL: "http://attacker.example/oauth2/token"}}

	_, err := dialClientCredentials(t.Context(), cfg, qualityoauth.Target{Endpoint: "api.eu.synq.io:443", Host: "api.eu.synq.io"}, "id", "secret")

	require.Error(t, err)
}

// TestALoopbackOAuthURLIsAllowed keeps a local authorization server usable: it
// never puts the credentials on a network, and demanding a certificate for it
// only teaches people to turn the check off.
func TestALoopbackOAuthURLIsAllowed(t *testing.T) {
	for _, raw := range []string{
		"http://127.0.0.1:8080/oauth2/token",
		"http://localhost:8080/oauth2/token",
		"http://[::1]:8080/oauth2/token",
	} {
		got, err := tokenURL(qualityoauth.Target{}, raw)
		require.NoError(t, err, raw)
		assert.Equal(t, raw, got)
	}
}

// TestAnOAuthURLOverridesTheDeployment keeps the reason the setting exists: a
// self-hosted deployment can serve its authorization server from another host.
func TestAnOAuthURLOverridesTheDeployment(t *testing.T) {
	got, err := tokenURL(qualityoauth.Target{}, "https://auth.example.internal/oauth2/token")

	require.NoError(t, err)
	assert.Equal(t, "https://auth.example.internal/oauth2/token", got)
}

// TestAnUnknownRegionInTheConfigFileDoesNotBlockAFlag is the reproducer for a
// typo in the file disabling the flag that exists to override the file.
//
// resolveTarget returns the TargetForRegion error before Sources.Resolve ever
// sees what the user typed, so `--region us` cannot rescue a config file with a
// bad quality.region.
func TestAnUnknownRegionInTheConfigFileDoesNotBlockAFlag(t *testing.T) {
	clearDeploymentEnv(t)

	target, err := resolveTarget(&config.Config{Quality: config.QualityConfig{
		Region:     "atlantis",
		RegionFlag: "us",
	}})

	require.NoError(t, err)
	assert.Equal(t, "us", target.Region)
}

// TestAuthKeepsTheDeploymentWhenTheGCPProjectIsMissing is the reproducer for
// `auth login --region us` logging into the wrong deployment.
//
// A sync needs a GCP project; logging in does not. LoadConfig returned nil
// alongside the validation error, so targetFromCommand threw away the flags and
// the file together and fell back to whatever the environment or the last login
// said.
func TestAuthKeepsTheDeploymentWhenTheGCPProjectIsMissing(t *testing.T) {
	clearDeploymentEnv(t)
	t.Setenv("GCP_PROJECT_ID", "")
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")
	t.Setenv("GCLOUD_PROJECT", "")
	t.Setenv("CLOUDSDK_CONFIG", t.TempDir())
	t.Setenv("PATH", "")

	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("quality:\n  region: us\n"), 0o600))

	cfg, err := config.LoadConfig(path)
	require.Error(t, err, "a sync still needs a GCP project")
	require.NotNil(t, cfg, "the parsed file is what an auth subcommand needs")

	target, err := resolveTarget(cfg)
	require.NoError(t, err)
	assert.Equal(t, "us", target.Region)
}
