package log

import "io"

type Fields map[string]interface{}

type MachineLogger interface {
	SetDebug(debug bool)

	SetOutWriter(io.Writer)
	SetErrWriter(io.Writer)

	WithFields(fields Fields) MachineLogger

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

func mergeFields(base, extra Fields) Fields {
	out := make(Fields, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}
