package rpcdriver

import (
	"errors"
	"testing"

	"github.com/docker/machine/drivers/fakedriver"
	"github.com/docker/machine/libmachine/drivers"
	"github.com/stretchr/testify/assert"
)

type panicDriver struct {
	*fakedriver.Driver
	panicErr  error
	returnErr error
}

type FakeStacker struct {
	trace []byte
}

func (fs *FakeStacker) Stack() []byte {
	return fs.trace
}

func (p *panicDriver) Create() error {
	if p.panicErr != nil {
		panic(p.panicErr)
	}
	return p.returnErr
}

func TestRPCServerDriverCreate(t *testing.T) {
	testCases := []struct {
		description  string
		expectedErr  error
		serverDriver *RPCServerDriver
		stacker      Stacker
	}{
		{
			description: "Happy path",
			expectedErr: nil,
			serverDriver: &RPCServerDriver{
				ActualDriver: &panicDriver{
					returnErr: nil,
				},
			},
		},
		{
			description: "Normal error, no panic",
			expectedErr: errors.New("API not available"),
			serverDriver: &RPCServerDriver{
				ActualDriver: &panicDriver{
					returnErr: errors.New("API not available"),
				},
			},
		},
		{
			description: "Panic happened during create",
			expectedErr: errors.New("Panic in the driver: index out of range\nSTACK TRACE"),
			serverDriver: &RPCServerDriver{
				ActualDriver: &panicDriver{
					panicErr: errors.New("index out of range"),
				},
			},
			stacker: &FakeStacker{
				trace: []byte("STACK TRACE"),
			},
		},
	}

	for _, tc := range testCases {
		stdStacker = tc.stacker
		assert.Equal(t, tc.expectedErr, tc.serverDriver.Create(nil, nil))
	}
}

type labelUpdaterDriver struct {
	*fakedriver.Driver
	labels map[string]string
}

func (d *labelUpdaterDriver) UpdateLabels(labels map[string]string) error {
	d.labels = labels
	return nil
}

func TestServerDriverUpdateLabels(t *testing.T) {
	updater := &labelUpdaterDriver{Driver: &fakedriver.Driver{}}
	server := NewRPCServerDriver(updater)

	labels := map[string]string{"runner_manager_heartbeat": "123"}
	assert.NoError(t, server.UpdateLabels(labels, nil))
	assert.Equal(t, labels, updater.labels)
}

func TestServerDriverUpdateLabelsUnsupported(t *testing.T) {
	server := NewRPCServerDriver(&fakedriver.Driver{})

	err := server.UpdateLabels(map[string]string{"a": "b"}, nil)
	assert.Equal(t, drivers.ErrLabelsNotSupported, err)
}

type tlsBootstrapDriver struct {
	*fakedriver.Driver
	requested bool
	bootstrap *drivers.TLSBootstrap
}

func (d *tlsBootstrapDriver) TLSBootstrapRequested() (bool, error) {
	return d.requested, nil
}

func (d *tlsBootstrapDriver) SetTLSBootstrap(b drivers.TLSBootstrap) error {
	d.bootstrap = &b
	return nil
}

func TestServerDriverTLSBootstrap(t *testing.T) {
	driver := &tlsBootstrapDriver{Driver: &fakedriver.Driver{}, requested: true}
	server := NewRPCServerDriver(driver)

	var requested bool
	assert.NoError(t, server.TLSBootstrapRequested(nil, &requested))
	assert.True(t, requested)

	b := drivers.TLSBootstrap{CACert: []byte("ca"), ServerCert: []byte("cert"), ServerKey: []byte("key"), DaemonDropin: []byte("dropin")}
	assert.NoError(t, server.SetTLSBootstrap(b, nil))
	assert.Equal(t, &b, driver.bootstrap)
}

func TestServerDriverTLSBootstrapUnsupported(t *testing.T) {
	server := NewRPCServerDriver(&fakedriver.Driver{})

	requested := true
	assert.NoError(t, server.TLSBootstrapRequested(nil, &requested))
	assert.False(t, requested)

	err := server.SetTLSBootstrap(drivers.TLSBootstrap{}, nil)
	assert.Equal(t, drivers.ErrTLSBootstrapNotSupported, err)
}
