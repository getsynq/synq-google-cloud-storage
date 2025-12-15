package main

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

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
