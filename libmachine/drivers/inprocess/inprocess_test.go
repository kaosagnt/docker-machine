package inprocess

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/docker/machine/drivers/fakedriver"
	"github.com/docker/machine/libmachine/drivers"
	"github.com/docker/machine/libmachine/drivers/rpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errPlugin = errors.New("plugin factory called")

type fakePluginFactory struct {
	calls  []string
	closed bool
}

func (f *fakePluginFactory) NewRPCClientDriver(driverName string, rawDriver []byte) (*rpcdriver.RPCClientDriver, error) {
	f.calls = append(f.calls, driverName)
	return nil, errPlugin
}

func (f *fakePluginFactory) Close() error {
	f.closed = true
	return nil
}

func newFakeDriver() drivers.Driver {
	return &fakedriver.Driver{BaseDriver: &drivers.BaseDriver{}}
}

func TestFactoryNewDriver(t *testing.T) {
	Register("fake", newFakeDriver)
	raw, err := json.Marshal(&drivers.BaseDriver{MachineName: "runner-abc", StorePath: "/store"})
	require.NoError(t, err)

	t.Run("registered driver runs in-process", func(t *testing.T) {
		plugin := &fakePluginFactory{}
		f := &Factory{Plugin: plugin}

		d, err := f.NewDriver("fake", raw)
		require.NoError(t, err)
		assert.Empty(t, plugin.calls)

		inner := d.(*recovering).Driver.(*fakedriver.Driver)
		assert.Equal(t, "runner-abc", inner.MachineName, "rawDriver is unmarshaled into the driver")
		assert.Equal(t, "/store", inner.StorePath)

		want, err := json.Marshal(inner)
		require.NoError(t, err)
		got, err := json.Marshal(d)
		require.NoError(t, err)
		assert.JSONEq(t, string(want), string(got), "the store must see the driver's own JSON")
	})

	t.Run("unknown driver goes to the plugin factory", func(t *testing.T) {
		plugin := &fakePluginFactory{}
		f := &Factory{Plugin: plugin}

		_, err := f.NewDriver("third-party", raw)
		assert.ErrorIs(t, err, errPlugin)
		assert.Equal(t, []string{"third-party"}, plugin.calls)
	})

	t.Run("disabled factory goes to the plugin factory", func(t *testing.T) {
		plugin := &fakePluginFactory{}
		f := &Factory{Plugin: plugin, disabled: true}

		_, err := f.NewDriver("fake", raw)
		assert.ErrorIs(t, err, errPlugin)
		assert.Equal(t, []string{"fake"}, plugin.calls)
	})

	t.Run("invalid JSON is an error", func(t *testing.T) {
		f := &Factory{Plugin: &fakePluginFactory{}}
		_, err := f.NewDriver("fake", []byte("{"))
		assert.Error(t, err)
	})

	t.Run("Close closes the plugin factory", func(t *testing.T) {
		plugin := &fakePluginFactory{}
		f := &Factory{Plugin: plugin}
		require.NoError(t, f.Close())
		assert.True(t, plugin.closed)
	})
}

func TestEnabled(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"", true},
		{"1", true},
		{"true", true},
		{"0", false},
		{"false", false},
		{"maybe", true},
	} {
		t.Run("value="+tc.value, func(t *testing.T) {
			t.Setenv(EnvKey, tc.value)
			assert.Equal(t, tc.want, Enabled())
		})
	}
	t.Run("factory reads the env", func(t *testing.T) {
		t.Setenv(EnvKey, "0")
		assert.True(t, NewFactory(&fakePluginFactory{}).disabled)
		t.Setenv(EnvKey, "")
		assert.False(t, NewFactory(&fakePluginFactory{}).disabled)
	})
}
