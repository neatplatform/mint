package http

import (
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHeaderConfig_canonical(t *testing.T) {
	tests := []struct {
		name     string
		m        HeaderConfig
		expected HeaderConfig
	}{
		{
			name:     "Nil",
			m:        nil,
			expected: nil,
		},
		{
			name:     "Empty",
			m:        HeaderConfig{},
			expected: HeaderConfig{},
		},
		{
			name: "OK",
			m: HeaderConfig{
				"Content-Type":  Truncate,
				"authorization": Redact,
			},
			expected: HeaderConfig{
				"content-type":  Truncate,
				"authorization": Redact,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, tc.m.canonical())
		})
	}
}

func TestOptions_withDefaults(t *testing.T) {
	tests := []struct {
		name string
		opts Options
	}{
		{
			name: "Empty",
			opts: Options{},
		},
		{
			name: "WithUUIDRegexp",
			opts: Options{
				UUIDRegexp: regexp.MustCompile("[0-9A-Fa-f]{24}"),
			},
		},
		{
			name: "WithExcludeRoutes",
			opts: Options{
				ExcludeRoutes: []string{
					"/health",
					"/ready",
				},
			},
		},
		{
			name: "WithLogHeaders",
			opts: Options{
				LogHeaders: map[string]LogMode{
					"Content-Type":  Truncate,
					"Authorization": Redact,
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opts := tc.opts.withDefaults()

			assert.NotNil(t, opts.UUIDRegexp)

			if tc.opts.UUIDRegexp == nil {
				assert.Equal(t, defaultUUIDRegexp, opts.UUIDRegexp)
			} else {
				assert.Equal(t, tc.opts.UUIDRegexp, opts.UUIDRegexp)
			}

			assert.Equal(t, tc.opts.ExcludeRoutes, opts.ExcludeRoutes)
			assert.Equal(t, tc.opts.LogHeaders.canonical(), opts.LogHeaders)
		})
	}
}

func TestHeaderCarrier(t *testing.T) {
	tests := []struct {
		name           string
		c              headerCarrier
		additions      map[string]string
		expectedGets   map[string]string
		expectedValues map[string][]string
		expectedKeys   []string
	}{
		{
			name:           "Empty",
			c:              headerCarrier(http.Header{}),
			additions:      map[string]string{},
			expectedGets:   map[string]string{},
			expectedValues: map[string][]string{},
			expectedKeys:   []string{},
		},
		{
			name: "WithHeaders",
			c: headerCarrier(http.Header{
				"Content-Type":     []string{"application/json; charset=utf-8"},
				"Content-Encoding": []string{"gzip"},
				"Accept-Encoding":  []string{"gzip, deflate"},
			}),
			additions: map[string]string{
				"User-Agent": "Firefly",
			},
			expectedGets: map[string]string{
				"Content-Type":     "application/json; charset=utf-8",
				"Content-Encoding": "gzip",
				"Accept-Encoding":  "gzip, deflate",
				"User-Agent":       "Firefly",
			},
			expectedValues: map[string][]string{
				"Content-Type":     {"application/json; charset=utf-8"},
				"Content-Encoding": {"gzip"},
				"Accept-Encoding":  {"gzip, deflate"},
				"User-Agent":       {"Firefly"},
			},
			expectedKeys: []string{
				"Content-Type",
				"Content-Encoding",
				"Accept-Encoding",
				"User-Agent",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Empty(t, tc.c.Get("non-existent"))

			for key, value := range tc.additions {
				tc.c.Set(key, value)
			}

			for key, val := range tc.expectedGets {
				assert.Equal(t, val, tc.c.Get(key))
			}

			for key, vals := range tc.expectedValues {
				assert.Equal(t, vals, tc.c.Values(key))
			}

			assert.ElementsMatch(t, tc.expectedKeys, tc.c.Keys())
		})
	}
}

func TestGetLoggableHeaderKV(t *testing.T) {
	tests := []struct {
		name       string
		keyPrefix  string
		header     http.Header
		allowlist  HeaderConfig
		expectedKV []any
	}{
		{
			name:       "EmptyHeader",
			keyPrefix:  "header_",
			header:     http.Header{},
			allowlist:  HeaderConfig{},
			expectedKV: []any{},
		},
		{
			name:      "EmptyAllowlist",
			keyPrefix: "header_",
			header: http.Header{
				"Content-Type": []string{"application/json"},
			},
			allowlist:  HeaderConfig{},
			expectedKV: []any{},
		},
		{
			name:      "DropHeader",
			keyPrefix: "header_",
			header: http.Header{
				"Content-Type": []string{"application/json"},
			},
			allowlist: HeaderConfig{
				"content-type": Drop,
			},
			expectedKV: []any{},
		},
		{
			name:      "TruncateHeader",
			keyPrefix: "header_",
			header: http.Header{
				"Content-Type": []string{strings.Repeat("*", maxHeaderValueLen+10)},
			},
			allowlist: HeaderConfig{
				"content-type": Truncate,
			},
			expectedKV: []any{
				"header_content-type", strings.Repeat("*", maxHeaderValueLen) + "...",
			},
		},
		{
			name:      "RedactHeader",
			keyPrefix: "header_",
			header: http.Header{
				"Authorization": []string{"Bearer secret-token"},
			},
			allowlist: HeaderConfig{
				"authorization": Redact,
			},
			expectedKV: []any{
				"header_authorization", "<redacted len=19>",
			},
		},
		{
			name:      "Mixed",
			keyPrefix: "header_",
			header: http.Header{
				"Content-Type":  []string{"application/json"},
				"Authorization": []string{"Bearer secret-token"},
				"Cookie":        []string{"session=abc"},
			},
			allowlist: HeaderConfig{
				"content-type":  Truncate,
				"authorization": Redact,
				"cookie":        Drop,
			},
			expectedKV: []any{
				"header_content-type", "application/json",
				"header_authorization", "<redacted len=19>",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			kv := getLoggableHeaderKV(tc.keyPrefix, tc.header, tc.allowlist)

			assert.ElementsMatch(t, tc.expectedKV, kv)
		})
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name     string
		s        string
		maxRunes int
		expected string
	}{
		{
			name:     "Empty",
			s:        "",
			maxRunes: 16,
			expected: "",
		},
		{
			name:     "ZeroMax",
			s:        "Hello, World!",
			maxRunes: 0,
			expected: "",
		},
		{
			name:     "ShorterThanMax",
			s:        "Hello, World!",
			maxRunes: 16,
			expected: "Hello, World!",
		},
		{
			name:     "EqualToMax",
			s:        "Hello, World!",
			maxRunes: 13,
			expected: "Hello, World!",
		},
		{
			name:     "LongerThanMax",
			s:        "Hello, World!",
			maxRunes: 5,
			expected: "Hello...",
		},
		{
			name:     "MultiByteRune",
			s:        "こんにちは",
			maxRunes: 2,
			expected: "こん...",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, truncate(tc.s, tc.maxRunes))
		})
	}
}
