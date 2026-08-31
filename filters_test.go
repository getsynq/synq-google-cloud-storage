package main

import (
	"testing"

	"github.com/getsynq/synq-google-cloud-storage/config"
	"github.com/stretchr/testify/assert"
)

// TestAnInvalidFilterPatternIsAConfigurationError is the reproducer for a typo
// in config.yaml arriving as a panic and a stack trace.
//
// NewRegexFilter returns the compile error, and lo.Must throws it away. The
// pattern is the user's, so the failure is theirs to read.
func TestAnInvalidFilterPatternIsAConfigurationError(t *testing.T) {
	cfg := &config.Config{
		Filter: config.FilterConfig{
			Buckets: config.FilterRules{Include: []string{"prod-("}},
		},
	}

	assert.NotPanics(t, func() { buildFilters(cfg) })
}
