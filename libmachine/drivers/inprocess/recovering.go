package inprocess

import (
	"encoding/json"
	"fmt"
	"runtime/debug"

	"github.com/docker/machine/libmachine/drivers"
	"github.com/docker/machine/libmachine/state"
)

// recovering turns a panic in the driver into an error, which is what the
// caller saw when the plugin process died during a call. Methods without an
// error result pass through.
type recovering struct {
	drivers.Driver
}

func panicError(r any) error {
	return fmt.Errorf("Panic in the driver: %v\n%s", r, debug.Stack())
}

func trap(err *error) {
	if r := recover(); r != nil {
		*err = panicError(r)
	}
}

func (r *recovering) MarshalJSON() ([]byte, error) {
	return json.Marshal(r.Driver)
}

func (r *recovering) UnmarshalJSON(data []byte) error {
	return json.Unmarshal(data, r.Driver)
}

func (r *recovering) Create() (err error) {
	defer trap(&err)
	return r.Driver.Create()
}

func (r *recovering) GetIP() (ip string, err error) {
	defer trap(&err)
	return r.Driver.GetIP()
}

func (r *recovering) GetSSHHostname() (hostname string, err error) {
	defer trap(&err)
	return r.Driver.GetSSHHostname()
}

func (r *recovering) GetSSHPort() (port int, err error) {
	defer trap(&err)
	return r.Driver.GetSSHPort()
}

func (r *recovering) GetURL() (url string, err error) {
	defer trap(&err)
	return r.Driver.GetURL()
}

func (r *recovering) GetState() (s state.State, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			s, err = state.Error, panicError(rec)
		}
	}()
	return r.Driver.GetState()
}

func (r *recovering) Kill() (err error) {
	defer trap(&err)
	return r.Driver.Kill()
}

func (r *recovering) PreCreateCheck() (err error) {
	defer trap(&err)
	return r.Driver.PreCreateCheck()
}

func (r *recovering) Remove() (err error) {
	defer trap(&err)
	return r.Driver.Remove()
}

func (r *recovering) Restart() (err error) {
	defer trap(&err)
	return r.Driver.Restart()
}

func (r *recovering) SetConfigFromFlags(opts drivers.DriverOptions) (err error) {
	defer trap(&err)
	return r.Driver.SetConfigFromFlags(opts)
}

func (r *recovering) Start() (err error) {
	defer trap(&err)
	return r.Driver.Start()
}

func (r *recovering) Stop() (err error) {
	defer trap(&err)
	return r.Driver.Stop()
}

func (r *recovering) UpdateLabels(labels map[string]string) (err error) {
	updater, ok := r.Driver.(drivers.LabelUpdater)
	if !ok {
		return drivers.ErrLabelsNotSupported
	}
	defer trap(&err)
	return updater.UpdateLabels(labels)
}

func (r *recovering) TLSBootstrapRequested() (requested bool, err error) {
	bootstrapper, ok := r.Driver.(drivers.TLSBootstrapper)
	if !ok {
		return false, nil
	}
	defer trap(&err)
	return bootstrapper.TLSBootstrapRequested()
}

func (r *recovering) SetTLSBootstrap(b drivers.TLSBootstrap) (err error) {
	bootstrapper, ok := r.Driver.(drivers.TLSBootstrapper)
	if !ok {
		return drivers.ErrTLSBootstrapNotSupported
	}
	defer trap(&err)
	return bootstrapper.SetTLSBootstrap(b)
}
