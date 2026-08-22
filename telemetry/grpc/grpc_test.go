package grpc

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type testContextKey string

func TestMetadataConfig_canonical(t *testing.T) {
	tests := []struct {
		name     string
		m        MetadataConfig
		expected MetadataConfig
	}{
		{
			name:     "Nil",
			m:        nil,
			expected: nil,
		},
		{
			name:     "Empty",
			m:        MetadataConfig{},
			expected: MetadataConfig{},
		},
		{
			name: "OK",
			m: MetadataConfig{
				"authorization": Redact,
				"User-Agent":    Truncate,
			},
			expected: MetadataConfig{
				"authorization": Redact,
				"user-agent":    Truncate,
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
			name: "WithExcludeMethods",
			opts: Options{
				ExcludeMethods: []string{
					"CheckHealth",
				},
			},
		},
		{
			name: "WithLogMetadata",
			opts: Options{
				LogMetadata: map[string]LogMode{
					"authorization": Redact,
					"user-agent":    Truncate,
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opts := tc.opts.withDefaults()

			assert.Equal(t, tc.opts.ExcludeMethods, opts.ExcludeMethods)
			assert.Equal(t, tc.opts.LogMetadata.canonical(), opts.LogMetadata)
		})
	}
}

func TestMetadataCarrier(t *testing.T) {
	tests := []struct {
		name           string
		c              metadataCarrier
		additions      map[string]string
		expectedGets   map[string]string
		expectedValues map[string][]string
		expectedKeys   []string
	}{
		{
			name:           "Empty",
			c:              metadataCarrier(metadata.MD{}),
			additions:      map[string]string{},
			expectedGets:   map[string]string{},
			expectedValues: map[string][]string{},
			expectedKeys:   []string{},
		},
		{
			name: "WithMetadata",
			c: metadataCarrier(metadata.MD{
				"authorization": []string{"Bearer token"},
			}),
			additions: map[string]string{
				"x-request-uuid": "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
			},
			expectedGets: map[string]string{
				"authorization":  "Bearer token",
				"x-request-uuid": "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
			},
			expectedValues: map[string][]string{
				"authorization":  {"Bearer token"},
				"x-request-uuid": {"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"},
			},
			expectedKeys: []string{
				"authorization",
				"x-request-uuid",
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

func TestParseFullMethod(t *testing.T) {
	tests := []struct {
		name            string
		fullMethod      string
		expectedOK      bool
		expectedPackage string
		expectedService string
		expectedMethod  string
	}{
		{
			name:       "NoSlash",
			fullMethod: "GetUser",
			expectedOK: false,
		},
		{
			name:       "NoDot",
			fullMethod: "UserService/GetUser",
			expectedOK: false,
		},
		{
			name:            "OK",
			fullMethod:      "/myapp.v1.UserService/GetUser",
			expectedOK:      true,
			expectedPackage: "myapp.v1",
			expectedService: "UserService",
			expectedMethod:  "GetUser",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pack, service, method, ok := parseFullMethod(tc.fullMethod)

			assert.Equal(t, tc.expectedOK, ok)
			assert.Equal(t, tc.expectedPackage, pack)
			assert.Equal(t, tc.expectedService, service)
			assert.Equal(t, tc.expectedMethod, method)
		})
	}
}

func TestGetMessageSize(t *testing.T) {
	tests := []struct {
		name         string
		m            any
		expectedSize int64
	}{
		{
			name:         "NotProtoMessage",
			m:            "Hello, World!",
			expectedSize: 0,
		},
		{
			name:         "ProtoMessage",
			m:            wrapperspb.String("Hello, World!"),
			expectedSize: 15,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expectedSize, getMessageSize(tc.m))
		})
	}
}

func TestGetLoggableMetadataKV(t *testing.T) {
	tests := []struct {
		name       string
		keyPrefix  string
		md         metadata.MD
		allowlist  MetadataConfig
		expectedKV []any
	}{
		{
			name:       "EmptyMetadata",
			keyPrefix:  "metadata_",
			md:         metadata.MD{},
			allowlist:  MetadataConfig{},
			expectedKV: []any{},
		},
		{
			name:      "EmptyAllowlist",
			keyPrefix: "metadata_",
			md: metadata.MD{
				"user-agent": []string{"client-name/1.0"},
			},
			allowlist:  MetadataConfig{},
			expectedKV: []any{},
		},
		{
			name:      "DropMetadata",
			keyPrefix: "metadata_",
			md: metadata.MD{
				"user-agent": []string{"client-name/1.0"},
			},
			allowlist: MetadataConfig{
				"user-agent": Drop,
			},
			expectedKV: []any{},
		},
		{
			name:      "TruncateMetadata",
			keyPrefix: "metadata_",
			md: metadata.MD{
				"user-agent": []string{strings.Repeat("*", maxMetadataValueLen+10)},
			},
			allowlist: MetadataConfig{
				"user-agent": Truncate,
			},
			expectedKV: []any{
				"metadata_user-agent", strings.Repeat("*", maxMetadataValueLen) + "...",
			},
		},
		{
			name:      "RedactMetadata",
			keyPrefix: "metadata_",
			md: metadata.MD{
				"authorization": []string{"Bearer secret-token"},
			},
			allowlist: MetadataConfig{
				"authorization": Redact,
			},
			expectedKV: []any{
				"metadata_authorization", "<redacted len=19>",
			},
		},
		{
			name:      "Mixed",
			keyPrefix: "metadata_",
			md: metadata.MD{
				"authorization":         []string{"Bearer secret-token"},
				"user-agent":            []string{"client-name/1.0"},
				"x-ratelimit-remaining": []string{"10"},
			},
			allowlist: MetadataConfig{
				"authorization": Redact,
				"user-agent":    Truncate,
				"cookie":        Drop,
			},
			expectedKV: []any{
				"metadata_authorization", "<redacted len=19>",
				"metadata_user-agent", "client-name/1.0",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			kv := getLoggableMetadataKV(tc.keyPrefix, tc.md, tc.allowlist)

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
