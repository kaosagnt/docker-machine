package google

import (
	"testing"
	"time"

	"github.com/docker/machine/libmachine/drivers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetConfigFromFlags(t *testing.T) {
	driver := NewDriver("", "")

	checkFlags := &drivers.CheckDriverOptions{
		FlagsValues: map[string]interface{}{
			"google-project": "PROJECT",
		},
		CreateFlags: driver.GetCreateFlags(),
	}

	err := driver.SetConfigFromFlags(checkFlags)

	assert.NoError(t, err)
	assert.Empty(t, checkFlags.InvalidFlags)
}

func TestSetConfigFromFlags_FlexStockoutCooldown(t *testing.T) {
	tests := map[string]struct {
		cooldown      string
		probeLease    string
		expected      time.Duration
		expectedProbe time.Duration
		expectErr     string
	}{
		"disabled by default": {expectedProbe: defaultFlexStockoutProbeLease},
		"durations are stored": {
			cooldown:      "2m",
			probeLease:    "5m",
			expected:      2 * time.Minute,
			expectedProbe: defaultFlexStockoutProbeLease,
		},
		"invalid cooldown is rejected": {
			cooldown:  "soon",
			expectErr: "google-flex-stockout-cooldown",
		},
		"negative cooldown is rejected": {
			cooldown:  "-1s",
			expectErr: "must be >= 0",
		},
		"cooldown requires bulkInsert": {
			cooldown:  "1m",
			expectErr: "requires --google-bulk-insert",
		},
		"invalid probe lease is rejected": {
			cooldown:   "1m",
			probeLease: "later",
			expectErr:  "google-flex-stockout-probe-lease",
		},
		"non-positive probe lease is rejected when enabled": {
			cooldown:   "1m",
			probeLease: "0s",
			expectErr:  "must be > 0",
		},
		"negative probe lease is rejected when disabled": {
			probeLease: "-1s",
			expectErr:  "must be >= 0",
		},
		"probe lease override requires bulkInsert": {
			probeLease: "6m",
			expectErr:  "requires --google-bulk-insert",
		},
		"probe lease must cover operation timeout": {
			cooldown:   "2m",
			probeLease: "90s",
			expectErr:  "must be >= google-operation-backoff-max-elapsed-time",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			driver := NewDriver("machine", t.TempDir())
			values := map[string]interface{}{
				"google-project": "PROJECT",
			}
			if tt.cooldown != "" {
				values["google-flex-stockout-cooldown"] = tt.cooldown
			}
			if tt.probeLease != "" {
				values["google-flex-stockout-probe-lease"] = tt.probeLease
			}
			if tt.expectErr == "" && tt.cooldown != "" {
				values["google-bulk-insert"] = true
				values["google-region"] = "us-east1"
			}
			if tt.expectErr == "must be >= google-operation-backoff-max-elapsed-time" {
				values["google-bulk-insert"] = true
				values["google-region"] = "us-east1"
			}

			flags := &drivers.CheckDriverOptions{FlagsValues: values, CreateFlags: driver.GetCreateFlags()}
			err := driver.SetConfigFromFlags(flags)
			if tt.expectErr != "" {
				require.ErrorContains(t, err, tt.expectErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected, driver.FlexStockoutCooldown)
			assert.Equal(t, tt.expectedProbe, driver.FlexStockoutProbeLease)
		})
	}
}

func TestSetConfigFromFlags_COSDockerNetworkReadinessGate(t *testing.T) {
	tests := map[string]struct {
		enabled          bool
		expectedMetadata metadataMap
	}{
		"disabled by default": {
			expectedMetadata: metadataMap{},
		},
		"enabled injects metadata": {
			enabled:          true,
			expectedMetadata: metadataMap{cosDockerNetworkReadinessMetadataKey: "true"},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			driver := NewDriver("", "")
			flags := &drivers.CheckDriverOptions{
				FlagsValues: map[string]interface{}{
					"google-project": "PROJECT",
					"google-cos-docker-network-readiness-gate": tt.enabled,
				},
				CreateFlags: driver.GetCreateFlags(),
			}

			require.NoError(t, driver.SetConfigFromFlags(flags))
			assert.Equal(t, tt.enabled, driver.COSDockerNetworkReadinessGate)
			assert.Equal(t, tt.expectedMetadata, driver.Metadata)
		})
	}
}

func TestSetConfigFromFlags_COSDockerNetworkReadinessURL(t *testing.T) {
	tests := map[string]struct {
		url              string
		expectErr        bool
		expectedMetadata metadataMap
	}{
		"unset by default": {
			expectedMetadata: metadataMap{},
		},
		"valid https url injects metadata": {
			url:              "https://gitlab.com/readiness",
			expectedMetadata: metadataMap{cosDockerNetworkReadinessURLMetadataKey: "https://gitlab.com/readiness"},
		},
		"non-http scheme rejected": {
			url:       "ftp://gitlab.com",
			expectErr: true,
		},
		"shell metacharacters rejected": {
			url:       "https://gitlab.com/$(reboot)",
			expectErr: true,
		},
		"missing host rejected": {
			url:       "https:///path",
			expectErr: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			driver := NewDriver("", "")
			flags := &drivers.CheckDriverOptions{
				FlagsValues: map[string]interface{}{
					"google-project": "PROJECT",
					"google-cos-docker-network-readiness-url": tt.url,
				},
				CreateFlags: driver.GetCreateFlags(),
			}

			err := driver.SetConfigFromFlags(flags)
			if tt.expectErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.url, driver.COSDockerNetworkReadinessURL)
			assert.Equal(t, tt.expectedMetadata, driver.Metadata)
		})
	}
}

func TestSetConfigFromFlags_ProvisionedIopsAndThroughput(t *testing.T) {
	tests := map[string]struct {
		iops               interface{}
		throughput         interface{}
		expectErr          bool
		expectedIops       int
		expectedThroughput int
	}{
		"unset flags default to zero (preserves API defaults)": {
			expectedIops:       0,
			expectedThroughput: 0,
		},
		"positive values are stored on the driver": {
			iops:               3000,
			throughput:         140,
			expectedIops:       3000,
			expectedThroughput: 140,
		},
		"negative iops is rejected": {
			iops:       -1,
			throughput: 140,
			expectErr:  true,
		},
		"negative throughput is rejected": {
			iops:       3000,
			throughput: -1,
			expectErr:  true,
		},
	}

	for tn, tt := range tests {
		t.Run(tn, func(t *testing.T) {
			driver := NewDriver("", "")

			flagsValues := map[string]interface{}{
				"google-project": "PROJECT",
			}
			if tt.iops != nil {
				flagsValues["google-provisioned-iops"] = tt.iops
			}
			if tt.throughput != nil {
				flagsValues["google-provisioned-throughput"] = tt.throughput
			}

			checkFlags := &drivers.CheckDriverOptions{
				FlagsValues: flagsValues,
				CreateFlags: driver.GetCreateFlags(),
			}

			err := driver.SetConfigFromFlags(checkFlags)

			if tt.expectErr {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			assert.Equal(t, tt.expectedIops, driver.ProvisionedIops)
			assert.Equal(t, tt.expectedThroughput, driver.ProvisionedThroughput)
		})
	}
}

func TestSetConfigFromFlags_BulkInsertFlexSelection(t *testing.T) {
	tests := map[string]struct {
		flagsValues  map[string]interface{}
		expectErr    bool
		errSubstring string
	}{
		"bulkInsert without flex selection falls back to --google-machine-type": {
			// Operators can opt into bulkInsert without a selection
			// ladder: createInstanceViaBulkInsert synthesises a single
			// selection from --google-machine-type. Validation should
			// accept this configuration.
			flagsValues: map[string]interface{}{
				"google-project":     "PROJECT",
				"google-bulk-insert": true,
				"google-region":      "us-east1",
			},
		},
		"bulkInsert with valid flex selection succeeds": {
			flagsValues: map[string]interface{}{
				"google-project":        "PROJECT",
				"google-bulk-insert":    true,
				"google-region":         "us-east1",
				"google-flex-selection": []string{"machine-type=n2-standard-2"},
			},
		},
		"bulkInsert with malformed flex selection is rejected": {
			flagsValues: map[string]interface{}{
				"google-project":        "PROJECT",
				"google-bulk-insert":    true,
				"google-region":         "us-east1",
				"google-flex-selection": []string{"n2-standard-2"},
			},
			expectErr:    true,
			errSubstring: "is not key=value",
		},
		"flex selection without bulkInsert is rejected": {
			flagsValues: map[string]interface{}{
				"google-project":        "PROJECT",
				"google-flex-selection": []string{"machine-type=n2-standard-2"},
			},
			expectErr:    true,
			errSubstring: "requires --google-bulk-insert",
		},
	}

	for tn, tt := range tests {
		t.Run(tn, func(t *testing.T) {
			driver := NewDriver("", "")
			checkFlags := &drivers.CheckDriverOptions{
				FlagsValues: tt.flagsValues,
				CreateFlags: driver.GetCreateFlags(),
			}
			err := driver.SetConfigFromFlags(checkFlags)
			if tt.expectErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstring)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestMetadataMapFromStringSlice(t *testing.T) {
	tests := map[string]struct {
		slice          []string
		expectedResult metadataMap
	}{
		"empty slice": {
			slice:          []string{""},
			expectedResult: metadataMap{},
		},
		"missing key=value pair": {
			slice:          []string{"key_1"},
			expectedResult: metadataMap{},
		},
		"key=value pair present": {
			slice:          []string{"key_1=value_1"},
			expectedResult: metadataMap{"key_1": "value_1"},
		},
		"multiple = characters present": {
			slice:          []string{"key_1=value=_1="},
			expectedResult: metadataMap{"key_1": "value=_1="},
		},
		"invalid key value pair": {
			slice:          []string{"key_1="},
			expectedResult: metadataMap{"key_1": ""},
		},
		"multiple metadata items": {
			slice: []string{"key_1=value_1", "key_2=value_2"},
			expectedResult: metadataMap{
				"key_1": "value_1",
				"key_2": "value_2",
			},
		},
	}

	for tn, tt := range tests {
		t.Run(tn, func(t *testing.T) {
			result := metadataMapFromStringSlice(tt.slice)
			assert.Equal(t, tt.expectedResult, result)
		})
	}
}
