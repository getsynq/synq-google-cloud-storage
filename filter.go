package main

import "regexp"

type Filter interface {
	Accept(id string) bool
}

var _ Filter = &IncludeExcludeFilter{}

func NewIncludeExcludeFilter(include, exclude []Filter) Filter {
	return &IncludeExcludeFilter{
		include: include,
		exclude: exclude,
	}
}

type IncludeExcludeFilter struct {
	include []Filter
	exclude []Filter
}

func (i IncludeExcludeFilter) Accept(id string) bool {
	accepted := len(i.include) == 0
	for _, f := range i.include {
		accepted = accepted || f.Accept(id)
	}
	if !accepted {
		return false
	}

	for _, f := range i.exclude {
		if f.Accept(id) {
			return false
		}
	}
	return accepted
}

func NewRegexFilter(exp string) (Filter, error) {

	r, err := regexp.Compile(exp)
	if err != nil {
		return nil, err
	}

	return &RegexFilter{
		regex: r,
	}, nil
}

var _ Filter = &RegexFilter{}

type RegexFilter struct {
	regex *regexp.Regexp
}

func (r RegexFilter) Accept(id string) bool {
	return r.regex.MatchString(id)
}
