package main

import (
	"testing"

	"github.com/getsynq/synq-google-cloud-storage/config"
	qualityoauth "github.com/getsynq/quality-oauth-go"
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
