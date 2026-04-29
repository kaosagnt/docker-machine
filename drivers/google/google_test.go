package google

import (
	"testing"

	"github.com/docker/machine/libmachine/drivers"
	"github.com/stretchr/testify/assert"
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
		"defaults are zero": {
			expectedIops:       0,
			expectedThroughput: 0,
		},
		"valid positive values are stored": {
			iops:               3000,
			throughput:         140,
			expectedIops:       3000,
			expectedThroughput: 140,
		},
		"zero is accepted (preserves API defaults)": {
			iops:               0,
			throughput:         0,
			expectedIops:       0,
			expectedThroughput: 0,
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
