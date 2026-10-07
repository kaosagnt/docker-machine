// Package inprocess runs the drivers compiled into this binary in the client
// process instead of in a plugin subprocess reached over RPC.
package inprocess

import (
	"encoding/json"
	"os"
	"strconv"
	"sync"

	"github.com/docker/machine/libmachine/drivers"
	"github.com/docker/machine/libmachine/drivers/rpc"
	"github.com/docker/machine/libmachine/log"
)

// EnvKey set to a false value puts core drivers back on the plugin path.
const EnvKey = "MACHINE_DRIVER_INPROCESS"

var (
	mu           sync.RWMutex
	constructors = map[string]func() drivers.Driver{}
)

// Register makes a compiled-in driver available to Factory under name.
func Register(name string, newDriver func() drivers.Driver) {
	mu.Lock()
	defer mu.Unlock()
	constructors[name] = newDriver
}

func lookup(name string) (func() drivers.Driver, bool) {
	mu.RLock()
	defer mu.RUnlock()
	newDriver, ok := constructors[name]
	return newDriver, ok
}

// Enabled reads EnvKey. Unset or unparsable means enabled.
func Enabled() bool {
	v := os.Getenv(EnvKey)
	if v == "" {
		return true
	}
	enabled, err := strconv.ParseBool(v)
	if err != nil {
		log.Warnf("Ignoring %s=%q: %s", EnvKey, v, err)
		return true
	}
	return enabled
}

// Factory builds registered drivers in-process and hands every other driver
// name to the RPC factory, so third-party driver binaries keep working.
type Factory struct {
	plugin   rpcdriver.RPCClientDriverFactory
	disabled bool
}

func NewFactory(plugin rpcdriver.RPCClientDriverFactory) *Factory {
	return &Factory{plugin: plugin, disabled: !Enabled()}
}

// NewDriver returns the driver for driverName loaded from the host's stored
// driver JSON.
func (f *Factory) NewDriver(driverName string, rawDriver []byte) (drivers.Driver, error) {
	newDriver, ok := lookup(driverName)
	if !ok || f.disabled {
		return f.plugin.NewRPCClientDriver(driverName, rawDriver)
	}

	d := newDriver()
	if err := json.Unmarshal(rawDriver, d); err != nil {
		return nil, err
	}
	log.Debugf("(%s) Running driver %s in-process", d.GetMachineName(), driverName)

	return &recovering{Driver: d}, nil
}

func (f *Factory) Close() error {
	return f.plugin.Close()
}
