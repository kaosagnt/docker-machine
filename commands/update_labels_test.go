package commands

import (
	"testing"

	"github.com/docker/machine/commands/commandstest"
	"github.com/docker/machine/drivers/fakedriver"
	"github.com/docker/machine/libmachine/drivers"
	"github.com/docker/machine/libmachine/host"
	"github.com/docker/machine/libmachine/libmachinetest"
	"github.com/stretchr/testify/assert"
)

type fakeLabelUpdaterDriver struct {
	fakedriver.Driver
	labels map[string]string
	err    error
}

func (d *fakeLabelUpdaterDriver) UpdateLabels(labels map[string]string) error {
	d.labels = labels
	return d.err
}

func TestCmdUpdateLabels(t *testing.T) {
	updater := &fakeLabelUpdaterDriver{}
	api := func(driver interface {
		drivers.Driver
	}) *libmachinetest.FakeAPI {
		return &libmachinetest.FakeAPI{
			Hosts: []*host.Host{{Name: "foo", Driver: driver}},
		}
	}

	testCases := []struct {
		name        string
		commandLine CommandLine
		driver      drivers.Driver
		expectedErr error
		expected    map[string]string
	}{
		{
			name: "merges labels on a supporting driver",
			commandLine: &commandstest.FakeCommandLine{
				CliArgs: []string{"foo"},
				LocalFlags: &commandstest.FakeFlagger{
					Data: map[string]interface{}{"label": []string{"runner_manager_heartbeat=123", "a=b"}},
				},
			},
			driver:   updater,
			expected: map[string]string{"runner_manager_heartbeat": "123", "a": "b"},
		},
		{
			name: "rejects malformed label",
			commandLine: &commandstest.FakeCommandLine{
				CliArgs: []string{"foo"},
				LocalFlags: &commandstest.FakeFlagger{
					Data: map[string]interface{}{"label": []string{"nodelimiter"}},
				},
			},
			driver:      updater,
			expectedErr: assert.AnError,
		},
		{
			name: "requires at least one label",
			commandLine: &commandstest.FakeCommandLine{
				CliArgs: []string{"foo"},
				LocalFlags: &commandstest.FakeFlagger{
					Data: map[string]interface{}{"label": []string{}},
				},
			},
			driver:      updater,
			expectedErr: errNoLabels,
		},
		{
			name: "unsupported driver",
			commandLine: &commandstest.FakeCommandLine{
				CliArgs: []string{"foo"},
				LocalFlags: &commandstest.FakeFlagger{
					Data: map[string]interface{}{"label": []string{"a=b"}},
				},
			},
			driver:      &fakedriver.Driver{},
			expectedErr: drivers.ErrLabelsNotSupported,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := cmdUpdateLabels(tc.commandLine, api(tc.driver))

			switch {
			case tc.expectedErr == assert.AnError:
				assert.Error(t, err)
			case tc.expectedErr != nil:
				assert.Equal(t, tc.expectedErr, err)
			default:
				assert.NoError(t, err)
				assert.Equal(t, tc.expected, updater.labels)
			}
		})
	}
}

func TestCmdUpdateLabelsTrimsWhitespace(t *testing.T) {
	updater := &fakeLabelUpdaterDriver{}
	commandLine := &commandstest.FakeCommandLine{
		CliArgs: []string{"foo"},
		LocalFlags: &commandstest.FakeFlagger{
			Data: map[string]interface{}{"label": []string{" key = value "}},
		},
	}
	api := &libmachinetest.FakeAPI{
		Hosts: []*host.Host{{Name: "foo", Driver: updater}},
	}

	assert.NoError(t, cmdUpdateLabels(commandLine, api))
	assert.Equal(t, map[string]string{"key": "value"}, updater.labels)
}

func TestCmdUpdateLabelsRequiresMachineName(t *testing.T) {
	err := cmdUpdateLabels(&commandstest.FakeCommandLine{
		CliArgs: []string{},
		LocalFlags: &commandstest.FakeFlagger{
			Data: map[string]interface{}{"label": []string{"a=b"}},
		},
	}, &libmachinetest.FakeAPI{})
	assert.Equal(t, ErrExpectedOneMachine, err)
}
