package main

import (
	"testing"

	"github.com/getsynq/synq-google-cloud-storage/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// TestAnInvalidFilterPatternIsAConfigurationError is the reproducer for a typo
// in config.yaml arriving as a panic and a stack trace.
//
// NewRegexFilter returns the compile error and lo.Must threw it away. The
// pattern is the user's, so the failure is theirs to read: it names the key and
// the pattern.
func TestAnInvalidFilterPatternIsAConfigurationError(t *testing.T) {
	cfg := &config.Config{
		Filter: config.FilterConfig{
			Buckets: config.FilterRules{Include: []string{"prod-("}},
		},
	}

	var err error
	require.NotPanics(t, func() { _, err = buildFilters(cfg) })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "filter.buckets.include")
	assert.Contains(t, err.Error(), "prod-(")
}

// TestAnInvalidRelationshipPatternNamesItsOwnSection keeps the two filter
// sections apart in the message; they are configured separately and a run has
// both.
func TestAnInvalidRelationshipPatternNamesItsOwnSection(t *testing.T) {
	cfg := &config.Config{
		Relationships: config.RelationshipsConfig{
			Filter: config.FilterRules{Exclude: []string{"*-audit"}},
		},
	}

	_, err := buildFilters(cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "relationships.filter.exclude")
}

// TestValidFilterPatternsCompile is the case the error path must not swallow.
func TestValidFilterPatternsCompile(t *testing.T) {
	cfg := &config.Config{
		Filter: config.FilterConfig{
			Buckets: config.FilterRules{Include: []string{"^prod-"}, Exclude: []string{"-tmp$"}},
		},
	}

	filters, err := buildFilters(cfg)

	require.NoError(t, err)
	assert.True(t, filters.buckets.Accept("prod-artefacts"))
	assert.False(t, filters.buckets.Accept("prod-artefacts-tmp"))
	assert.False(t, filters.buckets.Accept("staging-artefacts"))
}

func TestFilterSuite(t *testing.T) {
	suite.Run(t, new(FilterSuite))
}

type FilterSuite struct {
	suite.Suite
}

func (s *FilterSuite) TestFilter() {

	// Test regex filter for bucket names
	testBucketFilter, err := NewRegexFilter(`^test-.*$`)
	s.Require().NoError(err)
	s.True(testBucketFilter.Accept("test-bucket-123"))
	s.False(testBucketFilter.Accept("prod-bucket-456"))

	// Test include/exclude filter
	includeExcludeFilter := NewIncludeExcludeFilter(nil, []Filter{testBucketFilter})
	s.False(includeExcludeFilter.Accept("test-bucket-123"))
	s.True(includeExcludeFilter.Accept("prod-bucket-456"))

}
