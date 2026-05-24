package google

import (
	"testing"

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

func TestSetConfigFromFlags_BulkInsertRequiresFlexSelection(t *testing.T) {
	tests := map[string]struct {
		flagsValues  map[string]interface{}
		expectErr    bool
		errSubstring string
	}{
		"bulkInsert without flex selection is rejected": {
			flagsValues: map[string]interface{}{
				"google-project":     "PROJECT",
				"google-bulk-insert": true,
				"google-region":      "us-east1",
			},
			expectErr:    true,
			errSubstring: "requires at least one --google-flex-selection",
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
