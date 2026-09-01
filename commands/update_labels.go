package commands

import (
	"errors"
	"fmt"
	"strings"

	"github.com/docker/machine/libmachine"
	"github.com/docker/machine/libmachine/drivers"
	"github.com/docker/machine/libmachine/log"
)

var errNoLabels = errors.New("no --label given, expected at least one key=value pair")

func cmdUpdateLabels(c CommandLine, api libmachine.API) error {
	if len(c.Args()) != 1 {
		c.ShowHelp()
		return ErrExpectedOneMachine
	}

	labels := map[string]string{}
	for _, kv := range c.StringSlice("label") {
		key, value, found := strings.Cut(kv, "=")
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if !found || key == "" {
			return fmt.Errorf("invalid label format %q, expected key=value", kv)
		}
		labels[key] = value
	}
	if len(labels) == 0 {
		return errNoLabels
	}

	target := c.Args().First()

	host, err := api.Load(target)
	if err != nil {
		return err
	}

	updater, ok := host.Driver.(drivers.LabelUpdater)
	if !ok {
		return drivers.ErrLabelsNotSupported
	}

	if err := updater.UpdateLabels(labels); err != nil {
		return err
	}

	log.Infof("Updated labels on %s", target)
	return nil
}
