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

// EnvKey set to a true value runs core drivers in-process. Off by default.
const EnvKey = "MACHINE_DRIVER_INPROCESS"

var (
	mu           sync.RWMutex
	constructors = map[string]func() drivers.Driver{}
)

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

func Enabled() bool {
	v := os.Getenv(EnvKey)
	if v == "" {
		return false
	}
	enabled, err := strconv.ParseBool(v)
	if err != nil {
		log.Warnf("Ignoring %s=%q: %s", EnvKey, v, err)
		return false
	}
	return enabled
}

type Factory struct {
	plugin  rpcdriver.RPCClientDriverFactory
	enabled bool
}

func NewFactory(plugin rpcdriver.RPCClientDriverFactory) *Factory {
	return &Factory{plugin: plugin, enabled: Enabled()}
}

func (f *Factory) NewDriver(driverName string, rawDriver []byte) (drivers.Driver, error) {
	newDriver, ok := lookup(driverName)
	if !ok || !f.enabled {
		// Third-party driver binaries only exist on the plugin path.
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
