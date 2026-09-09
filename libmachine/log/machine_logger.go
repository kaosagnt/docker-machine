package log

import "io"

type MachineLogger interface {
	SetDebug(debug bool)

	SetOutWriter(io.Writer)
	SetErrWriter(io.Writer)

	// SetPhase tags subsequent entries with the create phase they belong
	// to. Only structured loggers render it.
	SetPhase(phase string)

	Debug(args ...interface{})
	Debugf(fmtString string, args ...interface{})

	Error(args ...interface{})
	Errorf(fmtString string, args ...interface{})

	Info(args ...interface{})
	Infof(fmtString string, args ...interface{})

	Warn(args ...interface{})
	Warnf(fmtString string, args ...interface{})

	History() []string
}
