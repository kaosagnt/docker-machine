package log

import "maps"

import "io"

type Fields map[string]any

type MachineLogger interface {
	SetDebug(debug bool)

	SetOutWriter(io.Writer)
	SetErrWriter(io.Writer)

	WithFields(fields Fields) MachineLogger

	Debug(args ...any)
	Debugf(fmtString string, args ...any)

	Error(args ...any)
	Errorf(fmtString string, args ...any)

	Info(args ...any)
	Infof(fmtString string, args ...any)

	Warn(args ...any)
	Warnf(fmtString string, args ...any)

	History() []string
}

func mergeFields(base, extra Fields) Fields {
	out := make(Fields, len(base)+len(extra))
	maps.Copy(out, base)
	maps.Copy(out, extra)
	return out
}
