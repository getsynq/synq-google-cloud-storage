package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateSVG(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid SVG",
			data:    []byte(`<svg xmlns="http://www.w3.org/2000/svg"><circle cx="50" cy="50" r="40"/></svg>`),
			wantErr: false,
		},
		{
			name:    "valid SVG with XML declaration",
			data:    []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"><circle cx="50" cy="50" r="40"/></svg>`),
			wantErr: false,
		},
		{
			name:    "valid SVG with whitespace",
			data:    []byte(`  <svg xmlns="http://www.w3.org/2000/svg"><circle cx="50" cy="50" r="40"/></svg>  `),
			wantErr: false,
		},
		{
			name:    "empty data",
			data:    []byte(``),
			wantErr: true,
			errMsg:  "icon data is empty",
		},
		{
			name:    "not SVG - plain text",
			data:    []byte(`This is not an SVG`),
			wantErr: true,
			errMsg:  "icon does not start with <svg> or <?xml> tag",
		},
		{
			name:    "not SVG - HTML",
			data:    []byte(`<html><body>Not SVG</body></html>`),
			wantErr: true,
			errMsg:  "icon does not start with <svg> or <?xml> tag",
		},
		{
			name:    "malformed XML",
			data:    []byte(`<svg xmlns="http://www.w3.org/2000/svg"><circle cx="50" cy="50" r="40">`),
			wantErr: true,
			errMsg:  "invalid XML structure",
		},
		{
			name:    "valid complex SVG",
			data:    defaultGCSIcon,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSVG(tt.data)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestLoadIcon(t *testing.T) {
	validSVG := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><circle cx="50" cy="50" r="40"/></svg>`)

	tests := []struct {
		name        string
		customPath  string
		defaultIcon []byte
		wantErr     bool
	}{
		{
			name:        "use default icon when no custom path",
			customPath:  "",
			defaultIcon: validSVG,
			wantErr:     false,
		},
		{
			name:        "invalid default icon",
			customPath:  "",
			defaultIcon: []byte("not svg"),
			wantErr:     true,
		},
		{
			name:        "non-existent file",
			customPath:  "/nonexistent/path/icon.svg",
			defaultIcon: validSVG,
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			icon, err := loadIcon(tt.customPath, tt.defaultIcon)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, icon)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, icon)
			}
		})
	}
}
