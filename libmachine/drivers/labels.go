package drivers

import "errors"

// ErrLabelsNotSupported is returned by UpdateLabels for drivers that
// have no label concept.
var ErrLabelsNotSupported = errors.New("driver does not support labels")

// LabelUpdater is implemented by drivers that can update provider-side
// labels on an existing machine.
type LabelUpdater interface {
	UpdateLabels(labels map[string]string) error
}
