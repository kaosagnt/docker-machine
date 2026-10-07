package inprocess

import (
	"errors"
	"testing"

	"github.com/docker/machine/drivers/fakedriver"
	"github.com/docker/machine/libmachine/drivers"
	"github.com/docker/machine/libmachine/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type panicDriver struct {
	*fakedriver.Driver
	panicWith any
	returnErr error
	labels    map[string]string
}

func (p *panicDriver) Create() error {
	if p.panicWith != nil {
		panic(p.panicWith)
	}
	return p.returnErr
}

func (p *panicDriver) GetState() (state.State, error) {
	if p.panicWith != nil {
		panic(p.panicWith)
	}
	return state.Running, p.returnErr
}

func (p *panicDriver) UpdateLabels(labels map[string]string) error {
	if p.panicWith != nil {
		panic(p.panicWith)
	}
	p.labels = labels
	return p.returnErr
}

func TestRecoveringCreate(t *testing.T) {
	t.Run("error passes through", func(t *testing.T) {
		want := errors.New("API not available")
		r := &recovering{Driver: &panicDriver{returnErr: want}}
		assert.Equal(t, want, r.Create())
	})

	t.Run("panic with an error becomes an error", func(t *testing.T) {
		r := &recovering{Driver: &panicDriver{panicWith: errors.New("index out of range")}}
		err := r.Create()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Panic in the driver: index out of range\n")
		assert.Contains(t, err.Error(), "recovering_test.go", "the stack trace is included")
	})

	t.Run("panic with a non-error becomes an error", func(t *testing.T) {
		r := &recovering{Driver: &panicDriver{panicWith: "boom"}}
		err := r.Create()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Panic in the driver: boom\n")
	})
}

func TestRecoveringGetState(t *testing.T) {
	r := &recovering{Driver: &panicDriver{}}
	s, err := r.GetState()
	require.NoError(t, err)
	assert.Equal(t, state.Running, s)

	r = &recovering{Driver: &panicDriver{panicWith: "boom"}}
	s, err = r.GetState()
	assert.Error(t, err)
	assert.Equal(t, state.Error, s)
}

func TestRecoveringOptionalInterfaces(t *testing.T) {
	t.Run("driver without labels or TLS bootstrap", func(t *testing.T) {
		r := &recovering{Driver: &fakedriver.Driver{}}

		assert.Equal(t, drivers.ErrLabelsNotSupported, r.UpdateLabels(map[string]string{"a": "b"}))

		requested, err := r.TLSBootstrapRequested()
		require.NoError(t, err)
		assert.False(t, requested)

		assert.Equal(t, drivers.ErrTLSBootstrapNotSupported, r.SetTLSBootstrap(drivers.TLSBootstrap{}))
	})

	t.Run("driver with labels", func(t *testing.T) {
		d := &panicDriver{}
		r := &recovering{Driver: d}
		require.NoError(t, r.UpdateLabels(map[string]string{"a": "b"}))
		assert.Equal(t, map[string]string{"a": "b"}, d.labels)

		d.panicWith = "boom"
		assert.Error(t, r.UpdateLabels(nil))
	})
}

func TestRecoveringGettersPassThrough(t *testing.T) {
	r := &recovering{Driver: &fakedriver.Driver{MockName: "m", MockIP: "10.0.0.1", MockState: state.Running}}
	assert.Equal(t, "m", r.GetMachineName())
	assert.Equal(t, "Driver", r.DriverName())

	ip, err := r.GetIP()
	require.NoError(t, err)
	assert.Equal(t, "10.0.0.1", ip)
}
