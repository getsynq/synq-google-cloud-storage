package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

// TestQualitySectionIsRead covers the promoted spelling of the section.
func TestQualitySectionIsRead(t *testing.T) {
	cfg, err := LoadConfig(writeConfig(t, `
gcp:
  project_id: example-project
quality:
  region: us
  client_id: promoted-id
  client_secret: promoted-secret
`))
	require.NoError(t, err)
	assert.Equal(t, "us", cfg.Quality.Region)
	assert.Equal(t, "promoted-id", cfg.Quality.ClientID)
	assert.Empty(t, cfg.DeprecatedKeys)
}

// TestSynqSectionIsStillRead is the compatibility case: the section this one
// replaced is in existing config files, so it keeps working and is named in the
// warning rather than silently ignored.
func TestSynqSectionIsStillRead(t *testing.T) {
	cfg, err := LoadConfig(writeConfig(t, `
gcp:
  project_id: example-project
synq:
  endpoint: api.us.synq.io:443
  client_id: legacy-id
  client_secret: legacy-secret
`))
	require.NoError(t, err)
	assert.Equal(t, "api.us.synq.io:443", cfg.Quality.Endpoint)
	assert.Equal(t, "legacy-id", cfg.Quality.ClientID)
	assert.ElementsMatch(t,
		[]string{"synq.endpoint", "synq.client_id", "synq.client_secret"},
		cfg.DeprecatedKeys)
}

// TestQualitySectionWinsOverSynq pins which way the merge runs. Getting it
// backwards would let a stale section in an old config file override the one the
// user just wrote.
func TestQualitySectionWinsOverSynq(t *testing.T) {
	cfg, err := LoadConfig(writeConfig(t, `
gcp:
  project_id: example-project
quality:
  endpoint: api.us.synq.io:443
synq:
  endpoint: developer.synq.io:443
  client_id: legacy-id
`))
	require.NoError(t, err)
	assert.Equal(t, "api.us.synq.io:443", cfg.Quality.Endpoint)
	// The field the promoted section left empty still comes from the older one.
	assert.Equal(t, "legacy-id", cfg.Quality.ClientID)
	assert.Equal(t, []string{"synq.client_id"}, cfg.DeprecatedKeys)
}

// TestMergeLeavesPopulatedFieldsAlone is the unit behind those two.
func TestMergeLeavesPopulatedFieldsAlone(t *testing.T) {
	q := QualityConfig{Endpoint: "api.us.synq.io:443"}
	used := q.merge(QualityConfig{Endpoint: "developer.synq.io:443", Token: "legacy-token"})

	assert.Equal(t, "api.us.synq.io:443", q.Endpoint)
	assert.Equal(t, "legacy-token", q.Token)
	assert.Equal(t, []string{"synq.token"}, used)
}

// TestCredentialsAreNotRequired keeps the config layer out of a decision it
// cannot make: a run can be authenticated by the environment or by a browser
// login in the shared store, neither of which is visible here.
func TestCredentialsAreNotRequired(t *testing.T) {
	cfg, err := LoadConfig(writeConfig(t, `
gcp:
  project_id: example-project
`))
	require.NoError(t, err)
	assert.Empty(t, cfg.Quality.ClientID)
	assert.Empty(t, cfg.Quality.Endpoint, "no endpoint default here; the deployment is resolved by the auth library")
}

// TestProjectIDIsStillRequired guards the one thing this tool cannot work out on
// its own when nothing in the environment supplies it.
func TestProjectIDIsStillRequired(t *testing.T) {
	t.Setenv("GCP_PROJECT_ID", "")
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")
	t.Setenv("GCLOUD_PROJECT", "")
	t.Setenv("CLOUDSDK_CONFIG", t.TempDir())
	t.Setenv("PATH", "")

	_, err := LoadConfig(writeConfig(t, "quality:\n  region: eu\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GCP project ID is required")
}
